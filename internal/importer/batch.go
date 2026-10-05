package importer

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/alec-jensen/sofar/internal/civil"
	"github.com/alec-jensen/sofar/internal/events"
	"github.com/alec-jensen/sofar/internal/store"
)

// Event kind for an applied batch (subject type "import_batch").
const (
	KindImportApply    = "import.apply"
	SubjectImportBatch = "import_batch"
)

// ImportApply is the payload of an applied batch.
type ImportApply struct {
	BatchID int64 `json:"batch_id"`
	Added   int   `json:"added"`
	Revived int   `json:"revived"`
}

var (
	// ErrBatchState means the batch cannot be applied in its current state.
	ErrBatchState = errors.New("importer: this import was already applied or undone")
	// ErrNoBatch means no such batch.
	ErrNoBatch = errors.New("importer: no such import")
)

// Service stages, applies and undoes import batches.
type Service struct {
	DB  *sql.DB
	Reg *events.Registry
}

// New builds a Service and registers its event payload.
func New(db *sql.DB, reg *events.Registry) *Service {
	reg.Register(KindImportApply, 1, ImportApply{})
	return &Service{DB: db, Reg: reg}
}

// Outcome says what happens to one row.
type Outcome string

const (
	OutcomeAdd       Outcome = "add"
	OutcomeDuplicate Outcome = "duplicate"
	OutcomeRevive    Outcome = "revive" // exists in an undone batch; applying brings it back
	OutcomeHidden    Outcome = "hidden" // the user hid it earlier; never re-imported
)

// Planned is one row with its provider id, fingerprint and what will happen.
type Planned struct {
	Row         Row     `json:"row"`
	ProviderID  string  `json:"provider_id"`
	Fingerprint string  `json:"fingerprint"`
	Outcome     Outcome `json:"outcome"`
	Reason      string  `json:"reason,omitempty"`
}

// Summary counts a plan.
type Summary struct {
	Total      int    `json:"total"`
	Add        int    `json:"add"`
	Duplicates int    `json:"duplicates"`
	Revive     int    `json:"revive"`
	Hidden     int    `json:"hidden"`
	Skipped    int    `json:"skipped"`
	From       string `json:"from,omitempty"`
	To         string `json:"to,omitempty"`
}

// Batch is a staged or applied import.
type Batch struct {
	ID       int64
	Status   string
	Source   string
	Filename string
	Account  string
	Summary  Summary
	Plan     []Planned
	Skipped  []Skipped
}

type staged struct {
	Rows    []Row     `json:"rows"`
	Skipped []Skipped `json:"skipped"`
}

// plan decides what each parsed row will do against the ledger as it is now.
// It reads only.
func (s *Service) plan(ctx context.Context, q events.DBTX, accountID string, rows []Row) ([]Planned, error) {
	planned := make([]Planned, len(rows))
	occurrence := map[string]int{}
	var ids, fps []string
	for i, r := range rows {
		fp := Fingerprint(accountID, r.Date, r.Amount, r.Description)
		occurrence[fp]++
		id := r.ProviderID
		if id == "" {
			id = "fp:" + fp + "#" + strconv.Itoa(occurrence[fp])
		}
		planned[i] = Planned{Row: r, ProviderID: id, Fingerprint: fp, Outcome: OutcomeAdd}
		ids = append(ids, id)
		fps = append(fps, fp)
	}

	type existing struct {
		fingerprint string
		undone      bool
	}
	byID := map[string]existing{}
	fpCount := map[string]int{}
	rs, err := q.QueryContext(ctx, `
		SELECT r.provider_txn_id, r.fingerprint, coalesce(b.status = 'undone', false)
		FROM raw_transactions r LEFT JOIN import_batches b ON b.id = r.import_batch_id
		WHERE r.account_id = $1 AND (r.provider_txn_id = ANY($2::text[]) OR r.fingerprint = ANY($3::text[]))`,
		accountID, store.TextArray(ids), store.TextArray(fps))
	if err != nil {
		return nil, err
	}
	for rs.Next() {
		var id string
		var e existing
		if err = rs.Scan(&id, &e.fingerprint, &e.undone); err != nil {
			rs.Close()
			return nil, err
		}
		byID[id] = e
		if !e.undone {
			fpCount[e.fingerprint]++
		}
	}
	rs.Close()

	tomb := map[string]bool{}
	ts, err := q.QueryContext(ctx, `SELECT provider_txn_id FROM hidden_tombstones WHERE account_id=$1 AND provider_txn_id = ANY($2::text[])`, accountID, store.TextArray(ids))
	if err != nil {
		return nil, err
	}
	for ts.Next() {
		var id string
		ts.Scan(&id)
		tomb[id] = true
	}
	ts.Close()

	consumed := map[string]int{}
	for i := range planned {
		p := &planned[i]
		switch e, found := byID[p.ProviderID]; {
		case tomb[p.ProviderID]:
			p.Outcome, p.Reason = OutcomeHidden, "you hid this transaction earlier"
		case found && e.undone:
			p.Outcome, p.Reason = OutcomeRevive, "was part of an import you undid"
		case found:
			p.Outcome, p.Reason = OutcomeDuplicate, "already in your ledger"
			if e.fingerprint == p.Fingerprint {
				consumed[p.Fingerprint]++
			}
		case consumed[p.Fingerprint] < fpCount[p.Fingerprint]:
			consumed[p.Fingerprint]++
			p.Outcome, p.Reason = OutcomeDuplicate, "same date, amount and description already exist"
		}
	}
	return planned, nil
}

func summarize(p []Planned, skipped []Skipped) Summary {
	s := Summary{Total: len(p), Skipped: len(skipped)}
	var from, to civil.Date
	for i, r := range p {
		switch r.Outcome {
		case OutcomeAdd:
			s.Add++
		case OutcomeDuplicate:
			s.Duplicates++
		case OutcomeRevive:
			s.Revive++
		case OutcomeHidden:
			s.Hidden++
		}
		if i == 0 || r.Row.Date.Before(from) {
			from = r.Row.Date
		}
		if i == 0 || r.Row.Date.After(to) {
			to = r.Row.Date
		}
	}
	if len(p) > 0 {
		s.From, s.To = from.String(), to.String()
	}
	return s
}

// DryRun stages a parsed file as a batch and reports what applying it would
// do. It writes no transactions, overlays or events.
func (s *Service) DryRun(ctx context.Context, accountID, source, filename string, parsed Parsed) (Batch, error) {
	var exists bool
	if err := s.DB.QueryRowContext(ctx, `SELECT true FROM accounts WHERE id=$1`, accountID).Scan(&exists); err != nil {
		return Batch{}, fmt.Errorf("importer: no such account")
	}
	planned, err := s.plan(ctx, s.DB, accountID, parsed.Rows)
	if err != nil {
		return Batch{}, err
	}
	sum := summarize(planned, parsed.Skipped)
	raw, _ := json.Marshal(staged{Rows: parsed.Rows, Skipped: parsed.Skipped})
	sumJSON, _ := json.Marshal(sum)
	b := Batch{Status: "dry_run", Source: source, Filename: filename, Account: accountID, Summary: sum, Plan: planned, Skipped: parsed.Skipped}
	err = s.DB.QueryRowContext(ctx, `INSERT INTO import_batches (source, filename, account_id, status, summary, parsed) VALUES ($1,$2,$3,'dry_run',$4,$5) RETURNING id`,
		source, filename, accountID, sumJSON, raw).Scan(&b.ID)
	return b, err
}

// Get loads a batch and, for a staged one, re-plans it against the current ledger.
func (s *Service) Get(ctx context.Context, id int64) (Batch, error) {
	var b Batch
	var sumJSON, parsedJSON []byte
	var acct sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT id, status, source, filename, account_id, summary, parsed FROM import_batches WHERE id=$1`, id).Scan(&b.ID, &b.Status, &b.Source, &b.Filename, &acct, &sumJSON, &parsedJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return b, ErrNoBatch
	}
	if err != nil {
		return b, err
	}
	b.Account = acct.String
	json.Unmarshal(sumJSON, &b.Summary)
	if b.Status == "dry_run" && len(parsedJSON) > 0 {
		var st staged
		if err = json.Unmarshal(parsedJSON, &st); err != nil {
			return b, err
		}
		b.Skipped = st.Skipped
		if b.Plan, err = s.plan(ctx, s.DB, b.Account, st.Rows); err != nil {
			return b, err
		}
		b.Summary = summarize(b.Plan, st.Skipped)
	}
	return b, nil
}

// Apply writes a staged batch as one transaction: rows are added (or revived),
// the batch is marked applied, and one event records it. Applying twice
// returns ErrBatchState.
func (s *Service) Apply(ctx context.Context, batchID int64, actor events.Actor) (events.Event, Summary, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return events.Event{}, Summary{}, err
	}
	defer tx.Rollback()
	var status string
	var acct sql.NullString
	var parsedJSON []byte
	err = tx.QueryRowContext(ctx, `SELECT status, account_id, parsed FROM import_batches WHERE id=$1 FOR UPDATE`, batchID).Scan(&status, &acct, &parsedJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return events.Event{}, Summary{}, ErrNoBatch
	}
	if err != nil {
		return events.Event{}, Summary{}, err
	}
	if status != "dry_run" {
		return events.Event{}, Summary{}, ErrBatchState
	}
	var st staged
	if err = json.Unmarshal(parsedJSON, &st); err != nil {
		return events.Event{}, Summary{}, err
	}
	planned, err := s.plan(ctx, tx, acct.String, st.Rows)
	if err != nil {
		return events.Event{}, Summary{}, err
	}
	added, revived := 0, 0
	for _, p := range planned {
		switch p.Outcome {
		case OutcomeAdd:
			payload, _ := json.Marshal(map[string]any{"imported_from": "file", "batch": batchID})
			if _, err = tx.ExecContext(ctx, `
				INSERT INTO raw_transactions (account_id, provider_txn_id, posted_on, amount, description, payee, memo, payload, fingerprint, import_batch_id)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
				acct.String, p.ProviderID, p.Row.Date.String(), int64(p.Row.Amount), p.Row.Description, p.Row.Payee, p.Row.Memo, payload, p.Fingerprint, batchID); err != nil {
				return events.Event{}, Summary{}, err
			}
			added++
		case OutcomeRevive:
			if _, err = tx.ExecContext(ctx, `UPDATE raw_transactions SET import_batch_id=$3 WHERE account_id=$1 AND provider_txn_id=$2`, acct.String, p.ProviderID, batchID); err != nil {
				return events.Event{}, Summary{}, err
			}
			revived++
		}
	}
	sum := summarize(planned, st.Skipped)
	sumJSON, _ := json.Marshal(sum)
	e, err := events.Append(ctx, tx, s.Reg, events.New{Actor: actor, Kind: KindImportApply, SubjectType: SubjectImportBatch, SubjectID: strconv.FormatInt(batchID, 10),
		Payload: ImportApply{BatchID: batchID, Added: added, Revived: revived}, IdempotencyKey: "import:" + strconv.FormatInt(batchID, 10)})
	if err != nil {
		return e, Summary{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE import_batches SET status='applied', summary=$2, parsed=NULL, applied_event_id=$3 WHERE id=$1`, batchID, sumJSON, e.ID); err != nil {
		return e, Summary{}, err
	}
	return e, sum, tx.Commit()
}

// Undo reverses an applied batch: its rows are hidden from every view, never deleted.
func (s *Service) Undo(ctx context.Context, actor events.Actor, eventID int64) (events.Event, error) {
	return s.flip(ctx, eventID, func(tx *sql.Tx) (events.Event, error) { return events.Undo(ctx, tx, actor, eventID, "") }, "undone")
}

// Redo reverses an undo, bringing the batch's rows back.
func (s *Service) Redo(ctx context.Context, actor events.Actor, undoEventID int64) (events.Event, error) {
	return s.flip(ctx, undoEventID, func(tx *sql.Tx) (events.Event, error) { return events.Redo(ctx, tx, actor, undoEventID, "") }, "applied")
}

func (s *Service) flip(ctx context.Context, eventID int64, do func(*sql.Tx) (events.Event, error), status string) (events.Event, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return events.Event{}, err
	}
	defer tx.Rollback()
	target, err := events.Get(ctx, tx, eventID)
	if err != nil {
		return events.Event{}, err
	}
	if target.SubjectType != SubjectImportBatch {
		return events.Event{}, fmt.Errorf("importer: event %d is not an import", eventID)
	}
	e, err := do(tx)
	if err != nil {
		return e, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE import_batches SET status=$2 WHERE id=$1`, target.SubjectID, status); err != nil {
		return e, err
	}
	return e, tx.Commit()
}
