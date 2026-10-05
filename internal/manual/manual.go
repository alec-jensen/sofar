// Package manual handles accounts and transactions the user enters by hand
// (S11, S4): add, edit and delete through events, and merge suggestions when a
// synced transaction later matches one. Manual rows are owned by the user, so
// unlike bank rows their fields can change; every change is an event.
package manual

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/alec-jensen/sofar/internal/civil"
	"github.com/alec-jensen/sofar/internal/events"
	"github.com/alec-jensen/sofar/internal/importer"
	"github.com/alec-jensen/sofar/internal/ledger"
	"github.com/alec-jensen/sofar/internal/money"
)

// Event kinds.
const (
	KindAccountCreate = "account.create"
	KindAdd           = "manual.add"
	KindEdit          = "manual.edit"
	SubjectAccount    = "account"

	// IDPrefix marks a manual transaction's provider id.
	IDPrefix = "manual:"
)

// Payloads.
type (
	// AccountCreate records a manual account.
	AccountCreate struct {
		AccountID string `json:"account_id"`
		Name      string `json:"name"`
		Kind      string `json:"kind"`
		Currency  string `json:"currency"`
	}
	// Add records a manual transaction.
	Add struct {
		TxnID       string `json:"txn_id"`
		AccountID   string `json:"account_id"`
		Date        string `json:"date"`
		Amount      int64  `json:"amount"`
		Description string `json:"description"`
		Payee       string `json:"payee,omitempty"`
	}
	// Edit changes some fields of a manual transaction; nil means unchanged.
	Edit struct {
		TxnID       string  `json:"txn_id"`
		Date        *string `json:"date,omitempty"`
		Amount      *int64  `json:"amount,omitempty"`
		Description *string `json:"description,omitempty"`
		Payee       *string `json:"payee,omitempty"`
	}
)

var (
	// ErrInvalid means a bad value.
	ErrInvalid = errors.New("manual: invalid value")
	// ErrNotManual means the transaction was not entered by hand.
	ErrNotManual = errors.New("manual: only transactions you entered yourself can be edited")
	// ErrNotFound means no such transaction or account.
	ErrNotFound = errors.New("manual: not found")
)

// Service manages manual accounts and transactions.
type Service struct {
	DB     *sql.DB
	Reg    *events.Registry
	Ledger *ledger.Service
}

// New builds a Service and registers its event payloads.
func New(db *sql.DB, reg *events.Registry, l *ledger.Service) *Service {
	reg.Register(KindAccountCreate, 1, AccountCreate{})
	reg.Register(KindAdd, 1, Add{})
	reg.Register(KindEdit, 1, Edit{})
	return &Service{DB: db, Reg: reg, Ledger: l}
}

func newID() string {
	var b [8]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// CreateAccount adds a manual account of any kind.
func (s *Service) CreateAccount(ctx context.Context, tx *sql.Tx, actor events.Actor, name, kind, currency string) (string, error) {
	name = strings.Join(strings.Fields(name), " ")
	if name == "" || len(name) > 80 {
		return "", fmt.Errorf("%w: account names are 1 to 80 characters", ErrInvalid)
	}
	switch kind {
	case "budget", "tracked", "balance_only", "liability":
	default:
		return "", fmt.Errorf("%w: unknown account kind %q", ErrInvalid, kind)
	}
	if currency == "" {
		currency = "USD"
	}
	var id string
	err := tx.QueryRowContext(ctx, `INSERT INTO accounts (provider, provider_account_id, name, kind, currency) VALUES ('manual',$1,$2,$3,$4) RETURNING id`, newID(), name, kind, currency).Scan(&id)
	if err != nil {
		return "", err
	}
	_, err = events.Append(ctx, tx, s.Reg, events.New{Actor: actor, Kind: KindAccountCreate, SubjectType: SubjectAccount, SubjectID: id, Payload: AccountCreate{AccountID: id, Name: name, Kind: kind, Currency: currency}})
	return id, err
}

func cleanText(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > max {
		s = s[:max]
	}
	return s
}

// AddTxn adds a manual transaction (negative amount is an outflow) and returns its id.
func (s *Service) AddTxn(ctx context.Context, tx *sql.Tx, actor events.Actor, accountID string, date civil.Date, amount money.Cents, description, payee string) (string, error) {
	description, payee = cleanText(description, 500), cleanText(payee, 500)
	if description == "" || amount == 0 || date.IsZero() {
		return "", fmt.Errorf("%w: a transaction needs a date, a non-zero amount and a description", ErrInvalid)
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT true FROM accounts WHERE id=$1`, accountID).Scan(&exists); err != nil {
		return "", ErrNotFound
	}
	fp := importer.Fingerprint(accountID, date, amount, description)
	var id string
	err := tx.QueryRowContext(ctx, `
		INSERT INTO raw_transactions (account_id, provider_txn_id, posted_on, amount, description, payee, fingerprint, payload)
		VALUES ($1,$2,$3,$4,$5,$6,$7,'{"manual":true}') RETURNING id`,
		accountID, IDPrefix+newID(), date.String(), int64(amount), description, payee, fp).Scan(&id)
	if err != nil {
		return "", err
	}
	_, err = events.Append(ctx, tx, s.Reg, events.New{Actor: actor, Kind: KindAdd, SubjectType: ledger.SubjectTxn, SubjectID: id,
		Payload: Add{TxnID: id, AccountID: accountID, Date: date.String(), Amount: int64(amount), Description: description, Payee: payee}})
	return id, err
}

// EditTxn changes fields of a manual transaction.
func (s *Service) EditTxn(ctx context.Context, tx *sql.Tx, actor events.Actor, txnID string, e Edit) (events.Event, error) {
	if err := s.requireManual(ctx, tx, txnID); err != nil {
		return events.Event{}, err
	}
	e.TxnID = txnID
	if e.Date != nil {
		if _, err := civil.ParseDate(*e.Date); err != nil {
			return events.Event{}, fmt.Errorf("%w: %v", ErrInvalid, err)
		}
	}
	if e.Amount != nil {
		if *e.Amount == 0 {
			return events.Event{}, fmt.Errorf("%w: amount cannot be zero", ErrInvalid)
		}
		var splits int
		tx.QueryRowContext(ctx, `SELECT count(*) FROM txn_splits WHERE raw_txn_id=$1`, txnID).Scan(&splits)
		if splits > 0 {
			return events.Event{}, fmt.Errorf("%w: remove the split before changing the amount", ErrInvalid)
		}
	}
	for _, p := range []**string{&e.Description, &e.Payee} {
		if *p != nil {
			c := cleanText(**p, 500)
			*p = &c
		}
	}
	if e.Description != nil && *e.Description == "" {
		return events.Event{}, fmt.Errorf("%w: description cannot be empty", ErrInvalid)
	}
	ev, err := events.Append(ctx, tx, s.Reg, events.New{Actor: actor, Kind: KindEdit, SubjectType: ledger.SubjectTxn, SubjectID: txnID, Payload: e})
	if err != nil {
		return ev, err
	}
	return ev, s.projectRaw(ctx, tx, txnID)
}

// DeleteTxn removes a manual transaction from every view (a reversible hide).
func (s *Service) DeleteTxn(ctx context.Context, tx *sql.Tx, actor events.Actor, txnID string) (events.Event, error) {
	if err := s.requireManual(ctx, tx, txnID); err != nil {
		return events.Event{}, err
	}
	return s.Ledger.Apply(ctx, tx, actor, txnID, ledger.KindHide, ledger.Hide{TxnID: txnID, Reason: "deleted"}, "")
}

func (s *Service) requireManual(ctx context.Context, tx *sql.Tx, txnID string) error {
	var pid string
	err := tx.QueryRowContext(ctx, `SELECT provider_txn_id FROM raw_transactions WHERE id=$1`, txnID).Scan(&pid)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if !strings.HasPrefix(pid, IDPrefix) {
		return ErrNotManual
	}
	return nil
}

// projectRaw rebuilds a manual transaction's own fields from its add and edit
// events, so undoing an edit restores the earlier values.
func (s *Service) projectRaw(ctx context.Context, tx *sql.Tx, txnID string) error {
	evs, err := events.Effective(ctx, tx, events.Filter{SubjectType: ledger.SubjectTxn, SubjectID: txnID, Kinds: []string{KindAdd, KindEdit}})
	if err != nil {
		return err
	}
	var cur Add
	var have bool
	for _, e := range evs {
		p, err := s.Reg.Decode(e)
		if err != nil {
			return err
		}
		switch v := p.(type) {
		case *Add:
			cur, have = *v, true
		case *Edit:
			if v.Date != nil {
				cur.Date = *v.Date
			}
			if v.Amount != nil {
				cur.Amount = *v.Amount
			}
			if v.Description != nil {
				cur.Description = *v.Description
			}
			if v.Payee != nil {
				cur.Payee = *v.Payee
			}
		}
	}
	if !have {
		return ErrNotFound
	}
	d, err := civil.ParseDate(cur.Date)
	if err != nil {
		return err
	}
	fp := importer.Fingerprint(cur.AccountID, d, money.Cents(cur.Amount), cur.Description)
	_, err = tx.ExecContext(ctx, `UPDATE raw_transactions SET posted_on=$2, amount=$3, description=$4, payee=$5, fingerprint=$6 WHERE id=$1`,
		txnID, d.String(), cur.Amount, cur.Description, cur.Payee, fp)
	return err
}

// Undo reverses a manual or merge event and refreshes what depends on it.
func (s *Service) Undo(ctx context.Context, tx *sql.Tx, actor events.Actor, eventID int64) (events.Event, error) {
	target, err := events.Get(ctx, tx, eventID)
	if err != nil {
		return events.Event{}, err
	}
	u, err := events.Undo(ctx, tx, actor, eventID, "")
	if err != nil {
		return u, err
	}
	return u, s.refresh(ctx, tx, target)
}

// Redo reverses an undo.
func (s *Service) Redo(ctx context.Context, tx *sql.Tx, actor events.Actor, undoID int64) (events.Event, error) {
	target, err := events.Get(ctx, tx, undoID)
	if err != nil {
		return events.Event{}, err
	}
	r, err := events.Redo(ctx, tx, actor, undoID, "")
	if err != nil {
		return r, err
	}
	return r, s.refresh(ctx, tx, target)
}

// refresh reprojects what an undone or redone event touched. target is the
// event that was reversed (for Redo, the undo, which shares its subject).
func (s *Service) refresh(ctx context.Context, tx *sql.Tx, target events.Event) error {
	if target.SubjectType != ledger.SubjectTxn {
		return nil
	}
	// the original event's kind is on the target, or on the reversed event for a redo
	kind := target.Kind
	if kind == events.KindUndo && target.Reverses != nil {
		orig, err := events.Get(ctx, tx, *target.Reverses)
		if err != nil {
			return err
		}
		kind = orig.Kind
		target = orig
	}
	switch kind {
	case KindAdd, KindEdit:
		if err := s.projectRaw(ctx, tx, target.SubjectID); err != nil {
			return err
		}
		return s.Ledger.Project(ctx, tx, target.SubjectID)
	case ledger.KindManualMerge:
		p, err := s.Reg.Decode(target)
		if err != nil {
			return err
		}
		mm := p.(*ledger.ManualMerge)
		if err = s.Ledger.Project(ctx, tx, mm.BankID); err != nil {
			return err
		}
		return s.Ledger.Project(ctx, tx, mm.ManualID)
	}
	return s.Ledger.Project(ctx, tx, target.SubjectID)
}

// Candidate is a suggested merge: a manual transaction and the synced one that
// probably is the same thing.
type Candidate struct {
	ManualID, BankID string
	ManualDate       civil.Date
	BankDate         civil.Date
	Amount           money.Cents
}

// MergeWindowDays is how far apart the dates may be (S11: within 3 days).
const MergeWindowDays = 3

// Suggestions finds manual transactions that a synced transaction in the same
// account now matches (same amount, within 3 days). It only suggests; nothing
// merges on its own (S11).
func (s *Service) Suggestions(ctx context.Context, accountID string) ([]Candidate, error) {
	q := `
	SELECT m.id, b.id, m.posted_on, coalesce(b.transacted_on, b.posted_on), m.amount
	FROM raw_transactions m
	JOIN raw_transactions b ON b.account_id = m.account_id AND b.amount = m.amount AND b.provider_txn_id NOT LIKE 'manual:%'
	WHERE m.provider_txn_id LIKE 'manual:%'
	  AND abs(coalesce(b.transacted_on, b.posted_on) - m.posted_on) <= $1
	  AND ($2 = '' OR m.account_id::text = $2)
	  AND NOT EXISTS (SELECT 1 FROM transaction_overlays o WHERE o.raw_txn_id = m.id AND o.hidden)
	  AND NOT EXISTS (SELECT 1 FROM transaction_overlays o WHERE o.raw_txn_id = b.id AND o.hidden)
	  AND NOT EXISTS (SELECT 1 FROM import_batches ib WHERE ib.id = b.import_batch_id AND ib.status = 'undone')
	ORDER BY abs(coalesce(b.transacted_on, b.posted_on) - m.posted_on), m.id, b.id`
	rows, err := s.DB.QueryContext(ctx, q, MergeWindowDays, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var all []Candidate
	for rows.Next() {
		var c Candidate
		var md, bd time.Time
		var amount int64
		if err = rows.Scan(&c.ManualID, &c.BankID, &md, &bd, &amount); err != nil {
			return nil, err
		}
		c.ManualDate, c.BankDate, c.Amount = civil.DateOf(md), civil.DateOf(bd), money.Cents(amount)
		all = append(all, c)
	}
	// one-to-one, closest dates first (the query is already ordered that way)
	usedM, usedB := map[string]bool{}, map[string]bool{}
	var out []Candidate
	for _, c := range all {
		if usedM[c.ManualID] || usedB[c.BankID] {
			continue
		}
		usedM[c.ManualID], usedB[c.BankID] = true, true
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ManualDate.Before(out[j].ManualDate) })
	return out, rows.Err()
}

// Merge replaces a manual transaction with the synced one it matches: the bank
// row becomes the raw side and inherits the manual row's overlay (category,
// notes, tags, review state), and the manual row stops appearing. Undo reverses it.
func (s *Service) Merge(ctx context.Context, tx *sql.Tx, actor events.Actor, manualID, bankID string) (events.Event, error) {
	if err := s.requireManual(ctx, tx, manualID); err != nil {
		return events.Event{}, err
	}
	var pid, acct, manualAcct string
	if err := tx.QueryRowContext(ctx, `SELECT provider_txn_id, account_id FROM raw_transactions WHERE id=$1`, bankID).Scan(&pid, &acct); err != nil {
		return events.Event{}, ErrNotFound
	}
	if err := tx.QueryRowContext(ctx, `SELECT account_id FROM raw_transactions WHERE id=$1`, manualID).Scan(&manualAcct); err != nil {
		return events.Event{}, ErrNotFound
	}
	if strings.HasPrefix(pid, IDPrefix) || acct != manualAcct {
		return events.Event{}, fmt.Errorf("%w: merge a manual transaction into a synced one in the same account", ErrInvalid)
	}
	e, err := events.Append(ctx, tx, s.Reg, events.New{Actor: actor, Kind: ledger.KindManualMerge, SubjectType: ledger.SubjectTxn, SubjectID: bankID, Payload: ledger.ManualMerge{ManualID: manualID, BankID: bankID}})
	if err != nil {
		return e, err
	}
	if err = s.Ledger.Project(ctx, tx, bankID); err != nil {
		return e, err
	}
	return e, s.Ledger.Project(ctx, tx, manualID)
}
