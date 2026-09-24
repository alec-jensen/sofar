package budget

import (
	"testing"
	"time"
)

func TestBaselineOnlyConfirmedReceivedMoney(t *testing.T) {
	ts := []Transaction{{ID: "salary", Date: "2026-08-20", Amount: 900000, Status: "confirmed", Direction: "in", IncomeStream: "salary"}, {ID: "freelance", Date: "2026-07-10", Amount: 150000, Status: "confirmed", Direction: "in", IncomeStream: "self-employed"}, {ID: "pending", Date: "2026-07-10", Amount: 99999999, Status: "pending", Direction: "in", IncomeStream: "salary"}, {ID: "current", Date: "2026-09-01", Amount: 99999999, Status: "confirmed", Direction: "in", IncomeStream: "salary"}, {ID: "old", Date: "2026-05-31", Amount: 99999999, Status: "confirmed", Direction: "in", IncomeStream: "salary"}, {ID: "repayment", Date: "2026-07-20", Amount: 99999999, Status: "confirmed", Direction: "in", IncomeStream: "salary"}, {ID: "transfer", Date: "2026-07-20", Amount: 99999999, Status: "confirmed", Direction: "in", IncomeStream: "transfer"}}
	rs := []Recurring{{Amount: 120000, Type: "bill", Category: "expenses", Cadence: "monthly", Confirmed: true}, {Amount: 99999999, Type: "bill", Category: "expenses", Cadence: "monthly", Confirmed: false}, {Amount: 99999999, Type: "bill", Category: "spending", Cadence: "monthly", Confirmed: true}}
	b := Calculate(ts, rs, []Link{{CreditID: "repayment"}}, 50000, time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC))
	if b.Salary != 300000 || b.SelfEmployed != 50000 || b.Bills != 120000 || b.Safe != 180000 {
		t.Fatalf("unexpected baseline: %+v", b)
	}
}
func TestMonthlyCadences(t *testing.T) {
	for _, c := range []struct {
		cadence      string
		amount, want int64
	}{{"weekly", 1200, 5200}, {"biweekly", 1200, 2600}, {"annual", 12000, 1000}, {"monthly", 1234, 1234}} {
		if got := Monthly(c.amount, c.cadence); got != c.want {
			t.Errorf("%s: got %d want %d", c.cadence, got, c.want)
		}
	}
}
func TestMerchantNormalization(t *testing.T) {
	if !Matches("NETFLIX.COM", "Netflix 08/23") {
		t.Fatal("merchant variations should match")
	}
	if Matches("Whole Foods", "Netflix") {
		t.Fatal("unrelated merchants should not match")
	}
	if Matches("", "") {
		t.Fatal("empty merchant should not match")
	}
}
func TestCadenceRequiresConsistentSpacing(t *testing.T) {
	dates := []time.Time{}
	for _, d := range []string{"2026-06-01", "2026-07-01", "2026-08-01"} {
		v, _ := time.Parse("2006-01-02", d)
		dates = append(dates, v)
	}
	if Cadence(dates) != "monthly" {
		t.Fatal("monthly series not detected")
	}
	dates[1] = dates[0].AddDate(0, 0, 1)
	if Cadence(dates) != "" {
		t.Fatal("irregular invoices must not be treated as regular salary")
	}
}
