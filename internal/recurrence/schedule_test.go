package recurrence

import (
	"reflect"
	"testing"
	"time"

	_ "time/tzdata"

	"github.com/alec-jensen/sofar/internal/civil"
)

func d(s string) civil.Date {
	v, err := civil.ParseDate(s)
	if err != nil {
		panic(err)
	}
	return v
}

func dates(os []Occurrence) []string {
	var out []string
	for _, o := range os {
		out = append(out, o.Date.String())
	}
	return out
}

func chicago(t *testing.T) *time.Location {
	t.Helper()
	l, err := time.LoadLocation("America/Chicago")
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestS14_Interval13Days(t *testing.T) {
	s := Schedule{Kind: KindInterval, IntervalDays: 13, Anchor: d("2026-10-01")}
	got := dates(s.Occurrences(d("2026-10-01"), d("2026-11-30"), nil))
	want := []string{"2026-10-01", "2026-10-14", "2026-10-27", "2026-11-09", "2026-11-22"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%v", got)
	}
	// a window that starts before and after the anchor lines up the same way
	if got = dates(s.Occurrences(d("2026-09-01"), d("2026-09-30"), nil)); !reflect.DeepEqual(got, []string{"2026-09-05", "2026-09-18"}) {
		t.Errorf("before the anchor: %v", got)
	}
	if got = dates(s.Occurrences(d("2026-10-15"), d("2026-10-26"), nil)); len(got) != 0 {
		t.Errorf("gap between occurrences: %v", got)
	}
}

func TestS14_Interval3Weeks(t *testing.T) {
	s := Schedule{Kind: KindInterval, IntervalDays: 21, Anchor: d("2026-01-02")}
	if got := dates(s.Occurrences(d("2026-01-01"), d("2026-03-01"), nil)); !reflect.DeepEqual(got, []string{"2026-01-02", "2026-01-23", "2026-02-13"}) {
		t.Fatalf("%v", got)
	}
}

func TestS14_Interval7Hours(t *testing.T) {
	anchor := time.Date(2026, 10, 1, 6, 0, 0, 0, time.UTC)
	s := Schedule{Kind: KindInterval, IntervalDur: 7 * time.Hour, AnchorTime: anchor}
	got := s.Occurrences(d("2026-10-01"), d("2026-10-02"), time.UTC)
	// 06:00, 13:00, 20:00, then 03:00 on the 2nd, 10:00, 17:00, 00:00 on the 3rd is outside
	if len(got) != 6 || got[3].At != anchor.Add(21*time.Hour) {
		t.Fatalf("%+v", got)
	}
	if got[3].Date != d("2026-10-02") {
		t.Errorf("date follows the instant: %v", got[3].Date)
	}
}

func TestS14_HourIntervalsUseAbsoluteTime(t *testing.T) {
	ch := chicago(t)
	// every 24 hours across the fall-back day: the wall clock shifts by an hour, the instants do not
	anchor := time.Date(2026, 10, 31, 12, 0, 0, 0, ch)
	s := Schedule{Kind: KindInterval, IntervalDur: 24 * time.Hour, AnchorTime: anchor}
	got := s.Occurrences(d("2026-10-31"), d("2026-11-03"), ch)
	if len(got) != 4 {
		t.Fatalf("%+v", got)
	}
	if gap := got[2].At.Sub(got[1].At); gap != 24*time.Hour {
		t.Errorf("instants are exactly 24h apart across DST: %v", gap)
	}
	if got[1].At.In(ch).Hour() != 11 {
		t.Errorf("so the wall clock moved: %v", got[1].At.In(ch))
	}
}

func TestS14_DSTDoesNotDriftDayIntervals(t *testing.T) {
	// a 14-day schedule across both DST changes stays on the same calendar days
	s := Schedule{Kind: KindInterval, IntervalDays: 14, Anchor: d("2026-03-06")}
	got := dates(s.Occurrences(d("2026-03-01"), d("2026-04-30"), chicago(t)))
	want := []string{"2026-03-06", "2026-03-20", "2026-04-03", "2026-04-17"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%v", got)
	}
	for _, o := range s.Occurrences(d("2026-10-20"), d("2026-11-30"), chicago(t)) {
		if o.Date.Weekday() != time.Friday {
			t.Errorf("drifted off Friday: %v", o.Date)
		}
	}
}

func TestS14_MonthlyDay31Clamps(t *testing.T) {
	s := Schedule{Kind: KindCalendar, Calendar: CalendarRule{Kind: Monthly, Day: 31}}
	got := dates(s.Occurrences(d("2026-01-01"), d("2026-06-30"), nil))
	want := []string{"2026-01-31", "2026-02-28", "2026-03-31", "2026-04-30", "2026-05-31", "2026-06-30"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%v", got)
	}
	leap := s.Occurrences(d("2028-02-01"), d("2028-02-29"), nil)
	if len(leap) != 1 || leap[0].Date != d("2028-02-29") {
		t.Errorf("leap year: %v", leap)
	}
}

func TestS14_Quarterly(t *testing.T) {
	s := Schedule{Kind: KindCalendar, Anchor: d("2026-01-15"), Calendar: CalendarRule{Kind: Monthly, Day: 15, EveryMonths: 3}}
	got := dates(s.Occurrences(d("2026-01-01"), d("2026-12-31"), nil))
	if !reflect.DeepEqual(got, []string{"2026-01-15", "2026-04-15", "2026-07-15", "2026-10-15"}) {
		t.Fatalf("%v", got)
	}
}

func TestS14_SemiMonthly(t *testing.T) {
	s := Schedule{Kind: KindCalendar, Calendar: CalendarRule{Kind: SemiMonthly}}
	if got := dates(s.Occurrences(d("2026-02-01"), d("2026-03-31"), nil)); !reflect.DeepEqual(got, []string{"2026-02-01", "2026-02-15", "2026-03-01", "2026-03-15"}) {
		t.Fatalf("%v", got)
	}
	c := Schedule{Kind: KindCalendar, Calendar: CalendarRule{Kind: SemiMonthly, Days: [2]int{10, 31}}}
	if got := dates(c.Occurrences(d("2026-02-01"), d("2026-02-28"), nil)); !reflect.DeepEqual(got, []string{"2026-02-10", "2026-02-28"}) {
		t.Fatalf("configurable days clamp: %v", got)
	}
}

func TestS14_NthWeekday(t *testing.T) {
	second := Schedule{Kind: KindCalendar, Calendar: CalendarRule{Kind: NthWeekday, Nth: 2, Weekday: time.Tuesday}}
	if got := dates(second.Occurrences(d("2026-10-01"), d("2026-11-30"), nil)); !reflect.DeepEqual(got, []string{"2026-10-13", "2026-11-10"}) {
		t.Fatalf("%v", got)
	}
	last := Schedule{Kind: KindCalendar, Calendar: CalendarRule{Kind: NthWeekday, Nth: -1, Weekday: time.Friday}}
	if got := dates(last.Occurrences(d("2026-10-01"), d("2026-11-30"), nil)); !reflect.DeepEqual(got, []string{"2026-10-30", "2026-11-27"}) {
		t.Fatalf("%v", got)
	}
}

func TestS14_LastBusinessDay(t *testing.T) {
	s := Schedule{Kind: KindCalendar, Calendar: CalendarRule{Kind: LastBusinessDay}}
	// Oct 31 2026 is a Saturday, Nov 30 a Monday, Jan 31 2027 a Sunday
	if got := dates(s.Occurrences(d("2026-10-01"), d("2027-01-31"), nil)); !reflect.DeepEqual(got, []string{"2026-10-30", "2026-11-30", "2026-12-31", "2027-01-29"}) {
		t.Fatalf("%v", got)
	}
}

func TestS14_ReanchorOnObserved(t *testing.T) {
	s := Schedule{Kind: KindInterval, IntervalDays: 14, Anchor: d("2026-10-02")}
	r := s.Reanchor(d("2026-10-17"), time.Time{}) // paid a day late
	if got := dates(r.Occurrences(d("2026-10-18"), d("2026-11-30"), nil)); !reflect.DeepEqual(got, []string{"2026-10-31", "2026-11-14", "2026-11-28"}) {
		t.Fatalf("%v", got)
	}
	cal := Schedule{Kind: KindCalendar, Anchor: d("2026-01-15"), Calendar: CalendarRule{Kind: Monthly, Day: 15}}
	if cal.Reanchor(d("2026-02-17"), time.Time{}).Anchor != cal.Anchor {
		t.Error("calendar rules keep their anchor")
	}
}

func TestS14_EffectiveDatedChange(t *testing.T) {
	biweekly := Schedule{Kind: KindInterval, IntervalDays: 14, Anchor: d("2026-01-02"), EffectiveFrom: d("2026-01-01")}
	semi := Schedule{Kind: KindCalendar, Calendar: CalendarRule{Kind: SemiMonthly}, EffectiveFrom: d("2026-03-01")}
	got := dates(OccurrencesAcross([]Schedule{semi, biweekly}, d("2026-02-01"), d("2026-03-31"), nil))
	want := []string{"2026-02-13", "2026-02-27", "2026-03-01", "2026-03-15"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%v", got)
	}
	// nothing before a version's own effective date
	if got := semi.Occurrences(d("2026-02-01"), d("2026-02-28"), nil); len(got) != 0 {
		t.Errorf("%v", got)
	}
}

func TestS14_SplitDepositsOneOccurrence(t *testing.T) {
	obs := []Observed{{"b", d("2026-10-16")}, {"a", d("2026-10-15")}, {"c", d("2026-10-31")}}
	groups := GroupSplits(obs, 2)
	if len(groups) != 2 || len(groups[0]) != 2 || groups[0][0].ID != "a" || len(groups[1]) != 1 {
		t.Fatalf("%+v", groups)
	}
}

func TestS14_GraceAndTolerance(t *testing.T) {
	monthly := Schedule{Kind: KindCalendar, Calendar: CalendarRule{Kind: Monthly, Day: 1}}
	if monthly.Grace() != 3*24*time.Hour {
		t.Errorf("a monthly grace is capped at 3 days: %v", monthly.Grace())
	}
	weekly := Schedule{Kind: KindInterval, IntervalDays: 7, Anchor: d("2026-10-01")}
	if g := weekly.Grace(); g != time.Duration(1.75*float64(24*time.Hour)) {
		t.Errorf("a weekly grace is 25%%: %v", g)
	}
	two := 2
	weekly.GraceDays = &two
	if weekly.Grace() != 48*time.Hour {
		t.Errorf("grace is editable: %v", weekly.Grace())
	}
	if monthly.BoundaryTolerance() != 5 || weekly.BoundaryTolerance() != 1 {
		t.Errorf("tolerance min(5, interval/4): %d %d", monthly.BoundaryTolerance(), weekly.BoundaryTolerance())
	}
}

func TestS14_ExpectedCountFindsThreePaycheckMonths(t *testing.T) {
	biweekly := Schedule{Kind: KindInterval, IntervalDays: 14, Anchor: d("2026-01-02")}
	var threes []string
	for ym := (civil.YearMonth{Y: 2026, M: 1}); ym.Y == 2026; ym = ym.Next() {
		if biweekly.ExpectedCount(ym, nil) == 3 {
			threes = append(threes, ym.String())
		}
	}
	if !reflect.DeepEqual(threes, []string{"2026-01", "2026-07"}) {
		t.Errorf("a biweekly paycheck has two three-paycheck months a year: %v", threes)
	}
}

func TestSchedule_Validate(t *testing.T) {
	for name, s := range map[string]Schedule{
		"no interval":   {Kind: KindInterval, Anchor: d("2026-01-01")},
		"no anchor":     {Kind: KindInterval, IntervalDays: 7},
		"bad day":       {Kind: KindCalendar, Calendar: CalendarRule{Kind: Monthly, Day: 32}},
		"bad nth":       {Kind: KindCalendar, Calendar: CalendarRule{Kind: NthWeekday, Nth: 5}},
		"unknown kind":  {Kind: "weird"},
		"unknown rule":  {Kind: KindCalendar, Calendar: CalendarRule{Kind: "x"}},
		"semi reversed": {Kind: KindCalendar, Calendar: CalendarRule{Kind: SemiMonthly, Days: [2]int{20, 5}}},
	} {
		if s.Validate() == nil {
			t.Errorf("%s should be invalid", name)
		}
		if s.Occurrences(d("2026-01-01"), d("2026-12-31"), nil) != nil {
			t.Errorf("%s must produce nothing", name)
		}
	}
}
