// Package recompute turns the ledger and events into month snapshots. It
// selects and classifies data, calls the pure budget.ComputeMonth, stores
// snapshots, and walks forward from the earliest dirty month with early
// cutoff. spec: S3, S4, S17, A2, A9.3 invariants 5 and 6.
package recompute

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/alec-jensen/sofar/internal/budget"
	"github.com/alec-jensen/sofar/internal/civil"
	"github.com/alec-jensen/sofar/internal/events"
	"github.com/alec-jensen/sofar/internal/ledger"
	"github.com/alec-jensen/sofar/internal/money"
)

// Event kinds. The subject is ("month", "2026-10").
const (
	KindBudgetEvent  = "budget.event"
	KindPlanOverride = "plan.override"
	SubjectMonth     = "month"
)

// BudgetEvent is the payload of a budget decision.
type BudgetEvent struct {
	Kind      string `json:"kind"`
	Amount    int64  `json:"amount,omitempty"`
	Dest      string `json:"dest,omitempty"`
	Source    string `json:"source,omitempty"`
	Permanent bool   `json:"permanent,omitempty"`
	Ref       string `json:"ref,omitempty"`
}

// PlanOverride sets the effective plan for a month, with a note (S4).
type PlanOverride struct {
	Cents int64  `json:"cents"`
	Note  string `json:"note,omitempty"`
}

// Service computes and stores month snapshots.
type Service struct {
	DB     *sql.DB
	Reg    *events.Registry
	Ledger *ledger.Service
	Home   *time.Location
	Now    func() time.Time
}

// New builds a Service and registers its event payloads.
func New(db *sql.DB, reg *events.Registry, l *ledger.Service, home *time.Location) *Service {
	reg.Register(KindBudgetEvent, 1, BudgetEvent{})
	reg.Register(KindPlanOverride, 1, PlanOverride{})
	if home == nil {
		home = time.UTC
	}
	return &Service{DB: db, Reg: reg, Ledger: l, Home: home, Now: time.Now}
}

func (s *Service) today() civil.Date { return civil.DateOf(s.Now().In(s.Home)) }

func monthDate(ym civil.YearMonth) time.Time {
	return time.Date(ym.Y, ym.M, 1, 0, 0, 0, 0, time.UTC)
}

// SetPlanOverride records a plan override for a month (undo removes it).
func (s *Service) SetPlanOverride(ctx context.Context, tx *sql.Tx, actor events.Actor, ym civil.YearMonth, cents money.Cents, note string) (events.Event, error) {
	return events.Append(ctx, tx, s.Reg, events.New{Actor: actor, Kind: KindPlanOverride, SubjectType: SubjectMonth, SubjectID: ym.String(), Payload: PlanOverride{Cents: int64(cents), Note: note}})
}

// RecordEvent records a budget decision (allocation, spend_base edit, ...) for a month.
func (s *Service) RecordEvent(ctx context.Context, tx *sql.Tx, actor events.Actor, ym civil.YearMonth, e BudgetEvent) (events.Event, error) {
	return events.Append(ctx, tx, s.Reg, events.New{Actor: actor, Kind: KindBudgetEvent, SubjectType: SubjectMonth, SubjectID: ym.String(), Payload: e})
}

// Assemble builds the engine inputs for a month from the database (M3: income
// and spending from the ledger, events, overrides; schedules and lines arrive in M4/M5).
func (s *Service) Assemble(ctx context.Context, q events.DBTX, ym civil.YearMonth) (budget.MonthInputs, error) {
	in := budget.MonthInputs{Month: ym}
	kinds, err := s.accountKinds(ctx, q)
	if err != nil {
		return in, err
	}
	income, outflows, err := s.classify(ctx, ym, kinds)
	if err != nil {
		return in, err
	}
	in.Income, in.Outflows = income, outflows
	prevIncome, _, err := s.classify(ctx, ym.Prev(), kinds)
	if err != nil {
		return in, err
	}
	for _, it := range prevIncome {
		in.PrevIncome += it.Amount
	}
	evs, err := events.Effective(ctx, q, events.Filter{SubjectType: SubjectMonth, SubjectID: ym.String(), Kinds: []string{KindBudgetEvent, KindPlanOverride}})
	if err != nil {
		return in, err
	}
	for _, e := range evs {
		p, err := s.Reg.Decode(e)
		if err != nil {
			return in, err
		}
		switch v := p.(type) {
		case *PlanOverride:
			c := money.Cents(v.Cents)
			in.PlanOverride = &c // the latest in force wins
		case *BudgetEvent:
			in.Events = append(in.Events, budget.Event{ID: e.ID, At: e.At, Kind: budget.EventKind(v.Kind), Amount: money.Cents(v.Amount),
				Dest: budget.EnvelopeID(v.Dest), Source: budget.EnvelopeID(v.Source), Permanent: v.Permanent, Ref: v.Ref})
		}
	}
	in.Learning, in.Partial, err = s.coverage(ctx, q, ym, kinds)
	if err != nil {
		return in, err
	}
	if ym == s.today().Month() {
		t := s.today()
		in.Today = &t
	}
	return in, nil
}

func (s *Service) accountKinds(ctx context.Context, q events.DBTX) (map[string]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, CASE WHEN active THEN kind ELSE 'closed' END FROM accounts`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, kind string
		if err = rows.Scan(&id, &kind); err != nil {
			return nil, err
		}
		out[id] = kind
	}
	return out, rows.Err()
}

// classify selects the month's counted income and outflows.
//
// Income counts when it is a posted, visible inflow in an income category
// that the user has reviewed. Deposits the user has not reviewed are held out
// of the plan (S4) until schedules can vouch for them (M4).
// Outflows are posted, visible, in budget accounts; split transactions
// contribute one outflow per part; neutral categories do not count; an
// uncategorized outflow counts as spending (S11).
func (s *Service) classify(ctx context.Context, ym civil.YearMonth, kinds map[string]string) ([]budget.IncomeItem, []budget.Outflow, error) {
	page, err := s.Ledger.View(ctx, ledger.Filter{Month: &ym})
	if err != nil {
		return nil, nil, err
	}
	var income []budget.IncomeItem
	var outflows []budget.Outflow
	seq := int64(0)
	// oldest first so Seq follows the order things happened
	for i := len(page.Items) - 1; i >= 0; i-- {
		t := page.Items[i]
		kind := kinds[t.AccountID]
		if t.Pending || t.Hidden || (kind != "budget" && kind != "tracked") {
			continue
		}
		if len(t.Splits) > 0 {
			for _, p := range t.Splits {
				seq++
				s.add(&income, &outflows, seq, t, kind, p.Bucket, p.Amount, t.ID+"#"+itoa(p.PartNo), true)
			}
			continue
		}
		seq++
		s.add(&income, &outflows, seq, t, kind, t.Bucket, t.Amount, t.ID, t.ReviewedAt != nil)
	}
	return income, outflows, nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

func (s *Service) add(income *[]budget.IncomeItem, outflows *[]budget.Outflow, seq int64, t ledger.Txn, kind, bucket string, amount money.Cents, id string, reviewed bool) {
	switch bucket {
	case "income":
		// a tracked account's inflow is a deposit and counts too (S4); both need review for now
		if reviewed {
			*income = append(*income, budget.IncomeItem{ID: id, Amount: amount, Type: t.CategoryID})
		}
	case "neutral":
	default:
		if kind != "budget" || amount >= 0 {
			return // refunds and credits are netted by links (M7)
		}
		b := budget.BucketSpending
		switch bucket {
		case "bills":
			b = budget.BucketBills
		case "subscriptions":
			b = budget.BucketSubscriptions
		case "savings":
			b = budget.BucketSavings
		}
		*outflows = append(*outflows, budget.Outflow{ID: id, Seq: seq, Date: t.PostedOn, Bucket: b, Amount: -amount, FromDiscretionary: t.FromDiscretionary, Unreviewed: !reviewed})
	}
}

// coverage decides learning and partial (S18, S13): learning when no budget
// account has data covering the previous full month; partial when only some do.
// M2's account_coverage table refines this with real gaps.
func (s *Service) coverage(ctx context.Context, q events.DBTX, ym civil.YearMonth, kinds map[string]string) (learning, partial bool, err error) {
	rows, err := q.QueryContext(ctx, `
		SELECT a.id, min(r.posted_on) FROM accounts a JOIN raw_transactions r ON r.account_id = a.id
		WHERE a.kind = 'budget' AND a.active GROUP BY a.id`)
	if err != nil {
		return false, false, err
	}
	defer rows.Close()
	prev := ym.Prev().First()
	covered, total := 0, 0
	for rows.Next() {
		var id string
		var earliest time.Time
		if err = rows.Scan(&id, &earliest); err != nil {
			return false, false, err
		}
		total++
		if !civil.DateOf(earliest).After(prev) {
			covered++
		}
	}
	if err = rows.Err(); err != nil {
		return false, false, err
	}
	return covered == 0, covered > 0 && covered < total, nil
}

// ---- snapshots and the recompute walk ----

// Snapshot is a stored month.
type Snapshot struct {
	Month      civil.YearMonth
	State      budget.MonthState
	ComputedAt time.Time
}

// GetSnapshot returns the stored state of a month, or sql.ErrNoRows.
func (s *Service) GetSnapshot(ctx context.Context, q events.DBTX, ym civil.YearMonth) (Snapshot, error) {
	var raw []byte
	var snap Snapshot
	err := q.QueryRowContext(ctx, `SELECT state, computed_at FROM month_snapshots WHERE month=$1 AND engine_version=$2`, monthDate(ym), budget.EngineVersion).Scan(&raw, &snap.ComputedAt)
	if err != nil {
		return snap, err
	}
	snap.Month = ym
	return snap, json.Unmarshal(raw, &snap.State)
}

func (s *Service) storeSnapshot(ctx context.Context, tx *sql.Tx, st budget.MonthState) error {
	raw, err := json.Marshal(st)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO month_snapshots (month, engine_version, input_hash, state, computed_at) VALUES ($1,$2,$3,$4,now())
		ON CONFLICT (month) DO UPDATE SET engine_version=EXCLUDED.engine_version, input_hash=EXCLUDED.input_hash, state=EXCLUDED.state, computed_at=now()`,
		monthDate(st.Month), st.EngineVersion, st.InputHash, raw)
	return err
}

// Result says what a recompute did.
type Result struct {
	Computed []civil.YearMonth
	Stopped  bool // true when early cutoff ended the walk
}

// firstDataMonth is the month of the earliest transaction, or false when there are none.
func (s *Service) firstDataMonth(ctx context.Context, q events.DBTX) (civil.YearMonth, bool, error) {
	var d sql.NullTime
	if err := q.QueryRowContext(ctx, `SELECT min(least(posted_on, coalesce(transacted_on, posted_on))) FROM raw_transactions`).Scan(&d); err != nil || !d.Valid {
		return civil.YearMonth{}, false, err
	}
	return civil.DateOf(d.Time).Month(), true, nil
}

// Recompute walks forward from the earliest dirty month to the current month,
// storing snapshots. It stops early once a month's state is unchanged and the
// next month has no changes of its own (early cutoff). The current month
// always recomputes. All writes happen in one transaction.
func (s *Service) Recompute(ctx context.Context) (Result, error) {
	return s.run(ctx, false, nil)
}

// RecomputeAll recomputes every month from the first one with data, ignoring snapshots.
func (s *Service) RecomputeAll(ctx context.Context) (Result, error) {
	return s.run(ctx, true, nil)
}

// RecomputeFrom recomputes from a given month forward.
func (s *Service) RecomputeFrom(ctx context.Context, from civil.YearMonth) (Result, error) {
	return s.run(ctx, false, &from)
}

func (s *Service) run(ctx context.Context, all bool, from *civil.YearMonth) (Result, error) {
	var res Result
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return res, err
	}
	defer tx.Rollback()
	// serialize recomputes
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(731892)`); err != nil {
		return res, err
	}
	first, any, err := s.firstDataMonth(ctx, tx)
	if err != nil || !any {
		return res, err
	}
	current := s.today().Month()
	if current.Before(first) {
		current = first
	}
	dirty, err := s.dirtySet(ctx, tx)
	if err != nil {
		return res, err
	}
	start := first
	switch {
	case all:
	case from != nil:
		start = *from
	default:
		earliest := current
		for m := range dirty {
			if m.Before(earliest) {
				earliest = m
			}
		}
		start = earliest
		if !s.snapshotsCurrent(ctx, tx, first, current) {
			start = first
		}
	}
	if start.Before(first) {
		start = first
	}

	var prev *budget.MonthState
	if start != first {
		snap, err := s.GetSnapshot(ctx, tx, start.Prev())
		switch {
		case err == nil:
			prev = &snap.State
		case errors.Is(err, sql.ErrNoRows):
			start = first // no stored predecessor: walk from the beginning
		default:
			return res, err
		}
	}
	for m := start; !current.Before(m); m = m.Next() {
		in, err := s.Assemble(ctx, tx, m)
		if err != nil {
			return res, err
		}
		state := budget.ComputeMonth(prev, in)
		old, oerr := s.GetSnapshot(ctx, tx, m)
		changed := oerr != nil || old.State.InputHash != state.InputHash
		if changed {
			if err = s.storeSnapshot(ctx, tx, state); err != nil {
				return res, err
			}
		}
		res.Computed = append(res.Computed, m)
		prev = &state
		if !all && !changed && m != current && m.Next() != current && !dirty[m.Next()] {
			// early cutoff: nothing downstream changed, so skip to the current
			// month, which always recomputes (per-day depends on today)
			snap, err := s.GetSnapshot(ctx, tx, current.Prev())
			if err != nil {
				return res, err
			}
			res.Stopped = true
			prev = &snap.State
			m = current.Prev()
		}
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM dirty_months`); err != nil {
		return res, err
	}
	return res, tx.Commit()
}

// snapshotsCurrent reports whether every month in [first, current] has a snapshot of this engine version.
func (s *Service) snapshotsCurrent(ctx context.Context, q events.DBTX, first, current civil.YearMonth) bool {
	var n int
	if q.QueryRowContext(ctx, `SELECT count(*) FROM month_snapshots WHERE month BETWEEN $1 AND $2 AND engine_version=$3`, monthDate(first), monthDate(current), budget.EngineVersion).Scan(&n) != nil {
		return false
	}
	want := (current.Y*12 + int(current.M)) - (first.Y*12 + int(first.M)) + 1
	return n == want
}

func (s *Service) dirtySet(ctx context.Context, q events.DBTX) (map[civil.YearMonth]bool, error) {
	rows, err := q.QueryContext(ctx, `SELECT month FROM dirty_months`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[civil.YearMonth]bool{}
	for rows.Next() {
		var d time.Time
		if err = rows.Scan(&d); err != nil {
			return nil, err
		}
		out[civil.DateOf(d).Month()] = true
	}
	return out, rows.Err()
}
