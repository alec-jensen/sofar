package sim

import (
	"reflect"
	"testing"
)

func TestSim_DeterministicBySeed(t *testing.T) {
	a, b := Generate(DefaultConfig(7)), Generate(DefaultConfig(7))
	if !reflect.DeepEqual(a, b) {
		t.Fatal("the same seed must give the same life")
	}
	c := Generate(DefaultConfig(8))
	if reflect.DeepEqual(a.Txns, c.Txns) {
		t.Fatal("a different seed must differ")
	}
}

func TestSim_ShapeOfALife(t *testing.T) {
	l := Generate(DefaultConfig(3))
	kinds := map[Kind]int{}
	cats := map[string]int{}
	for _, tx := range l.Txns {
		kinds[tx.Truth.Kind]++
		cats[tx.Truth.Category]++
	}
	for _, k := range []Kind{KindSpending, KindIncome, KindBill, KindSubscription, KindCardPayment, KindReimbursement} {
		if kinds[k] == 0 {
			t.Errorf("a year should include %s", k)
		}
	}
	if kinds[KindIncome] < 24 {
		t.Errorf("biweekly pay for a year: %d paychecks", kinds[KindIncome])
	}
	if len(l.Pulls) < 600 {
		t.Errorf("two pulls a day with some outages: %d", len(l.Pulls))
	}
	for _, c := range []string{"groceries", "dining_out", "fuel", "shopping", "home_household"} {
		if cats[c] == 0 {
			t.Errorf("no %s spending", c)
		}
	}
}

func TestSim_AmbiguousMerchantsSplitByAmount(t *testing.T) {
	l := Generate(DefaultConfig(11))
	seen := map[string]bool{}
	for _, tx := range l.Txns {
		if tx.Truth.Merchant == "Amazon" {
			seen[tx.Truth.Category] = true
		}
	}
	if len(seen) < 2 {
		t.Errorf("Amazon spans several categories: %v", seen)
	}
}

func TestSim_PendingRowsSettleAndTipsChangeAmount(t *testing.T) {
	l := Generate(DefaultConfig(5))
	var pendingSeen, tipChanged bool
	for _, p := range l.Pulls {
		for _, r := range p.Rows {
			if r.Pending {
				pendingSeen = true
			}
		}
	}
	for _, tx := range l.Txns {
		if tx.PendingAmount != 0 && tx.PendingAmount != tx.Raw.Amount {
			tipChanged = true
		}
	}
	if !pendingSeen || !tipChanged {
		t.Errorf("pending rows and tip changes should occur: %v %v", pendingSeen, tipChanged)
	}
}

func TestSim_DuplicatesRotationsAndOutages(t *testing.T) {
	cfg := DefaultConfig(2)
	cfg.DuplicateRate, cfg.RotateRate = 0.05, 0.05
	l := Generate(cfg)
	var dups, rotated int
	for _, tx := range l.Txns {
		if tx.DuplicateOf != "" {
			dups++
		}
		if tx.NewID != "" {
			rotated++
		}
	}
	if dups == 0 || rotated == 0 {
		t.Errorf("duplicates %d rotated %d", dups, rotated)
	}
	days := map[string]bool{}
	for _, p := range l.Pulls {
		days[p.Day.String()] = true
	}
	if len(days) >= 365 {
		t.Error("an outage should skip some days")
	}
	noOutage := cfg
	noOutage.OutageEvery = 0
	if got := len(Generate(noOutage).Pulls); got != 365*2 {
		t.Errorf("without outages: %d pulls", got)
	}
}

func TestSim_OracleErrorRate(t *testing.T) {
	l := Generate(DefaultConfig(4))
	o := NewOracle(1, 0.1)
	wrong, total := 0, 0
	for _, tx := range l.Txns {
		if tx.Truth.Kind != KindSpending {
			continue
		}
		total++
		if o.Label(tx) != tx.Truth.Category {
			wrong++
		}
	}
	if rate := float64(wrong) / float64(total); rate < 0.04 || rate > 0.12 {
		t.Errorf("error rate %.3f should be near 0.1 (some random picks coincide with the truth)", rate)
	}
	if perfect := NewOracle(1, 0); perfect.Label(l.Txns[0]) != l.Txns[0].Truth.Category {
		t.Error("a perfect oracle is always right")
	}
}
