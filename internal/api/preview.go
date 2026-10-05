package api

import (
	"net/http"
	"sort"
	"time"

	"github.com/alec-jensen/sofar/internal/budget"
	"github.com/alec-jensen/sofar/internal/budgetcfg"
	"github.com/alec-jensen/sofar/internal/civil"
	"github.com/alec-jensen/sofar/internal/httpx"
	"github.com/alec-jensen/sofar/internal/money"
)

type figures struct {
	Plan          int64 `json:"plan"`
	Fixed         int64 `json:"fixed"`
	SpendBase     int64 `json:"spend_base"`
	Uncategorized int64 `json:"uncategorized"`
	Overcommitted int64 `json:"overcommitted"`
	SpendLeft     int64 `json:"spend_left"`
	PerDay        int64 `json:"per_day"`
	EFTarget      int64 `json:"ef_target"`
}

func figuresOf(st budget.MonthState) figures {
	return figures{int64(st.Plan), int64(st.Fixed), int64(st.SpendBase), int64(st.Pot), int64(st.Overcommitted), int64(st.SpendLeft), int64(st.PerDay), int64(st.EFTarget)}
}

type goalFigure struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Before      int64   `json:"balance_before"`
	After       int64   `json:"balance_after"`
	DerivedDate *string `json:"derived_date_after"`
}

// preview runs a proposed change in memory and returns before and after. It writes nothing (S19.8).
func (s *server) preview(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Month        string         `json:"month"`
		PlanOverride *int64         `json:"plan_override"`
		SpendBase    *int64         `json:"spend_base"`
		Allocations  []allocationIn `json:"allocations"`
		Savings      *savingsDTO    `json:"savings"`
	}
	if httpx.Decode(r, &in) != nil || len(in.Allocations) > 20 {
		httpx.Fail(w, http.StatusBadRequest, "invalid_input", "Invalid preview.")
		return
	}
	ym := s.currentMonth()
	if in.Month != "" {
		m, err := civil.ParseYearMonth(in.Month)
		if err != nil {
			httpx.Fail(w, http.StatusBadRequest, "invalid_input", "Month must look like 2026-10.")
			return
		}
		ym = m
	}
	if _, err := s.Recompute.Recompute(r.Context()); err != nil {
		s.fail(w, err)
		return
	}
	var bad error
	res, err := s.Recompute.Preview(r.Context(), ym, func(mi *budget.MonthInputs) {
		if in.PlanOverride != nil {
			v := money.Cents(*in.PlanOverride)
			mi.PlanOverride = &v
		}
		base := time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)
		n := int64(1 << 60)
		add := func(e budget.Event) {
			n++
			e.ID, e.At = n, base.Add(time.Duration(n)*time.Second)
			mi.Events = append(mi.Events, e)
		}
		if in.SpendBase != nil {
			add(budget.Event{Kind: budget.EvSpendBaseEdit, Amount: money.Cents(*in.SpendBase)})
		}
		for _, a := range in.Allocations {
			dest, derr := s.envFor(r.Context(), a.Dest)
			if derr != nil || a.Amount <= 0 {
				bad = badRequest("each allocation needs a valid destination and a positive amount")
				return
			}
			add(budget.Event{Kind: budget.EvAllocation, Dest: dest, Amount: money.Cents(a.Amount), Permanent: a.Permanent})
		}
		if in.Savings != nil {
			mi.Lines.Savings = budget.SavingsRule{Fixed: money.Cents(in.Savings.Fixed), BasisPoints: in.Savings.BasisPoints, UseBasisPoints: in.Savings.UseBasisPoints}
		}
	})
	if bad != nil {
		s.fail(w, bad)
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	goals, _ := budgetcfg.ListGoals(r.Context(), s.DB)
	gf := []goalFigure{}
	for _, g := range goals {
		id := budget.Goal(g.ID)
		f := goalFigure{ID: g.ID, Name: g.Name, Before: int64(res.Before.Envelopes[id]), After: int64(res.After.Envelopes[id])}
		if d, ok := g.DerivedDate(ym, res.After.Envelopes[id]); ok {
			x := d.String()
			f.DerivedDate = &x
		}
		gf = append(gf, f)
	}
	sort.Slice(gf, func(i, j int) bool { return gf[i].ID < gf[j].ID })
	httpx.Respond(w, map[string]any{
		"month":  ym.String(),
		"before": figuresOf(res.Before), "after": figuresOf(res.After),
		"next_before": figuresOf(res.NextBefore), "next_after": figuresOf(res.NextAfter),
		"goals": gf,
	})
}

type checklistItem struct {
	Label  string `json:"label"`
	Kind   string `json:"kind"`
	Amount int64  `json:"amount"`
}

// checklist lists the transfers to make to match what the plan credited to
// savings, goals and the emergency fund this month (S19.11). A matching
// transfer verifies it automatically; "i moved it" confirms by hand.
func (s *server) checklist(w http.ResponseWriter, r *http.Request) {
	ym := s.currentMonth()
	st, ok, err := s.freshState(r.Context(), ym)
	if err != nil {
		s.fail(w, err)
		return
	}
	items := []checklistItem{}
	if ok {
		goals, _ := budgetcfg.ListGoals(r.Context(), s.DB)
		names := map[string]string{}
		for _, g := range goals {
			names[string(budget.Goal(g.ID))] = g.Name
		}
		for _, e := range st.Entries {
			switch e.Kind {
			case budget.KindFundSavings:
				items = append(items, checklistItem{"general savings", "savings", int64(e.Amount)})
			case budget.KindFundGoal:
				items = append(items, checklistItem{"goal: " + names[string(e.To)], "goal", int64(e.Amount)})
			case budget.KindFundEF, budget.KindFundRebuild:
				items = append(items, checklistItem{"emergency fund", "ef", int64(e.Amount)})
			}
		}
	}
	var accts []map[string]string
	if rows, err := s.DB.QueryContext(r.Context(), `SELECT id, name FROM accounts WHERE active AND kind IN ('tracked','balance_only') ORDER BY name`); err == nil {
		for rows.Next() {
			var id, name string
			rows.Scan(&id, &name)
			accts = append(accts, map[string]string{"id": id, "name": name})
		}
		rows.Close()
	}
	if accts == nil {
		accts = []map[string]string{}
	}
	planned, verified := int64(st.SavingsPlanned), int64(st.SavingsVerified)
	remaining := planned - verified
	if remaining < 0 {
		remaining = 0
	}
	httpx.Respond(w, map[string]any{"month": ym.String(), "items": items, "planned": planned, "verified": verified, "remaining": remaining, "accounts": accts})
}
