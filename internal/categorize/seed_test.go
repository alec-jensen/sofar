package categorize

import (
	"testing"

	"github.com/alec-jensen/sofar/internal/civil"
	"github.com/alec-jensen/sofar/internal/rules"
)

var validCategories = map[string]bool{}

func init() {
	for _, id := range []string{"salary", "self_employment", "interest", "gift_other", "rent_mortgage", "utilities", "internet_phone", "insurance", "loan_payments", "other_subscriptions", "memberships",
		"general_savings", "investments", "groceries", "dining_out", "fuel", "auto_transit", "shopping", "home_household", "entertainment", "travel", "personal_care", "gifts_donations", "health_medical",
		"education", "fees_interest", "other_spending", "transfer", "credit_card_payment"} {
		validCategories[id] = true
	}
}

func TestS18a_SeedRulesUseStableIDs(t *testing.T) {
	seeds := SeedRules()
	if len(seeds) < 20 {
		t.Fatalf("expected a real directory, got %d rules", len(seeds))
	}
	for _, r := range seeds {
		if err := r.Validate(); err != nil {
			t.Fatalf("seed rule %q invalid: %v", r.Name, err)
		}
		if r.Layer != rules.LayerSeed {
			t.Errorf("%s must sit in the seed layer", r.Name)
		}
		if r.Actions.Category != nil && !validCategories[*r.Actions.Category] {
			t.Errorf("%s points at %q, which is not a stable S1a id", r.Name, *r.Actions.Category)
		}
	}
}

func decide(desc string, cents int64) string {
	rt := rules.FromBank("t", desc, "", "", "a", "budget", cents, civil.Date{Y: 2026, M: 10, D: 5})
	res, _ := rules.Evaluate(rt, SeedRules())
	return res.Category
}

func TestS18a_SeedRulesCategorize(t *testing.T) {
	for _, tc := range []struct {
		desc  string
		cents int64
		want  string
	}{
		{"PURCHASE AUTHORIZED ON 09/12 HEB #423 ARLINGTON TX", -8400, "groceries"},
		{"SQ *BLUE BOTTLE COFFEE", -650, "dining_out"},
		{"CHEVRON 0097821 XXXX1234", -4000, "fuel"},
		{"NETFLIX.COM LOS GATOS CA", -1549, "other_subscriptions"},
		{"PURCHASE INTEREST CHARGE", -2300, "fees_interest"},
		{"OVERDRAFT FEE", -3500, "fees_interest"},
		{"INTEREST PAID", 1200, "interest"},
		{"TRANSFER TO SAVINGS", -50000, "transfer"},
		{"CHASE CREDIT CRD AUTOPAY", -45000, "credit_card_payment"},
	} {
		if got := decide(tc.desc, tc.cents); got != tc.want {
			t.Errorf("%q (%d) = %q, want %q", tc.desc, tc.cents, got, tc.want)
		}
	}
}

func TestS18a_E12_NewMerchantFallsBackToPrior(t *testing.T) {
	// a merchant the directory has never seen, but whose name says what it is
	if got := decide("JOES COFFEE HOUSE #5", -540); got != "dining_out" {
		t.Errorf("the keyword prior should call a coffee house dining out, got %q", got)
	}
	if got := decide("ZXQ HOLDINGS 4412", -9900); got != "" {
		t.Errorf("an unknown, keyword-free merchant gets no seed guess: %q", got)
	}
	// word boundaries: "shell" must not fire inside another word
	if got := decide("MICHELLES BAKERY", -500); got == "fuel" {
		t.Errorf("seed rules match whole words, got %q", got)
	}
}
