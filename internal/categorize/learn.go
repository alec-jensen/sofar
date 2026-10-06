package categorize

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/alec-jensen/sofar/internal/events"
	"github.com/alec-jensen/sofar/internal/ledger"
	"github.com/alec-jensen/sofar/internal/merchants"
	"github.com/alec-jensen/sofar/internal/normalize"
	"github.com/alec-jensen/sofar/internal/rules"
)

// Scope says how widely a label should generalize (S18a).
type Scope string

// Scopes. ScopeThisTime is an exception: it never trains.
const (
	ScopeThisTime    Scope = "this_time"
	ScopeMerchant    Scope = "merchant"
	ScopeAmountRange Scope = "merchant_amount_range"
	ScopeAccount     Scope = "merchant_account"
	ScopePattern     Scope = "description_pattern"
)

// Weights (S18a): an explicit pick counts 2, a correction 3, a quick confirm 1.
// A label given in under 1.5 seconds or in a rapid streak is silently reduced.
const (
	WeightPick       = 2.0
	WeightCorrection = 3.0
	WeightConfirm    = 1.0
	quickDwellMS     = 1500
	rapidWindow      = 20 * time.Second
	rapidCount       = 8
	rubberStampScale = 0.25
	halfLifeDays     = 120.0
	probationMatches = 5
)

// ReviewInput is one decision made on a review card.
type ReviewInput struct {
	TxnID      string
	Final      string // the category the user chose or confirmed
	Scope      Scope
	Suggested  string // what the card pre-selected, if anything
	Source     string // rule, learned, model, memory, prior or none
	Confidence float64
	RuleID     string
	SessionID  string
	DwellMS    int
}

// ReviewResult is what a review did, including anything that needs the user's attention.
type ReviewResult struct {
	LabelID      int64
	Weight       float64
	LearnedRules []string
	// Prompts the UI should raise: "update this rule?" after repeated corrections,
	// "this conflicts with your rule" with the choices edit, exception or delete,
	// and "ambiguous" when the merchant needs a conditional split.
	Prompts []Prompt
}

// Prompt is a question the engine asks instead of acting.
type Prompt struct {
	Kind   string // rule_drift, conflicts_with_user_rule, ambiguous_merchant
	RuleID string
	Detail string
}

// Review records the user's decision: it confirms the transaction with the
// chosen category, records a label event, and learns from it unless the scope
// is "this time". Only user labels ever train (invariant 12).
func (s *Service) Review(ctx context.Context, tx *sql.Tx, actor events.Actor, in ReviewInput) (ReviewResult, error) {
	var res ReviewResult
	t, err := s.Ledger.Get(ctx, in.TxnID)
	if err != nil {
		return res, ErrNotFound
	}
	if in.Final == "" {
		return res, fmt.Errorf("%w: choose a category", ErrInvalid)
	}
	if in.Scope == "" {
		in.Scope = ScopeMerchant
	}
	switch in.Scope {
	case ScopeThisTime, ScopeMerchant, ScopeAmountRange, ScopeAccount, ScopePattern:
	default:
		return res, fmt.Errorf("%w: unknown scope %q", ErrInvalid, in.Scope)
	}
	cat := in.Final
	if _, err = s.Ledger.Apply(ctx, tx, actor, in.TxnID, ledger.KindSetCategory, ledger.SetCategory{TxnID: in.TxnID, CategoryID: &cat}, ""); err != nil {
		return res, err
	}
	if _, err = s.Ledger.Apply(ctx, tx, actor, in.TxnID, ledger.KindSetReviewed, ledger.SetReviewed{TxnID: in.TxnID, Reviewed: true}, ""); err != nil {
		return res, err
	}

	weight := WeightPick
	switch {
	case in.Suggested != "" && in.Suggested != in.Final:
		weight = WeightCorrection
	case in.Suggested != "" && in.Suggested == in.Final:
		weight = WeightConfirm
	}
	if in.DwellMS > 0 && in.DwellMS < quickDwellMS {
		weight *= rubberStampScale
	}
	var recent int
	tx.QueryRowContext(ctx, `SELECT count(*) FROM label_events WHERE session_id=$1 AND session_id<>'' AND at > $2`, in.SessionID, s.Now().Add(-rapidWindow)).Scan(&recent)
	if recent >= rapidCount {
		weight *= rubberStampScale
	}
	if in.Scope == ScopeThisTime {
		weight = 0 // an exception: recorded, never trained on
	}
	key := normalize.MerchantKey(t.Description)
	var ruleID, dwell any
	if in.RuleID != "" {
		ruleID = in.RuleID
	}
	if in.DwellMS > 0 {
		dwell = in.DwellMS
	}
	var sugg any
	if in.Suggested != "" {
		sugg = in.Suggested
	}
	err = tx.QueryRowContext(ctx, `
		INSERT INTO label_events (txn_id, suggested, source, confidence, final, scope, weight, session_id, dwell_ms, merchant_key, amount, account_id, rule_id, label_source, at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,'user',$14) RETURNING id`,
		in.TxnID, sugg, orNone(in.Source), in.Confidence, in.Final, string(in.Scope), weight, in.SessionID, dwell, key, int64(t.Amount.Abs()), t.AccountID, ruleID, s.Now()).Scan(&res.LabelID)
	if err != nil {
		return res, err
	}
	res.Weight = weight

	if in.RuleID != "" {
		s.recordRuleOutcome(ctx, tx, in.RuleID, in.Suggested == in.Final)
	}
	// the suggestion has been answered
	if _, err = tx.ExecContext(ctx, `DELETE FROM suggestions WHERE txn_id=$1`, in.TxnID); err != nil {
		return res, err
	}
	if in.Scope == ScopeThisTime {
		// an exception to the rule that suggested it, if any: it will not apply to this transaction again
		if in.RuleID != "" {
			tx.ExecContext(ctx, `INSERT INTO rule_exceptions (txn_id, rule_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, in.TxnID, in.RuleID)
		}
		return res, nil
	}
	res.LearnedRules, res.Prompts, err = s.relearn(ctx, tx, key, in)
	return res, err
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

func (s *Service) recordRuleOutcome(ctx context.Context, tx *sql.Tx, ruleID string, accepted bool) {
	col := "corrected"
	if accepted {
		col = "accepted"
	}
	tx.ExecContext(ctx, `INSERT INTO rule_stats (rule_id, hits, `+col+`, last_hit) VALUES ($1,1,1,$2)
		ON CONFLICT (rule_id) DO UPDATE SET hits=rule_stats.hits+1, `+col+`=rule_stats.`+col+`+1, last_hit=$2`, ruleID, s.Now())
	if accepted {
		tx.ExecContext(ctx, `UPDATE rules SET probation_left=greatest(probation_left-1,0) WHERE id=$1`, ruleID)
	}
}

// UndoLabel removes a label's influence without touching the transaction (S18a
// "undo learning"): the learned rules are rebuilt from the remaining labels.
func (s *Service) UndoLabel(ctx context.Context, tx *sql.Tx, labelID int64) error {
	var key string
	err := tx.QueryRowContext(ctx, `UPDATE label_events SET undone=true WHERE id=$1 AND NOT undone RETURNING merchant_key`, labelID).Scan(&key)
	if err != nil {
		return ErrNotFound
	}
	_, _, err = s.relearn(ctx, tx, key, ReviewInput{})
	return err
}

type label struct {
	final   string
	amount  int64
	account string
	weight  float64
	at      time.Time
	rule    sql.NullString
	source  string
}

// evidence loads the training labels for a merchant. Only labels the user gave
// (or the legacy import carried over) count: the system's own guesses never
// train it (invariant 12).
func (s *Service) evidence(ctx context.Context, q events.DBTX, key string) ([]label, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT final, coalesce(amount,0), coalesce(account_id::text,''), weight, at, rule_id::text, source
		FROM label_events
		WHERE merchant_key=$1 AND NOT undone AND scope <> 'this_time' AND label_source IN ('user','legacy-import') AND weight > 0
		ORDER BY at, id`, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []label
	for rows.Next() {
		var l label
		if err = rows.Scan(&l.final, &l.amount, &l.account, &l.weight, &l.at, &l.rule, &l.source); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// decayed ages a label's weight: older labels count less (S18a drift).
func (s *Service) decayed(l label) float64 {
	days := s.Now().Sub(l.at).Hours() / 24
	if days < 0 {
		days = 0
	}
	return l.weight * math.Pow(0.5, days/halfLifeDays)
}

// learnedName names a merchant's learned rules so they can be replaced together.
func learnedName(key string, n int) string { return fmt.Sprintf("learned: %s #%d", key, n) }

func (s *Service) isAmbiguous(ctx context.Context, q events.DBTX, key string) bool {
	var amb bool
	q.QueryRowContext(ctx, `SELECT ambiguous FROM merchants WHERE canonical_key=$1`, key).Scan(&amb)
	return amb || merchants.SeedAmbiguous(key)
}

// relearn rebuilds a merchant's learned rules from its labels (S18a induction).
//
// Two consistent labels make a merchant rule for a non-ambiguous merchant. An
// ambiguous merchant, or one whose labels disagree, needs three labels that
// separate cleanly by account or by amount to become conditional rules.
// Contradictory labels that do not separate create no rule and ask instead.
func (s *Service) relearn(ctx context.Context, tx *sql.Tx, key string, in ReviewInput) (created []string, prompts []Prompt, err error) {
	if key == "" {
		return nil, nil, nil
	}
	labels, err := s.evidence(ctx, tx, key)
	if err != nil {
		return nil, nil, err
	}
	// drop the old learned rules for this merchant (archived, never deleted)
	rows, err := tx.QueryContext(ctx, `SELECT id FROM rules WHERE layer='learned' AND archived_at IS NULL AND name LIKE $1`, "learned: "+key+" #%")
	if err != nil {
		return nil, nil, err
	}
	var old []string
	for rows.Next() {
		var id string
		rows.Scan(&id)
		old = append(old, id)
	}
	rows.Close()
	for _, id := range old {
		if err = s.Rules.Archive(ctx, tx, id); err != nil {
			return nil, nil, err
		}
	}
	if len(labels) == 0 {
		return nil, nil, nil
	}

	weightBy := map[string]float64{}
	count := map[string]int{}
	for _, l := range labels {
		weightBy[l.final] += s.decayed(l)
		count[l.final]++
	}
	ambiguous := s.isAmbiguous(ctx, tx, key)
	mk := func(extra []rules.Condition, cat string, n int) error {
		conds := append([]rules.Condition{{Field: "merchant", Op: "is", Value: key}}, extra...)
		c := rules.Condition{Op: "and", Args: conds}
		r, err := s.Rules.Create(ctx, tx, rules.Rule{Name: learnedName(key, n), Layer: rules.LayerLearned, Stage: rules.StageDefault, Conditions: c, Actions: rules.Actions{Category: sp(cat)}, Priority: 100, Provenance: "learned"}, "")
		if err != nil {
			return err
		}
		tx.ExecContext(ctx, `UPDATE rules SET probation_left=$2 WHERE id=$1`, r.ID, probationMatches)
		created = append(created, r.ID)
		return nil
	}

	switch {
	case len(count) == 1:
		var cat string
		for c := range count {
			cat = c
		}
		need := 2
		if ambiguous {
			need = 3
		}
		if count[cat] >= need {
			err = mk(nil, cat, 1)
		}
	case len(labels) >= 3:
		if parts, ok := separateByAccount(labels); ok {
			n := 0
			for acct, cat := range parts {
				n++
				if err = mk([]rules.Condition{{Field: "account", Op: "is", Value: acct}}, cat, n); err != nil {
					return created, prompts, err
				}
			}
		} else if bands, ok := separateByAmount(labels); ok {
			for i, b := range bands {
				cond := rules.Condition{Field: "amount", Op: "between", Value: float64(b.lo), Value2: float64(b.hi)}
				if err = mk([]rules.Condition{cond}, b.cat, i+1); err != nil {
					return created, prompts, err
				}
			}
		} else {
			prompts = append(prompts, Prompt{Kind: "ambiguous_merchant", Detail: key})
			tx.ExecContext(ctx, `UPDATE merchants SET ambiguous=true WHERE canonical_key=$1`, key)
		}
	}
	if err != nil {
		return created, prompts, err
	}
	prompts = append(prompts, s.driftPrompts(ctx, tx, key, in, labels)...)
	return created, prompts, nil
}

// separateByAccount maps accounts to categories when each account has exactly one.
func separateByAccount(ls []label) (map[string]string, bool) {
	m := map[string]string{}
	for _, l := range ls {
		if l.account == "" {
			return nil, false
		}
		if c, ok := m[l.account]; ok && c != l.final {
			return nil, false
		}
		m[l.account] = l.final
	}
	cats := map[string]bool{}
	for _, c := range m {
		cats[c] = true
	}
	return m, len(m) >= 2 && len(cats) >= 2
}

type band struct {
	cat    string
	lo, hi int64
}

// separateByAmount finds contiguous amount bands, one category each, with at
// least one label per band and no interleaving.
func separateByAmount(ls []label) ([]band, bool) {
	sorted := append([]label(nil), ls...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].amount < sorted[j].amount })
	var bands []band
	for _, l := range sorted {
		if n := len(bands); n > 0 && bands[n-1].cat == l.final {
			bands[n-1].hi = l.amount
			continue
		}
		bands = append(bands, band{l.final, l.amount, l.amount})
	}
	seen := map[string]bool{}
	for _, b := range bands {
		if seen[b.cat] {
			return nil, false // categories interleave: not separable by amount
		}
		seen[b.cat] = true
	}
	if len(bands) < 2 {
		return nil, false
	}
	// stretch the bands to cover the gaps between them, so a new amount lands somewhere
	for i := range bands {
		if i == 0 {
			bands[i].lo = 0
		}
		if i+1 < len(bands) {
			mid := (bands[i].hi + bands[i+1].lo) / 2
			bands[i].hi, bands[i+1].lo = mid, mid+1
		} else {
			bands[i].hi = 1 << 40
		}
	}
	return bands, true
}

// driftPrompts raises the questions that need the user: a learned rule corrected
// three times in a row, and a correction that conflicts with one of their own rules.
func (s *Service) driftPrompts(ctx context.Context, q events.DBTX, key string, in ReviewInput, ls []label) []Prompt {
	var out []Prompt
	if in.RuleID == "" {
		return out
	}
	var layer string
	if q.QueryRowContext(ctx, `SELECT layer FROM rules WHERE id::text=$1`, in.RuleID).Scan(&layer) != nil {
		return out
	}
	if in.Suggested != "" && in.Suggested != in.Final && layer == string(rules.LayerUser) {
		out = append(out, Prompt{Kind: "conflicts_with_user_rule", RuleID: in.RuleID, Detail: "edit the rule, add an exception, or delete it"})
	}
	if layer == string(rules.LayerLearned) {
		rows, err := q.QueryContext(ctx, `SELECT suggested, final FROM label_events WHERE rule_id::text=$1 AND NOT undone ORDER BY at DESC, id DESC LIMIT 3`, in.RuleID)
		if err != nil {
			return out
		}
		defer rows.Close()
		corrections := 0
		for rows.Next() {
			var sug sql.NullString
			var fin string
			rows.Scan(&sug, &fin)
			if sug.Valid && sug.String != fin {
				corrections++
			}
		}
		if corrections == 3 {
			out = append(out, Prompt{Kind: "rule_drift", RuleID: in.RuleID, Detail: "you corrected this rule three times in a row. update it?"})
		}
	}
	return out
}
