// Package rules is the categorization rules language and evaluator (S18a):
// condition trees over transaction fields, actions, layers (user, learned,
// seed) and stages (pre, default, post), with a trace that explains every
// decision. Regular expressions are RE2 only and length-capped, and bank text
// is never turned into a pattern.
package rules

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/alec-jensen/sofar/internal/civil"
	"github.com/alec-jensen/sofar/internal/normalize"
)

// Layer says where a rule came from; user rules beat learned rules beat seed rules.
type Layer string

// Layers, highest priority first.
const (
	LayerUser    Layer = "user"
	LayerLearned Layer = "learned"
	LayerSeed    Layer = "seed"
)

func (l Layer) rank() int {
	switch l {
	case LayerUser:
		return 0
	case LayerLearned:
		return 1
	}
	return 2
}

// Stage orders rules before, during and after the default pass.
type Stage string

// Stages in the order they run.
const (
	StagePre     Stage = "pre"
	StageDefault Stage = "default"
	StagePost    Stage = "post"
)

func (s Stage) rank() int {
	switch s {
	case StagePre:
		return 0
	case StageDefault:
		return 1
	}
	return 2
}

// Limits keep rules safe to store and run.
const (
	MaxRegexLen    = 200
	MaxValueLen    = 300
	MaxDepth       = 6
	MaxConditions  = 40
	MaxOneOfValues = 100
)

// Condition is a node of the condition tree: either a boolean combinator
// (Op and, or, not, with Args) or a leaf (Field, Op, Value).
type Condition struct {
	Op     string      `json:"op"`
	Args   []Condition `json:"args,omitempty"`
	Field  string      `json:"field,omitempty"`
	Value  any         `json:"value,omitempty"` // string, number or []any depending on the operator
	Value2 any         `json:"value2,omitempty"`
}

// Fields a condition may test.
var fieldKinds = map[string]string{
	"merchant": "text", "description": "text", "payee": "text", "memo": "text", "mcc": "text", "processor": "text",
	"account": "text", "account_kind": "text", "direction": "text", "tag": "text",
	"amount": "number", "weekday": "number", "day_of_month": "number",
}

var textOps = map[string]bool{"is": true, "is_not": true, "contains": true, "not_contains": true, "starts_with": true, "ends_with": true, "regex": true, "not_regex": true, "one_of": true, "not_one_of": true, "fuzzy_gte": true}
var numberOps = map[string]bool{"is": true, "is_not": true, "between": true, "gte": true, "lte": true, "within_pct": true, "one_of": true}

// Validate checks a condition tree.
func (c Condition) Validate() error {
	n := 0
	return c.validate(0, &n)
}

func (c Condition) validate(depth int, n *int) error {
	if depth > MaxDepth {
		return errors.New("conditions are nested too deeply")
	}
	*n++
	if *n > MaxConditions {
		return errors.New("too many conditions")
	}
	switch c.Op {
	case "and", "or":
		if len(c.Args) == 0 {
			return fmt.Errorf("%s needs at least one condition", c.Op)
		}
		for _, a := range c.Args {
			if err := a.validate(depth+1, n); err != nil {
				return err
			}
		}
		return nil
	case "not":
		if len(c.Args) != 1 {
			return errors.New("not takes exactly one condition")
		}
		return c.Args[0].validate(depth+1, n)
	}
	kind, ok := fieldKinds[c.Field]
	if !ok {
		return fmt.Errorf("unknown field %q", c.Field)
	}
	if kind == "text" {
		if !textOps[c.Op] {
			return fmt.Errorf("operator %q does not apply to %s", c.Op, c.Field)
		}
		switch c.Op {
		case "one_of", "not_one_of":
			vs, ok := c.Value.([]any)
			if !ok || len(vs) == 0 || len(vs) > MaxOneOfValues {
				return errors.New("one_of needs a list of values")
			}
			for _, v := range vs {
				if s, ok := v.(string); !ok || len(s) > MaxValueLen {
					return errors.New("one_of values must be short strings")
				}
			}
		case "fuzzy_gte":
			if _, ok := num(c.Value2); !ok {
				return errors.New("fuzzy_gte needs a minimum similarity between 0 and 1")
			}
			fallthrough
		default:
			s, ok := c.Value.(string)
			if !ok || s == "" || len(s) > MaxValueLen {
				return fmt.Errorf("%s needs a short text value", c.Op)
			}
			if c.Op == "regex" || c.Op == "not_regex" {
				if len(s) > MaxRegexLen {
					return errors.New("regular expressions are limited to 200 characters")
				}
				if _, err := regexp.Compile(s); err != nil {
					return fmt.Errorf("invalid regular expression: %v", err)
				}
			}
		}
		return nil
	}
	if !numberOps[c.Op] {
		return fmt.Errorf("operator %q does not apply to %s", c.Op, c.Field)
	}
	if _, ok := num(c.Value); !ok && c.Op != "one_of" {
		return fmt.Errorf("%s needs a number", c.Op)
	}
	if c.Op == "between" || c.Op == "within_pct" {
		if _, ok := num(c.Value2); !ok {
			return fmt.Errorf("%s needs a second number", c.Op)
		}
	}
	return nil
}

func num(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	}
	return 0, false
}

// specificity scores how narrow a condition is: more and stricter leaves rank higher.
func (c Condition) specificity() int {
	switch c.Op {
	case "and":
		t := 0
		for _, a := range c.Args {
			t += a.specificity()
		}
		return t
	case "or":
		best := 0
		for _, a := range c.Args {
			if s := a.specificity(); best == 0 || s < best {
				best = s
			}
		}
		return best
	case "not":
		return 0
	case "is", "one_of":
		return 4
	case "starts_with", "ends_with", "between", "within_pct":
		return 3
	case "contains", "gte", "lte", "fuzzy_gte":
		return 2
	}
	return 1
}

// Actions are what a rule does. Pointer and slice fields are nil when unset.
type Actions struct {
	Category               *string     `json:"category,omitempty"`
	Tags                   []string    `json:"tags,omitempty"`
	RenameMerchant         *string     `json:"rename_merchant,omitempty"`
	Note                   *string     `json:"note,omitempty"`
	Type                   *string     `json:"type,omitempty"`
	ExpectingReimbursement *bool       `json:"expecting_reimbursement,omitempty"`
	LinkBill               *string     `json:"link_bill,omitempty"`
	FromDiscretionary      *bool       `json:"from_discretionary,omitempty"`
	Month                  *string     `json:"month,omitempty"`
	Hide                   *bool       `json:"hide,omitempty"`
	Flag                   *string     `json:"flag,omitempty"`
	SkipLearning           *bool       `json:"skip_learning,omitempty"`
	Split                  []SplitPart `json:"split,omitempty"`
	Stop                   bool        `json:"stop,omitempty"` // stop processing later rules
}

// SplitPart divides a transaction by percentage (basis points) between categories.
type SplitPart struct {
	Category    string `json:"category"`
	BasisPoints int    `json:"basis_points"`
}

// Validate checks the actions.
func (a Actions) Validate() error {
	if a.Category == nil && len(a.Tags) == 0 && a.RenameMerchant == nil && a.Note == nil && a.Type == nil && a.ExpectingReimbursement == nil &&
		a.LinkBill == nil && a.FromDiscretionary == nil && a.Month == nil && a.Hide == nil && a.Flag == nil && a.SkipLearning == nil && len(a.Split) == 0 && !a.Stop {
		return errors.New("a rule needs at least one action")
	}
	if a.Month != nil {
		if _, err := civil.ParseYearMonth(*a.Month); err != nil {
			return errors.New("month must look like 2026-10")
		}
	}
	if len(a.Split) > 0 {
		if len(a.Split) < 2 {
			return errors.New("a split needs at least two parts")
		}
		total := 0
		for _, p := range a.Split {
			if p.Category == "" || p.BasisPoints <= 0 {
				return errors.New("each split part needs a category and a share")
			}
			total += p.BasisPoints
		}
		if total != 10000 {
			return errors.New("split shares must add up to 100%")
		}
	}
	for _, t := range a.Tags {
		if t == "" || len(t) > 60 {
			return errors.New("tags are 1 to 60 characters")
		}
	}
	return nil
}

// Rule is one rule.
type Rule struct {
	ID         string
	Name       string
	Layer      Layer
	Stage      Stage
	Conditions Condition
	Actions    Actions
	Priority   int // lower runs first within a layer, after specificity
	Enabled    bool
	Provenance string
	ValidFrom  *civil.Date
	ValidTo    *civil.Date
	UpdatedAt  time.Time
}

// Validate checks the rule.
func (r Rule) Validate() error {
	switch r.Layer {
	case LayerUser, LayerLearned, LayerSeed:
	default:
		return fmt.Errorf("unknown layer %q", r.Layer)
	}
	switch r.Stage {
	case StagePre, StageDefault, StagePost:
	default:
		return fmt.Errorf("unknown stage %q", r.Stage)
	}
	if err := r.Conditions.Validate(); err != nil {
		return err
	}
	return r.Actions.Validate()
}

// Txn is the transaction as the evaluator sees it.
type Txn struct {
	ID          string
	Description string
	Merchant    string // normalized key
	Payee       string
	Memo        string
	MCC         string
	Processor   string
	Account     string
	AccountKind string
	Amount      int64 // cents, signed
	Date        civil.Date
	Tags        []string
}

// FromBank builds a Txn from raw bank fields, cleaning the strings.
func FromBank(id, description, payee, memo, account, accountKind string, amount int64, date civil.Date) Txn {
	c := normalize.Clean(description)
	return Txn{ID: id, Description: c.Raw, Merchant: c.Key, Payee: normalize.Sanitize(payee), Memo: normalize.Sanitize(memo), Processor: c.Processor, Account: account, AccountKind: accountKind, Amount: amount, Date: date}
}

func (t Txn) text(field string) string {
	switch field {
	case "merchant":
		return t.Merchant
	case "description":
		return strings.ToLower(t.Description)
	case "payee":
		return strings.ToLower(t.Payee)
	case "memo":
		return strings.ToLower(t.Memo)
	case "mcc":
		return t.MCC
	case "processor":
		return t.Processor
	case "account":
		return t.Account
	case "account_kind":
		return t.AccountKind
	case "direction":
		if t.Amount > 0 {
			return "in"
		}
		return "out"
	}
	return ""
}

func (t Txn) number(field string) float64 {
	switch field {
	case "amount":
		a := t.Amount
		if a < 0 {
			a = -a
		}
		return float64(a)
	case "weekday":
		return float64(t.Date.Weekday())
	case "day_of_month":
		return float64(t.Date.D)
	}
	return 0
}

var regexCache = map[string]*regexp.Regexp{}

func compile(p string) *regexp.Regexp {
	if re, ok := regexCache[p]; ok {
		return re
	}
	re, err := regexp.Compile("(?i)" + p)
	if err != nil {
		re = nil
	}
	if len(regexCache) < 4096 {
		regexCache[p] = re
	}
	return re
}

// match evaluates a condition against a transaction and returns a short reason.
func (c Condition) match(t Txn) (bool, string) {
	switch c.Op {
	case "and":
		for _, a := range c.Args {
			if ok, why := a.match(t); !ok {
				return false, why
			}
		}
		return true, "all conditions held"
	case "or":
		last := ""
		for _, a := range c.Args {
			ok, why := a.match(t)
			if ok {
				return true, why
			}
			last = why
		}
		return false, last
	case "not":
		ok, _ := c.Args[0].match(t)
		return !ok, "not"
	}
	if fieldKinds[c.Field] == "number" {
		return c.matchNumber(t)
	}
	return c.matchText(t)
}

func (c Condition) matchText(t Txn) (bool, string) {
	var got string
	if c.Field == "tag" {
		want, _ := c.Value.(string)
		for _, tag := range t.Tags {
			if strings.EqualFold(tag, want) {
				got = tag
			}
		}
		if c.Op == "is" || c.Op == "contains" {
			return got != "", fmt.Sprintf("tag %q", want)
		}
		return got == "", fmt.Sprintf("tag %q", want)
	}
	got = t.text(c.Field)
	val, _ := c.Value.(string)
	lv := strings.ToLower(val)
	spaced := got // regular expressions and fuzzy matching see the spaced key
	if c.Field == "merchant" {
		// compare without spaces, so "heb" matches "h e b" and "bluebottle" matches "blue bottle"
		lv = strings.ReplaceAll(normalize.Normalize(val), " ", "")
		got = strings.ReplaceAll(got, " ", "")
	}
	why := fmt.Sprintf("%s %s %q", c.Field, strings.ReplaceAll(c.Op, "_", " "), val)
	switch c.Op {
	case "is":
		return got == lv, why
	case "is_not":
		return got != lv, why
	case "contains":
		return strings.Contains(got, lv), why
	case "not_contains":
		return !strings.Contains(got, lv), why
	case "starts_with":
		return strings.HasPrefix(got, lv), why
	case "ends_with":
		return strings.HasSuffix(got, lv), why
	case "regex", "not_regex":
		re := compile(val)
		matched := re != nil && re.MatchString(spaced)
		if c.Op == "not_regex" {
			return !matched, why
		}
		return matched, why
	case "one_of", "not_one_of":
		in := false
		for _, v := range c.Value.([]any) {
			s, _ := v.(string)
			if strings.ToLower(s) == got || (c.Field == "merchant" && strings.ReplaceAll(normalize.Normalize(s), " ", "") == got) {
				in = true
			}
		}
		if c.Op == "not_one_of" {
			return !in, why
		}
		return in, why
	case "fuzzy_gte":
		min, _ := num(c.Value2)
		return normalize.Similarity(spaced, val) >= min, why
	}
	return false, why
}

func (c Condition) matchNumber(t Txn) (bool, string) {
	got := t.number(c.Field)
	v, _ := num(c.Value)
	why := fmt.Sprintf("%s %s %v", c.Field, strings.ReplaceAll(c.Op, "_", " "), v)
	switch c.Op {
	case "is":
		return got == v, why
	case "is_not":
		return got != v, why
	case "gte":
		return got >= v, why
	case "lte":
		return got <= v, why
	case "between":
		v2, _ := num(c.Value2)
		return got >= v && got <= v2, why
	case "within_pct":
		pct, _ := num(c.Value2)
		return v > 0 && got >= v*(1-pct/100) && got <= v*(1+pct/100), why
	case "one_of":
		for _, x := range c.Value.([]any) {
			if f, ok := num(x); ok && f == got {
				return true, why
			}
		}
	}
	return false, why
}

// order sorts rules the way the evaluator runs them: stage, then layer, then
// specificity (narrow first), then priority, then recency (newer first).
func order(rs []Rule) []Rule {
	out := append([]Rule(nil), rs...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Stage.rank() != b.Stage.rank() {
			return a.Stage.rank() < b.Stage.rank()
		}
		if a.Layer.rank() != b.Layer.rank() {
			return a.Layer.rank() < b.Layer.rank()
		}
		if sa, sb := a.Conditions.specificity(), b.Conditions.specificity(); sa != sb {
			return sa > sb
		}
		if a.Priority != b.Priority {
			return a.Priority < b.Priority
		}
		if !a.UpdatedAt.Equal(b.UpdatedAt) {
			return a.UpdatedAt.After(b.UpdatedAt)
		}
		return a.ID < b.ID
	})
	return out
}
