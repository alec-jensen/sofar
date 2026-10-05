// Package recurrence holds the schedule model: when recurring things (income,
// bills, subscriptions, payment plans) are expected, and how to detect them.
// spec: S14, S5, S6, S3 boundary attribution. Day-multiple intervals use
// calendar arithmetic in the home zone; hour and minute intervals use absolute
// time, so DST never drifts either.
package recurrence

import (
	"errors"
	"sort"
	"time"

	"github.com/alec-jensen/sofar/internal/civil"
)

// Kind is how a schedule repeats.
type Kind string

const (
	// KindInterval repeats every fixed duration.
	KindInterval Kind = "interval"
	// KindCalendar follows a calendar rule.
	KindCalendar Kind = "calendar"
)

// CalendarKind is a calendar rule.
type CalendarKind string

const (
	// Monthly is on a day of the month (31 clamps to the month's end).
	Monthly CalendarKind = "monthly"
	// SemiMonthly is on two days of the month (default the 1st and 15th).
	SemiMonthly CalendarKind = "semi_monthly"
	// NthWeekday is the nth (or -1 for last) weekday of the month.
	NthWeekday CalendarKind = "nth_weekday"
	// LastBusinessDay is the last weekday (Mon to Fri) of the month.
	LastBusinessDay CalendarKind = "last_business_day"
)

// CalendarRule parameterizes a calendar schedule.
type CalendarRule struct {
	Kind    CalendarKind
	Day     int          // Monthly: 1..31
	Days    [2]int       // SemiMonthly: two days, ascending (default 1 and 15)
	Nth     int          // NthWeekday: 1..4, or -1 for the last
	Weekday time.Weekday // NthWeekday
	// EveryMonths repeats every n months (default 1; 3 is quarterly, 12 annual),
	// counted from Anchor's month. Only Monthly honors it.
	EveryMonths int
}

// Schedule says when something recurs.
type Schedule struct {
	Kind Kind

	// IntervalDays is for day-multiple intervals (7, 13, 21). Zero when the
	// interval is a Duration.
	IntervalDays int
	// IntervalDur is for hour and minute intervals, in absolute time.
	IntervalDur time.Duration
	// Anchor is an occurrence date (day-multiple intervals and the base month of
	// a calendar rule). AnchorTime is the occurrence instant for Duration intervals.
	Anchor     civil.Date
	AnchorTime time.Time
	Calendar   CalendarRule

	// GraceDays overrides the default grace (25% of the interval, at most 3 days).
	GraceDays *int
	// EffectiveFrom is when this version of the schedule starts applying.
	EffectiveFrom civil.Date
}

var (
	// ErrInvalid means the schedule is not usable.
	ErrInvalid = errors.New("recurrence: invalid schedule")
)

// Validate checks that the schedule can produce occurrences.
func (s Schedule) Validate() error {
	switch s.Kind {
	case KindInterval:
		if s.IntervalDays <= 0 && s.IntervalDur <= 0 {
			return ErrInvalid
		}
		if s.IntervalDays > 0 && s.Anchor.IsZero() {
			return ErrInvalid
		}
		if s.IntervalDur > 0 && s.AnchorTime.IsZero() {
			return ErrInvalid
		}
	case KindCalendar:
		c := s.Calendar
		switch c.Kind {
		case Monthly:
			if c.Day < 1 || c.Day > 31 {
				return ErrInvalid
			}
		case SemiMonthly:
			if c.Days[0] != 0 && (c.Days[0] < 1 || c.Days[1] > 31 || c.Days[0] >= c.Days[1]) {
				return ErrInvalid
			}
		case NthWeekday:
			if c.Nth == 0 || c.Nth < -1 || c.Nth > 4 {
				return ErrInvalid
			}
		case LastBusinessDay:
		default:
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}

// Occurrence is one expected occurrence.
type Occurrence struct {
	Date civil.Date
	// At is set for absolute-time intervals: the instant, before conversion to the home zone.
	At time.Time
}

// IntervalApproxDays is the schedule's typical length in days, for grace and
// tolerance calculations.
func (s Schedule) IntervalApproxDays() float64 {
	switch {
	case s.Kind == KindInterval && s.IntervalDays > 0:
		return float64(s.IntervalDays)
	case s.Kind == KindInterval:
		return s.IntervalDur.Hours() / 24
	}
	switch s.Calendar.Kind {
	case SemiMonthly:
		return 15.2
	default:
		months := s.Calendar.EveryMonths
		if months < 1 {
			months = 1
		}
		return 30.4375 * float64(months)
	}
}

// Grace is how long past an expected occurrence it may be before it counts as
// missing: 25% of the interval, capped at 3 days, unless overridden (S14, S5).
func (s Schedule) Grace() time.Duration {
	if s.GraceDays != nil {
		return time.Duration(*s.GraceDays) * 24 * time.Hour
	}
	g := time.Duration(s.IntervalApproxDays() * 0.25 * float64(24*time.Hour))
	if limit := 3 * 24 * time.Hour; g > limit {
		g = limit
	}
	return g
}

// BoundaryTolerance is S3's attribution window: min(5 days, interval/4).
func (s Schedule) BoundaryTolerance() int {
	t := int(s.IntervalApproxDays() / 4)
	if t > 5 {
		t = 5
	}
	return t
}

func (s Schedule) location(home *time.Location) *time.Location {
	if home == nil {
		return time.UTC
	}
	return home
}

// Occurrences lists expected occurrences with from <= date <= to, in order.
// Only dates on or after EffectiveFrom are produced.
func (s Schedule) Occurrences(from, to civil.Date, home *time.Location) []Occurrence {
	if s.Validate() != nil || to.Before(from) {
		return nil
	}
	if !s.EffectiveFrom.IsZero() && from.Before(s.EffectiveFrom) {
		from = s.EffectiveFrom
		if to.Before(from) {
			return nil
		}
	}
	var out []Occurrence
	switch {
	case s.Kind == KindInterval && s.IntervalDays > 0:
		n := s.IntervalDays
		d := s.Anchor.AddDays(floorDiv(from.DaysSince(s.Anchor), n) * n)
		if d.Before(from) {
			d = d.AddDays(n)
		}
		for ; !d.After(to); d = d.AddDays(n) {
			out = append(out, Occurrence{Date: d})
		}
	case s.Kind == KindInterval:
		loc := s.location(home)
		startOfFrom := time.Date(from.Y, from.M, from.D, 0, 0, 0, 0, loc)
		k := floorDiv(int(startOfFrom.Sub(s.AnchorTime)), int(s.IntervalDur))
		for ; ; k++ {
			at := s.AnchorTime.Add(time.Duration(k) * s.IntervalDur)
			d := civil.DateOf(at.In(loc))
			if d.After(to) {
				break
			}
			if !d.Before(from) {
				out = append(out, Occurrence{Date: d, At: at})
			}
		}
	default:
		for ym := from.Month(); !to.Month().Before(ym); ym = ym.Next() {
			for _, d := range s.calendarDates(ym) {
				if !d.Before(from) && !d.After(to) {
					out = append(out, Occurrence{Date: d})
				}
			}
		}
	}
	return out
}

func (s Schedule) calendarDates(ym civil.YearMonth) []civil.Date {
	c := s.Calendar
	switch c.Kind {
	case Monthly:
		if c.EveryMonths > 1 {
			base := s.Anchor.Month()
			if s.Anchor.IsZero() {
				return nil
			}
			months := (ym.Y*12 + int(ym.M)) - (base.Y*12 + int(base.M))
			if months%c.EveryMonths != 0 {
				return nil
			}
		}
		return []civil.Date{clampDay(ym, c.Day)}
	case SemiMonthly:
		a, b := c.Days[0], c.Days[1]
		if a == 0 {
			a, b = 1, 15
		}
		return []civil.Date{clampDay(ym, a), clampDay(ym, b)}
	case NthWeekday:
		return []civil.Date{nthWeekday(ym, c.Nth, c.Weekday)}
	case LastBusinessDay:
		d := ym.Last()
		for d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
			d = d.AddDays(-1)
		}
		return []civil.Date{d}
	}
	return nil
}

func clampDay(ym civil.YearMonth, day int) civil.Date {
	if dim := ym.Days(); day > dim {
		day = dim
	}
	return civil.Date{Y: ym.Y, M: ym.M, D: day}
}

func nthWeekday(ym civil.YearMonth, nth int, wd time.Weekday) civil.Date {
	if nth > 0 {
		d := ym.First()
		for d.Weekday() != wd {
			d = d.AddDays(1)
		}
		return d.AddDays(7 * (nth - 1))
	}
	d := ym.Last()
	for d.Weekday() != wd {
		d = d.AddDays(-1)
	}
	return d
}

// ExpectedCount is the number of occurrences expected in a month.
func (s Schedule) ExpectedCount(ym civil.YearMonth, home *time.Location) int {
	return len(s.Occurrences(ym.First(), ym.Last(), home))
}

// Reanchor moves an interval schedule's anchor to an observed occurrence, so
// small drifts do not accumulate. Calendar rules keep their anchor (S14).
func (s Schedule) Reanchor(observed civil.Date, observedAt time.Time) Schedule {
	if s.Kind != KindInterval {
		return s
	}
	if s.IntervalDays > 0 {
		s.Anchor = observed
	} else if !observedAt.IsZero() {
		s.AnchorTime = observedAt
	}
	return s
}

// OccurrencesAcross lists occurrences across effective-dated versions of a
// schedule: each version applies from its EffectiveFrom until the next version starts.
func OccurrencesAcross(versions []Schedule, from, to civil.Date, home *time.Location) []Occurrence {
	vs := append([]Schedule(nil), versions...)
	sort.SliceStable(vs, func(i, j int) bool { return vs[i].EffectiveFrom.Before(vs[j].EffectiveFrom) })
	var out []Occurrence
	for i, v := range vs {
		end := to
		if i+1 < len(vs) {
			if last := vs[i+1].EffectiveFrom.AddDays(-1); last.Before(end) {
				end = last
			}
		}
		out = append(out, v.Occurrences(from, end, home)...)
	}
	return out
}

// Observed is a real transaction that may satisfy an occurrence.
type Observed struct {
	ID   string
	Date civil.Date
}

// GroupSplits collapses observed deposits from the same stream that fall within
// toleranceDays of each other into one occurrence each (S14 split deposits).
func GroupSplits(obs []Observed, toleranceDays int) [][]Observed {
	sorted := append([]Observed(nil), obs...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Date != sorted[j].Date {
			return sorted[i].Date.Before(sorted[j].Date)
		}
		return sorted[i].ID < sorted[j].ID
	})
	var groups [][]Observed
	for _, o := range sorted {
		if n := len(groups); n > 0 && o.Date.DaysSince(groups[n-1][0].Date) <= toleranceDays {
			groups[n-1] = append(groups[n-1], o)
			continue
		}
		groups = append(groups, []Observed{o})
	}
	return groups
}

func floorDiv(a, b int) int {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}
