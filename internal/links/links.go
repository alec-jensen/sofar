// Package links records which payment pays back which expense (S12): a
// reimbursement, a refund, a chargeback. A link nets the expense in the
// expense's own month, tentative or confirmed, in either order of arrival.
// Links never create income and never exceed what is left to reimburse
// unless the user overrides.
package links

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/alec-jensen/sofar/internal/events"
)

// Event kinds (subject type "link").
const (
	KindCreate = "link.create"
	KindState  = "link.state"
	Subject    = "link"
)

// States of a link.
const (
	Tentative = "tentative"
	Confirmed = "confirmed"
	Rejected  = "rejected"
	Removed   = "removed"
)

// Creators of a link.
const (
	ByUser           = "user"
	ByAutoReceivable = "auto_receivable"
	ByAutoPattern    = "auto_pattern"
)

type (
	// Create records a new link.
	Create struct {
		LinkID int64  `json:"link_id"`
		State  string `json:"state"`
	}
	// SetState records a change of state.
	SetState struct {
		LinkID int64  `json:"link_id"`
		From   string `json:"from"`
		To     string `json:"to"`
	}
)

var (
	// ErrNotFound means no such link or transaction.
	ErrNotFound = errors.New("links: not found")
	// ErrInvalid means a bad request.
	ErrInvalid = errors.New("links: invalid request")
	// ErrExceeds means the link is larger than what is left to reimburse; the
	// user can override (S12).
	ErrExceeds = errors.New("links: exceeds the remaining amount")
)

// Link is one row.
type Link struct {
	ID           int64
	PaymentID    string
	ExpenseID    string
	PartNo       *int
	ReceivableID *string
	Amount       int64
	State        string
	CreatedBy    string
}

// Active reports whether the link nets the expense (tentative links net immediately).
func (l Link) Active() bool { return l.State == Tentative || l.State == Confirmed }

// Service manages links.
type Service struct {
	DB  *sql.DB
	Reg *events.Registry
}

// New builds a Service and registers its event payloads.
func New(db *sql.DB, reg *events.Registry) *Service {
	reg.Register(KindCreate, 1, Create{})
	reg.Register(KindState, 1, SetState{})
	return &Service{DB: db, Reg: reg}
}

// Params creates a link.
type Params struct {
	PaymentID    string
	ExpenseID    string
	PartNo       *int
	ReceivableID *string
	Amount       int64  // cents; 0 means as much as fits
	State        string // tentative (default) or confirmed
	CreatedBy    string // user by default
	// Override lets the link exceed the expense's remaining amount.
	Override bool
}

const cols = `id, payment_txn_id, expense_txn_id, part_no, receivable_id, amount, state, created_by`

func scan(r interface{ Scan(...any) error }) (Link, error) {
	var l Link
	var part sql.NullInt32
	var rec sql.NullString
	if err := r.Scan(&l.ID, &l.PaymentID, &l.ExpenseID, &part, &rec, &l.Amount, &l.State, &l.CreatedBy); err != nil {
		return l, err
	}
	if part.Valid {
		p := int(part.Int32)
		l.PartNo = &p
	}
	if rec.Valid {
		l.ReceivableID = &rec.String
	}
	return l, nil
}

// Get returns one link.
func (s *Service) Get(ctx context.Context, q events.DBTX, id int64) (Link, error) {
	l, err := scan(q.QueryRowContext(ctx, `SELECT `+cols+` FROM links WHERE id=$1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return l, ErrNotFound
	}
	return l, err
}

// List returns every link, oldest first.
func (s *Service) List(ctx context.Context, q events.DBTX) ([]Link, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+cols+` FROM links ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Link
	for rows.Next() {
		l, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// Remaining is what is left on an expense (as a positive number) after its
// active links, for the whole transaction or one split part.
func (s *Service) Remaining(ctx context.Context, q events.DBTX, expenseID string, part *int) (int64, error) {
	var total int64
	var err error
	if part != nil {
		err = q.QueryRowContext(ctx, `SELECT abs(amount) FROM txn_splits WHERE raw_txn_id=$1 AND part_no=$2`, expenseID, *part).Scan(&total)
	} else {
		err = q.QueryRowContext(ctx, `SELECT abs(amount) FROM raw_transactions WHERE id=$1`, expenseID).Scan(&total)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	var used int64
	err = q.QueryRowContext(ctx, `SELECT coalesce(sum(amount),0) FROM links WHERE expense_txn_id=$1 AND state IN ('tentative','confirmed') AND part_no IS NOT DISTINCT FROM $2::int`, expenseID, part).Scan(&used)
	return total - used, err
}

// PaymentRemaining is how much of a payment is not yet linked.
func (s *Service) PaymentRemaining(ctx context.Context, q events.DBTX, paymentID string) (int64, error) {
	var total int64
	err := q.QueryRowContext(ctx, `SELECT amount FROM raw_transactions WHERE id=$1`, paymentID).Scan(&total)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	var used int64
	err = q.QueryRowContext(ctx, `SELECT coalesce(sum(amount),0) FROM links WHERE payment_txn_id=$1 AND state IN ('tentative','confirmed')`, paymentID).Scan(&used)
	return total - used, err
}

// Create links a payment to an expense. The payment must be money in and the
// expense money out. The amount may not exceed what is left on either side;
// the expense side can be overridden (the excess then asks, S12).
func (s *Service) Create(ctx context.Context, tx *sql.Tx, actor events.Actor, p Params) (Link, error) {
	if p.State == "" {
		p.State = Tentative
	}
	if p.State != Tentative && p.State != Confirmed {
		return Link{}, fmt.Errorf("%w: a new link is tentative or confirmed", ErrInvalid)
	}
	if p.CreatedBy == "" {
		p.CreatedBy = ByUser
	}
	var pay, exp int64
	// lock both rows so two links cannot both fit into the same remainder
	rows, err := tx.QueryContext(ctx, `SELECT id, amount FROM raw_transactions WHERE id = ANY($1::uuid[]) ORDER BY id FOR UPDATE`, "{"+p.PaymentID+","+p.ExpenseID+"}")
	if err != nil {
		return Link{}, ErrNotFound
	}
	found := 0
	for rows.Next() {
		var id string
		var a int64
		rows.Scan(&id, &a)
		found++
		if id == p.PaymentID {
			pay = a
		}
		if id == p.ExpenseID {
			exp = a
		}
	}
	rows.Close()
	if p.PaymentID == p.ExpenseID || found != 2 {
		return Link{}, ErrNotFound
	}
	if pay <= 0 || exp >= 0 {
		return Link{}, fmt.Errorf("%w: a payment is money in and an expense is money out", ErrInvalid)
	}
	var hidden bool
	tx.QueryRowContext(ctx, `SELECT coalesce((SELECT hidden FROM transaction_overlays WHERE raw_txn_id=$1), false)`, p.ExpenseID).Scan(&hidden)
	if hidden {
		return Link{}, fmt.Errorf("%w: that expense is hidden", ErrInvalid)
	}
	payLeft, err := s.PaymentRemaining(ctx, tx, p.PaymentID)
	if err != nil {
		return Link{}, err
	}
	expLeft, err := s.Remaining(ctx, tx, p.ExpenseID, p.PartNo)
	if err != nil {
		return Link{}, err
	}
	switch {
	case p.Amount < 0:
		return Link{}, fmt.Errorf("%w: amount", ErrInvalid)
	case p.Amount == 0:
		p.Amount = min(payLeft, expLeft)
	case p.Amount > payLeft:
		return Link{}, fmt.Errorf("%w: the payment has only %d left", ErrInvalid, payLeft)
	case p.Amount > expLeft && !p.Override:
		return Link{}, ErrExceeds
	}
	if p.Amount <= 0 {
		return Link{}, fmt.Errorf("%w: nothing left to link", ErrInvalid)
	}
	var id int64
	err = tx.QueryRowContext(ctx, `INSERT INTO links (payment_txn_id, expense_txn_id, part_no, receivable_id, amount, state, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id`, p.PaymentID, p.ExpenseID, p.PartNo, p.ReceivableID, p.Amount, p.State, p.CreatedBy).Scan(&id)
	if err != nil {
		return Link{}, err
	}
	if _, err = events.Append(ctx, tx, s.Reg, events.New{Actor: actor, Kind: KindCreate, SubjectType: Subject, SubjectID: fmt.Sprint(id), Payload: Create{LinkID: id, State: p.State}}); err != nil {
		return Link{}, err
	}
	return s.Get(ctx, tx, id)
}

// Confirm makes a tentative link confirmed. The math does not change.
func (s *Service) Confirm(ctx context.Context, tx *sql.Tx, actor events.Actor, id int64) (Link, error) {
	return s.move(ctx, tx, actor, id, Confirmed, Tentative)
}

// Reject drops a link and records it as a negative pair; the netting reverts.
func (s *Service) Reject(ctx context.Context, tx *sql.Tx, actor events.Actor, id int64) (Link, error) {
	return s.move(ctx, tx, actor, id, Rejected, Tentative)
}

// Remove unlinks a link (confirmed or tentative); the netting reverts.
func (s *Service) Remove(ctx context.Context, tx *sql.Tx, actor events.Actor, id int64) (Link, error) {
	return s.move(ctx, tx, actor, id, Removed, Tentative, Confirmed)
}

func (s *Service) move(ctx context.Context, tx *sql.Tx, actor events.Actor, id int64, to string, from ...string) (Link, error) {
	l, err := s.Get(ctx, tx, id)
	if err != nil {
		return l, err
	}
	ok := false
	for _, f := range from {
		ok = ok || l.State == f
	}
	if !ok {
		return l, fmt.Errorf("%w: a %s link cannot become %s", ErrInvalid, l.State, to)
	}
	if err = s.setState(ctx, tx, id, to); err != nil {
		return l, err
	}
	if _, err = events.Append(ctx, tx, s.Reg, events.New{Actor: actor, Kind: KindState, SubjectType: Subject, SubjectID: fmt.Sprint(id), Payload: SetState{LinkID: id, From: l.State, To: to}}); err != nil {
		return l, err
	}
	return s.Get(ctx, tx, id)
}

func (s *Service) setState(ctx context.Context, tx *sql.Tx, id int64, state string) error {
	_, err := tx.ExecContext(ctx, `UPDATE links SET state=$2, updated_at=now() WHERE id=$1`, id, state)
	return err
}

// Undo reverses a create or a change of state.
func (s *Service) Undo(ctx context.Context, tx *sql.Tx, actor events.Actor, eventID int64) (events.Event, error) {
	return s.flip(ctx, tx, actor, eventID, false)
}

// Redo reverses an undo.
func (s *Service) Redo(ctx context.Context, tx *sql.Tx, actor events.Actor, undoID int64) (events.Event, error) {
	return s.flip(ctx, tx, actor, undoID, true)
}

func (s *Service) flip(ctx context.Context, tx *sql.Tx, actor events.Actor, id int64, redo bool) (events.Event, error) {
	target, err := events.Get(ctx, tx, id)
	if err != nil {
		return events.Event{}, err
	}
	orig := target
	var out events.Event
	if redo {
		if target.Kind != events.KindUndo || target.Reverses == nil {
			return events.Event{}, events.ErrNotUndone
		}
		if orig, err = events.Get(ctx, tx, *target.Reverses); err != nil {
			return events.Event{}, err
		}
		out, err = events.Redo(ctx, tx, actor, id, "")
	} else {
		out, err = events.Undo(ctx, tx, actor, id, "")
	}
	if err != nil {
		return out, err
	}
	p, err := s.Reg.Decode(orig)
	if err != nil {
		return out, err
	}
	switch v := p.(type) {
	case *Create:
		state := Removed
		if redo {
			state = v.State
		}
		return out, s.setState(ctx, tx, v.LinkID, state)
	case *SetState:
		state := v.From
		if redo {
			state = v.To
		}
		return out, s.setState(ctx, tx, v.LinkID, state)
	}
	return out, nil
}

// Net is the netting the engine needs: for each expense (and split part) the
// cents netted away, and for each payment the cents linked.
type Net struct {
	Expense map[string]int64 // key: txn id, or txn id#part
	Payment map[string]int64
}

// ExpenseKey names an expense or one of its split parts.
func ExpenseKey(txnID string, part *int) string {
	if part == nil {
		return txnID
	}
	return fmt.Sprintf("%s#%d", txnID, *part)
}

// LoadNet reads the active links.
func LoadNet(ctx context.Context, q events.DBTX) (Net, error) {
	n := Net{Expense: map[string]int64{}, Payment: map[string]int64{}}
	rows, err := q.QueryContext(ctx, `SELECT expense_txn_id, part_no, payment_txn_id, amount FROM links WHERE state IN ('tentative','confirmed')`)
	if err != nil {
		return n, err
	}
	defer rows.Close()
	for rows.Next() {
		var e, p string
		var part sql.NullInt32
		var a int64
		if err = rows.Scan(&e, &part, &p, &a); err != nil {
			return n, err
		}
		var pp *int
		if part.Valid {
			v := int(part.Int32)
			pp = &v
		}
		n.Expense[ExpenseKey(e, pp)] += a
		n.Payment[p] += a
	}
	if err = rows.Err(); err != nil {
		return n, err
	}
	// a receivable marked "treat as netted now" nets what has not landed yet (S12a)
	pend, err := q.QueryContext(ctx, `
		SELECT r.expense_txn_id, r.part_no, r.amount - coalesce((SELECT sum(l.amount) FROM links l WHERE l.receivable_id = r.id AND l.state IN ('tentative','confirmed')), 0)
		FROM receivables r WHERE r.netted_now AND r.written_off_at IS NULL AND r.deleted_at IS NULL`)
	if err != nil {
		return n, err
	}
	defer pend.Close()
	for pend.Next() {
		var e string
		var part sql.NullInt32
		var a int64
		if err = pend.Scan(&e, &part, &a); err != nil {
			return n, err
		}
		if a <= 0 {
			continue
		}
		var pp *int
		if part.Valid {
			v := int(part.Int32)
			pp = &v
		}
		n.Expense[ExpenseKey(e, pp)] += a
	}
	return n, pend.Err()
}
