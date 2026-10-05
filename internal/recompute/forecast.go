package recompute

import (
	"context"
	"fmt"
	"math"
	"sort"

	"github.com/alec-jensen/sofar/internal/civil"
	"github.com/alec-jensen/sofar/internal/events"
	"github.com/alec-jensen/sofar/internal/money"
	"github.com/alec-jensen/sofar/internal/recurrence"
	"github.com/alec-jensen/sofar/internal/recurring"
)

// baselineCount is how many occurrences a stream normally has in a month, so a
// month with more is labeled "extra" (S14): 1 for monthly, 2 for biweekly or
// semi-monthly, 4 for weekly.
func baselineCount(s recurrence.Schedule) int {
	n := int(math.Floor(30.4375 / s.IntervalApproxDays()))
	if n < 1 {
		n = 1
	}
	return n
}

// ForecastResult is next month's plan so far (S2 example A, S14).
type ForecastResult struct {
	Month    civil.YearMonth // the month whose income funds next month
	SoFar    money.Cents     // counted income received this month
	Expected money.Cents     // what the schedules predict for the whole month
	Extras   []string        // for example "extra paycheck: acme payroll (3 this month)"
}

// Forecast reports, for a month, the income received so far, the expectation by
// schedule, and which streams have an extra occurrence. It is a forecast only.
func (s *Service) Forecast(ctx context.Context, q events.DBTX, ym civil.YearMonth) (ForecastResult, error) {
	d, err := s.load(ctx, q)
	if err != nil {
		return ForecastResult{}, err
	}
	res := ForecastResult{Month: ym}
	for _, it := range s.classify(d, ym).income {
		res.SoFar += it.Amount
	}
	for _, it := range d.items {
		if it.Kind != recurrence.ItemIncome || !it.ActiveIn(ym) {
			continue
		}
		sch, ok := it.ScheduleAt(ym.Last())
		if !ok {
			continue
		}
		n := sch.ExpectedCount(ym, s.Home)
		res.Expected += it.Amount * money.Cents(n)
		if n > baselineCount(sch) {
			res.Extras = append(res.Extras, fmt.Sprintf("extra paycheck: %s (%d this month)", it.Name, n))
		}
	}
	if res.Expected < res.SoFar {
		res.Expected = res.SoFar
	}
	sort.Strings(res.Extras)
	return res, nil
}

// Suggestion is a proposed destination for part of the pot. It is only ever
// shown; nothing applies it for the user (S2).
type Suggestion struct {
	Stream  string
	Kind    string // extra_occurrence, amount_increase, lump
	Amount  money.Cents
	Suggest string // one_time or permanent
	Label   string
}

// Decompose breaks the income that funded a month into suggestions: an extra
// occurrence of a stream is one-time, a per-deposit increase is permanent, and
// an unscheduled lump is one-time. Suggestions never apply themselves.
func (s *Service) Decompose(ctx context.Context, q events.DBTX, ym civil.YearMonth) ([]Suggestion, error) {
	d, err := s.load(ctx, q)
	if err != nil {
		return nil, err
	}
	src := ym.Prev()
	var out []Suggestion
	counted := map[string]bool{}
	for i := range d.items {
		it := d.items[i]
		if it.Kind != recurrence.ItemIncome || !it.ActiveIn(src) {
			continue
		}
		sch, ok := it.ScheduleAt(src.Last())
		if !ok {
			continue
		}
		var deposits []money.Cents
		for _, inc := range s.classify(d, src).income {
			for _, t := range d.txns {
				if t.ID == inc.ID && d.matches(it, t) && inc.Amount > 0 {
					deposits = append(deposits, inc.Amount)
					counted[inc.ID] = true
				}
			}
		}
		if base := baselineCount(sch); len(deposits) > base {
			extra := len(deposits) - base
			var sum money.Cents
			for _, a := range deposits[base:] {
				sum += a
			}
			out = append(out, Suggestion{Stream: it.Name, Kind: "extra_occurrence", Amount: sum, Suggest: "one_time",
				Label: fmt.Sprintf("%s: +%d occurrence (+%s)", it.Name, extra, dollarsLabel(sum))})
			deposits = deposits[:base]
		}
		for _, a := range deposits {
			if a > it.Amount && (a-it.Amount)*10000 > it.Amount*money.Cents(it.ToleranceBP) {
				out = append(out, Suggestion{Stream: it.Name, Kind: "amount_increase", Amount: a - it.Amount, Suggest: "permanent",
					Label: fmt.Sprintf("%s: each deposit is %s more", it.Name, dollarsLabel(a-it.Amount))})
				break
			}
		}
	}
	for _, inc := range s.classify(d, src).income {
		if !counted[inc.ID] && inc.Amount > 0 {
			out = append(out, Suggestion{Stream: inc.Type, Kind: "lump", Amount: inc.Amount, Suggest: "one_time", Label: fmt.Sprintf("one-time income: %s", dollarsLabel(inc.Amount))})
		}
	}
	return out, nil
}

func dollarsLabel(c money.Cents) string {
	return fmt.Sprintf("$%d", c/100)
}

// Signal is something about a recurring item the user may want to know (alert plumbing is M9).
type Signal struct {
	ItemID string
	Name   string
	Kind   string // bill.missing, bill.ended_check, bill.price_change
	On     civil.Date
	Amount money.Cents
}

// Signals reports missing occurrences past the grace window, items with no
// occurrence for two intervals ("still active?"), and amounts outside tolerance.
func (s *Service) Signals(ctx context.Context, q events.DBTX, today civil.Date) ([]Signal, error) {
	d, err := s.load(ctx, q)
	if err != nil {
		return nil, err
	}
	var out []Signal
	for _, it := range d.items {
		if it.Kind == recurrence.ItemIncome || it.EndedOn != nil && it.EndedOn.Before(today) {
			continue
		}
		sch, ok := it.ScheduleAt(today)
		if !ok {
			continue
		}
		var dates []civil.Date
		var lastAmount money.Cents
		for _, t := range d.txns {
			if t.Hidden || t.Pending || t.Amount >= 0 || !d.matches(it, t) {
				continue
			}
			dates = append(dates, t.PostedOn)
			lastAmount = t.Amount.Abs()
		}
		hit := func(on civil.Date, slack int) bool {
			for _, dt := range dates {
				if gap := dt.DaysSince(on); gap >= -slack && gap <= slack+3 {
					return true
				}
			}
			return false
		}
		graceDays := int(sch.Grace().Hours() / 24)
		occ := sch.Occurrences(it.ActiveFrom, today, s.Home)
		if n := len(occ); n > 0 {
			latest := occ[n-1].Date
			if latest.AddDays(graceDays).Before(today) && !hit(latest, graceDays) {
				out = append(out, Signal{ItemID: it.ID, Name: it.Name, Kind: "bill.missing", On: latest})
			}
		}
		if n := len(occ); n >= 2 {
			a, b := occ[n-1].Date, occ[n-2].Date
			if a.AddDays(graceDays).Before(today) && !hit(a, graceDays) && !hit(b, graceDays) {
				out = append(out, Signal{ItemID: it.ID, Name: it.Name, Kind: "bill.ended_check", On: a})
			}
		}
		if lastAmount > 0 && it.Amount > 0 && !it.Variable {
			diff := lastAmount - it.Amount
			if diff < 0 {
				diff = -diff
			}
			if diff*10000 > it.Amount*money.Cents(it.ToleranceBP) {
				out = append(out, Signal{ItemID: it.ID, Name: it.Name, Kind: "bill.price_change", Amount: lastAmount})
			}
		}
	}
	return out, nil
}

var _ = recurring.Item{}
