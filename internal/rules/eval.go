package rules

import "github.com/alec-jensen/sofar/internal/civil"

// Result is what the winning rules decided. Each single-valued field is set by
// the first rule that sets it, in evaluation order; tags accumulate.
type Result struct {
	Category               string
	Tags                   []string
	MerchantName           string
	Note                   string
	Type                   string
	ExpectingReimbursement bool
	LinkBill               string
	FromDiscretionary      bool
	Month                  string
	Hide                   bool
	Flag                   string
	SkipLearning           bool
	Split                  []SplitPart
	// Winners names the rule that set each field.
	Winners map[string]string
	// Matched lists the ids of every rule that matched, in order.
	Matched []string
}

// Step is one entry of the trace.
type Step struct {
	RuleID  string
	Name    string
	Layer   Layer
	Stage   Stage
	Matched bool
	Reason  string   // why it matched or which condition failed
	Skipped string   // set when the rule was not evaluated: disabled, expired, not yet valid, after stop
	Won     []string // the fields this rule set
}

// Trace explains an evaluation rule by rule.
type Trace struct{ Steps []Step }

// active reports whether a rule applies on a date (enabled, inside its validity window).
func (r Rule) active(on civil.Date) (bool, string) {
	if !r.Enabled {
		return false, "disabled"
	}
	if r.ValidFrom != nil && on.Before(*r.ValidFrom) {
		return false, "not valid yet"
	}
	if r.ValidTo != nil && on.After(*r.ValidTo) {
		return false, "expired"
	}
	return true, ""
}

// Evaluate runs rules against a transaction. The trace lists every rule, why it
// matched or not, and which fields it won; evaluation stops at a matching rule
// whose action says stop.
func Evaluate(t Txn, rs []Rule) (Result, Trace) {
	res := Result{Winners: map[string]string{}}
	var tr Trace
	stopped := false
	for _, r := range order(rs) {
		step := Step{RuleID: r.ID, Name: r.Name, Layer: r.Layer, Stage: r.Stage}
		if stopped {
			step.Skipped = "an earlier rule said stop"
			tr.Steps = append(tr.Steps, step)
			continue
		}
		if ok, why := r.active(t.Date); !ok {
			step.Skipped = why
			tr.Steps = append(tr.Steps, step)
			continue
		}
		ok, why := r.Conditions.match(t)
		step.Matched, step.Reason = ok, why
		if ok {
			res.Matched = append(res.Matched, r.ID)
			step.Won = apply(&res, r)
			if r.Actions.Stop {
				stopped = true
			}
		}
		tr.Steps = append(tr.Steps, step)
	}
	return res, tr
}

func apply(res *Result, r Rule) []string {
	var won []string
	set := func(field string, already bool, do func()) {
		if already {
			return
		}
		do()
		res.Winners[field] = r.ID
		won = append(won, field)
	}
	a := r.Actions
	if a.Category != nil {
		set("category", res.Category != "", func() { res.Category = *a.Category })
	}
	if a.RenameMerchant != nil {
		set("merchant_name", res.MerchantName != "", func() { res.MerchantName = *a.RenameMerchant })
	}
	if a.Note != nil {
		set("note", res.Note != "", func() { res.Note = *a.Note })
	}
	if a.Type != nil {
		set("type", res.Type != "", func() { res.Type = *a.Type })
	}
	if a.ExpectingReimbursement != nil {
		set("expecting_reimbursement", res.Winners["expecting_reimbursement"] != "", func() { res.ExpectingReimbursement = *a.ExpectingReimbursement })
	}
	if a.LinkBill != nil {
		set("link_bill", res.LinkBill != "", func() { res.LinkBill = *a.LinkBill })
	}
	if a.FromDiscretionary != nil {
		set("from_discretionary", res.Winners["from_discretionary"] != "", func() { res.FromDiscretionary = *a.FromDiscretionary })
	}
	if a.Month != nil {
		set("month", res.Month != "", func() { res.Month = *a.Month })
	}
	if a.Hide != nil {
		set("hide", res.Winners["hide"] != "", func() { res.Hide = *a.Hide })
	}
	if a.Flag != nil {
		set("flag", res.Flag != "", func() { res.Flag = *a.Flag })
	}
	if a.SkipLearning != nil {
		set("skip_learning", res.Winners["skip_learning"] != "", func() { res.SkipLearning = *a.SkipLearning })
	}
	if len(a.Split) > 0 {
		set("split", len(res.Split) > 0, func() { res.Split = append([]SplitPart(nil), a.Split...) })
	}
	for _, tag := range a.Tags {
		dup := false
		for _, have := range res.Tags {
			dup = dup || have == tag
		}
		if !dup {
			res.Tags = append(res.Tags, tag)
			if res.Winners["tags"] == "" {
				res.Winners["tags"] = r.ID
				won = append(won, "tags")
			}
		}
	}
	return won
}
