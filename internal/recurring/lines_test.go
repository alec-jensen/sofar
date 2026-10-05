package recurring

import (
	"testing"
	"time"

	"github.com/alec-jensen/sofar/internal/civil"
	"github.com/alec-jensen/sofar/internal/money"
	"github.com/alec-jensen/sofar/internal/recurrence"
)

func d(s string) civil.Date {
	v, err := civil.ParseDate(s)
	if err != nil {
		panic(err)
	}
	return v
}

func monthlyItem(id string, kind recurrence.ItemKind, amount money.Cents, day int, from string) Item {
	return Item{ID: id, Kind: kind, Name: id, Key: id, Amount: amount, ActiveFrom: d(from),
		Schedules: []recurrence.Schedule{{Kind: recurrence.KindCalendar, Calendar: recurrence.CalendarRule{Kind: recurrence.Monthly, Day: day}, EffectiveFrom: d(from)}}}
}

var oct = civil.YearMonth{Y: 2026, M: 10}

func TestS5_ReserveUsesLastActual(t *testing.T) {
	rent := monthlyItem("rent", recurrence.ItemBill, 100000, 1, "2026-01-01")
	lines := BuildLines([]Item{rent}, oct, LineInputs{})
	if len(lines.Bills) != 1 || lines.Bills[0].Amount != 100000 {
		t.Fatalf("no actual yet: the expected amount: %+v", lines.Bills)
	}
	lines = BuildLines([]Item{rent}, oct, LineInputs{LastActual: map[string]money.Cents{"rent": 110000}})
	if lines.Bills[0].Amount != 110000 {
		t.Fatalf("the last actual wins: %+v", lines.Bills)
	}
}

func TestS5_ItemsOutsideTheirActiveWindowHaveNoLine(t *testing.T) {
	later := monthlyItem("later", recurrence.ItemBill, 5000, 5, "2026-11-01")
	ended := monthlyItem("ended", recurrence.ItemBill, 5000, 5, "2026-01-01")
	end := d("2026-09-30")
	ended.EndedOn = &end
	last := monthlyItem("last", recurrence.ItemBill, 5000, 5, "2026-01-01")
	end2 := d("2026-10-03")
	last.EndedOn = &end2
	lines := BuildLines([]Item{later, ended, last}, oct, LineInputs{})
	if len(lines.Bills) != 1 || lines.Bills[0].ID != "last" {
		t.Fatalf("only the item still active in October: %+v", lines.Bills)
	}
}

func TestS6_SubscriptionsAreTheirOwnLines(t *testing.T) {
	sub := monthlyItem("netflix", recurrence.ItemSubscription, 1549, 12, "2026-01-01")
	lines := BuildLines([]Item{sub}, oct, LineInputs{})
	if len(lines.Subscriptions) != 1 || len(lines.Bills) != 0 {
		t.Fatalf("%+v", lines)
	}
}

func TestS5_WeeklyBillReservesEveryOccurrence(t *testing.T) {
	it := Item{ID: "w", Kind: recurrence.ItemBill, Amount: 2000, ActiveFrom: d("2026-01-01"),
		Schedules: []recurrence.Schedule{{Kind: recurrence.KindInterval, IntervalDays: 7, Anchor: d("2026-10-02"), EffectiveFrom: d("2026-01-01")}}}
	lines := BuildLines([]Item{it}, oct, LineInputs{Home: time.UTC})
	if lines.Bills[0].Amount != 2000*5 { // Oct 2, 9, 16, 23, 30
		t.Fatalf("%+v", lines.Bills)
	}
}

func TestS5_SinkingSetAsideFormulas(t *testing.T) {
	annual := monthlyItem("ins", recurrence.ItemBill, 120000, 15, "2026-01-01")
	annual.Schedules[0].Calendar.EveryMonths = 12
	annual.Schedules[0].Anchor = d("2026-03-15")
	if got := BuildLines([]Item{annual}, oct, LineInputs{}).Sinking; len(got) != 1 || got[0].Amount != 10000 {
		t.Fatalf("annual: amount / 12: %+v", got)
	}
	quarterly := monthlyItem("tax", recurrence.ItemBill, 30000, 15, "2026-01-01")
	quarterly.Schedules[0].Calendar.EveryMonths = 3
	quarterly.Schedules[0].Anchor = d("2026-01-15")
	if got := BuildLines([]Item{quarterly}, oct, LineInputs{}).Sinking; got[0].Amount != 10000 {
		t.Fatalf("quarterly: amount / 3: %+v", got)
	}
	every45 := Item{ID: "x", Kind: recurrence.ItemBill, Amount: 45000, ActiveFrom: d("2026-01-01"),
		Schedules: []recurrence.Schedule{{Kind: recurrence.KindInterval, IntervalDays: 45, Anchor: d("2026-09-01"), EffectiveFrom: d("2026-01-01")}}}
	got := BuildLines([]Item{every45}, oct, LineInputs{}).Sinking
	if want := money.Cents(45000 * 304375 / (10000 * 45)); got[0].Amount != want {
		t.Fatalf("fixed interval: amount * 30.4375 / days = %v, got %+v", want, got)
	}
}

func TestS5_SinkingCatchUp(t *testing.T) {
	annual := monthlyItem("ins", recurrence.ItemBill, 120000, 15, "2026-10-01")
	annual.Schedules[0].Calendar.EveryMonths = 12
	annual.Schedules[0].Anchor = d("2027-02-15")
	// first month: due in February, four months away: amount / 4
	if got := BuildLines([]Item{annual}, oct, LineInputs{}).Sinking; got[0].Amount != 30000 {
		t.Fatalf("catch-up: %+v", got)
	}
	// after that, the normal rate
	if got := BuildLines([]Item{annual}, civil.YearMonth{Y: 2026, M: 11}, LineInputs{}).Sinking; got[0].Amount != 10000 {
		t.Fatalf("normal rate: %+v", got)
	}
}

func TestS19_9_PaymentPlanCompletesAndFrees(t *testing.T) {
	plan := monthlyItem("laptop", recurrence.ItemPaymentPlan, 10000, 5, "2026-07-01")
	plan.InstallmentsTotal = 4
	for paid, want := range map[int]money.Cents{0: 10000, 3: 10000, 4: 0} {
		lines := BuildLines([]Item{plan}, oct, LineInputs{Paid: map[string]int{"laptop": paid}})
		var got money.Cents
		for _, l := range lines.PaymentPlans {
			got += l.Amount
		}
		if got != want {
			t.Errorf("paid %d: reserve %v, want %v", paid, got, want)
		}
	}
}

func TestItem_MatchesByMerchantKey(t *testing.T) {
	it := Item{Key: "city water utility"}
	if !it.Matches("CITY WATER UTILITY #4412 ARLINGTON TX") || it.Matches("Kroger") {
		t.Error("matching ignores store numbers and locations")
	}
}
