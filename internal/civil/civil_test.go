package civil

import (
	"testing"
	"time"

	_ "time/tzdata"

	"pgregory.net/rapid"
)

func d(s string) Date {
	v, err := ParseDate(s)
	if err != nil {
		panic(err)
	}
	return v
}

func ym(y int, m time.Month) YearMonth { return YearMonth{y, m} }

func chicago(t *testing.T) *time.Location {
	t.Helper()
	l, err := time.LoadLocation("America/Chicago")
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestCivil_DatesAndMonths(t *testing.T) {
	if got := d("2026-02-28").AddDays(1).String(); got != "2026-03-01" {
		t.Errorf("AddDays across month: %s", got)
	}
	if got := d("2026-01-01").AddDays(-1).String(); got != "2025-12-31" {
		t.Errorf("AddDays across year: %s", got)
	}
	if n := d("2026-03-08").DaysSince(d("2026-03-01")); n != 7 {
		t.Errorf("DaysSince = %d", n)
	}
	for in, want := range map[string]int{"2026-02-01": 28, "2028-02-01": 29, "2026-04-01": 30, "2026-12-01": 31} {
		if got := d(in).Month().Days(); got != want {
			t.Errorf("Days(%s) = %d want %d", in, got, want)
		}
	}
	if got := ym(2026, 12).Next(); got != ym(2027, 1) {
		t.Errorf("Next = %v", got)
	}
	if got := ym(2026, 1).Prev(); got != ym(2025, 12) {
		t.Errorf("Prev = %v", got)
	}
	if got := ym(2026, 1).AddMonths(-13); got != ym(2024, 12) {
		t.Errorf("AddMonths(-13) = %v", got)
	}
	if _, err := ParseDate("2026-02-30"); err == nil {
		t.Error("an impossible date must be rejected")
	}
	if _, err := ParseDate("2026-1-5"); err == nil {
		t.Error("non-padded dates must be rejected")
	}
}

func TestCivil_DaysLeftIncludesToday(t *testing.T) {
	for in, want := range map[string]int{"2026-10-05": 27, "2026-10-31": 1, "2026-10-01": 31, "2026-02-28": 1} {
		if got := DaysLeft(d(in)); got != want {
			t.Errorf("DaysLeft(%s) = %d want %d", in, got, want)
		}
	}
}

func TestCivil_AddMonthsClamped(t *testing.T) {
	for _, tc := range []struct {
		in   string
		n    int
		want string
	}{
		{"2026-01-31", 1, "2026-02-28"},
		{"2028-01-31", 1, "2028-02-29"},
		{"2026-03-31", -1, "2026-02-28"},
		{"2026-01-31", 2, "2026-03-31"},
		{"2026-05-31", 1, "2026-06-30"},
		{"2026-10-15", 3, "2027-01-15"},
	} {
		if got := AddMonthsClamped(d(tc.in), tc.n).String(); got != tc.want {
			t.Errorf("AddMonthsClamped(%s,%d) = %s want %s", tc.in, tc.n, got, tc.want)
		}
	}
}

func TestS3_DateOnlyStaysOnTheFirst(t *testing.T) {
	// A bank date-only value is midnight UTC. Converted to a US zone it would
	// land on the 31st of the previous month; as a calendar date it must not.
	date := d("2026-03-01")
	if got := MonthOf(TxnTime{Date: &date}, chicago(t)); got != ym(2026, 3) {
		t.Fatalf("a date-only value on the 1st must stay in its month, got %v", got)
	}
	midnightUTC := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	if got := MonthOf(TxnTime{Instant: &midnightUTC}, chicago(t)); got != ym(2026, 2) {
		t.Fatalf("a real instant converts to the home zone, got %v", got)
	}
}

func TestS3_InstantConvertsToHomeZone(t *testing.T) {
	ch := chicago(t)
	// 23:30 on Oct 31 in Chicago (CDT) is 04:30 UTC on Nov 1.
	late := time.Date(2026, 11, 1, 4, 30, 0, 0, time.UTC)
	if got := MonthOf(TxnTime{Instant: &late}, ch); got != ym(2026, 10) {
		t.Errorf("23:30 home time on the 31st stays in October, got %v", got)
	}
	if got := MonthOf(TxnTime{Instant: &late}, time.UTC); got != ym(2026, 11) {
		t.Errorf("the same instant in UTC is November, got %v", got)
	}
	if got := MonthOf(TxnTime{Instant: &late}, nil); got != ym(2026, 11) {
		t.Errorf("a nil zone means UTC, got %v", got)
	}
	if got := MonthOf(TxnTime{}, ch); got != (YearMonth{}) {
		t.Errorf("no time at all gives the zero month, got %v", got)
	}
}

func TestS3_DSTTransitionDays(t *testing.T) {
	ch := chicago(t)
	for _, tc := range []struct {
		name string
		at   time.Time
		want YearMonth
	}{
		// Fall back: Nov 1, 2026, 02:00 CDT becomes 01:00 CST.
		{"just before midnight Oct 31 CDT", time.Date(2026, 11, 1, 4, 59, 0, 0, time.UTC), ym(2026, 10)},
		{"midnight Nov 1 CDT", time.Date(2026, 11, 1, 5, 0, 0, 0, time.UTC), ym(2026, 11)},
		{"first 01:30 on the fall-back day", time.Date(2026, 11, 1, 6, 30, 0, 0, time.UTC), ym(2026, 11)},
		{"repeated 01:30 on the fall-back day", time.Date(2026, 11, 1, 7, 30, 0, 0, time.UTC), ym(2026, 11)},
		// Spring forward: Mar 8, 2026, 02:00 CST becomes 03:00 CDT.
		{"before the spring-forward gap", time.Date(2026, 3, 8, 7, 59, 0, 0, time.UTC), ym(2026, 3)},
		{"after the spring-forward gap", time.Date(2026, 3, 8, 8, 0, 0, 0, time.UTC), ym(2026, 3)},
		{"Feb 28 22:30 CST is still February", time.Date(2026, 3, 1, 4, 30, 0, 0, time.UTC), ym(2026, 2)},
	} {
		at := tc.at
		if got := MonthOf(TxnTime{Instant: &at}, ch); got != tc.want {
			t.Errorf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}

func TestS3_ZoneChangeIsEffectiveDated(t *testing.T) {
	ch, ny := chicago(t), mustLoad(t, "America/New_York")
	changes := []ZoneChange{{From: ym(2026, 6), Zone: ny}, {From: ym(2024, 1), Zone: ch}}
	for _, tc := range []struct {
		m    YearMonth
		want *time.Location
	}{
		{ym(2023, 5), ch}, // before the first change: the original zone
		{ym(2024, 1), ch},
		{ym(2026, 5), ch}, // past months keep their original zone
		{ym(2026, 6), ny},
		{ym(2027, 1), ny},
	} {
		if got := HomeZoneAt(tc.m, changes); got != tc.want {
			t.Errorf("HomeZoneAt(%v) = %v want %v", tc.m, got, tc.want)
		}
	}
	if HomeZoneAt(ym(2026, 1), nil) != time.UTC {
		t.Error("no changes means UTC")
	}
}

func mustLoad(t *testing.T, name string) *time.Location {
	t.Helper()
	l, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestS3_BoundaryAttribution(t *testing.T) {
	for _, tc := range []struct {
		name            string
		actual, nominal string
		interval        int
		wantMonth       YearMonth
		wantAttributed  bool
	}{
		{"paid 2 days early, across the boundary", "2026-10-30", "2026-11-01", 30, ym(2026, 11), true},
		{"paid on the last tolerated day", "2026-10-27", "2026-11-01", 30, ym(2026, 11), true},
		{"paid 3 days late", "2026-11-03", "2026-10-31", 30, ym(2026, 10), true},
		{"outside tolerance stays put", "2026-10-25", "2026-11-01", 30, ym(2026, 10), false},
		{"same month is not an attribution", "2026-11-02", "2026-11-01", 30, ym(2026, 11), false},
		{"weekly is never attributed", "2026-10-31", "2026-11-01", 7, ym(2026, 10), false},
		{"biweekly is never attributed", "2026-10-31", "2026-11-01", 14, ym(2026, 10), false},
		{"annual tolerance caps at 5 days", "2026-12-27", "2027-01-01", 365, ym(2027, 1), true},
		{"annual 6 days early is outside", "2026-12-26", "2027-01-01", 365, ym(2026, 12), false},
	} {
		m, attributed := NominalMonth(d(tc.actual), d(tc.nominal), tc.interval)
		if m != tc.wantMonth || attributed != tc.wantAttributed {
			t.Errorf("%s: got %v/%v want %v/%v", tc.name, m, attributed, tc.wantMonth, tc.wantAttributed)
		}
	}
}

func TestS3_BoundaryToleranceShrinksForShortIntervals(t *testing.T) {
	// interval 28 -> tolerance 7 capped to 5; interval 28 is the shortest that applies.
	if m, a := NominalMonth(d("2026-10-31"), d("2026-11-01"), 28); m != ym(2026, 11) || !a {
		t.Errorf("28-day interval should attribute, got %v %v", m, a)
	}
	if m, a := NominalMonth(d("2026-10-31"), d("2026-11-01"), 27); m != ym(2026, 10) || a {
		t.Errorf("27-day interval is too short, got %v %v", m, a)
	}
}

func TestCivil_AddDaysRoundTrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		start := Date{2020, 1, 1}.AddDays(rapid.IntRange(0, 3650).Draw(t, "offset"))
		n := rapid.IntRange(-2000, 2000).Draw(t, "n")
		if got := start.AddDays(n).AddDays(-n); got != start {
			t.Fatalf("%v + %d - %d = %v", start, n, n, got)
		}
		if start.AddDays(n).DaysSince(start) != n {
			t.Fatalf("DaysSince disagrees with AddDays for %v %d", start, n)
		}
	})
}
