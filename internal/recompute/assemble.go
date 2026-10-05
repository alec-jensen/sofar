package recompute

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/alec-jensen/sofar/internal/budget"
	"github.com/alec-jensen/sofar/internal/budgetcfg"
	"github.com/alec-jensen/sofar/internal/civil"
	"github.com/alec-jensen/sofar/internal/events"
	"github.com/alec-jensen/sofar/internal/ledger"
	"github.com/alec-jensen/sofar/internal/money"
	"github.com/alec-jensen/sofar/internal/normalize"
	"github.com/alec-jensen/sofar/internal/recurrence"
	"github.com/alec-jensen/sofar/internal/recurring"
)

// KindStandingRemoved is a budget event kind that removes a standing line
// created by a permanent allocation (Ref is that allocation's event id).
const KindStandingRemoved = "standing_removed"

// Budget event kinds handled outside the engine.
const (
	// KindStandingAdded turns a one-time allocation into a standing line from its month.
	KindStandingAdded = "standing_added"
	// KindRebuildLine spreads an EF withdrawal (Amount) over Months months from the next month.
	KindRebuildLine = "rebuild_line"
	// KindRebuildRemoved deletes a rebuild line (Ref is its event id).
	KindRebuildRemoved = "rebuild_removed"
)

// data is everything a recompute run reads once: all visible transactions,
// recurring items and the budget events of every month.
type data struct {
	kinds  map[string]string
	txns   []ledger.Txn // oldest first
	items  []recurring.Item
	links  map[string]map[string]bool
	cfg    []budgetcfg.Version
	goals  []budgetcfg.Goal
	events map[civil.YearMonth][]eventRow
}

type eventRow struct {
	ev      events.Event
	payload BudgetEvent
}

func (s *Service) load(ctx context.Context, q events.DBTX) (*data, error) {
	d := &data{}
	var err error
	if d.kinds, err = s.accountKinds(ctx, q); err != nil {
		return nil, err
	}
	var all []ledger.Txn
	cursor := ""
	for {
		page, err := s.Ledger.View(ctx, ledger.Filter{Limit: 1000, Cursor: cursor})
		if err != nil {
			return nil, err
		}
		all = append(all, page.Items...)
		if page.Next == "" {
			break
		}
		cursor = page.Next
	}
	for i := len(all) - 1; i >= 0; i-- { // View is newest first
		d.txns = append(d.txns, all[i])
	}
	if d.items, err = recurring.ListItems(ctx, q); err != nil {
		return nil, err
	}
	if d.links, err = recurring.Links(ctx, q); err != nil {
		return nil, err
	}
	if d.cfg, err = budgetcfg.Versions(ctx, q); err != nil {
		return nil, err
	}
	if d.goals, err = budgetcfg.ListGoals(ctx, q); err != nil {
		return nil, err
	}
	evs, err := events.Effective(ctx, q, events.Filter{SubjectType: SubjectMonth, Kinds: []string{KindBudgetEvent}})
	if err != nil {
		return nil, err
	}
	d.events = map[civil.YearMonth][]eventRow{}
	for _, e := range evs {
		p, err := s.Reg.Decode(e)
		if err != nil {
			return nil, err
		}
		ym, err := civil.ParseYearMonth(e.SubjectID)
		if err != nil {
			continue
		}
		d.events[ym] = append(d.events[ym], eventRow{e, *p.(*BudgetEvent)})
	}
	return d, nil
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

// Assemble builds the engine inputs for a month from the database.
func (s *Service) Assemble(ctx context.Context, q events.DBTX, ym civil.YearMonth) (budget.MonthInputs, error) {
	d, err := s.load(ctx, q)
	if err != nil {
		return budget.MonthInputs{}, err
	}
	return s.assemble(ctx, q, d, ym)
}

func (s *Service) assemble(ctx context.Context, q events.DBTX, d *data, ym civil.YearMonth) (budget.MonthInputs, error) {
	in := budget.MonthInputs{Month: ym}
	c := s.classify(d, ym)
	in.Income, in.Outflows = c.income, c.outflows
	prev := s.classify(d, ym.Prev())
	for _, it := range prev.income {
		in.PrevIncome += it.Amount
	}
	in.Lines = s.lines(d, ym)
	in.Lines.Standing = standing(d, ym)
	in.Lines.Savings = budgetcfg.SavingsAt(d.cfg, ym)
	ef := budgetcfg.EFAt(d.cfg, ym)
	in.Lines.EF = budget.Line{ID: "ef", Label: "emergency fund", Amount: ef.Line}
	in.EFMonths, in.EFTargetOverride = ef.Months, ef.TargetOverride
	for _, g := range d.goals {
		if line, ok := g.Line(ym); ok {
			in.Lines.Goals = append(in.Lines.Goals, line)
		}
	}
	in.Lines.Rebuild = rebuildLines(d, ym)
	for i := 1; i <= 3; i++ {
		for _, o := range s.classify(d, ym.AddMonths(-i)).outflows {
			if o.Bucket == budget.BucketSpending || o.Bucket == budget.BucketBills || o.Bucket == budget.BucketSubscriptions {
				in.TrailingOutflow[i-1] += o.Amount
			}
		}
	}

	evs, err := events.Effective(ctx, q, events.Filter{SubjectType: SubjectMonth, SubjectID: ym.String(), Kinds: []string{KindPlanOverride}})
	if err != nil {
		return in, err
	}
	for _, e := range evs {
		p, err := s.Reg.Decode(e)
		if err != nil {
			return in, err
		}
		cents := money.Cents(p.(*PlanOverride).Cents)
		in.PlanOverride = &cents // the latest in force wins
	}
	for _, r := range d.events[ym] {
		switch r.payload.Kind {
		case KindStandingRemoved, KindStandingAdded, KindRebuildLine, KindRebuildRemoved:
		default:
			in.Events = append(in.Events, budget.Event{ID: r.ev.ID, At: r.ev.At, Kind: budget.EventKind(r.payload.Kind), Amount: money.Cents(r.payload.Amount),
				Dest: budget.EnvelopeID(r.payload.Dest), Source: budget.EnvelopeID(r.payload.Source), Permanent: r.payload.Permanent, Ref: r.payload.Ref})
		}
	}
	in.AbsorptionCauses = s.causes(d, ym, in.Lines.Standing)
	if in.Learning, in.Partial, err = s.coverage(ctx, q, ym, d.kinds); err != nil {
		return in, err
	}
	if ym == s.today().Month() {
		t := s.today()
		in.Today = &t
	}
	return in, nil
}

// standing lists the permanent allocations of earlier months that are still in force.
func standing(d *data, ym civil.YearMonth) []budget.StandingLine {
	removed := map[string]bool{}
	for _, rows := range d.events {
		for _, r := range rows {
			if r.payload.Kind == KindStandingRemoved {
				removed[r.payload.Ref] = true
			}
		}
	}
	var out []budget.StandingLine
	for m, rows := range d.events {
		for _, r := range rows {
			id := strconv.FormatInt(r.ev.ID, 10)
			if r.payload.Kind == KindStandingAdded && !m.After(ym) && r.payload.Amount > 0 && !removed[id] {
				out = append(out, budget.StandingLine{ID: "standing:" + id, Label: "standing line", Amount: money.Cents(r.payload.Amount), Dest: budget.EnvelopeID(r.payload.Dest), Since: m})
				continue
			}
			if !m.Before(ym) {
				continue
			}
			if r.payload.Kind != string(budget.EvAllocation) || !r.payload.Permanent || r.payload.Amount <= 0 || budget.EnvelopeID(r.payload.Dest) == budget.EnvSpendPool || removed[id] {
				continue
			}
			out = append(out, budget.StandingLine{ID: "standing:" + id, Label: "standing line", Amount: money.Cents(r.payload.Amount), Dest: budget.EnvelopeID(r.payload.Dest), Since: m})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// rebuildLines spreads each EF withdrawal over its months, starting the month
// after the withdrawal, until the user removes it (S8).
func rebuildLines(d *data, ym civil.YearMonth) []budget.Line {
	removed := map[string]bool{}
	for _, rows := range d.events {
		for _, r := range rows {
			if r.payload.Kind == KindRebuildRemoved {
				removed[r.payload.Ref] = true
			}
		}
	}
	var out []budget.Line
	for m, rows := range d.events {
		for _, r := range rows {
			id := strconv.FormatInt(r.ev.ID, 10)
			if r.payload.Kind != KindRebuildLine || r.payload.Months < 1 || removed[id] {
				continue
			}
			first := m.Next()
			if ym.Before(first) || !ym.Before(first.AddMonths(r.payload.Months)) {
				continue
			}
			per := (money.Cents(r.payload.Amount) + money.Cents(r.payload.Months) - 1) / money.Cents(r.payload.Months)
			out = append(out, budget.Line{ID: "rebuild:" + id, Label: "rebuilding the emergency fund", Amount: per})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// matches reports whether a transaction belongs to an item: attached by hand, or
// the merchant matches and the direction fits the item's kind.
func (d *data) matches(it recurring.Item, t ledger.Txn) bool {
	if d.links[it.ID][t.ID] {
		return true
	}
	if (it.Kind == recurrence.ItemIncome) != (t.Amount > 0) {
		return false
	}
	return it.Matches(t.Description)
}

func (d *data) itemFor(t ledger.Txn, month civil.YearMonth) *recurring.Item {
	for i := range d.items {
		it := &d.items[i]
		if it.ActiveIn(month) && d.matches(*it, t) {
			return it
		}
	}
	return nil
}

type classified struct {
	income   []budget.IncomeItem
	outflows []budget.Outflow
	held     money.Cents // unreviewed deposits kept out of the plan
}

// classify selects the month's counted income and outflows (engine-semantics
// section 2). A transaction is evaluated once and lands in exactly one month,
// so it can count in income at most once (invariant 8).
func (s *Service) classify(d *data, ym civil.YearMonth) classified {
	var out classified
	seq := int64(0)
	for _, t := range d.txns {
		kind := d.kinds[t.AccountID]
		if t.Pending || t.Hidden || (kind != "budget" && kind != "tracked") {
			continue
		}
		if len(t.Splits) > 0 {
			if t.Month != ym {
				continue
			}
			for _, p := range t.Splits {
				seq++
				s.outflow(&out, seq, t, kind, p.Bucket, p.Amount, t.ID+"#"+strconv.Itoa(p.PartNo), nil)
			}
			continue
		}
		reviewed := t.ReviewedAt != nil
		if t.Amount > 0 && t.Bucket == "savings" && kind == "tracked" {
			// a deposit into a savings account verifies planned savings (S7)
			if t.Month == ym {
				seq++
				out.outflows = append(out.outflows, budget.Outflow{ID: t.ID, Seq: seq, Date: t.PostedOn, Bucket: budget.BucketSavings, Amount: t.Amount})
			}
			continue
		}
		if t.Amount > 0 || t.Bucket == "income" {
			month, counts, provisional := s.depositMonth(d, t, reviewed)
			if month != ym {
				continue
			}
			if counts {
				out.income = append(out.income, budget.IncomeItem{ID: t.ID, Amount: t.Amount, Type: t.CategoryID, Provisional: provisional})
			} else if t.Amount > 0 && !reviewed && t.Bucket != "neutral" {
				out.held += t.Amount
			}
			continue
		}
		if t.Month != ym {
			continue
		}
		seq++
		s.outflow(&out, seq, t, kind, t.Bucket, t.Amount, t.ID, d.itemFor(t, t.Month))
	}
	return out
}

// depositMonth decides whether an inflow (or a reversal) counts and in which month (S3, S4).
func (s *Service) depositMonth(d *data, t ledger.Txn, reviewed bool) (month civil.YearMonth, counts, provisional bool) {
	month = t.Month
	var item *recurring.Item
	if t.Amount > 0 {
		item = d.itemFor(t, t.Month)
		if t.MonthOverride == nil && item != nil {
			if sch, ok := item.ScheduleAt(t.PostedOn); ok && sch.IntervalApproxDays() >= 28 {
				if nominal, ok := nearestOccurrence(sch, t.PostedOn); ok {
					month, _ = civil.NominalMonth(t.PostedOn, nominal, int(sch.IntervalApproxDays()))
				}
			}
		}
	}
	switch {
	case t.Bucket == "neutral":
		return month, false, false
	case t.Amount < 0:
		// a reversal or clawback nets against the original deposit's month (S4)
		if t.Bucket != "income" {
			return month, false, false
		}
		if t.MonthOverride == nil {
			if orig, ok := originalDeposit(d, t); ok {
				month = orig.Month
			}
		}
		return month, true, false
	case t.Bucket == "income" && (reviewed || item != nil):
		return month, true, !reviewed
	case item != nil && !reviewed && t.Bucket == "":
		return month, true, true // matches a known schedule: counts provisionally
	}
	return month, false, false
}

// nearestOccurrence finds the scheduled date closest to an actual one.
func nearestOccurrence(sch recurrence.Schedule, actual civil.Date) (civil.Date, bool) {
	occ := sch.Occurrences(actual.AddDays(-12), actual.AddDays(12), nil)
	best, found := civil.Date{}, false
	bestGap := 1 << 30
	for _, o := range occ {
		gap := o.Date.DaysSince(actual)
		if gap < 0 {
			gap = -gap
		}
		if gap < bestGap {
			best, bestGap, found = o.Date, gap, true
		}
	}
	return best, found
}

func originalDeposit(d *data, rev ledger.Txn) (ledger.Txn, bool) {
	key := normalize.MerchantKey(rev.Description)
	if key == "" {
		return ledger.Txn{}, false
	}
	var best ledger.Txn
	found := false
	for _, t := range d.txns {
		if t.ID == rev.ID || t.Amount <= 0 || t.Bucket != "income" || t.Hidden || t.Pending || t.PostedOn.After(rev.PostedOn) || rev.PostedOn.DaysSince(t.PostedOn) > 120 {
			continue
		}
		if t.Amount >= -rev.Amount && normalize.MerchantKey(t.Description) == key && (!found || t.PostedOn.After(best.PostedOn)) {
			best, found = t, true
		}
	}
	return best, found
}

func (s *Service) outflow(out *classified, seq int64, t ledger.Txn, kind, bucket string, amount money.Cents, id string, item *recurring.Item) {
	if amount >= 0 || kind != "budget" {
		return // refunds and credits are netted by links (M7); tracked accounts do not spend
	}
	if bucket == "neutral" {
		return
	}
	o := budget.Outflow{ID: id, Seq: seq, Date: t.PostedOn, Amount: -amount, FromDiscretionary: t.FromDiscretionary, Unreviewed: t.ReviewedAt == nil}
	switch {
	case strings.HasPrefix(t.Type, "goal:"):
		o.Bucket, o.GoalID = budget.BucketGoalPurchase, strings.TrimPrefix(t.Type, "goal:")
	case item != nil && item.Kind != recurrence.ItemIncome:
		o.Bucket, o.LineID = budget.BucketBills, item.ID
		if item.Kind == recurrence.ItemSubscription {
			o.Bucket = budget.BucketSubscriptions
		}
	case bucket == "bills":
		o.Bucket = budget.BucketBills
	case bucket == "subscriptions":
		o.Bucket = budget.BucketSubscriptions
	case bucket == "savings":
		o.Bucket = budget.BucketSavings
	default:
		o.Bucket = budget.BucketSpending
	}
	out.outflows = append(out.outflows, o)
}

// lines builds the engine's fixed lines from recurring items (S5, S6, S19.9).
func (s *Service) lines(d *data, ym civil.YearMonth) budget.FixedLines {
	in := recurring.LineInputs{Home: s.Home, LastActual: map[string]money.Cents{}, Paid: map[string]int{}}
	cutoff := ym.First()
	for _, it := range d.items {
		if it.Kind == recurrence.ItemIncome {
			continue
		}
		var lastDate civil.Date
		for _, t := range d.txns {
			if t.Hidden || t.Pending || t.Amount >= 0 || !t.PostedOn.Before(cutoff) || !d.matches(it, t) {
				continue
			}
			in.Paid[it.ID]++
			if !t.PostedOn.Before(lastDate) {
				lastDate = t.PostedOn
				in.LastActual[it.ID] = t.Amount.Abs()
			}
		}
	}
	return recurring.BuildLines(d.items, ym, in)
}

// causes explains a fall in what is available, for the absorption ledger (S2, S4).
func (s *Service) causes(d *data, ym civil.YearMonth, st []budget.StandingLine) []budget.Cause {
	var out []budget.Cause
	for _, l := range st {
		out = append(out, budget.Cause{Kind: "standing_lines", Label: fmt.Sprintf("standing lines added from the raise in %s", monthName(l.Since)), Amount: l.Amount, Ref: l.ID})
	}
	last, before := s.classify(d, ym.Prev()), s.classify(d, ym.Prev().Prev())
	var a, b money.Cents
	for _, it := range last.income {
		a += it.Amount
	}
	for _, it := range before.income {
		b += it.Amount
	}
	if drop := b - a; drop > 0 {
		expected := func(m civil.YearMonth) (e money.Cents) {
			for _, it := range d.items {
				if it.Kind == recurrence.ItemIncome && it.ActiveIn(m) {
					if sch, ok := it.ScheduleAt(m.Last()); ok {
						e += it.Amount * money.Cents(sch.ExpectedCount(m, s.Home))
					}
				}
			}
			return
		}
		if expected(ym.Prev().Prev())-expected(ym.Prev()) >= drop/2 && expected(ym.Prev().Prev()) > 0 {
			out = append(out, budget.Cause{Kind: "plan_dropped_expected", Label: "the plan dropped as scheduled", Amount: drop})
		} else {
			out = append(out, budget.Cause{Kind: "plan_dropped_unexpected", Label: "a paycheck did not arrive", Amount: drop})
		}
	}
	return out
}

func monthName(ym civil.YearMonth) string {
	return strings.ToLower(ym.M.String()) + " " + strconv.Itoa(ym.Y)
}

// Held is the sum of unreviewed deposits kept out of next month's plan for a month.
func (s *Service) Held(ctx context.Context, q events.DBTX, ym civil.YearMonth) (money.Cents, error) {
	d, err := s.load(ctx, q)
	if err != nil {
		return 0, err
	}
	return s.classify(d, ym).held, nil
}
