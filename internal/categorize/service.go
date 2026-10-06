// Package categorize turns rules, merchants, labels and models into
// suggestions the user reviews, and lets the user inspect, preview, backtest
// and apply rules. It never confirms anything on its own (S18a, S11).
package categorize

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/alec-jensen/sofar/internal/civil"
	"github.com/alec-jensen/sofar/internal/events"
	"github.com/alec-jensen/sofar/internal/ledger"
	"github.com/alec-jensen/sofar/internal/rules"
)

// Event kinds.
const (
	KindApplyPast = "rules.apply_past"
	Subject       = "rules"
)

// ApplyPast records a bulk application of a rule to past transactions; undo
// reverses every overlay event it wrote.
type ApplyPast struct {
	RuleID   string  `json:"rule_id"`
	EventIDs []int64 `json:"event_ids"`
	Count    int     `json:"count"`
}

var (
	// ErrNotFound means no such transaction or rule.
	ErrNotFound = errors.New("categorize: not found")
	// ErrInvalid means a bad request.
	ErrInvalid = errors.New("categorize: invalid request")
)

// Service is the categorization application layer.
type Service struct {
	DB     *sql.DB
	Ledger *ledger.Service
	Rules  *rules.Store
	Reg    *events.Registry
	Now    func() time.Time
	// AuditShare is the share of high-confidence items whose suggestion is hidden (audit mode; 0 is off).
	AuditShare float64
}

// New builds a Service and registers its event payloads.
func New(db *sql.DB, l *ledger.Service, reg *events.Registry) *Service {
	reg.Register(KindApplyPast, 1, ApplyPast{})
	return &Service{DB: db, Ledger: l, Rules: &rules.Store{DB: db}, Reg: reg, Now: time.Now}
}

// txnSet is every visible transaction with its account kind, oldest first.
type txnSet struct {
	txns  []ledger.Txn
	kinds map[string]string
}

func (s *Service) loadTxns(ctx context.Context, q events.DBTX) (*txnSet, error) {
	set := &txnSet{kinds: map[string]string{}}
	rows, err := q.QueryContext(ctx, `SELECT id, kind FROM accounts`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, kind string
		rows.Scan(&id, &kind)
		set.kinds[id] = kind
	}
	rows.Close()
	cursor := ""
	var all []ledger.Txn
	for {
		page, err := s.Ledger.View(ctx, ledger.Filter{Limit: 1000, Cursor: cursor})
		if err != nil {
			return nil, err
		}
		all = append(all, page.Items...)
		if page.Next == "" {
			break
		}
		cursor = page.Next
	}
	for i := len(all) - 1; i >= 0; i-- {
		set.txns = append(set.txns, all[i])
	}
	return set, nil
}

// RuleTxn builds the evaluator's view of a ledger transaction.
func RuleTxn(t ledger.Txn, accountKind string) rules.Txn {
	rt := rules.FromBank(t.ID, t.Description, t.Payee, t.Memo, t.AccountID, accountKind, int64(t.Amount), t.PostedOn)
	rt.MCC = t.MCC
	rt.Tags = t.Tags
	return rt
}

// Decision is what the rules say about one transaction, with the reasoning.
type Decision struct {
	Result rules.Result
	Trace  rules.Trace
}

// Explain evaluates every rule against a transaction and returns the full trace
// (the rules debugger).
func (s *Service) Explain(ctx context.Context, txnID string) (Decision, error) {
	t, err := s.Ledger.Get(ctx, txnID)
	if err != nil {
		return Decision{}, ErrNotFound
	}
	kind := ""
	s.DB.QueryRowContext(ctx, `SELECT kind FROM accounts WHERE id=$1`, t.AccountID).Scan(&kind)
	rs, err := s.Rules.List(ctx, s.DB)
	if err != nil {
		return Decision{}, err
	}
	res, tr := rules.Evaluate(RuleTxn(t, kind), rs)
	return Decision{res, tr}, nil
}

// Match is a transaction a rule would apply to.
type Match struct {
	TxnID       string
	Date        civil.Date
	Description string
	Amount      int64
	Category    string // its category today
	Reviewed    bool
}

func ruleOnly(r rules.Rule) rules.Rule {
	r.Enabled = true
	if r.ID == "" {
		r.ID = "candidate"
	}
	return r
}

// Preview lists the existing transactions a candidate rule matches ("matches
// N existing transactions"). It writes nothing.
func (s *Service) Preview(ctx context.Context, r rules.Rule, limit int) (int, []Match, error) {
	r = r.WithDefaults()
	if err := r.Validate(); err != nil {
		return 0, nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	set, err := s.loadTxns(ctx, s.DB)
	if err != nil {
		return 0, nil, err
	}
	r = ruleOnly(r)
	total := 0
	var out []Match
	for i := len(set.txns) - 1; i >= 0; i-- { // newest first
		t := set.txns[i]
		res, _ := rules.Evaluate(RuleTxn(t, set.kinds[t.AccountID]), []rules.Rule{r})
		if len(res.Matched) == 0 {
			continue
		}
		total++
		if limit <= 0 || len(out) < limit {
			out = append(out, Match{t.ID, t.PostedOn, t.Description, int64(t.Amount), t.CategoryID, t.ReviewedAt != nil})
		}
	}
	return total, out, nil
}

// Change is what adding or editing a rule would do to one transaction.
type Change struct {
	TxnID       string
	Date        civil.Date
	Description string
	From, To    string
	Reviewed    bool
}

// Backtest shows what the rules would decide differently with a candidate rule
// added (or replacing replaceID), across all history. Reviewed transactions are
// listed but would change only through "apply to past". It writes nothing.
func (s *Service) Backtest(ctx context.Context, candidate rules.Rule, replaceID string) ([]Change, error) {
	candidate = candidate.WithDefaults()
	if err := candidate.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	current, err := s.Rules.List(ctx, s.DB)
	if err != nil {
		return nil, err
	}
	var after []rules.Rule
	for _, r := range current {
		if r.ID != replaceID {
			after = append(after, r)
		}
	}
	after = append(after, ruleOnly(candidate))
	set, err := s.loadTxns(ctx, s.DB)
	if err != nil {
		return nil, err
	}
	var out []Change
	for i := len(set.txns) - 1; i >= 0; i-- {
		t := set.txns[i]
		rt := RuleTxn(t, set.kinds[t.AccountID])
		was, _ := rules.Evaluate(rt, current)
		now, _ := rules.Evaluate(rt, after)
		if was.Category != now.Category {
			out = append(out, Change{t.ID, t.PostedOn, t.Description, was.Category, now.Category, t.ReviewedAt != nil})
		}
	}
	return out, nil
}

// ApplyToPast sets the category of every existing transaction a rule matches,
// reviewed ones included: this is the explicit action that overrides the
// "reviewed transactions are untouched" default (S18a retroactive scope). It
// is one batch event, and undoing it reverses every change.
func (s *Service) ApplyToPast(ctx context.Context, tx *sql.Tx, actor events.Actor, r rules.Rule) (events.Event, int, error) {
	if r.Actions.Category == nil {
		return events.Event{}, 0, fmt.Errorf("%w: only a rule that sets a category can be applied to the past", ErrInvalid)
	}
	_, matches, err := s.Preview(ctx, r, 0)
	if err != nil {
		return events.Event{}, 0, err
	}
	var ids []int64
	for _, m := range matches {
		if m.Category == *r.Actions.Category {
			continue
		}
		cat := *r.Actions.Category
		e, err := s.Ledger.Apply(ctx, tx, actor, m.TxnID, ledger.KindSetCategory, ledger.SetCategory{TxnID: m.TxnID, CategoryID: &cat}, "")
		if err != nil {
			return events.Event{}, 0, err
		}
		ids = append(ids, e.ID)
	}
	e, err := events.Append(ctx, tx, s.Reg, events.New{Actor: actor, Kind: KindApplyPast, SubjectType: Subject, SubjectID: r.ID, Payload: ApplyPast{RuleID: r.ID, EventIDs: ids, Count: len(ids)}})
	return e, len(ids), err
}

// UndoApplyPast reverses a bulk application.
func (s *Service) UndoApplyPast(ctx context.Context, tx *sql.Tx, actor events.Actor, eventID int64) (events.Event, error) {
	target, err := events.Get(ctx, tx, eventID)
	if err != nil {
		return events.Event{}, err
	}
	p, err := s.Reg.Decode(target)
	ap, ok := p.(*ApplyPast)
	if err != nil || !ok {
		return events.Event{}, ErrInvalid
	}
	u, err := events.Undo(ctx, tx, actor, eventID, "")
	if err != nil {
		return u, err
	}
	for _, id := range ap.EventIDs {
		if _, err = s.Ledger.Undo(ctx, tx, actor, id, ""); err != nil && !errors.Is(err, events.ErrAlreadyUndone) {
			return u, err
		}
	}
	return u, nil
}

// RuleHealth is a rule's record and any problems with it.
type RuleHealth struct {
	RuleID    string
	Name      string
	Layer     rules.Layer
	Matches   int // existing transactions it matches
	Wins      int // of those, how many it decided the category for
	Hits      int // recorded uses
	Accepted  int
	Corrected int
	LastMatch civil.Date
	Issues    []string // shadowed, conflicting, overlapping, stale, low_precision
}

// Health reports how each rule is doing against history: matches and wins,
// shadowed rules (matches but never decides), conflicts and overlaps with other
// rules, stale rules, and low precision.
func (s *Service) Health(ctx context.Context) ([]RuleHealth, error) {
	rs, err := s.Rules.List(ctx, s.DB)
	if err != nil {
		return nil, err
	}
	set, err := s.loadTxns(ctx, s.DB)
	if err != nil {
		return nil, err
	}
	stats := map[string][3]int{}
	rows, err := s.DB.QueryContext(ctx, `SELECT rule_id, hits, accepted, corrected FROM rule_stats`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		var h, a, c int
		rows.Scan(&id, &h, &a, &c)
		stats[id] = [3]int{h, a, c}
	}
	rows.Close()
	byID := map[string]*RuleHealth{}
	var order []string
	for _, r := range rs {
		byID[r.ID] = &RuleHealth{RuleID: r.ID, Name: r.Name, Layer: r.Layer}
		order = append(order, r.ID)
		st := stats[r.ID]
		byID[r.ID].Hits, byID[r.ID].Accepted, byID[r.ID].Corrected = st[0], st[1], st[2]
	}
	pairs := map[[2]string][2]int{} // [a,b] -> {same category, different category} among co-matches
	for _, t := range set.txns {
		res, _ := rules.Evaluate(RuleTxn(t, set.kinds[t.AccountID]), rs)
		for i, a := range res.Matched {
			h := byID[a]
			h.Matches++
			if res.Winners["category"] == a {
				h.Wins++
			}
			if h.LastMatch.IsZero() || t.PostedOn.After(h.LastMatch) {
				h.LastMatch = t.PostedOn
			}
			for _, b := range res.Matched[i+1:] {
				key := [2]string{a, b}
				p := pairs[key]
				if sameCategory(rs, a, b) {
					p[0]++
				} else {
					p[1]++
				}
				pairs[key] = p
			}
		}
	}
	today := civil.DateOf(s.Now())
	for _, id := range order {
		h := byID[id]
		if h.Matches > 0 && h.Wins == 0 && hasCategory(rs, id) {
			h.Issues = append(h.Issues, "shadowed")
		}
		if h.Matches == 0 || (!h.LastMatch.IsZero() && today.DaysSince(h.LastMatch) > 90) {
			h.Issues = append(h.Issues, "stale")
		}
		if n := h.Accepted + h.Corrected; n >= 5 && h.Corrected*100 > n*30 {
			h.Issues = append(h.Issues, "low_precision")
		}
	}
	for key, p := range pairs {
		if p[1] > 0 {
			byID[key[0]].Issues = appendOnce(byID[key[0]].Issues, "conflicting")
			byID[key[1]].Issues = appendOnce(byID[key[1]].Issues, "conflicting")
		} else if p[0] > 0 {
			byID[key[0]].Issues = appendOnce(byID[key[0]].Issues, "overlapping")
			byID[key[1]].Issues = appendOnce(byID[key[1]].Issues, "overlapping")
		}
	}
	out := make([]RuleHealth, 0, len(order))
	for _, id := range order {
		sort.Strings(byID[id].Issues)
		out = append(out, *byID[id])
	}
	return out, nil
}

func appendOnce(xs []string, s string) []string {
	for _, x := range xs {
		if x == s {
			return xs
		}
	}
	return append(xs, s)
}

func hasCategory(rs []rules.Rule, id string) bool {
	for _, r := range rs {
		if r.ID == id {
			return r.Actions.Category != nil
		}
	}
	return false
}

func sameCategory(rs []rules.Rule, a, b string) bool {
	var ca, cb string
	for _, r := range rs {
		if r.Actions.Category == nil {
			continue
		}
		switch r.ID {
		case a:
			ca = *r.Actions.Category
		case b:
			cb = *r.Actions.Category
		}
	}
	return ca == cb
}

// ArchiveStale retires enabled rules that have not matched anything for 90 days
// (or ever), and disables learned rules whose precision has fallen (S18a). It
// returns the ids it acted on.
func (s *Service) ArchiveStale(ctx context.Context, tx *sql.Tx) (archived, demoted []string, err error) {
	health, err := s.Health(ctx)
	if err != nil {
		return nil, nil, err
	}
	for _, h := range health {
		for _, issue := range h.Issues {
			switch {
			case issue == "stale" && h.Layer != rules.LayerUser:
				if err = s.Rules.Archive(ctx, tx, h.RuleID); err != nil {
					return archived, demoted, err
				}
				archived = append(archived, h.RuleID)
			case issue == "low_precision" && h.Layer == rules.LayerLearned:
				if err = s.Rules.SetEnabled(ctx, tx, h.RuleID, false); err != nil {
					return archived, demoted, err
				}
				demoted = append(demoted, h.RuleID)
			}
		}
	}
	return archived, demoted, nil
}
