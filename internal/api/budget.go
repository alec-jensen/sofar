package api

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/alec-jensen/sofar/internal/budget"
	"github.com/alec-jensen/sofar/internal/civil"
	"github.com/alec-jensen/sofar/internal/events"
	"github.com/alec-jensen/sofar/internal/httpx"
	"github.com/alec-jensen/sofar/internal/ledger"
	"github.com/alec-jensen/sofar/internal/money"
)

const staleAfter = 48 * time.Hour

// freshState recomputes anything dirty and returns a month's stored state.
func (s *server) freshState(ctx context.Context, ym civil.YearMonth) (budget.MonthState, bool, error) {
	if _, err := s.Recompute.Recompute(ctx); err != nil {
		return budget.MonthState{}, false, err
	}
	snap, err := s.Recompute.GetSnapshot(ctx, s.DB, ym)
	if err == sql.ErrNoRows {
		return budget.MonthState{}, false, nil
	}
	return snap.State, err == nil, err
}

func (s *server) currentMonth() civil.YearMonth {
	return civil.DateOf(s.Recompute.Now().In(s.Recompute.Home)).Month()
}

type homeDTO struct {
	Month           string  `json:"month"`
	SpendLeft       int64   `json:"spend_left"`
	PerDay          int64   `json:"per_day"`
	LastDayExtra    int64   `json:"last_day_extra"`
	Learning        bool    `json:"learning"`
	Partial         bool    `json:"partial"`
	Overcommitted   int64   `json:"overcommitted"`
	Uncategorized   int64   `json:"uncategorized"`
	Stale           bool    `json:"stale"`
	DataAsOf        *string `json:"data_as_of"`
	UnreviewedCount int     `json:"unreviewed_count"`
	HasData         bool    `json:"has_data"`
}

// home is the hero: what is safe to spend, computed from the stored snapshot.
func (s *server) home(w http.ResponseWriter, r *http.Request) {
	ym := s.currentMonth()
	state, ok, err := s.freshState(r.Context(), ym)
	if err != nil {
		s.fail(w, err)
		return
	}
	d := homeDTO{Month: ym.String(), HasData: ok, Stale: true}
	if ok {
		d.SpendLeft, d.PerDay, d.LastDayExtra = int64(state.SpendLeft), int64(state.PerDay), int64(state.LastDayExtra)
		d.Learning, d.Partial = state.Learning, state.Partial
		d.Overcommitted, d.Uncategorized = int64(state.Overcommitted), int64(state.Pot)
	}
	var asOf sql.NullTime
	s.DB.QueryRowContext(r.Context(), `SELECT max(last_seen_at) FROM raw_transactions`).Scan(&asOf)
	if asOf.Valid {
		t := asOf.Time.UTC().Format(time.RFC3339)
		d.DataAsOf = &t
		d.Stale = s.Recompute.Now().Sub(asOf.Time) > staleAfter
	}
	page, err := s.Ledger.View(r.Context(), ledger.Filter{Month: &ym})
	if err != nil {
		s.fail(w, err)
		return
	}
	for _, t := range page.Items {
		if !t.Pending && t.ReviewedAt == nil {
			d.UnreviewedCount++
		}
	}
	httpx.Respond(w, d)
}

type entryDTO struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Amount int64  `json:"amount"`
	Ref    string `json:"ref,omitempty"`
}

type explainGroup struct {
	Kind    string     `json:"kind"`
	Label   string     `json:"label"`
	Total   int64      `json:"total"`
	Entries []entryDTO `json:"entries"`
}

type explainLine struct {
	Label  string `json:"label"`
	Amount int64  `json:"amount"`
}

type absorptionDTO struct {
	Amount int64    `json:"amount"`
	From   int64    `json:"from"`
	To     int64    `json:"to"`
	Causes []string `json:"causes"`
}

var kindLabels = map[string]string{
	budget.KindIncome: "last month's income funds this month's plan", budget.KindFundBills: "set aside for bills", budget.KindFundSubs: "set aside for subscriptions",
	budget.KindFundSinking: "set aside for yearly bills", budget.KindFundSavings: "saved", budget.KindFundGoal: "set aside for goals", budget.KindFundEF: "emergency fund",
	budget.KindFundRebuild: "rebuilding the emergency fund", budget.KindFundStanding: "standing lines", budget.KindFundTax: "set aside for taxes",
	budget.KindFundSpending: "your spending money", budget.KindPot: "uncategorized, waiting for you", budget.KindOvercommitted: "set aside beyond the plan",
	budget.KindAllocate: "you moved money", budget.KindTakeBack: "taken back because the plan shrank", budget.KindSpend: "spent",
	budget.KindSweepLeftover: "last month's leftover went to savings", budget.KindSweepPot: "last month's uncategorized went to savings",
	budget.KindSweepUnderage: "unused bill money went to savings", budget.KindCarry: "carried over", budget.KindOpening: "opening balances",
}

func dollars(c money.Cents) string {
	sign := ""
	if c < 0 {
		sign, c = "-", -c
	}
	return fmt.Sprintf("%s$%d.%02d", sign, c/100, c%100)
}

// explain answers "why is my number $X" from the stored entries (S17).
func (s *server) explain(w http.ResponseWriter, r *http.Request) {
	ym, err := civil.ParseYearMonth(r.PathValue("ym"))
	if err != nil {
		httpx.Fail(w, http.StatusBadRequest, "invalid_input", "Month must look like 2026-10.")
		return
	}
	st, ok, err := s.freshState(r.Context(), ym)
	if err != nil {
		s.fail(w, err)
		return
	}
	if !ok {
		httpx.Fail(w, http.StatusNotFound, "not_found", "Nothing to explain for that month yet.")
		return
	}
	groups := map[string]*explainGroup{}
	var order []string
	var spentFromPool money.Cents
	for _, e := range st.Entries {
		g := groups[e.Kind]
		if g == nil {
			g = &explainGroup{Kind: e.Kind, Label: kindLabels[e.Kind], Entries: []entryDTO{}}
			if g.Label == "" {
				g.Label = e.Kind
			}
			groups[e.Kind] = g
			order = append(order, e.Kind)
		}
		g.Total += int64(e.Amount)
		g.Entries = append(g.Entries, entryDTO{string(e.From), string(e.To), int64(e.Amount), e.Ref})
		if e.Kind == budget.KindSpend && e.From == budget.EnvSpendPool {
			spentFromPool += e.Amount
		}
	}
	sort.Strings(order)
	out := make([]explainGroup, 0, len(order))
	for _, k := range order {
		out = append(out, *groups[k])
	}
	// the lines below add up to spend_left, so the screen can show the arithmetic
	lines := []explainLine{{"your spending money this month", int64(st.SpendPool)}}
	if st.Deductions > 0 {
		lines = append(lines, explainLine{"carried in from last month's overspending", -int64(st.Deductions)})
	}
	lines = append(lines, explainLine{"spent so far", -int64(spentFromPool)})

	var sentences []string
	prev := ym.Prev()
	months := []string{"", "january", "february", "march", "april", "may", "june", "july", "august", "september", "october", "november", "december"}
	switch {
	case st.Learning:
		sentences = append(sentences, "sofar is still learning: there is no full month of data yet, so there is no plan.")
	default:
		sentences = append(sentences, fmt.Sprintf("this month's budget came from %s's income: %s.", months[prev.M], dollars(st.Plan)))
	}
	sentences = append(sentences, fmt.Sprintf("%s of it is set aside for fixed costs and savings, leaving %s to plan with.", dollars(st.Fixed), dollars(st.Available)))
	if st.Pot > 0 {
		sentences = append(sentences, fmt.Sprintf("%s is uncategorized: it is waiting for you to decide where it goes.", dollars(st.Pot)))
	}
	if st.Overcommitted > 0 {
		sentences = append(sentences, fmt.Sprintf("fixed costs and spending money add up to %s more than the plan.", dollars(st.Overcommitted)))
	}
	for _, a := range st.Absorption {
		why := ""
		if len(a.Causes) > 0 {
			why = ", " + strings.ToLower(a.Causes[0].Label)
		}
		sentences = append(sentences, fmt.Sprintf("your spending money dropped by %s%s.", dollars(a.Amount), why))
	}
	abs := make([]absorptionDTO, 0, len(st.Absorption))
	for _, a := range st.Absorption {
		d := absorptionDTO{int64(a.Amount), int64(a.From), int64(a.To), []string{}}
		for _, c := range a.Causes {
			d.Causes = append(d.Causes, c.Label)
		}
		abs = append(abs, d)
	}
	httpx.Respond(w, map[string]any{
		"month": ym.String(), "spend_left": int64(st.SpendLeft), "spend_left_lines": lines, "sentences": sentences, "groups": out, "absorption": abs,
		"summary": map[string]int64{"plan": int64(st.Plan), "fixed": int64(st.Fixed), "available": int64(st.Available), "spend_base": int64(st.SpendBase),
			"uncategorized": int64(st.Pot), "spend_pool": int64(st.SpendPool), "overcommitted": int64(st.Overcommitted)},
	})
}

// planOverride sets (or, with null, only records nothing) the plan for a month.
func (s *server) planOverride(w http.ResponseWriter, r *http.Request) {
	ym, err := civil.ParseYearMonth(r.PathValue("ym"))
	var in struct {
		Cents int64  `json:"cents"`
		Note  string `json:"note"`
	}
	if err != nil || httpx.Decode(r, &in) != nil || in.Cents < 0 || len(in.Note) > 500 {
		httpx.Fail(w, http.StatusBadRequest, "invalid_input", "Send a month like 2026-10, a plan in cents and an optional note.")
		return
	}
	var e events.Event
	if !s.withTx(w, r, func(ctx context.Context, tx *sql.Tx) (err error) {
		e, err = s.Recompute.SetPlanOverride(ctx, tx, events.ActorUser, ym, money.Cents(in.Cents), in.Note)
		return
	}) {
		return
	}
	httpx.Respond(w, map[string]any{"event_id": e.ID})
}
