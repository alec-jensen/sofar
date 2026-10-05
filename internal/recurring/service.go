package recurring

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/alec-jensen/sofar/internal/civil"
	"github.com/alec-jensen/sofar/internal/events"
	"github.com/alec-jensen/sofar/internal/ledger"
	"github.com/alec-jensen/sofar/internal/money"
	"github.com/alec-jensen/sofar/internal/normalize"
	"github.com/alec-jensen/sofar/internal/recurrence"
)

// Event kinds (subject type "recurring").
const (
	KindCreate  = "recurring.create"
	KindEnd     = "recurring.end"
	KindDismiss = "recurring.dismiss"
	KindUpdate  = "recurring.update"
	Subject     = "recurring"
)

type (
	// Create records a new item.
	Create struct {
		ItemID string `json:"item_id"`
		Kind   string `json:"kind"`
		Name   string `json:"name"`
		Key    string `json:"key"`
	}
	// End records that an item stopped.
	End struct {
		ItemID  string `json:"item_id"`
		EndedOn string `json:"ended_on"`
	}
	// Dismiss records "not recurring" for a merchant.
	Dismiss struct {
		Key       string `json:"key"`
		Direction string `json:"direction"`
	}
	// UpdateEvent records a change to an item's amount or schedule.
	UpdateEvent struct {
		ItemID string `json:"item_id"`
		Note   string `json:"note"`
	}
)

var (
	// ErrInvalid means a bad request.
	ErrInvalid = errors.New("recurring: invalid request")
	// ErrNotFound means no such item or transaction.
	ErrNotFound = errors.New("recurring: not found")
)

// Service manages recurring items.
type Service struct {
	DB     *sql.DB
	Reg    *events.Registry
	Ledger *ledger.Service
	Home   *time.Location
	Now    func() time.Time
	Detect recurrence.DetectConfig
}

// New builds a Service and registers its event payloads.
func New(db *sql.DB, reg *events.Registry, l *ledger.Service, home *time.Location) *Service {
	reg.Register(KindCreate, 1, Create{})
	reg.Register(KindEnd, 1, End{})
	reg.Register(KindDismiss, 1, Dismiss{})
	reg.Register(KindUpdate, 1, UpdateEvent{})
	if home == nil {
		home = time.UTC
	}
	return &Service{DB: db, Reg: reg, Ledger: l, Home: home, Now: time.Now, Detect: recurrence.DefaultConfig()}
}

func (s *Service) today() civil.Date { return civil.DateOf(s.Now().In(s.Home)) }

const itemCols = `id, kind, name, match_key, coalesce(category_id,''), amount, variable, tolerance_bp, schedules, active_from, ended_on, coalesce(installments_total,0)`

func scanItem(r interface{ Scan(...any) error }) (Item, error) {
	var it Item
	var kind string
	var sched []byte
	var from time.Time
	var ended sql.NullTime
	var amount int64
	if err := r.Scan(&it.ID, &kind, &it.Name, &it.Key, &it.CategoryID, &amount, &it.Variable, &it.ToleranceBP, &sched, &from, &ended, &it.InstallmentsTotal); err != nil {
		return it, err
	}
	it.Kind, it.Amount, it.ActiveFrom = recurrence.ItemKind(kind), money.Cents(amount), civil.DateOf(from)
	if ended.Valid {
		d := civil.DateOf(ended.Time)
		it.EndedOn = &d
	}
	return it, json.Unmarshal(sched, &it.Schedules)
}

// List returns every live item, newest first.
func (s *Service) List(ctx context.Context, q events.DBTX) ([]Item, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+itemCols+` FROM recurring_items WHERE deleted_at IS NULL ORDER BY created_at DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Item
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// ListItems returns every live item without needing a Service.
func ListItems(ctx context.Context, q events.DBTX) ([]Item, error) { return (&Service{}).List(ctx, q) }

// Links returns the transactions attached to each item by hand: item id -> txn ids.
func Links(ctx context.Context, q events.DBTX) (map[string]map[string]bool, error) {
	rows, err := q.QueryContext(ctx, `SELECT item_id, raw_txn_id FROM recurring_links`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]map[string]bool{}
	for rows.Next() {
		var item, txn string
		if err = rows.Scan(&item, &txn); err != nil {
			return nil, err
		}
		if out[item] == nil {
			out[item] = map[string]bool{}
		}
		out[item][txn] = true
	}
	return out, rows.Err()
}

// Get returns one item.
func (s *Service) Get(ctx context.Context, q events.DBTX, id string) (Item, error) {
	it, err := scanItem(q.QueryRowContext(ctx, `SELECT `+itemCols+` FROM recurring_items WHERE id::text=$1 AND deleted_at IS NULL`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return it, ErrNotFound
	}
	return it, err
}

// Params describes an item to create.
type Params struct {
	Kind              recurrence.ItemKind
	Name              string
	Key               string
	CategoryID        string
	Amount            money.Cents
	Variable          bool
	ToleranceBP       int
	Schedule          recurrence.Schedule
	ActiveFrom        civil.Date
	InstallmentsTotal int
}

func (s *Service) create(ctx context.Context, tx *sql.Tx, actor events.Actor, p Params) (Item, error) {
	p.Name = strings.Join(strings.Fields(p.Name), " ")
	if p.Name == "" || p.Key == "" || p.Amount < 0 {
		return Item{}, fmt.Errorf("%w: an item needs a name and a non-negative amount", ErrInvalid)
	}
	switch p.Kind {
	case recurrence.ItemBill, recurrence.ItemSubscription, recurrence.ItemIncome, recurrence.ItemPaymentPlan:
	default:
		return Item{}, fmt.Errorf("%w: unknown kind %q", ErrInvalid, p.Kind)
	}
	if p.Kind == recurrence.ItemPaymentPlan && p.InstallmentsTotal < 1 {
		return Item{}, fmt.Errorf("%w: a payment plan needs its number of installments", ErrInvalid)
	}
	if err := p.Schedule.Validate(); err != nil {
		return Item{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if p.ToleranceBP == 0 {
		p.ToleranceBP = 1000
	}
	if p.ActiveFrom.IsZero() {
		p.ActiveFrom = s.today().Month().First()
	}
	p.Schedule.EffectiveFrom = p.ActiveFrom
	sched, _ := json.Marshal([]recurrence.Schedule{p.Schedule})
	var cat any
	if p.CategoryID != "" {
		cat = p.CategoryID
	}
	var inst any
	if p.InstallmentsTotal > 0 {
		inst = p.InstallmentsTotal
	}
	var id string
	err := tx.QueryRowContext(ctx, `
		INSERT INTO recurring_items (kind, name, match_key, category_id, amount, variable, tolerance_bp, schedules, active_from, installments_total)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id`,
		string(p.Kind), p.Name, p.Key, cat, int64(p.Amount), p.Variable, p.ToleranceBP, sched, p.ActiveFrom.String(), inst).Scan(&id)
	if err != nil {
		return Item{}, err
	}
	if _, err = events.Append(ctx, tx, s.Reg, events.New{Actor: actor, Kind: KindCreate, SubjectType: Subject, SubjectID: id, Payload: Create{ItemID: id, Kind: string(p.Kind), Name: p.Name, Key: p.Key}}); err != nil {
		return Item{}, err
	}
	return s.Get(ctx, tx, id)
}

// Create adds an item the user described (a new bill is a user-made change, so
// it starts this month and raises fixed immediately).
func (s *Service) Create(ctx context.Context, tx *sql.Tx, actor events.Actor, p Params) (Item, error) {
	if p.Key == "" {
		p.Key = normalize.Normalize(p.Name)
	}
	return s.create(ctx, tx, actor, p)
}

// Candidate is a detected possibility for the review queue.
type Candidate = recurrence.Candidate

// Candidates detects recurring candidates the user has not yet answered.
func (s *Service) Candidates(ctx context.Context) ([]Candidate, error) {
	txns, err := s.history(ctx)
	if err != nil {
		return nil, err
	}
	all := recurrence.Detect(txns, s.Detect)
	items, err := s.List(ctx, s.DB)
	if err != nil {
		return nil, err
	}
	supp := map[string]bool{}
	rows, err := s.DB.QueryContext(ctx, `SELECT match_key, direction FROM recurring_suppressions`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var k, dir string
		rows.Scan(&k, &dir)
		supp[dir+":"+k] = true
	}
	rows.Close()
	var out []Candidate
next:
	for _, c := range all {
		dir := "out"
		if c.Kind == recurrence.ItemIncome {
			dir = "in"
		}
		if supp[dir+":"+c.Key] {
			continue
		}
		for _, it := range items {
			if (it.Kind == recurrence.ItemIncome) == (dir == "in") && (it.Key == c.Key || normalize.Matches(it.Key, c.Key)) {
				continue next
			}
		}
		out = append(out, c)
	}
	return out, nil
}

// history loads the transactions detection looks at: visible, posted, in
// budget or tracked accounts, outside the neutral bucket.
func (s *Service) history(ctx context.Context) ([]recurrence.Txn, error) {
	var out []recurrence.Txn
	cursor := ""
	for {
		page, err := s.Ledger.View(ctx, ledger.Filter{Limit: 500, Cursor: cursor})
		if err != nil {
			return nil, err
		}
		for _, t := range page.Items {
			if t.Pending || t.Bucket == "neutral" || len(t.Splits) > 0 {
				continue
			}
			out = append(out, recurrence.Txn{ID: t.ID, Date: t.PostedOn, Amount: t.Amount.Abs(), Inflow: t.Amount > 0, Description: t.Description})
		}
		if page.Next == "" {
			return out, nil
		}
		cursor = page.Next
	}
}

// Confirm turns a candidate into an item. Detection only asks; this is the user saying yes (S5).
func (s *Service) Confirm(ctx context.Context, tx *sql.Tx, actor events.Actor, c Candidate, kind recurrence.ItemKind) (Item, error) {
	if kind == "" {
		kind = c.Kind
	}
	from := s.today().Month().First()
	if kind == recurrence.ItemIncome && len(c.TxnIDs) > 0 {
		from = c.Schedule.Anchor // income streams are recognized in history too
		if from.IsZero() {
			from = c.LastDate
		}
	}
	return s.create(ctx, tx, actor, Params{Kind: kind, Name: c.Name, Key: c.Key, CategoryID: c.CategoryID, Amount: c.Amount, Variable: c.Variable, Schedule: c.Schedule, ActiveFrom: from})
}

// Dismiss records "not recurring" for a merchant so detection never asks again.
func (s *Service) Dismiss(ctx context.Context, tx *sql.Tx, actor events.Actor, key, direction string) (events.Event, error) {
	if key == "" || (direction != "in" && direction != "out") {
		return events.Event{}, ErrInvalid
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO recurring_suppressions (match_key, direction) VALUES ($1,$2) ON CONFLICT DO NOTHING`, key, direction); err != nil {
		return events.Event{}, err
	}
	return events.Append(ctx, tx, s.Reg, events.New{Actor: actor, Kind: KindDismiss, SubjectType: Subject, SubjectID: direction + ":" + key, Payload: Dismiss{Key: key, Direction: direction}})
}

// MarkParams is what the user says when marking a transaction as recurring (S14).
type MarkParams struct {
	Kind              recurrence.ItemKind
	Schedule          recurrence.Schedule
	Amount            money.Cents // 0 means the transaction's amount
	Variable          bool
	ToleranceBP       int
	CategoryID        string
	InstallmentsTotal int
}

// MarkAsRecurring creates an item from any transaction, bypassing the
// detection threshold, and attaches the matching history. This is how annual
// bills enter (S14).
func (s *Service) MarkAsRecurring(ctx context.Context, tx *sql.Tx, actor events.Actor, txnID string, p MarkParams) (Item, error) {
	t, err := s.Ledger.Get(ctx, txnID)
	if err != nil {
		return Item{}, ErrNotFound
	}
	amount := p.Amount
	if amount == 0 {
		amount = t.Amount.Abs()
	}
	kind := p.Kind
	if kind == "" {
		kind = recurrence.ItemBill
		if t.Amount > 0 {
			kind = recurrence.ItemIncome
		}
	}
	key := normalize.MerchantKey(t.Description)
	cat := p.CategoryID
	if cat == "" {
		cat = t.CategoryID
	}
	// the item starts at the transaction's month so its own charge belongs to it
	item, err := s.create(ctx, tx, actor, Params{Kind: kind, Name: normalize.Prettify(t.Description), Key: key, CategoryID: cat, Amount: amount,
		Variable: p.Variable, ToleranceBP: p.ToleranceBP, Schedule: p.Schedule, ActiveFrom: s.activeFor(kind, t.PostedOn), InstallmentsTotal: p.InstallmentsTotal})
	if err != nil {
		return Item{}, err
	}
	// attach matching history
	hist, err := s.history(ctx)
	if err != nil {
		return Item{}, err
	}
	for _, h := range hist {
		if (h.Inflow == (t.Amount > 0)) && item.Matches(h.Description) {
			if _, err = tx.ExecContext(ctx, `INSERT INTO recurring_links (item_id, raw_txn_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, item.ID, h.ID); err != nil {
				return Item{}, err
			}
		}
	}
	return item, nil
}

func (s *Service) activeFor(kind recurrence.ItemKind, posted civil.Date) civil.Date {
	if kind == recurrence.ItemIncome {
		return posted
	}
	if m := s.today().Month(); posted.Month().Before(m) {
		return m.First()
	}
	return posted.Month().First()
}

// EndItem stops an item after a date (a cancelled bill frees its reserve into the pot).
func (s *Service) EndItem(ctx context.Context, tx *sql.Tx, actor events.Actor, id string, on civil.Date) (events.Event, error) {
	res, err := tx.ExecContext(ctx, `UPDATE recurring_items SET ended_on=$2 WHERE id::text=$1 AND deleted_at IS NULL`, id, on.String())
	if err != nil {
		return events.Event{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return events.Event{}, ErrNotFound
	}
	return events.Append(ctx, tx, s.Reg, events.New{Actor: actor, Kind: KindEnd, SubjectType: Subject, SubjectID: id, Payload: End{ItemID: id, EndedOn: on.String()}})
}

// UpdateAmount changes the expected amount (the reserve follows the last actual anyway).
func (s *Service) UpdateAmount(ctx context.Context, tx *sql.Tx, actor events.Actor, id string, amount money.Cents) error {
	if amount < 0 {
		return ErrInvalid
	}
	res, err := tx.ExecContext(ctx, `UPDATE recurring_items SET amount=$2 WHERE id::text=$1 AND deleted_at IS NULL`, id, int64(amount))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	_, err = events.Append(ctx, tx, s.Reg, events.New{Actor: actor, Kind: KindUpdate, SubjectType: Subject, SubjectID: id, Payload: UpdateEvent{ItemID: id, Note: "amount"}})
	return err
}

// Undo reverses a create, end or dismiss.
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
	restore := redo // redo re-applies the original change
	switch v := p.(type) {
	case *Create:
		if restore {
			_, err = tx.ExecContext(ctx, `UPDATE recurring_items SET deleted_at=NULL WHERE id::text=$1`, v.ItemID)
		} else {
			_, err = tx.ExecContext(ctx, `UPDATE recurring_items SET deleted_at=now() WHERE id::text=$1`, v.ItemID)
		}
	case *End:
		if restore {
			_, err = tx.ExecContext(ctx, `UPDATE recurring_items SET ended_on=$2 WHERE id::text=$1`, v.ItemID, v.EndedOn)
		} else {
			_, err = tx.ExecContext(ctx, `UPDATE recurring_items SET ended_on=NULL WHERE id::text=$1`, v.ItemID)
		}
	case *Dismiss:
		if restore {
			_, err = tx.ExecContext(ctx, `INSERT INTO recurring_suppressions (match_key, direction) VALUES ($1,$2) ON CONFLICT DO NOTHING`, v.Key, v.Direction)
		} else {
			_, err = tx.ExecContext(ctx, `DELETE FROM recurring_suppressions WHERE match_key=$1 AND direction=$2`, v.Key, v.Direction)
		}
	}
	return out, err
}
