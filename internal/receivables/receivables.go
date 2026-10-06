// Package receivables tracks money owed to the user (S12a): "dad owes me $11".
// A receivable counts in full against spending until the money lands, unless
// the user chose to treat it as netted now. It is satisfied by links from real
// payments, shows aging, and can be written off. Status is derived from the
// links, never stored, so it cannot drift.
package receivables

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/alec-jensen/sofar/internal/civil"
	"github.com/alec-jensen/sofar/internal/counterparties"
	"github.com/alec-jensen/sofar/internal/events"
	"github.com/alec-jensen/sofar/internal/links"
	"github.com/alec-jensen/sofar/internal/manual"
	"github.com/alec-jensen/sofar/internal/money"
)

// Event kinds (subject type "receivable").
const (
	KindCreate   = "receivable.create"
	KindUpdate   = "receivable.update"
	KindWriteOff = "receivable.writeoff"
	Subject      = "receivable"
)

type (
	// CreateEvt records a new receivable.
	CreateEvt struct {
		ID string `json:"id"`
	}
	// UpdateEvt records a change of the netted-now flag, amount or payer.
	UpdateEvt struct {
		ID    string `json:"id"`
		Field string `json:"field"`
		From  string `json:"from"`
		To    string `json:"to"`
	}
	// WriteOffEvt records a write-off; Undo clears it.
	WriteOffEvt struct {
		ID string `json:"id"`
	}
)

var (
	// ErrNotFound means no such receivable or expense.
	ErrNotFound = errors.New("receivables: not found")
	// ErrInvalid means a bad request.
	ErrInvalid = errors.New("receivables: invalid request")
)

// Status is the derived state of a receivable.
type Status string

// Statuses. Overdue is a flag on top of expected and partial.
const (
	Expected   Status = "expected"
	Partial    Status = "partially_received"
	Received   Status = "received"
	WrittenOff Status = "written_off"
)

// DefaultAging are the days at which an open receivable raises an alert (S12a edge 8).
var DefaultAging = []int{14, 30, 60}

// Receivable is one debt.
type Receivable struct {
	ID             string
	CounterpartyID *string
	ExpenseID      string
	PartNo         *int
	Amount         int64
	AmountMax      *int64
	ExpectedBy     *civil.Date
	NettedNow      bool
	WrittenOff     bool
	Note           string
	Source         string
	OpenedOn       civil.Date

	// derived
	ReceivedAmount int64
	Status         Status
	Overdue        bool
	AgeDays        int
}

// Open reports whether money is still expected.
func (r Receivable) Open() bool { return r.Status == Expected || r.Status == Partial }

// Unrecovered is what a netted-now receivable netted that never arrived, once written off.
func (r Receivable) Unrecovered() int64 {
	if r.WrittenOff && r.NettedNow {
		return max(0, r.Amount-r.ReceivedAmount)
	}
	return 0
}

// Service manages receivables.
type Service struct {
	DB     *sql.DB
	Reg    *events.Registry
	Links  *links.Service
	Cps    *counterparties.Service
	Manual *manual.Service
	Now    func() time.Time
	Home   *time.Location
	// OverdueDays is the age at which a receivable without an expected-by date is overdue.
	OverdueDays int
}

// New builds a Service and registers its event payloads.
func New(db *sql.DB, reg *events.Registry, l *links.Service, c *counterparties.Service, m *manual.Service) *Service {
	reg.Register(KindCreate, 1, CreateEvt{})
	reg.Register(KindUpdate, 1, UpdateEvt{})
	reg.Register(KindWriteOff, 1, WriteOffEvt{})
	return &Service{DB: db, Reg: reg, Links: l, Cps: c, Manual: m, Now: time.Now, Home: time.UTC, OverdueDays: 30}
}

func (s *Service) today() civil.Date { return civil.DateOf(s.Now().In(s.Home)) }

// Params creates a receivable.
type Params struct {
	ExpenseID      string
	PartNo         *int
	Amount         int64 // cents the other person owes
	AmountMax      *int64
	CounterpartyID *string
	ExpectedBy     *civil.Date
	// NettedNow nets the amount against the expense now; nil follows the payer's setting (off by default).
	NettedNow *bool
	Note      string
	Source    string // tap (default), split, suggestion or rule
}

const cols = `r.id, r.counterparty_id, r.expense_txn_id, r.part_no, r.amount, r.amount_max, r.expected_by, r.netted_now, r.written_off_at IS NOT NULL, r.note, r.source, r.opened_on,
	coalesce((SELECT sum(l.amount) FROM links l WHERE l.receivable_id = r.id AND l.state IN ('tentative','confirmed')), 0)`

func scan(r interface{ Scan(...any) error }) (Receivable, error) {
	var x Receivable
	var cp sql.NullString
	var part sql.NullInt32
	var max sql.NullInt64
	var by, opened sql.NullTime
	if err := r.Scan(&x.ID, &cp, &x.ExpenseID, &part, &x.Amount, &max, &by, &x.NettedNow, &x.WrittenOff, &x.Note, &x.Source, &opened, &x.ReceivedAmount); err != nil {
		return x, err
	}
	if cp.Valid {
		x.CounterpartyID = &cp.String
	}
	if part.Valid {
		p := int(part.Int32)
		x.PartNo = &p
	}
	if max.Valid {
		x.AmountMax = &max.Int64
	}
	if by.Valid {
		d := civil.DateOf(by.Time)
		x.ExpectedBy = &d
	}
	x.OpenedOn = civil.DateOf(opened.Time)
	return x, nil
}

func (s *Service) derive(x Receivable) Receivable {
	switch {
	case x.WrittenOff:
		x.Status = WrittenOff
	case x.ReceivedAmount >= x.Amount:
		x.Status = Received
	case x.ReceivedAmount > 0:
		x.Status = Partial
	default:
		x.Status = Expected
	}
	today := s.today()
	x.AgeDays = max(0, today.DaysSince(x.OpenedOn))
	if x.Open() {
		if x.ExpectedBy != nil {
			x.Overdue = today.After(*x.ExpectedBy)
		} else {
			x.Overdue = x.AgeDays >= s.OverdueDays
		}
	}
	return x
}

// Get returns one receivable.
func (s *Service) Get(ctx context.Context, q events.DBTX, id string) (Receivable, error) {
	x, err := scan(q.QueryRowContext(ctx, `SELECT `+cols+` FROM receivables r WHERE r.id::text=$1 AND r.deleted_at IS NULL`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return x, ErrNotFound
	}
	if err != nil {
		return x, err
	}
	return s.derive(x), nil
}

// List returns every receivable, newest first; openOnly keeps those still expecting money.
func (s *Service) List(ctx context.Context, q events.DBTX, openOnly bool) ([]Receivable, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+cols+` FROM receivables r WHERE r.deleted_at IS NULL ORDER BY r.opened_on DESC, r.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Receivable
	for rows.Next() {
		x, err := scan(rows)
		if err != nil {
			return nil, err
		}
		x = s.derive(x)
		if !openOnly || x.Open() {
			out = append(out, x)
		}
	}
	return out, rows.Err()
}

// Create records that someone owes the user for (part of) an expense. It
// counts in full against spending until money lands, unless netted now.
func (s *Service) Create(ctx context.Context, tx *sql.Tx, actor events.Actor, p Params) (Receivable, error) {
	if p.Amount <= 0 {
		return Receivable{}, fmt.Errorf("%w: say how much is owed", ErrInvalid)
	}
	if p.AmountMax != nil && *p.AmountMax < p.Amount {
		return Receivable{}, fmt.Errorf("%w: the most cannot be below the least", ErrInvalid)
	}
	if p.Source == "" {
		p.Source = "tap"
	}
	left, err := s.Links.Remaining(ctx, tx, p.ExpenseID, p.PartNo)
	if err != nil {
		return Receivable{}, ErrNotFound
	}
	if p.Amount > left {
		return Receivable{}, fmt.Errorf("%w: only %d of that expense is left to be paid back", ErrInvalid, left)
	}
	netted := false
	if p.NettedNow != nil {
		netted = *p.NettedNow
	} else if p.CounterpartyID != nil {
		if c, err := s.Cps.Get(ctx, tx, *p.CounterpartyID); err == nil {
			netted = c.AutoNet
		}
	}
	var id string
	err = tx.QueryRowContext(ctx, `INSERT INTO receivables (counterparty_id, expense_txn_id, part_no, amount, amount_max, expected_by, netted_now, note, source, opened_on)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id`,
		p.CounterpartyID, p.ExpenseID, p.PartNo, p.Amount, p.AmountMax, nullDate(p.ExpectedBy), netted, p.Note, p.Source, s.today().String()).Scan(&id)
	if err != nil {
		return Receivable{}, err
	}
	if err = s.emit(ctx, tx, actor, KindCreate, id, CreateEvt{ID: id}); err != nil {
		return Receivable{}, err
	}
	return s.Get(ctx, tx, id)
}

func nullDate(d *civil.Date) any {
	if d == nil {
		return nil
	}
	return d.String()
}

func (s *Service) emit(ctx context.Context, tx *sql.Tx, actor events.Actor, kind, id string, payload any) error {
	_, err := events.Append(ctx, tx, s.Reg, events.New{Actor: actor, Kind: kind, SubjectType: Subject, SubjectID: id, Payload: payload})
	return err
}

// SetNettedNow turns "treat as netted now" on or off.
func (s *Service) SetNettedNow(ctx context.Context, tx *sql.Tx, actor events.Actor, id string, on bool) error {
	x, err := s.Get(ctx, tx, id)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE receivables SET netted_now=$2 WHERE id::text=$1`, id, on); err != nil {
		return err
	}
	return s.emit(ctx, tx, actor, KindUpdate, id, UpdateEvt{ID: id, Field: "netted_now", From: fmt.Sprint(x.NettedNow), To: fmt.Sprint(on)})
}

// WriteOff gives up on the money. The expense counts in full again (a netted-now
// receivable stops netting); the receivable stays on record, and this can be undone.
func (s *Service) WriteOff(ctx context.Context, tx *sql.Tx, actor events.Actor, id string) (Receivable, error) {
	x, err := s.Get(ctx, tx, id)
	if err != nil {
		return x, err
	}
	if x.WrittenOff || x.Status == Received {
		return x, fmt.Errorf("%w: nothing left to write off", ErrInvalid)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE receivables SET written_off_at=now() WHERE id::text=$1`, id); err != nil {
		return x, err
	}
	if err = s.emit(ctx, tx, actor, KindWriteOff, id, WriteOffEvt{ID: id}); err != nil {
		return x, err
	}
	return s.Get(ctx, tx, id)
}

// Receive links a real payment to a receivable. The link is confirmed (the user
// said so) and nets the expense for what was paid. Amount 0 takes as much as
// fits, bounded by what is still owed.
func (s *Service) Receive(ctx context.Context, tx *sql.Tx, actor events.Actor, id, paymentID string, amount int64) (links.Link, error) {
	x, err := s.Get(ctx, tx, id)
	if err != nil {
		return links.Link{}, err
	}
	owed := max(0, x.Amount-x.ReceivedAmount)
	if amount == 0 {
		left, err := s.Links.PaymentRemaining(ctx, tx, paymentID)
		if err != nil {
			return links.Link{}, err
		}
		amount = min(owed, left)
	}
	if amount <= 0 {
		return links.Link{}, fmt.Errorf("%w: nothing is owed", ErrInvalid)
	}
	rid := x.ID
	return s.Links.Create(ctx, tx, actor, links.Params{PaymentID: paymentID, ExpenseID: x.ExpenseID, PartNo: x.PartNo, ReceivableID: &rid, Amount: amount, State: links.Confirmed, CreatedBy: links.ByUser, Override: amount > owed})
}

// MarkReceivedCash records a payback that left no bank trace ("dad paid me
// cash"): it adds a manual transaction and links it. When the real transaction
// arrives, the manual-merge suggestion of S11 offers to merge them.
func (s *Service) MarkReceivedCash(ctx context.Context, tx *sql.Tx, actor events.Actor, id, accountID string, on civil.Date, amount int64) (links.Link, string, error) {
	x, err := s.Get(ctx, tx, id)
	if err != nil {
		return links.Link{}, "", err
	}
	if amount == 0 {
		amount = max(0, x.Amount-x.ReceivedAmount)
	}
	if accountID == "" {
		if err = tx.QueryRowContext(ctx, `SELECT account_id FROM raw_transactions WHERE id=$1`, x.ExpenseID).Scan(&accountID); err != nil {
			return links.Link{}, "", err
		}
	}
	desc := "cash received"
	if x.CounterpartyID != nil {
		if c, err := s.Cps.Get(ctx, tx, *x.CounterpartyID); err == nil {
			desc = "cash from " + c.Name
		}
	}
	txn, err := s.Manual.AddTxn(ctx, tx, actor, accountID, on, money.Cents(amount), desc, "")
	if err != nil {
		return links.Link{}, "", err
	}
	l, err := s.Receive(ctx, tx, actor, id, txn, amount)
	return l, txn, err
}

// Alert is an aging notice for an open receivable.
type Alert struct {
	ReceivableID string
	AgeDays      int
	Threshold    int
}

// Aging lists open receivables that have crossed an aging threshold, with the
// highest one crossed. thresholds nil means the defaults (14, 30 and 60 days);
// a per-payer function replaces them with the payer's own lag (M7.6).
func (s *Service) Aging(ctx context.Context, q events.DBTX, thresholds func(counterpartyID *string) []int) ([]Alert, error) {
	all, err := s.List(ctx, q, true)
	if err != nil {
		return nil, err
	}
	var out []Alert
	for _, r := range all {
		ts := DefaultAging
		if thresholds != nil {
			if t := thresholds(r.CounterpartyID); len(t) > 0 {
				ts = t
			}
		}
		hit := 0
		for _, t := range ts {
			if r.AgeDays >= t && t > hit {
				hit = t
			}
		}
		if hit > 0 {
			out = append(out, Alert{ReceivableID: r.ID, AgeDays: r.AgeDays, Threshold: hit})
		}
	}
	return out, nil
}

// Undo reverses a create, update or write-off.
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
	apply := redo
	switch v := p.(type) {
	case *CreateEvt:
		if apply {
			_, err = tx.ExecContext(ctx, `UPDATE receivables SET deleted_at=NULL WHERE id::text=$1`, v.ID)
		} else {
			_, err = tx.ExecContext(ctx, `UPDATE receivables SET deleted_at=now() WHERE id::text=$1`, v.ID)
		}
	case *WriteOffEvt:
		if apply {
			_, err = tx.ExecContext(ctx, `UPDATE receivables SET written_off_at=now() WHERE id::text=$1`, v.ID)
		} else {
			_, err = tx.ExecContext(ctx, `UPDATE receivables SET written_off_at=NULL WHERE id::text=$1`, v.ID)
		}
	case *UpdateEvt:
		to := v.From
		if apply {
			to = v.To
		}
		if v.Field == "netted_now" {
			_, err = tx.ExecContext(ctx, `UPDATE receivables SET netted_now=$2::boolean WHERE id::text=$1`, v.ID, to)
		}
	}
	return out, err
}
