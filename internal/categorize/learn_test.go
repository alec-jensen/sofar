//go:build integration

package categorize_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/alec-jensen/sofar/internal/categorize"
	"github.com/alec-jensen/sofar/internal/events"
	"github.com/alec-jensen/sofar/internal/rules"
)

func (e *env) review(txn, final string, scope categorize.Scope, mod func(*categorize.ReviewInput)) categorize.ReviewResult {
	e.t.Helper()
	in := categorize.ReviewInput{TxnID: txn, Final: final, Scope: scope, SessionID: "s1"}
	if mod != nil {
		mod(&in)
	}
	var res categorize.ReviewResult
	e.tx(func(tx *sql.Tx) {
		var err error
		if res, err = e.svc.Review(context.Background(), tx, events.ActorUser, in); err != nil {
			e.t.Fatal(err)
		}
	})
	return res
}

func (e *env) learned() []rules.Rule {
	all, _ := e.svc.Rules.List(context.Background(), e.db)
	var out []rules.Rule
	for _, r := range all {
		if r.Layer == rules.LayerLearned {
			out = append(out, r)
		}
	}
	return out
}

func TestS18a_TwoLabelsInduceMerchantRule(t *testing.T) {
	e := setup(t)
	a := e.buy("TORCHYS TACOS #12", "2026-09-01", 1500, "", false)
	b := e.buy("TORCHYS TACOS #98", "2026-09-05", 1800, "", false)
	e.review(a, "dining_out", categorize.ScopeMerchant, nil)
	if len(e.learned()) != 0 {
		t.Fatal("one label is not enough")
	}
	res := e.review(b, "dining_out", categorize.ScopeMerchant, nil)
	got := e.learned()
	if len(got) != 1 || len(res.LearnedRules) != 1 || *got[0].Actions.Category != "dining_out" {
		t.Fatalf("two consistent labels make a merchant rule: %+v", got)
	}
	c := e.buy("TORCHYS TACOS #7", "2026-09-09", 1700, "", false)
	d, _ := e.svc.Explain(context.Background(), c)
	if d.Result.Category != "dining_out" || d.Result.Winners["category"] != got[0].ID {
		t.Fatalf("the new rule decides for the next taco purchase: %+v", d.Result)
	}
}

func TestS18a_ThreeLabelsForConditionalRule(t *testing.T) {
	e := setup(t)
	small1 := e.buy("AMZN Mktp US*1A", "2026-09-01", 1200, "", false)
	small2 := e.buy("AMZN Mktp US*2B", "2026-09-02", 1500, "", false)
	big := e.buy("AMZN Mktp US*3C", "2026-09-03", 9000, "", false)
	e.review(small1, "shopping", categorize.ScopeMerchant, nil)
	e.review(small2, "shopping", categorize.ScopeMerchant, nil)
	if len(e.learned()) != 0 {
		t.Fatal("amazon is ambiguous: two consistent labels are not enough")
	}
	e.review(big, "groceries", categorize.ScopeMerchant, nil)
	got := e.learned()
	if len(got) != 2 {
		t.Fatalf("three labels that separate by amount make conditional rules: %+v", got)
	}
	probe := func(cents int64) string {
		id := e.buy("AMZN Mktp US*9Z", "2026-09-20", cents, "", false)
		d, _ := e.svc.Explain(context.Background(), id)
		return d.Result.Category
	}
	if probe(1300) != "shopping" || probe(8800) != "groceries" {
		t.Fatalf("small amounts shopping, large groceries: %q %q", probe(1300), probe(8800))
	}
}

func TestS18a_E4_AmbiguousMerchantNoRule(t *testing.T) {
	e := setup(t)
	ids := []string{
		e.buy("WALMART SUPERCENTER #1", "2026-09-01", 3000, "", false),
		e.buy("WALMART SUPERCENTER #1", "2026-09-02", 3100, "", false),
		e.buy("WALMART SUPERCENTER #1", "2026-09-03", 3200, "", false),
	}
	e.review(ids[0], "groceries", categorize.ScopeMerchant, nil)
	e.review(ids[1], "shopping", categorize.ScopeMerchant, nil)
	res := e.review(ids[2], "groceries", categorize.ScopeMerchant, nil)
	if len(e.learned()) != 0 {
		t.Fatalf("contradictory labels that do not separate create no rule: %+v", e.learned())
	}
	var asked bool
	for _, p := range res.Prompts {
		asked = asked || p.Kind == "ambiguous_merchant"
	}
	if !asked {
		t.Fatalf("it asks instead: %+v", res.Prompts)
	}
}

func TestS18a_ProbationFiveMatches(t *testing.T) {
	e := setup(t)
	a := e.buy("TORCHYS TACOS #12", "2026-09-01", 1500, "", false)
	b := e.buy("TORCHYS TACOS #98", "2026-09-05", 1800, "", false)
	e.review(a, "dining_out", categorize.ScopeMerchant, nil)
	e.review(b, "dining_out", categorize.ScopeMerchant, nil)
	r := e.learned()[0]
	left := func() int {
		var n int
		e.db.QueryRow(`SELECT probation_left FROM rules WHERE id=$1`, r.ID).Scan(&n)
		return n
	}
	if left() != 5 {
		t.Fatalf("a new learned rule starts on probation: %d", left())
	}
	for i := 0; i < 5; i++ {
		id := e.buy("TORCHYS TACOS #5", "2026-09-10", 1600, "", false)
		e.review(id, "dining_out", categorize.ScopeThisTime, func(in *categorize.ReviewInput) { in.Suggested, in.RuleID = "dining_out", r.ID })
	}
	if left() != 0 {
		t.Fatalf("five accepted matches graduate it: %d", left())
	}
}

func TestS18a_E5_CorrectionVsUserRuleAsks(t *testing.T) {
	e := setup(t)
	mine := e.addRule(rule("groceries", "torchys"))
	id := e.buy("TORCHYS TACOS #12", "2026-09-01", 1500, "", false)
	res := e.review(id, "dining_out", categorize.ScopeMerchant, func(in *categorize.ReviewInput) { in.Suggested, in.RuleID = "groceries", mine.ID })
	var asked bool
	for _, p := range res.Prompts {
		asked = asked || (p.Kind == "conflicts_with_user_rule" && p.RuleID == mine.ID)
	}
	if !asked {
		t.Fatalf("a correction against the user's own rule asks what to do: %+v", res.Prompts)
	}
	list, _ := e.svc.Rules.List(context.Background(), e.db)
	for _, r := range list {
		if r.ID == mine.ID && *r.Actions.Category != "groceries" {
			t.Fatal("the user's rule is never rewritten silently")
		}
	}
}

func TestS18a_E7_DriftPrompt(t *testing.T) {
	e := setup(t)
	a := e.buy("TORCHYS TACOS #12", "2026-09-01", 1500, "", false)
	b := e.buy("TORCHYS TACOS #98", "2026-09-05", 1800, "", false)
	e.review(a, "dining_out", categorize.ScopeMerchant, nil)
	e.review(b, "dining_out", categorize.ScopeMerchant, nil)
	r := e.learned()[0]
	var last categorize.ReviewResult
	for i := 0; i < 3; i++ {
		id := e.buy("TORCHYS TACOS #5", "2026-09-10", 1600, "", false)
		last = e.review(id, "groceries", categorize.ScopeMerchant, func(in *categorize.ReviewInput) { in.Suggested, in.RuleID = "dining_out", r.ID })
	}
	var asked bool
	for _, p := range last.Prompts {
		asked = asked || p.Kind == "rule_drift"
	}
	if !asked {
		t.Fatalf("three corrections in a row prompt: update this rule? %+v", last.Prompts)
	}
}

func TestS18a_E8_RubberStampWeightReduced(t *testing.T) {
	e := setup(t)
	a := e.buy("TORCHYS TACOS #12", "2026-09-01", 1500, "", false)
	slow := e.review(a, "dining_out", categorize.ScopeMerchant, func(in *categorize.ReviewInput) { in.Suggested, in.DwellMS = "dining_out", 4000 })
	b := e.buy("TORCHYS TACOS #98", "2026-09-05", 1800, "", false)
	fast := e.review(b, "dining_out", categorize.ScopeMerchant, func(in *categorize.ReviewInput) { in.Suggested, in.DwellMS = "dining_out", 400 })
	if fast.Weight >= slow.Weight || slow.Weight != categorize.WeightConfirm {
		t.Fatalf("a confirm in under 1.5 seconds counts for less: %v vs %v", fast.Weight, slow.Weight)
	}
	pick := e.buy("TORCHYS TACOS #7", "2026-09-06", 1700, "", false)
	if r := e.review(pick, "dining_out", categorize.ScopeMerchant, func(in *categorize.ReviewInput) { in.SessionID = "s2" }); r.Weight != categorize.WeightPick {
		t.Errorf("an explicit pick counts 2: %v", r.Weight)
	}
	fix := e.buy("TORCHYS TACOS #8", "2026-09-07", 1700, "", false)
	if r := e.review(fix, "groceries", categorize.ScopeMerchant, func(in *categorize.ReviewInput) { in.Suggested, in.SessionID = "dining_out", "s3" }); r.Weight != categorize.WeightCorrection {
		t.Errorf("a correction counts 3: %v", r.Weight)
	}
	// a burst of labels in one session is a rapid streak: the later ones count for less
	var first, lastW float64
	for i := 0; i < 12; i++ {
		id := e.buy("KROGER #44", "2026-09-08", 2000, "", false)
		w := e.review(id, "groceries", categorize.ScopeMerchant, func(in *categorize.ReviewInput) { in.SessionID = "burst" }).Weight
		if i == 0 {
			first = w
		}
		lastW = w
	}
	if lastW >= first {
		t.Fatalf("the twelfth label of a rapid streak counts for less than the first: %v vs %v", lastW, first)
	}
}

func TestS18a_E9_OneOffIsException(t *testing.T) {
	e := setup(t)
	a := e.buy("TORCHYS TACOS #12", "2026-09-01", 1500, "", false)
	b := e.buy("TORCHYS TACOS #98", "2026-09-05", 1800, "", false)
	e.review(a, "dining_out", categorize.ScopeThisTime, nil)
	e.review(b, "dining_out", categorize.ScopeThisTime, nil)
	if len(e.learned()) != 0 {
		t.Fatal("exceptions never train, however many there are")
	}
	g, _ := e.led.Get(context.Background(), a)
	if g.CategoryID != "dining_out" || g.ReviewedAt == nil {
		t.Fatalf("the transaction itself is still confirmed: %+v", g)
	}
}

func TestS18a_E11_UndoLearning(t *testing.T) {
	e := setup(t)
	a := e.buy("TORCHYS TACOS #12", "2026-09-01", 1500, "", false)
	b := e.buy("TORCHYS TACOS #98", "2026-09-05", 1800, "", false)
	e.review(a, "dining_out", categorize.ScopeMerchant, nil)
	res := e.review(b, "dining_out", categorize.ScopeMerchant, nil)
	if len(e.learned()) != 1 {
		t.Fatal("learned")
	}
	e.tx(func(tx *sql.Tx) {
		if err := e.svc.UndoLabel(context.Background(), tx, res.LabelID); err != nil {
			t.Fatal(err)
		}
	})
	if len(e.learned()) != 0 {
		t.Fatal("undoing a label removes its influence")
	}
	g, _ := e.led.Get(context.Background(), b)
	if g.CategoryID != "dining_out" {
		t.Fatal("but the transaction keeps its category")
	}
}

func TestS18a_E10_ConcurrentLabelsBothKept(t *testing.T) {
	e := setup(t)
	a := e.buy("TORCHYS TACOS #12", "2026-09-01", 1500, "", false)
	e.review(a, "dining_out", categorize.ScopeMerchant, func(in *categorize.ReviewInput) { in.SessionID = "phone" })
	e.review(a, "groceries", categorize.ScopeMerchant, func(in *categorize.ReviewInput) { in.SessionID = "laptop" })
	var n int
	e.db.QueryRow(`SELECT count(*) FROM label_events WHERE txn_id=$1`, a).Scan(&n)
	if n != 2 {
		t.Fatalf("both decisions are kept: %d", n)
	}
	if g, _ := e.led.Get(context.Background(), a); g.CategoryID != "groceries" {
		t.Fatalf("the later decision is the one in force: %q", g.CategoryID)
	}
}

func TestA9_Inv12_LearnOnlyFromUserLabels(t *testing.T) {
	e := setup(t)
	a := e.buy("TORCHYS TACOS #12", "2026-09-01", 1500, "", false)
	b := e.buy("TORCHYS TACOS #98", "2026-09-05", 1800, "", false)
	// labels written by the system (an auto-applied suggestion) must never train
	for _, id := range []string{a, b} {
		if _, err := e.db.Exec(`INSERT INTO label_events (txn_id, source, final, scope, weight, merchant_key, label_source) VALUES ($1,'model','dining_out','merchant',2,'torchy s tacos','system')`, id); err != nil {
			t.Fatal(err)
		}
	}
	e.review(a, "dining_out", categorize.ScopeThisTime, nil)
	if len(e.learned()) != 0 {
		t.Fatal("system labels do not count as evidence")
	}
	e.review(a, "dining_out", categorize.ScopeMerchant, nil)
	if len(e.learned()) != 0 {
		t.Fatal("one user label plus two system labels is still one label of evidence")
	}
}
