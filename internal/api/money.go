package api

import (
	"context"
	"database/sql"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/alec-jensen/sofar/internal/budget"
	"github.com/alec-jensen/sofar/internal/budgetcfg"
	"github.com/alec-jensen/sofar/internal/civil"
	"github.com/alec-jensen/sofar/internal/events"
	"github.com/alec-jensen/sofar/internal/httpx"
	"github.com/alec-jensen/sofar/internal/money"
	"github.com/alec-jensen/sofar/internal/recompute"
)

// envFor maps an API destination or source name to an envelope.
func (s *server) envFor(ctx context.Context, name string) (budget.EnvelopeID, error) {
	switch name {
	case "spending":
		return budget.EnvSpendPool, nil
	case "savings":
		return budget.EnvSavings, nil
	case "ef":
		return budget.EnvEF, nil
	case "discretionary":
		return budget.EnvDiscretionary, nil
	case "next_month":
		return budget.EnvNextMonth, nil
	case "bills_reserve":
		return budget.EnvBillsReserve, nil
	case "uncovered":
		return budget.EnvUncovered, nil
	}
	if id, ok := strings.CutPrefix(name, "goal:"); ok {
		if _, err := s.Budget.GetGoal(ctx, s.DB, id); err != nil {
			return "", badRequest("unknown goal")
		}
		return budget.Goal(id), nil
	}
	return "", badRequest("unknown destination %q", name)
}

func envName(e budget.EnvelopeID) string {
	switch e {
	case budget.EnvSpendPool:
		return "spending"
	}
	return string(e)
}

func monthParam(w http.ResponseWriter, r *http.Request) (civil.YearMonth, bool) {
	ym, err := civil.ParseYearMonth(r.PathValue("ym"))
	if err != nil {
		httpx.Fail(w, http.StatusBadRequest, "invalid_input", "Month must look like 2026-10.")
		return ym, false
	}
	return ym, true
}

// ---- settings ----

type savingsDTO struct {
	Fixed          int64  `json:"fixed"`
	BasisPoints    int64  `json:"basis_points"`
	UseBasisPoints bool   `json:"use_basis_points"`
	From           string `json:"from,omitempty"`
}

func (s *server) getSavings(w http.ResponseWriter, r *http.Request) {
	vs, err := budgetcfg.Versions(r.Context(), s.DB)
	if err != nil {
		s.fail(w, err)
		return
	}
	rule := budgetcfg.SavingsAt(vs, s.currentMonth())
	httpx.Respond(w, savingsDTO{Fixed: int64(rule.Fixed), BasisPoints: rule.BasisPoints, UseBasisPoints: rule.UseBasisPoints})
}

func (s *server) putSavings(w http.ResponseWriter, r *http.Request) {
	var in savingsDTO
	if httpx.Decode(r, &in) != nil {
		httpx.Fail(w, http.StatusBadRequest, "invalid_input", "Invalid savings settings.")
		return
	}
	from := s.currentMonth()
	if in.From != "" {
		m, err := civil.ParseYearMonth(in.From)
		if err != nil {
			httpx.Fail(w, http.StatusBadRequest, "invalid_input", "From must look like 2026-10.")
			return
		}
		from = m
	}
	var versionID int64
	if !s.withTx(w, r, func(ctx context.Context, tx *sql.Tx) (err error) {
		versionID, err = s.Budget.SetSavings(ctx, tx, events.ActorUser, from, budget.SavingsRule{Fixed: money.Cents(in.Fixed), BasisPoints: in.BasisPoints, UseBasisPoints: in.UseBasisPoints})
		return
	}) {
		return
	}
	httpx.Respond(w, map[string]any{"version_id": versionID})
}

type efDTO struct {
	Months         int    `json:"months"`
	TargetOverride *int64 `json:"target_override"`
	Line           int64  `json:"line"`
	From           string `json:"from,omitempty"`
}

func (s *server) getEF(w http.ResponseWriter, r *http.Request) {
	vs, err := budgetcfg.Versions(r.Context(), s.DB)
	if err != nil {
		s.fail(w, err)
		return
	}
	cfg := budgetcfg.EFAt(vs, s.currentMonth())
	d := efDTO{Months: cfg.Months, Line: int64(cfg.Line)}
	if cfg.TargetOverride != nil {
		v := int64(*cfg.TargetOverride)
		d.TargetOverride = &v
	}
	var target, balance int64
	if st, ok, _ := s.freshState(r.Context(), s.currentMonth()); ok {
		target, balance = int64(st.EFTarget), int64(st.Envelopes[budget.EnvEF])
	}
	httpx.Respond(w, map[string]any{"months": d.Months, "target_override": d.TargetOverride, "line": d.Line, "target": target, "balance": balance})
}

func (s *server) putEF(w http.ResponseWriter, r *http.Request) {
	var in efDTO
	if httpx.Decode(r, &in) != nil {
		httpx.Fail(w, http.StatusBadRequest, "invalid_input", "Invalid emergency fund settings.")
		return
	}
	from := s.currentMonth()
	cfg := budgetcfg.EFConfig{Months: in.Months, Line: money.Cents(in.Line)}
	if in.TargetOverride != nil {
		v := money.Cents(*in.TargetOverride)
		cfg.TargetOverride = &v
	}
	if !s.withTx(w, r, func(ctx context.Context, tx *sql.Tx) error {
		_, err := s.Budget.SetEF(ctx, tx, events.ActorUser, from, cfg)
		return err
	}) {
		return
	}
	httpx.Respond(w, map[string]bool{"ok": true})
}

// ---- goals ----

type goalDTO struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	CategoryID  string  `json:"category_id"`
	Target      int64   `json:"target"`
	Monthly     int64   `json:"monthly"`
	TargetDate  *string `json:"target_date"`
	DerivedDate *string `json:"derived_date"`
	Balance     int64   `json:"balance"`
	Paused      bool    `json:"paused"`
	Done        bool    `json:"done"`
	Borrowed    bool    `json:"borrowed"`
	Priority    int     `json:"priority"`
}

func (s *server) goalDTO(g budgetcfg.Goal, st budget.MonthState, have bool, ym civil.YearMonth) goalDTO {
	v, _ := g.At(ym)
	d := goalDTO{ID: g.ID, Name: g.Name, CategoryID: g.CategoryID, Target: int64(v.Target), Monthly: int64(v.Monthly), TargetDate: v.TargetDate, Paused: v.Paused,
		Done: g.CompletedOn != nil || g.AbandonedOn != nil, Priority: g.Priority}
	if have {
		d.Balance = int64(st.Envelopes[budget.Goal(g.ID)])
		for _, f := range st.Flags {
			if f.Kind == budget.FlagBorrowedGoal && f.Ref == string(budget.Goal(g.ID)) {
				d.Borrowed = true
			}
		}
	}
	if dd, ok := g.DerivedDate(ym, money.Cents(d.Balance)); ok && v.TargetDate == nil {
		x := dd.String()
		d.DerivedDate = &x
	}
	return d
}

func (s *server) listGoals(w http.ResponseWriter, r *http.Request) {
	ym := s.currentMonth()
	st, have, err := s.freshState(r.Context(), ym)
	if err != nil {
		s.fail(w, err)
		return
	}
	goals, err := budgetcfg.ListGoals(r.Context(), s.DB)
	if err != nil {
		s.fail(w, err)
		return
	}
	out := []goalDTO{}
	for _, g := range goals {
		out = append(out, s.goalDTO(g, st, have, ym))
	}
	httpx.Respond(w, map[string]any{"items": out})
}

func (s *server) createGoal(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name       string `json:"name"`
		Target     int64  `json:"target"`
		Monthly    int64  `json:"monthly"`
		TargetDate string `json:"target_date"`
		Priority   int    `json:"priority"`
	}
	if httpx.Decode(r, &in) != nil {
		httpx.Fail(w, http.StatusBadRequest, "invalid_input", "Invalid goal.")
		return
	}
	p := budgetcfg.GoalParams{Name: in.Name, Target: money.Cents(in.Target), Monthly: money.Cents(in.Monthly), Priority: in.Priority}
	if in.TargetDate != "" {
		m, err := civil.ParseYearMonth(in.TargetDate)
		if err != nil {
			httpx.Fail(w, http.StatusBadRequest, "invalid_input", "Date must look like 2027-06.")
			return
		}
		p.TargetDate = &m
	}
	var g budgetcfg.Goal
	if !s.withTx(w, r, func(ctx context.Context, tx *sql.Tx) (err error) {
		g, err = s.Budget.CreateGoal(ctx, tx, events.ActorUser, p)
		return
	}) {
		return
	}
	httpx.RespondStatus(w, http.StatusCreated, s.goalDTO(g, budget.MonthState{}, false, s.currentMonth()))
}

func (s *server) patchGoal(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Target     *int64  `json:"target"`
		Monthly    *int64  `json:"monthly"`
		TargetDate *string `json:"target_date"`
		Paused     *bool   `json:"paused"`
	}
	if httpx.Decode(r, &in) != nil {
		httpx.Fail(w, http.StatusBadRequest, "invalid_input", "Invalid goal.")
		return
	}
	e := budgetcfg.GoalEdit{Paused: in.Paused}
	if in.Target != nil {
		v := money.Cents(*in.Target)
		e.Target = &v
	}
	if in.Monthly != nil {
		v := money.Cents(*in.Monthly)
		e.Monthly = &v
	}
	if in.TargetDate != nil {
		m, err := civil.ParseYearMonth(*in.TargetDate)
		if err != nil {
			httpx.Fail(w, http.StatusBadRequest, "invalid_input", "Date must look like 2027-06.")
			return
		}
		e.TargetDate = &m
	}
	var g budgetcfg.Goal
	if !s.withTx(w, r, func(ctx context.Context, tx *sql.Tx) (err error) {
		g, err = s.Budget.EditGoal(ctx, tx, events.ActorUser, r.PathValue("id"), e)
		return
	}) {
		return
	}
	st, have, _ := s.freshState(r.Context(), s.currentMonth())
	httpx.Respond(w, s.goalDTO(g, st, have, s.currentMonth()))
}

func (s *server) finishGoal(abandon bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in struct{}
		if httpx.Decode(r, &in) != nil {
			httpx.Fail(w, http.StatusBadRequest, "invalid_input", "Invalid request.")
			return
		}
		today := civil.DateOf(s.Recompute.Now().In(s.Recompute.Home))
		if !s.withTx(w, r, func(ctx context.Context, tx *sql.Tx) error {
			if abandon {
				return s.Budget.AbandonGoal(ctx, tx, events.ActorUser, r.PathValue("id"), today)
			}
			return s.Budget.CompleteGoal(ctx, tx, events.ActorUser, r.PathValue("id"), today)
		}) {
			return
		}
		httpx.Respond(w, map[string]bool{"ok": true})
	}
}

// ---- pot, allocations, spend_base ----

func (s *server) record(ctx context.Context, tx *sql.Tx, ym civil.YearMonth, e recompute.BudgetEvent) (events.Event, error) {
	return s.Recompute.RecordEvent(ctx, tx, events.ActorUser, ym, e)
}

type allocationIn struct {
	Dest      string `json:"dest"`
	Amount    int64  `json:"amount"`
	Permanent bool   `json:"permanent"`
}

// allocate sends pot money to destinations, one-time or permanent (S2). Over-allocating is allowed: the app shows it, never refuses.
func (s *server) allocate(w http.ResponseWriter, r *http.Request) {
	ym, ok := monthParam(w, r)
	if !ok {
		return
	}
	var in struct {
		Items []allocationIn `json:"items"`
	}
	if httpx.Decode(r, &in) != nil || len(in.Items) == 0 || len(in.Items) > 20 {
		httpx.Fail(w, http.StatusBadRequest, "invalid_input", "Send one to twenty allocations.")
		return
	}
	var ids []int64
	if !s.withTx(w, r, func(ctx context.Context, tx *sql.Tx) error {
		for _, it := range in.Items {
			dest, err := s.envFor(ctx, it.Dest)
			if err != nil {
				return err
			}
			if it.Amount <= 0 || dest == budget.EnvNextMonth || dest == budget.EnvUncovered || dest == budget.EnvBillsReserve {
				return badRequest("allocations need a positive amount and a spending, savings, goal, ef or discretionary destination")
			}
			e, err := s.record(ctx, tx, ym, recompute.BudgetEvent{Kind: string(budget.EvAllocation), Amount: it.Amount, Dest: string(dest), Permanent: it.Permanent})
			if err != nil {
				return err
			}
			ids = append(ids, e.ID)
		}
		return nil
	}) {
		return
	}
	httpx.Respond(w, map[string]any{"event_ids": ids})
}

// makePermanent converts a past one-time allocation into a standing line from this month (S2).
func (s *server) makePermanent(w http.ResponseWriter, r *http.Request) {
	ym, ok := monthParam(w, r)
	var empty struct{}
	if !ok || httpx.Decode(r, &empty) != nil {
		httpx.Fail(w, http.StatusBadRequest, "invalid_input", "Invalid request.")
		return
	}
	var id int64
	var perr error
	if id, perr = strconv.ParseInt(r.PathValue("event"), 10, 64); perr != nil {
		httpx.Fail(w, http.StatusNotFound, "not_found", "Not found.")
		return
	}
	var out events.Event
	if !s.withTx(w, r, func(ctx context.Context, tx *sql.Tx) error {
		src, err := events.Get(ctx, tx, id)
		if err != nil {
			return err
		}
		p, err := s.Events.Decode(src)
		be, isBudget := p.(*recompute.BudgetEvent)
		if err != nil || !isBudget || be.Kind != string(budget.EvAllocation) || be.Permanent {
			return badRequest("that is not a one-time allocation")
		}
		out, err = s.record(ctx, tx, ym, recompute.BudgetEvent{Kind: recompute.KindStandingAdded, Amount: be.Amount, Dest: be.Dest, Ref: r.PathValue("event")})
		return err
	}) {
		return
	}
	httpx.Respond(w, map[string]any{"event_id": out.ID})
}

func (s *server) removeStanding(w http.ResponseWriter, r *http.Request) {
	ym, ok := monthParam(w, r)
	var empty struct{}
	if !ok || httpx.Decode(r, &empty) != nil {
		httpx.Fail(w, http.StatusBadRequest, "invalid_input", "Invalid request.")
		return
	}
	var out events.Event
	if !s.withTx(w, r, func(ctx context.Context, tx *sql.Tx) (err error) {
		out, err = s.record(ctx, tx, ym, recompute.BudgetEvent{Kind: recompute.KindStandingRemoved, Ref: r.PathValue("event")})
		return
	}) {
		return
	}
	httpx.Respond(w, map[string]any{"event_id": out.ID})
}

func (s *server) editSpendBase(w http.ResponseWriter, r *http.Request) {
	ym, ok := monthParam(w, r)
	var in struct {
		Amount int64 `json:"amount"`
	}
	if !ok || httpx.Decode(r, &in) != nil || in.Amount < 0 {
		httpx.Fail(w, http.StatusBadRequest, "invalid_input", "Send a non-negative amount in cents.")
		return
	}
	var out events.Event
	if !s.withTx(w, r, func(ctx context.Context, tx *sql.Tx) (err error) {
		out, err = s.record(ctx, tx, ym, recompute.BudgetEvent{Kind: string(budget.EvSpendBaseEdit), Amount: in.Amount})
		return
	}) {
		return
	}
	httpx.Respond(w, map[string]any{"event_id": out.ID})
}

func (s *server) unpinSpendBase(w http.ResponseWriter, r *http.Request) {
	ym, ok := monthParam(w, r)
	var empty struct{}
	if !ok || httpx.Decode(r, &empty) != nil {
		httpx.Fail(w, http.StatusBadRequest, "invalid_input", "Invalid request.")
		return
	}
	var out events.Event
	if !s.withTx(w, r, func(ctx context.Context, tx *sql.Tx) (err error) {
		out, err = s.record(ctx, tx, ym, recompute.BudgetEvent{Kind: string(budget.EvPinRemoved)})
		return
	}) {
		return
	}
	httpx.Respond(w, map[string]any{"event_id": out.ID})
}

func (s *server) movedIt(w http.ResponseWriter, r *http.Request) {
	ym, ok := monthParam(w, r)
	var in struct {
		Amount int64 `json:"amount"`
	}
	if !ok || httpx.Decode(r, &in) != nil || in.Amount <= 0 {
		httpx.Fail(w, http.StatusBadRequest, "invalid_input", "Send the amount you moved, in cents.")
		return
	}
	var out events.Event
	if !s.withTx(w, r, func(ctx context.Context, tx *sql.Tx) (err error) {
		out, err = s.record(ctx, tx, ym, recompute.BudgetEvent{Kind: string(budget.EvMovedIt), Amount: in.Amount})
		return
	}) {
		return
	}
	httpx.Respond(w, map[string]any{"event_id": out.ID})
}

type potSuggestion struct {
	Kind   string       `json:"kind"` // make_permanent, restore
	Label  string       `json:"label"`
	Amount int64        `json:"amount"`
	Split  []splitEntry `json:"split,omitempty"`
}

type splitEntry struct {
	Dest   string `json:"dest"`
	Amount int64  `json:"amount"`
}

// pot shows the uncategorized money and what the user can do with it. Suggestions never apply themselves.
func (s *server) pot(w http.ResponseWriter, r *http.Request) {
	ym, ok := monthParam(w, r)
	if !ok {
		return
	}
	st, have, err := s.freshState(r.Context(), ym)
	if err != nil {
		s.fail(w, err)
		return
	}
	if !have {
		httpx.Fail(w, http.StatusNotFound, "not_found", "Nothing for that month yet.")
		return
	}
	prev, prevOK, _ := s.freshState(r.Context(), ym.Prev())
	sugs := []potSuggestion{}
	if st.Pot > 0 && prevOK && prev.Pot == st.Pot {
		// the same pot two months running: suggest making last month's split permanent
		var split []splitEntry
		for _, e := range prev.Entries {
			if e.Kind == budget.KindAllocate && e.From == budget.EnvPot {
				split = append(split, splitEntry{envName(e.To), int64(e.Amount)})
			}
		}
		sugs = append(sugs, potSuggestion{Kind: "make_permanent", Label: "the same amount is uncategorized two months in a row. make it permanent?", Amount: int64(st.Pot), Split: split})
	}
	if st.Pot > 0 {
		if restore := s.restoreAmount(r.Context(), ym, st); restore > 0 {
			sugs = append(sugs, potSuggestion{Kind: "restore", Label: "restore this to spending", Amount: int64(restore), Split: []splitEntry{{"spending", int64(restore)}}})
		}
	}
	httpx.Respond(w, map[string]any{"month": ym.String(), "uncategorized": int64(st.Pot), "allocated": int64(st.PotAllocated), "unallocated": int64(st.PotUnallocated), "suggestions": sugs})
}

// restoreAmount is how much spending money was absorbed earlier and not yet restored (example D).
func (s *server) restoreAmount(ctx context.Context, ym civil.YearMonth, st budget.MonthState) money.Cents {
	for i := 1; i <= 12; i++ {
		past, ok, err := s.freshState(ctx, ym.AddMonths(-i))
		if err != nil || !ok {
			return 0
		}
		if len(past.Absorption) > 0 {
			var before money.Cents
			for _, a := range past.Absorption {
				if a.From > before {
					before = a.From
				}
			}
			if rest := before - st.SpendBase; rest > 0 {
				return money.Min(rest, st.Pot)
			}
			return 0
		}
	}
	return 0
}

// ---- overspend ----

type overspendDTO struct {
	Month   string `json:"month"`
	Gross   int64  `json:"gross"`
	Covers  int64  `json:"covers"`
	Open    int64  `json:"open"`
	Prompt  bool   `json:"prompt"`  // current or previous month with something still open
	Passive bool   `json:"passive"` // older months show a marker and a resolve button, never a prompt (S3)
}

func (s *server) listOverspends(w http.ResponseWriter, r *http.Request) {
	cur := s.currentMonth()
	out := []overspendDTO{}
	for i := 0; i < 12; i++ {
		ym := cur.AddMonths(-i)
		st, ok, err := s.freshState(r.Context(), ym)
		if err != nil {
			s.fail(w, err)
			return
		}
		if !ok || st.OverspendGross == 0 {
			continue
		}
		recent := i <= 1
		out = append(out, overspendDTO{Month: ym.String(), Gross: int64(st.OverspendGross), Covers: int64(st.CoversApplied), Open: int64(st.OverspendOpen),
			Prompt: recent && st.OverspendOpen > 0, Passive: !recent && st.OverspendOpen > 0})
	}
	httpx.Respond(w, map[string]any{"items": out})
}

type coverIn struct {
	Source string `json:"source"`
	Amount int64  `json:"amount"`
}

// cover resolves an overspend from one or more sources, in any split and with no cap (S10).
// Discretionary is never a source (A-008): it is drawn automatically.
func (s *server) cover(w http.ResponseWriter, r *http.Request) {
	ym, ok := monthParam(w, r)
	var in struct {
		Covers        []coverIn `json:"covers"`
		RebuildMonths int       `json:"rebuild_months"`
	}
	if !ok || httpx.Decode(r, &in) != nil || len(in.Covers) == 0 || len(in.Covers) > 10 {
		httpx.Fail(w, http.StatusBadRequest, "invalid_input", "Send one to ten covers.")
		return
	}
	var ids []int64
	if !s.withTx(w, r, func(ctx context.Context, tx *sql.Tx) error {
		for _, c := range in.Covers {
			src, err := s.envFor(ctx, c.Source)
			if err != nil {
				return err
			}
			if c.Amount <= 0 || src == budget.EnvDiscretionary || src == budget.EnvSpendPool {
				return badRequest("a cover needs a positive amount from next month, savings, a goal, the ef, the bills reserve, or uncovered")
			}
			e, err := s.record(ctx, tx, ym, recompute.BudgetEvent{Kind: string(budget.EvCover), Amount: c.Amount, Source: string(src)})
			if err != nil {
				return err
			}
			ids = append(ids, e.ID)
			if src == budget.EnvEF && in.RebuildMonths > 0 {
				// covering from the emergency fund starts a rebuild line (S8)
				e, err = s.record(ctx, tx, ym, recompute.BudgetEvent{Kind: recompute.KindRebuildLine, Amount: c.Amount, Months: in.RebuildMonths})
				if err != nil {
					return err
				}
				ids = append(ids, e.ID)
			}
		}
		return nil
	}) {
		return
	}
	httpx.Respond(w, map[string]any{"event_ids": ids})
}

func (s *server) settle(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Source string `json:"source"`
		Amount int64  `json:"amount"`
	}
	if httpx.Decode(r, &in) != nil || in.Amount <= 0 {
		httpx.Fail(w, http.StatusBadRequest, "invalid_input", "Send a source and an amount in cents.")
		return
	}
	var out events.Event
	if !s.withTx(w, r, func(ctx context.Context, tx *sql.Tx) error {
		src, err := s.envFor(ctx, in.Source)
		if err != nil || src == budget.EnvUncovered || src == budget.EnvNextMonth || src == budget.EnvSpendPool {
			return badRequest("settle from savings, a goal or the ef")
		}
		out, err = s.record(ctx, tx, s.currentMonth(), recompute.BudgetEvent{Kind: string(budget.EvSettle), Amount: in.Amount, Source: string(src)})
		return err
	}) {
		return
	}
	httpx.Respond(w, map[string]any{"event_id": out.ID})
}

// sortedEnvelopes is a helper for stable output.
func sortedEnvelopes(m map[budget.EnvelopeID]money.Cents) []budget.EnvelopeID {
	out := make([]budget.EnvelopeID, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
