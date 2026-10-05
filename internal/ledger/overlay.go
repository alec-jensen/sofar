package ledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/alec-jensen/sofar/internal/civil"
	"github.com/alec-jensen/sofar/internal/events"
	"github.com/alec-jensen/sofar/internal/store"
)

var (
	// ErrNotFound means no such transaction.
	ErrNotFound = errors.New("ledger: no such transaction")
	// ErrSplitSum means split parts do not add up to the transaction total.
	ErrSplitSum = errors.New("ledger: split parts must sum to the transaction total")
	// ErrInvalid means a bad value in a request.
	ErrInvalid = errors.New("ledger: invalid value")
)

// Service reads the ledger and applies user decisions to it.
type Service struct {
	DB  *sql.DB
	Reg *events.Registry
	// Zones are the effective-dated home zone changes; empty means UTC.
	Zones []civil.ZoneChange
}

// New builds a Service and registers the overlay event payloads.
func New(db *sql.DB, reg *events.Registry) *Service {
	Register(reg)
	return &Service{DB: db, Reg: reg}
}

// overlayState is the fold of a transaction's effective overlay events.
type overlayState struct {
	touched        bool
	category       *string
	month          *string
	note           string
	tags           []string
	hidden         bool
	fromDiscretion bool
	typ            string
	reviewedAt     *time.Time
	splits         []SplitPart
}

func (o *overlayState) apply(e events.Event, payload any) {
	o.touched = true
	switch p := payload.(type) {
	case *SetCategory:
		o.category = p.CategoryID
	case *SetNote:
		o.note = p.Note
	case *SetTags:
		o.tags = p.Tags
	case *Hide:
		o.hidden = true
	case *Unhide:
		o.hidden = false
	case *SetMonth:
		o.month = p.Month
	case *Split:
		o.splits = p.Parts
	case *SetFromDiscretionary:
		o.fromDiscretion = p.Value
	case *SetReviewed:
		if p.Reviewed {
			at := e.At
			o.reviewedAt = &at
		} else {
			o.reviewedAt = nil
		}
	case *SetType:
		o.typ = p.Type
	}
}

// Apply appends an overlay event for a transaction and updates the projection.
// payload must be one of the overlay payload structs (SetCategory, ...).
func (s *Service) Apply(ctx context.Context, tx *sql.Tx, actor events.Actor, txnID, kind string, payload any, key string) (events.Event, error) {
	amount, err := s.rawAmount(ctx, tx, txnID)
	if err != nil {
		return events.Event{}, err
	}
	if err = s.validate(ctx, tx, kind, payload, amount); err != nil {
		return events.Event{}, err
	}
	e, err := events.Append(ctx, tx, s.Reg, events.New{Actor: actor, Kind: kind, SubjectType: SubjectTxn, SubjectID: txnID, Payload: payload, IdempotencyKey: key})
	if err != nil {
		return e, err
	}
	if e.Replayed {
		return e, nil
	}
	return e, s.Project(ctx, tx, txnID)
}

func (s *Service) rawAmount(ctx context.Context, q events.DBTX, txnID string) (int64, error) {
	var amount int64
	err := q.QueryRowContext(ctx, `SELECT amount FROM raw_transactions WHERE id=$1`, txnID).Scan(&amount)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return amount, err
}

func (s *Service) validate(ctx context.Context, q events.DBTX, kind string, payload any, amount int64) error {
	switch p := payload.(type) {
	case SetMonth:
		return validMonth(p.Month)
	case *SetMonth:
		return validMonth(p.Month)
	case Split:
		return validSplit(p.Parts, amount)
	case *Split:
		return validSplit(p.Parts, amount)
	}
	return nil
}

func validMonth(m *string) error {
	if m == nil {
		return nil
	}
	if _, err := civil.ParseYearMonth(*m); err != nil {
		return fmt.Errorf("%w: month %q", ErrInvalid, *m)
	}
	return nil
}

func validSplit(parts []SplitPart, total int64) error {
	if len(parts) == 0 {
		return nil
	}
	if len(parts) < 2 {
		return fmt.Errorf("%w: a split needs at least two parts", ErrInvalid)
	}
	var sum int64
	for _, p := range parts {
		if p.CategoryID == "" || p.Amount == 0 {
			return fmt.Errorf("%w: every part needs a category and an amount", ErrInvalid)
		}
		sum += p.Amount
	}
	if sum != total {
		return ErrSplitSum
	}
	return nil
}

// Project rebuilds one transaction's overlay row, splits and hidden tombstone
// from the effective events. Events are the truth; the overlay is a cache.
func (s *Service) Project(ctx context.Context, tx *sql.Tx, txnID string) error {
	evs, err := events.Effective(ctx, tx, events.Filter{SubjectType: SubjectTxn, SubjectID: txnID})
	if err != nil {
		return err
	}
	var st overlayState
	for _, e := range evs {
		p, err := s.Reg.Decode(e)
		if err != nil {
			return err
		}
		st.apply(e, p)
	}
	return s.write(ctx, tx, txnID, st)
}

func (s *Service) write(ctx context.Context, tx *sql.Tx, txnID string, st overlayState) error {
	var accountID, providerID string
	if err := tx.QueryRowContext(ctx, `SELECT account_id, provider_txn_id FROM raw_transactions WHERE id=$1`, txnID).Scan(&accountID, &providerID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM txn_splits WHERE raw_txn_id=$1`, txnID); err != nil {
		return err
	}
	if !st.touched {
		if _, err := tx.ExecContext(ctx, `DELETE FROM transaction_overlays WHERE raw_txn_id=$1`, txnID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM hidden_tombstones WHERE account_id=$1 AND provider_txn_id=$2`, accountID, providerID)
		return err
	}
	var month any
	if st.month != nil {
		ym, err := civil.ParseYearMonth(*st.month)
		if err != nil {
			return err
		}
		month = time.Date(ym.Y, ym.M, 1, 0, 0, 0, 0, time.UTC)
	}
	var typ any
	if st.typ != "" {
		typ = st.typ
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO transaction_overlays (raw_txn_id, category_id, month_override, note, tags, hidden, from_discretionary, type, reviewed_at, updated_at)
		VALUES ($1,$2,$3,$4,$5::text[],$6,$7,$8,$9,now())
		ON CONFLICT (raw_txn_id) DO UPDATE SET category_id=EXCLUDED.category_id, month_override=EXCLUDED.month_override, note=EXCLUDED.note,
		  tags=EXCLUDED.tags, hidden=EXCLUDED.hidden, from_discretionary=EXCLUDED.from_discretionary, type=EXCLUDED.type,
		  reviewed_at=EXCLUDED.reviewed_at, updated_at=now()`,
		txnID, st.category, month, st.note, store.TextArray(st.tags), st.hidden, st.fromDiscretion, typ, st.reviewedAt); err != nil {
		return err
	}
	for i, p := range st.splits {
		if _, err := tx.ExecContext(ctx, `INSERT INTO txn_splits (raw_txn_id, part_no, amount, category_id, tags, note) VALUES ($1,$2,$3,$4,$5::text[],$6)`,
			txnID, i+1, p.Amount, p.CategoryID, store.TextArray(p.Tags), p.Note); err != nil {
			return err
		}
	}
	if st.hidden {
		_, err := tx.ExecContext(ctx, `INSERT INTO hidden_tombstones (account_id, provider_txn_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, accountID, providerID)
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM hidden_tombstones WHERE account_id=$1 AND provider_txn_id=$2`, accountID, providerID)
	return err
}

// Undo reverses an overlay event and refreshes the projection.
func (s *Service) Undo(ctx context.Context, tx *sql.Tx, actor events.Actor, eventID int64, key string) (events.Event, error) {
	u, err := events.Undo(ctx, tx, actor, eventID, key)
	if err != nil || u.Replayed {
		return u, err
	}
	return u, s.projectSubject(ctx, tx, u)
}

// Redo reverses an undo and refreshes the projection.
func (s *Service) Redo(ctx context.Context, tx *sql.Tx, actor events.Actor, undoID int64, key string) (events.Event, error) {
	r, err := events.Redo(ctx, tx, actor, undoID, key)
	if err != nil || r.Replayed {
		return r, err
	}
	return r, s.projectSubject(ctx, tx, r)
}

func (s *Service) projectSubject(ctx context.Context, tx *sql.Tx, e events.Event) error {
	if e.SubjectType != SubjectTxn {
		return nil
	}
	return s.Project(ctx, tx, e.SubjectID)
}

// RebuildAll drops every overlay row and rebuilds them from the events. It
// exists to prove the projection is derivable and to repair after a bug fix.
func (s *Service) RebuildAll(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT subject_id FROM events WHERE subject_type=$1`, SubjectTxn)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if _, err = tx.ExecContext(ctx, `DELETE FROM txn_splits`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM transaction_overlays`); err != nil {
		return err
	}
	for _, id := range ids {
		// A tombstone can outlive its raw row; skip transactions that are gone.
		if err = s.Project(ctx, tx, id); err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
	}
	return nil
}
