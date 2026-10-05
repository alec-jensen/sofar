package recurrence

import (
	"fmt"
	"testing"

	"github.com/alec-jensen/sofar/internal/civil"
	"github.com/alec-jensen/sofar/internal/money"
)

func series(desc string, amount money.Cents, inflow bool, dates ...string) []Txn {
	var out []Txn
	for i, ds := range dates {
		out = append(out, Txn{ID: fmt.Sprintf("%s-%d", desc, i), Date: d(ds), Amount: amount, Inflow: inflow, Description: desc})
	}
	return out
}

func find(cs []Candidate, key string) *Candidate {
	for i := range cs {
		if cs[i].Key == key {
			return &cs[i]
		}
	}
	return nil
}

func TestS14_DetectMedianGap(t *testing.T) {
	var txns []Txn
	txns = append(txns, series("ACME PAYROLL", 200000, true, "2026-06-05", "2026-06-19", "2026-07-03", "2026-07-17")...)
	txns = append(txns, series("City Water Utility", 4500, false, "2026-06-10", "2026-07-11", "2026-08-10", "2026-09-10")...)
	txns = append(txns, series("Spotify", 1199, false, "2026-07-02", "2026-08-02", "2026-09-02")...)
	txns = append(txns, series("Kroger", 6000, false, "2026-09-01", "2026-09-08", "2026-09-15")...) // groceries repeat but are not a bill
	cs := Detect(txns, DefaultConfig())
	pay := find(cs, "acme payroll")
	if pay == nil || pay.Kind != ItemIncome || pay.Schedule.Kind != KindInterval || pay.Schedule.IntervalDays != 14 || pay.NextDate != d("2026-07-31") {
		t.Fatalf("biweekly payroll: %+v", pay)
	}
	water := find(cs, "city water utility")
	if water == nil || water.Kind != ItemBill || water.Schedule.Calendar.Kind != Monthly || water.Schedule.Calendar.Day != 10 || water.NextDate != d("2026-10-10") {
		t.Fatalf("monthly water bill: %+v", water)
	}
	if water.Variable {
		t.Errorf("amounts are identical: %+v", water)
	}
	spot := find(cs, "spotify")
	if spot == nil || spot.Kind != ItemSubscription || spot.CategoryID == "" {
		t.Fatalf("subscription: %+v", spot)
	}
	if find(cs, "kroger") != nil {
		t.Error("everyday spending is not a bill")
	}
}

func TestS14_ThresholdConfigurable(t *testing.T) {
	txns := series("Gym Co", 3000, false, "2026-08-03", "2026-09-03")
	if len(Detect(txns, DetectConfig{Threshold: 3, AfterOneKinds: map[string]bool{}})) != 0 {
		t.Error("two occurrences are not enough at the default threshold")
	}
	if len(Detect(txns, DetectConfig{Threshold: 2, AfterOneKinds: map[string]bool{}})) != 1 {
		t.Error("a threshold of 2 detects it")
	}
}

func TestS14_AmountVariationIsVariable(t *testing.T) {
	txns := []Txn{
		{ID: "a", Date: d("2026-06-15"), Amount: 5000, Description: "Power Co Electric"},
		{ID: "b", Date: d("2026-07-15"), Amount: 9000, Description: "Power Co Electric"},
		{ID: "c", Date: d("2026-08-15"), Amount: 12000, Description: "Power Co Electric"},
	}
	c := find(Detect(txns, DefaultConfig()), "power co electric")
	if c == nil || !c.Variable || c.Amount != 9000 {
		t.Fatalf("%+v", c)
	}
}

func TestS14_InconsistentTimingIsNotRecurring(t *testing.T) {
	txns := series("Random Shop", 2500, false, "2026-06-01", "2026-06-09", "2026-08-20", "2026-09-02")
	if len(Detect(txns, DefaultConfig())) != 0 {
		t.Error("irregular gaps are not a schedule")
	}
}

func TestS14_KnownMonthlyKindsAfterOneCharge(t *testing.T) {
	cs := Detect(series("Netflix", 1549, false, "2026-09-12"), DefaultConfig())
	if len(cs) != 1 || !cs[0].AfterOne || cs[0].Kind != ItemSubscription || cs[0].NextDate != d("2026-10-12") {
		t.Fatalf("a streaming service after one charge: %+v", cs)
	}
	if len(Detect(series("Kroger", 1549, false, "2026-09-12"), DefaultConfig())) != 0 {
		t.Error("an ordinary merchant is not suggested after one charge")
	}
	if len(Detect(series("ACME PAYROLL", 100000, true, "2026-09-12"), DefaultConfig())) != 0 {
		t.Error("one deposit is never an income stream")
	}
}

func TestS14_AnnualAndQuarterlyGaps(t *testing.T) {
	annual := Detect(series("Auto Insurance Co", 90000, false, "2024-03-10", "2025-03-11", "2026-03-10"), DefaultConfig())
	if len(annual) != 1 || annual[0].Schedule.Calendar.EveryMonths != 12 || annual[0].NextDate.Y != 2027 {
		t.Fatalf("%+v", annual)
	}
	q := Detect(series("Quarterly Fee", 5000, false, "2026-01-15", "2026-04-15", "2026-07-14"), DefaultConfig())
	if len(q) != 1 || q[0].Schedule.Calendar.EveryMonths != 3 {
		t.Fatalf("%+v", q)
	}
}

func TestS14_SplitDepositsCountAsOneOccurrence(t *testing.T) {
	txns := series("ACME PAYROLL", 100000, true, "2026-06-01", "2026-06-01", "2026-07-01", "2026-07-02", "2026-08-01")
	c := find(Detect(txns, DefaultConfig()), "acme payroll")
	if c == nil || c.Occurrences != 3 || c.Amount != 200000 {
		t.Fatalf("two deposits within two days are one occurrence: %+v", c)
	}
}

var _ = civil.Date{}
