package categorize

import (
	"math/rand"
	"reflect"
	"testing"
)

func ex(in FeatureInput, class string) Example {
	return Example{Feats: Features(in), Class: class, Weight: 1}
}

func TestModel_LearnsMerchantsAndGeneralizesBySubstring(t *testing.T) {
	var train []Example
	for i := 0; i < 20; i++ {
		train = append(train,
			ex(FeatureInput{Merchant: "shell oil", Description: "shell oil 5521", Amount: int64(3000 + i*100)}, "fuel"),
			ex(FeatureInput{Merchant: "chevron", Description: "chevron 0097", Amount: int64(3500 + i*50)}, "fuel"),
			ex(FeatureInput{Merchant: "starbucks", Description: "starbucks", Amount: int64(450 + i*10)}, "dining_out"),
			ex(FeatureInput{Merchant: "kroger", Description: "kroger 441", Amount: int64(4000 + i*100)}, "groceries"))
	}
	m := Fit(train, 1, 6, 1)
	for in, want := range map[string]string{"shell oil": "fuel", "starbucks": "dining_out", "kroger": "groceries"} {
		if got := m.Predict(Features(FeatureInput{Merchant: in, Description: in, Amount: 3000})); got.Class != want {
			t.Errorf("%s -> %s want %s", in, got.Class, want)
		}
	}
	// an unseen merchant with a familiar n-gram leans the right way ("shell" in "shell express")
	if got := m.Predict(Features(FeatureInput{Merchant: "shell express", Description: "shell express", Amount: 3200})); got.Class != "fuel" {
		t.Errorf("n-grams generalize: %v", got)
	}
}

func TestS18a_E13_AccountConditionedSplitsPersonalVsBusiness(t *testing.T) {
	var train []Example
	for i := 0; i < 30; i++ {
		train = append(train,
			ex(FeatureInput{Merchant: "staples", Account: "personal", AccountKind: "budget", Amount: int64(2000 + i*10)}, "shopping"),
			ex(FeatureInput{Merchant: "staples", Account: "business", AccountKind: "budget", Amount: int64(2000 + i*10)}, "business_expenses"))
	}
	m := Fit(train, 1, 8, 1)
	if got := m.Predict(Features(FeatureInput{Merchant: "staples", Account: "personal", AccountKind: "budget", Amount: 2100})); got.Class != "shopping" {
		t.Errorf("personal: %v", got)
	}
	if got := m.Predict(Features(FeatureInput{Merchant: "staples", Account: "business", AccountKind: "budget", Amount: 2100})); got.Class != "business_expenses" {
		t.Errorf("business: %v", got)
	}
}

func TestS18a_E14_RecurringVsDiscretionarySameMerchant(t *testing.T) {
	var train []Example
	for i := 0; i < 30; i++ {
		train = append(train,
			ex(FeatureInput{Merchant: "apple", Recurring: true, Amount: 999}, "other_subscriptions"),
			ex(FeatureInput{Merchant: "apple", Recurring: false, Amount: int64(5000 + i*500)}, "shopping"))
	}
	m := Fit(train, 1, 8, 1)
	if got := m.Predict(Features(FeatureInput{Merchant: "apple", Recurring: true, Amount: 999})); got.Class != "other_subscriptions" {
		t.Errorf("the monthly charge: %v", got)
	}
	if got := m.Predict(Features(FeatureInput{Merchant: "apple", Recurring: false, Amount: 12000})); got.Class != "shopping" {
		t.Errorf("the one-off purchase: %v", got)
	}
}

func TestModel_DeterministicAndSerializable(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	var train []Example
	for i := 0; i < 200; i++ {
		c := []string{"a", "b", "c"}[r.Intn(3)]
		train = append(train, ex(FeatureInput{Merchant: "m" + c, Amount: int64(100 + r.Intn(500))}, c))
	}
	a, b := Fit(train, 2, 4, 9), Fit(train, 2, 4, 9)
	if !reflect.DeepEqual(a.W, b.W) || !reflect.DeepEqual(a.Bias, b.Bias) {
		t.Fatal("training is a pure function of its inputs and seed")
	}
	raw, err := a.Encode()
	if err != nil {
		t.Fatal(err)
	}
	back, err := DecodeModel(raw)
	if err != nil || !reflect.DeepEqual(back.Classes, a.Classes) || !reflect.DeepEqual(back.W, a.W) {
		t.Fatalf("roundtrip: %v", err)
	}
	if len(raw) > 2<<20 {
		t.Errorf("a trained model should compress well: %d bytes", len(raw))
	}
	if (*Model)(nil).Predict(nil).Class != "" || Fit(nil, 1, 1, 1).Predict(nil).Class != "" {
		t.Error("an empty model predicts nothing")
	}
}

func TestModel_CalibrationShrinksOverconfidence(t *testing.T) {
	// the model says 0.95 but is right only 60% of the time
	var probs []float64
	var ok []bool
	for i := 0; i < 200; i++ {
		probs = append(probs, 0.95)
		ok = append(ok, i%5 < 3)
	}
	cal := FitCalibrator(probs, ok)
	got := cal.Apply(0.95)
	if got < 0.5 || got > 0.7 {
		t.Errorf("calibrated confidence should be near 0.6, got %.3f", got)
	}
	if FitCalibrator(probs[:5], ok[:5]) != Identity {
		t.Error("too little data: identity")
	}
}

func TestMemory_NearestExplains(t *testing.T) {
	var mem Memory
	for i := 0; i < 5; i++ {
		mem.Add(Features(FeatureInput{Merchant: "chevron", Amount: int64(4000 + i*100)}), "fuel", 4000, "chevron")
	}
	mem.Add(Features(FeatureInput{Merchant: "kroger", Amount: 5000}), "groceries", 5000, "kroger")
	ns := mem.Nearest(Features(FeatureInput{Merchant: "chevron", Amount: 4200}), 4)
	class, share, why := Vote(ns)
	if class != "fuel" || share < 0.9 || why != "similar to chevron, all fuel" {
		t.Fatalf("%q %.2f %q", class, share, why)
	}
	if c, _, _ := Vote(nil); c != "" {
		t.Error("no neighbors, no vote")
	}
}
