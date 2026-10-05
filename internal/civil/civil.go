// Package civil holds date-only (civil) dates and month arithmetic with no
// clocks and no I/O. A bank's date-only value is a calendar date and is never
// converted between zones (S3). It imports nothing from sofar.
package civil

import (
	"fmt"
	"time"
)

// Date is a calendar date.
type Date struct {
	Y int
	M time.Month
	D int
}

// YearMonth is a calendar month.
type YearMonth struct {
	Y int
	M time.Month
}

// DateOf builds a Date from a time, using the time's own wall-clock fields.
func DateOf(t time.Time) Date {
	y, m, d := t.Date()
	return Date{y, m, d}
}

// ParseDate parses "YYYY-MM-DD" strictly.
func ParseDate(s string) (Date, error) {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return Date{}, fmt.Errorf("invalid date %q", s)
	}
	return DateOf(t), nil
}

// ParseYearMonth parses "YYYY-MM" strictly.
func ParseYearMonth(s string) (YearMonth, error) {
	t, err := time.Parse("2006-01", s)
	if err != nil {
		return YearMonth{}, fmt.Errorf("invalid month %q", s)
	}
	return YearMonth{t.Year(), t.Month()}, nil
}

func (d Date) utc() time.Time { return time.Date(d.Y, d.M, d.D, 0, 0, 0, 0, time.UTC) }

// String is "YYYY-MM-DD".
func (d Date) String() string { return d.utc().Format("2006-01-02") }

// IsZero reports whether d is the zero Date.
func (d Date) IsZero() bool { return d == Date{} }

// Month returns the month d falls in.
func (d Date) Month() YearMonth { return YearMonth{d.Y, d.M} }

// Weekday returns the day of the week.
func (d Date) Weekday() time.Weekday { return d.utc().Weekday() }

// AddDays returns d shifted by n days (n may be negative).
func (d Date) AddDays(n int) Date { return DateOf(d.utc().AddDate(0, 0, n)) }

// DaysSince returns the number of days from o to d (negative if d is earlier).
func (d Date) DaysSince(o Date) int {
	return int(d.utc().Sub(o.utc()) / (24 * time.Hour))
}

// Compare returns -1, 0 or 1.
func (d Date) Compare(o Date) int {
	switch {
	case d.Y != o.Y:
		return sign(d.Y - o.Y)
	case d.M != o.M:
		return sign(int(d.M) - int(o.M))
	default:
		return sign(d.D - o.D)
	}
}

// Before reports whether d is earlier than o.
func (d Date) Before(o Date) bool { return d.Compare(o) < 0 }

// After reports whether d is later than o.
func (d Date) After(o Date) bool { return d.Compare(o) > 0 }

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	}
	return 0
}

// DaysIn returns the number of days in the month.
func DaysIn(y int, m time.Month) int { return time.Date(y, m+1, 0, 0, 0, 0, 0, time.UTC).Day() }

// Days returns the number of days in the month.
func (ym YearMonth) Days() int { return DaysIn(ym.Y, ym.M) }

// String is "YYYY-MM".
func (ym YearMonth) String() string { return fmt.Sprintf("%04d-%02d", ym.Y, int(ym.M)) }

// First returns the first day of the month.
func (ym YearMonth) First() Date { return Date{ym.Y, ym.M, 1} }

// Last returns the last day of the month.
func (ym YearMonth) Last() Date { return Date{ym.Y, ym.M, ym.Days()} }

// AddMonths shifts by n months (n may be negative).
func (ym YearMonth) AddMonths(n int) YearMonth {
	idx := ym.Y*12 + int(ym.M) - 1 + n
	y, m := idx/12, idx%12
	if m < 0 {
		m += 12
		y--
	}
	return YearMonth{y, time.Month(m + 1)}
}

// Next is the following month.
func (ym YearMonth) Next() YearMonth { return ym.AddMonths(1) }

// Prev is the preceding month.
func (ym YearMonth) Prev() YearMonth { return ym.AddMonths(-1) }

// Compare returns -1, 0 or 1.
func (ym YearMonth) Compare(o YearMonth) int {
	if ym.Y != o.Y {
		return sign(ym.Y - o.Y)
	}
	return sign(int(ym.M) - int(o.M))
}

// Before reports whether ym is earlier than o.
func (ym YearMonth) Before(o YearMonth) bool { return ym.Compare(o) < 0 }

// Contains reports whether d falls in the month.
func (ym YearMonth) Contains(d Date) bool { return d.Y == ym.Y && d.M == ym.M }

// DaysLeft returns the days remaining in today's month, including today.
func DaysLeft(today Date) int { return DaysIn(today.Y, today.M) - today.D + 1 }

// AddMonthsClamped shifts a date by n months, clamping to the end of a shorter
// month: Jan 31 plus one month is Feb 28 (or 29).
func AddMonthsClamped(d Date, n int) Date {
	ym := d.Month().AddMonths(n)
	day := d.D
	if dim := ym.Days(); day > dim {
		day = dim
	}
	return Date{ym.Y, ym.M, day}
}

// TxnTime is the time a bank reports for a transaction: either a calendar date
// (date-only values, such as midnight UTC) or a real instant.
type TxnTime struct {
	Date    *Date
	Instant *time.Time
}

// MonthOf decides a transaction's calendar month (S3). A date-only value is a
// calendar date and is never converted; a real instant converts to the home zone.
func MonthOf(t TxnTime, home *time.Location) YearMonth {
	switch {
	case t.Date != nil:
		return t.Date.Month()
	case t.Instant != nil:
		if home == nil {
			home = time.UTC
		}
		return DateOf(t.Instant.In(home)).Month()
	}
	return YearMonth{}
}

// ZoneChange says the calculation zone becomes Zone from month From onward.
type ZoneChange struct {
	From YearMonth
	Zone *time.Location
}

// HomeZoneAt returns the calculation zone in force for a month. Changes are
// effective-dated, so past months keep their original zone (S3). Months before
// the first change use the first change's zone; with no changes the answer is UTC.
func HomeZoneAt(ym YearMonth, changes []ZoneChange) *time.Location {
	var best *ZoneChange
	var earliest *ZoneChange
	for i := range changes {
		c := &changes[i]
		if earliest == nil || c.From.Before(earliest.From) {
			earliest = c
		}
		if !ym.Before(c.From) && (best == nil || best.From.Before(c.From)) {
			best = c
		}
	}
	switch {
	case best != nil:
		return best.Zone
	case earliest != nil:
		return earliest.Zone
	}
	return time.UTC
}

// NominalMonth applies S3 boundary attribution to a recurring item: if the
// actual transaction lands across a month boundary but within the tolerance
// min(5, interval/4) days of its expected date, it belongs to the expected
// (nominal) month. Per A-010 this applies only to intervals of 28 days or more.
func NominalMonth(actual, nominal Date, intervalDays int) (YearMonth, bool) {
	if intervalDays < 28 || actual.Month() == nominal.Month() {
		return actual.Month(), false
	}
	tolerance := intervalDays / 4
	if tolerance > 5 {
		tolerance = 5
	}
	diff := actual.DaysSince(nominal)
	if diff < 0 {
		diff = -diff
	}
	if diff <= tolerance {
		return nominal.Month(), true
	}
	return actual.Month(), false
}
