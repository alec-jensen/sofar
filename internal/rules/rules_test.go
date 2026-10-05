package rules

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/alec-jensen/sofar/internal/civil"
)

func sp(s string) *string { return &s }
func bp(b bool) *bool     { return &b }

func leaf(field, op string, v any) Condition { return Condition{Field: field, Op: op, Value: v} }

func rule(id string, layer Layer, c Condition, cat string) Rule {
	return Rule{ID: id, Name: id, Layer: layer, Stage: StageDefault, Conditions: c, Actions: Actions{Category: sp(cat)}, Priority: 100, Enabled: true}
}

var oct5 = civil.Date{Y: 2026, M: 10, D: 5}

func coffee() Txn {
	return FromBank("t1", "SQ *BLUE BOTTLE COFFEE #12", "Blue Bottle", "", "acct1", "budget", -650, oct5)
}

func TestS18a_UserRuleBeatsLearned(t *testing.T) {
	learned := rule("learned", LayerLearned, leaf("merchant", "is", "Blue Bottle Coffee"), "groceries")
	user := rule("user", LayerUser, leaf("merchant", "contains", "bottle"), "dining_out")
	seed := rule("seed", LayerSeed, leaf("merchant", "is", "blue bottle coffee"), "entertainment")
	res, _ := Evaluate(coffee(), []Rule{seed, learned, user})
	if res.Category != "dining_out" || res.Winners["category"] != "user" {
		t.Fatalf("user beats learned beats seed, even when the others are more specific: %+v", res)
	}
	if len(res.Matched) != 3 {
		t.Errorf("every rule still matches: %v", res.Matched)
	}
}

func TestS18a_SpecificityOrder(t *testing.T) {
	broad := rule("broad", LayerUser, leaf("merchant", "contains", "coffee"), "dining_out")
	narrow := rule("narrow", LayerUser, Condition{Op: "and", Args: []Condition{leaf("merchant", "is", "blue bottle coffee"), leaf("amount", "between", 500)}}, "groceries")
	narrow.Conditions.Args[1].Value2 = 800
	res, _ := Evaluate(coffee(), []Rule{broad, narrow})
	if res.Category != "groceries" {
		t.Fatalf("the narrower rule wins within a layer: %+v", res)
	}
	// equal specificity: priority, then recency
	a := rule("a", LayerUser, leaf("merchant", "contains", "coffee"), "one")
	b := rule("b", LayerUser, leaf("merchant", "contains", "coffee"), "two")
	b.Priority = 50
	if res, _ = Evaluate(coffee(), []Rule{a, b}); res.Category != "two" {
		t.Errorf("lower priority number first: %+v", res)
	}
	b.Priority = 100
	b.UpdatedAt = time.Now()
	if res, _ = Evaluate(coffee(), []Rule{a, b}); res.Category != "two" {
		t.Errorf("then the most recent: %+v", res)
	}
}

func TestS18a_StagesAndStopProcessing(t *testing.T) {
	pre := rule("pre", LayerSeed, leaf("merchant", "contains", "coffee"), "pre_cat")
	pre.Stage = StagePre
	def := rule("default", LayerUser, leaf("merchant", "contains", "coffee"), "default_cat")
	post := rule("post", LayerUser, leaf("merchant", "contains", "coffee"), "post_cat")
	post.Stage = StagePost
	post.Actions.Tags = []string{"later"}
	res, _ := Evaluate(coffee(), []Rule{post, def, pre})
	if res.Category != "pre_cat" || res.Winners["tags"] != "post" {
		t.Fatalf("pre runs first, and post can still add tags: %+v", res)
	}
	pre.Actions.Stop = true
	res, tr := Evaluate(coffee(), []Rule{post, def, pre})
	if res.Category != "pre_cat" || len(res.Matched) != 1 || res.Tags != nil {
		t.Fatalf("stop processing: %+v", res)
	}
	if tr.Steps[1].Skipped == "" || tr.Steps[2].Skipped == "" {
		t.Errorf("later rules are shown as skipped: %+v", tr.Steps)
	}
}

func TestS18a_TraceExplainsEveryRule(t *testing.T) {
	hit := rule("hit", LayerUser, leaf("merchant", "contains", "bottle"), "dining_out")
	miss := rule("miss", LayerUser, leaf("amount", "gte", 100000), "travel")
	off := rule("off", LayerUser, leaf("merchant", "contains", "bottle"), "x")
	off.Enabled = false
	_, tr := Evaluate(coffee(), []Rule{hit, miss, off})
	if len(tr.Steps) != 3 {
		t.Fatalf("one step per rule: %+v", tr.Steps)
	}
	byID := map[string]Step{}
	for _, s := range tr.Steps {
		byID[s.RuleID] = s
	}
	if !byID["hit"].Matched || byID["hit"].Won[0] != "category" || byID["hit"].Reason == "" {
		t.Errorf("hit: %+v", byID["hit"])
	}
	if byID["miss"].Matched || !strings.Contains(byID["miss"].Reason, "amount gte") {
		t.Errorf("miss says which condition failed: %+v", byID["miss"])
	}
	if byID["off"].Skipped != "disabled" {
		t.Errorf("off: %+v", byID["off"])
	}
}

func TestS18a_RegexIsRE2AndBounded(t *testing.T) {
	bad := []string{"(?<=a)b", "a(?=b)", `(a+)+\1`, "[", strings.Repeat("a", MaxRegexLen+1)}
	for _, p := range bad {
		if err := leaf("description", "regex", p).Validate(); err == nil {
			t.Errorf("%q should be refused", p)
		}
	}
	if err := leaf("description", "regex", `blue\s+bottle|peet`).Validate(); err != nil {
		t.Fatal(err)
	}
	evil := Condition{Field: "description", Op: "regex", Value: `(a|aa)+$`}
	long := coffee()
	long.Description = strings.Repeat("a", 5000) + "!"
	done := make(chan bool)
	go func() { evil.match(long); done <- true }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RE2 matching must be linear time")
	}
	// a bank string is never compiled as a pattern: a description full of regex syntax matches literally
	odd := coffee()
	odd.Description = "(a+)+ [x"
	if ok, _ := leaf("description", "contains", "(a+)+ [").match(odd); !ok {
		t.Error("contains is a literal substring test")
	}
}

func TestS18a_TimeBoundRule(t *testing.T) {
	from, to := civil.Date{Y: 2026, M: 10, D: 1}, civil.Date{Y: 2026, M: 10, D: 31}
	r := rule("promo", LayerUser, leaf("merchant", "contains", "bottle"), "entertainment")
	r.ValidFrom, r.ValidTo = &from, &to
	if res, _ := Evaluate(coffee(), []Rule{r}); res.Category != "entertainment" {
		t.Fatal("inside the window")
	}
	late := coffee()
	late.Date = civil.Date{Y: 2026, M: 11, D: 2}
	res, tr := Evaluate(late, []Rule{r})
	if res.Category != "" || tr.Steps[0].Skipped != "expired" {
		t.Fatalf("after the window: %+v %+v", res, tr.Steps[0])
	}
	early := coffee()
	early.Date = civil.Date{Y: 2026, M: 9, D: 30}
	if _, tr = Evaluate(early, []Rule{r}); tr.Steps[0].Skipped != "not valid yet" {
		t.Fatal("before the window")
	}
}

func TestS18a_OperatorsAndFields(t *testing.T) {
	txn := FromBank("t", "TRANSFER FROM SMITH,JOHN A", "John Smith", "dinner back", "a1", "budget", 5500, oct5)
	txn.Tags = []string{"Work"}
	for name, c := range map[string]Condition{
		"is":          leaf("direction", "is", "in"),
		"starts_with": leaf("description", "starts_with", "transfer from"),
		"ends_with":   leaf("description", "ends_with", "john a"),
		"one_of":      {Field: "account_kind", Op: "one_of", Value: []any{"budget", "tracked"}},
		"not_one_of":  {Field: "account", Op: "not_one_of", Value: []any{"zzz"}},
		"fuzzy":       {Field: "payee", Op: "fuzzy_gte", Value: "jon smith", Value2: 0.5},
		"memo":        leaf("memo", "contains", "dinner"),
		"within":      {Field: "amount", Op: "within_pct", Value: 5300, Value2: 10},
		"weekday":     leaf("weekday", "is", float64(oct5.Weekday())),
		"day":         leaf("day_of_month", "gte", 5),
		"tag":         leaf("tag", "is", "work"),
		"not":         {Op: "not", Args: []Condition{leaf("memo", "contains", "zzz")}},
		"or":          {Op: "or", Args: []Condition{leaf("memo", "is", "nope"), leaf("direction", "is", "in")}},
	} {
		if err := c.Validate(); err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if ok, why := c.match(txn); !ok {
			t.Errorf("%s should match: %s", name, why)
		}
	}
	if ok, _ := leaf("direction", "is", "out").match(txn); ok {
		t.Error("an inflow is not out")
	}
}

func TestS18a_ValidationRejectsNonsense(t *testing.T) {
	for name, c := range map[string]Condition{
		"unknown field":   leaf("color", "is", "x"),
		"bad op for text": leaf("merchant", "between", "x"),
		"bad op for num":  leaf("amount", "contains", 5),
		"empty and":       {Op: "and"},
		"not with two":    {Op: "not", Args: []Condition{leaf("memo", "is", "a"), leaf("memo", "is", "b")}},
		"empty value":     leaf("merchant", "is", ""),
		"between needs 2": leaf("amount", "between", 5),
		"fuzzy needs min": {Field: "payee", Op: "fuzzy_gte", Value: "x"},
	} {
		if c.Validate() == nil {
			t.Errorf("%s should be invalid", name)
		}
	}
	deep := leaf("memo", "is", "x")
	for i := 0; i < MaxDepth+2; i++ {
		deep = Condition{Op: "not", Args: []Condition{deep}}
	}
	if deep.Validate() == nil {
		t.Error("deep nesting is refused")
	}
	if (Actions{}).Validate() == nil {
		t.Error("a rule needs an action")
	}
	if (Actions{Split: []SplitPart{{"a", 5000}, {"b", 4000}}}).Validate() == nil {
		t.Error("split shares must total 100%")
	}
	if (Actions{Month: sp("nope")}).Validate() == nil {
		t.Error("bad month")
	}
}

func TestS18a_ConditionsRoundTripThroughJSON(t *testing.T) {
	c := Condition{Op: "and", Args: []Condition{leaf("merchant", "contains", "heb"), {Field: "amount", Op: "between", Value: 500, Value2: 20000}}}
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var back Condition
	if err = json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if err = back.Validate(); err != nil {
		t.Fatalf("a decoded condition validates (numbers become float64): %v", err)
	}
	txn := FromBank("t", "H-E-B #423", "", "", "a", "budget", -3000, oct5)
	if ok, _ := back.match(txn); !ok {
		t.Error("and it still matches")
	}
}

func TestS18a_ActionsFirstWinsAndTagsAccumulate(t *testing.T) {
	a := rule("a", LayerUser, leaf("merchant", "contains", "bottle"), "dining_out")
	a.Actions.Tags = []string{"coffee"}
	a.Actions.Note = sp("morning")
	b := rule("b", LayerSeed, leaf("merchant", "contains", "bottle"), "groceries")
	b.Actions.Tags = []string{"coffee", "treat"}
	b.Actions.Note = sp("ignored")
	b.Actions.FromDiscretionary = bp(true)
	res, _ := Evaluate(coffee(), []Rule{a, b})
	if res.Category != "dining_out" || res.Note != "morning" || len(res.Tags) != 2 || !res.FromDiscretionary {
		t.Fatalf("%+v", res)
	}
	if res.Winners["note"] != "a" || res.Winners["from_discretionary"] != "b" {
		t.Errorf("%v", res.Winners)
	}
}
