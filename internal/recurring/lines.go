// Package recurring stores recurring items (income streams, bills,
// subscriptions, payment plans), detects candidates, and turns active items
// into the engine's fixed lines and income expectations. spec: S5, S6, S14, S19.9.
package recurring

import (
	"time"

	"github.com/alec-jensen/sofar/internal/budget"
	"github.com/alec-jensen/sofar/internal/civil"
	"github.com/alec-jensen/sofar/internal/money"
	"github.com/alec-jensen/sofar/internal/normalize"
	"github.com/alec-jensen/sofar/internal/recurrence"
)

// Item is one recurring thing.
type Item struct {
	ID                string
	Kind              recurrence.ItemKind
	Name              string
	Key               string // normalized merchant
	CategoryID        string
	Amount            money.Cents // expected amount per occurrence, positive
	Variable          bool
	ToleranceBP       int
	Schedules         []recurrence.Schedule // effective-dated versions
	ActiveFrom        civil.Date
	EndedOn           *civil.Date
	InstallmentsTotal int // payment plans: number of installments
}

// ActiveIn reports whether the item has anything to reserve or expect in a month.
func (it Item) ActiveIn(ym civil.YearMonth) bool {
	if it.ActiveFrom.After(ym.Last()) {
		return false
	}
	return it.EndedOn == nil || !it.EndedOn.Before(ym.First())
}

// ScheduleAt is the schedule version in force at a date (the latest one that has started).
func (it Item) ScheduleAt(d civil.Date) (recurrence.Schedule, bool) {
	var best recurrence.Schedule
	found := false
	for _, s := range it.Schedules {
		if !s.EffectiveFrom.After(d) && (!found || !s.EffectiveFrom.Before(best.EffectiveFrom)) {
			best, found = s, true
		}
	}
	if !found && len(it.Schedules) > 0 {
		return it.Schedules[0], true
	}
	return best, found
}

// Matches reports whether a transaction's description belongs to this item.
func (it Item) Matches(description string) bool {
	k := normalize.MerchantKey(description)
	return k != "" && (k == it.Key || normalize.Matches(k, it.Key))
}

// Monthly reports whether the item is reserved month by month (intervals up to
// about a month); longer ones are set aside gradually in a sinking envelope (S5).
func (it Item) Monthly(at civil.Date) bool {
	s, ok := it.ScheduleAt(at)
	return !ok || s.IntervalApproxDays() <= 35
}

// LineInputs carries what the pure line builder needs from the ledger.
type LineInputs struct {
	Home *time.Location
	// LastActual is the most recent charge matched to each item before the month.
	LastActual map[string]money.Cents
	// Paid is how many installments of each payment plan were paid before the month.
	Paid map[string]int
}

func (in LineInputs) amount(it Item) money.Cents {
	if a, ok := in.LastActual[it.ID]; ok && a > 0 {
		return a // S5: the last actual amount, else the expected one
	}
	return it.Amount
}

// BuildLines converts the items active in a month into engine lines (S5, S6, S19.9).
func BuildLines(items []Item, ym civil.YearMonth, in LineInputs) budget.FixedLines {
	var out budget.FixedLines
	for _, it := range items {
		if it.Kind == recurrence.ItemIncome || !it.ActiveIn(ym) {
			continue
		}
		s, ok := it.ScheduleAt(ym.Last())
		if !ok {
			continue
		}
		amt := in.amount(it)
		line := budget.Line{ID: it.ID, Label: it.Name}
		if it.Kind == recurrence.ItemPaymentPlan {
			remaining := it.InstallmentsTotal - in.Paid[it.ID]
			if remaining <= 0 {
				continue
			}
			n := s.ExpectedCount(ym, in.Home)
			if n > remaining {
				n = remaining
			}
			line.Amount = amt * money.Cents(n)
			if line.Amount > 0 {
				out.PaymentPlans = append(out.PaymentPlans, line)
			}
			continue
		}
		if it.Monthly(ym.Last()) {
			line.Amount = amt * money.Cents(s.ExpectedCount(ym, in.Home))
			if line.Amount <= 0 {
				continue
			}
			if it.Kind == recurrence.ItemSubscription {
				out.Subscriptions = append(out.Subscriptions, line)
			} else {
				out.Bills = append(out.Bills, line)
			}
			continue
		}
		line.Amount = sinkingSetAside(it, s, amt, ym, in.Home)
		if line.Amount > 0 {
			out.Sinking = append(out.Sinking, line)
		}
	}
	return out
}

// sinkingSetAside is the monthly set-aside for a non-monthly item (S5): amount
// divided by the months in the period for quarterly and annual, otherwise
// amount * 30.4375 / interval days. The first month catches up: the amount
// divided by the months until it is due.
func sinkingSetAside(it Item, s recurrence.Schedule, amt money.Cents, ym civil.YearMonth, home *time.Location) money.Cents {
	if ym == it.ActiveFrom.Month() {
		if next := s.Occurrences(ym.First(), ym.First().AddDays(800), home); len(next) > 0 {
			due := next[0].Date.Month()
			months := (due.Y*12 + int(due.M)) - (ym.Y*12 + int(ym.M))
			if months < 1 {
				months = 1
			}
			return amt / money.Cents(months)
		}
	}
	if s.Kind == recurrence.KindCalendar && s.Calendar.EveryMonths > 1 {
		return amt / money.Cents(s.Calendar.EveryMonths)
	}
	days := int64(s.IntervalApproxDays() + 0.5)
	if days <= 0 {
		return 0
	}
	return money.Cents(int64(amt) * 304375 / (10000 * days)) // amount * 30.4375 / interval days, in integers
}
