package categorize

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/alec-jensen/sofar/internal/events"
	"github.com/alec-jensen/sofar/internal/normalize"
	"github.com/alec-jensen/sofar/internal/rules"
)

// ScopeProposal tells the user how far a correction would reach before they
// choose its scope: how many other transactions from this merchant wait for
// review, and how many were already labeled.
type ScopeProposal struct {
	MerchantKey string
	Unreviewed  int
	Labeled     int
	// Suggested is the scope the card pre-selects.
	Suggested Scope
}

// Proposal builds the scope proposal for one transaction.
func (s *Service) Proposal(ctx context.Context, txnID string) (ScopeProposal, error) {
	t, err := s.Ledger.Get(ctx, txnID)
	if err != nil {
		return ScopeProposal{}, ErrNotFound
	}
	key := normalize.MerchantKey(t.Description)
	p := ScopeProposal{MerchantKey: key, Suggested: ScopeMerchant}
	set, err := s.loadTxns(ctx, s.DB)
	if err != nil {
		return p, err
	}
	for _, o := range set.txns {
		if o.ID == txnID || o.Hidden || normalize.MerchantKey(o.Description) != key {
			continue
		}
		if o.ReviewedAt == nil {
			if !o.Pending {
				p.Unreviewed++
			}
		} else {
			p.Labeled++
		}
	}
	if s.isAmbiguous(ctx, s.DB, key) {
		p.Suggested = ScopeThisTime
	}
	return p, nil
}

// Confirm accepts the live suggestion on a transaction.
func (s *Service) Confirm(ctx context.Context, tx *sql.Tx, actor events.Actor, in ReviewInput) (ReviewResult, error) {
	var cat, source, ruleID sql.NullString
	var conf float64
	err := tx.QueryRowContext(ctx, `SELECT category_id, source, confidence, rule_id FROM suggestions WHERE txn_id=$1`, in.TxnID).Scan(&cat, &source, &conf, &ruleID)
	if err == sql.ErrNoRows || !cat.Valid || cat.String == "" {
		return ReviewResult{}, fmt.Errorf("%w: there is no suggestion to confirm, choose a category", ErrInvalid)
	}
	if err != nil {
		return ReviewResult{}, err
	}
	in.Final, in.Suggested, in.Source, in.Confidence, in.RuleID = cat.String, cat.String, source.String, conf, ruleID.String
	return s.Review(ctx, tx, actor, in)
}

// BatchResult says what a by-merchant confirmation did.
type BatchResult struct {
	Confirmed []string
	// Outliers are items of the group left alone because their amount is far
	// outside the usual; the user looks at those one by one.
	Outliers []string
	Prompts  []Prompt
}

// ReviewBatch confirms every waiting transaction of one merchant with one
// category. Outliers are called out and skipped unless includeOutliers is set.
func (s *Service) ReviewBatch(ctx context.Context, tx *sql.Tx, actor events.Actor, key string, category string, scope Scope, includeOutliers bool, session string) (BatchResult, error) {
	var out BatchResult
	if key == "" || category == "" {
		return out, fmt.Errorf("%w: a merchant and a category are needed", ErrInvalid)
	}
	q, err := s.Queue(ctx, 0)
	if err != nil {
		return out, err
	}
	for _, it := range q {
		if it.GroupKey != key {
			continue
		}
		if !includeOutliers && hasFlag(it.Suggestion.Flags, FlagUnusualAmount) {
			out.Outliers = append(out.Outliers, it.Txn.ID)
			continue
		}
		sugg := ""
		if !it.Hidden {
			sugg = it.Suggestion.Category
		}
		res, err := s.Review(ctx, tx, actor, ReviewInput{TxnID: it.Txn.ID, Final: category, Scope: scope, Suggested: sugg, Source: it.Suggestion.Source, Confidence: it.Suggestion.Confidence, RuleID: it.Suggestion.RuleID, SessionID: session})
		if err != nil {
			return out, err
		}
		out.Confirmed = append(out.Confirmed, it.Txn.ID)
		out.Prompts = append(out.Prompts, res.Prompts...)
	}
	return out, nil
}

// MerchantProposal is a merchant that probably belongs in a new category.
type MerchantProposal struct {
	Key     string
	Count   int
	Current string
}

// ProposeForCategory finds merchants worth moving into a newly created
// category: those whose name shares a word with the category's name and that
// are still in the catch-all or uncategorized. It only proposes; nothing moves
// until the user says so (S18a daily review loop).
func (s *Service) ProposeForCategory(ctx context.Context, categoryName string) ([]MerchantProposal, error) {
	words := nameWords(categoryName)
	if len(words) == 0 {
		return nil, nil
	}
	set, err := s.loadTxns(ctx, s.DB)
	if err != nil {
		return nil, err
	}
	byKey := map[string]*MerchantProposal{}
	for _, t := range set.txns {
		if t.Hidden || (t.CategoryID != "" && t.CategoryID != "other_spending") {
			continue
		}
		key := normalize.MerchantKey(t.Description)
		match := false
		for _, w := range words {
			if strings.Contains(strings.ReplaceAll(key, " ", ""), w) {
				match = true
			}
		}
		if !match {
			continue
		}
		p := byKey[key]
		if p == nil {
			p = &MerchantProposal{Key: key, Current: t.CategoryID}
			byKey[key] = p
		}
		p.Count++
	}
	var out []MerchantProposal
	for _, p := range byKey {
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Key < out[j].Key
	})
	return out, nil
}

// nameWords are the words of a category name worth matching: lowercase, at
// least four letters, with a plural "s" trimmed.
func nameWords(name string) []string {
	var out []string
	for _, w := range strings.FieldsFunc(strings.ToLower(name), func(r rune) bool { return r < 'a' || r > 'z' }) {
		w = strings.TrimSuffix(w, "s")
		if len(w) >= 3 {
			out = append(out, w)
		}
	}
	return out
}

// MoveMerchants moves the chosen merchants (by key) into a category: it adds a
// user rule for each and applies it to past transactions, as one undoable
// batch event per merchant.
func (s *Service) MoveMerchants(ctx context.Context, tx *sql.Tx, actor events.Actor, category string, keys []string) ([]events.Event, error) {
	var out []events.Event
	for _, key := range keys {
		cat := category
		r, err := s.Rules.Create(ctx, tx, rules.Rule{Name: key, Layer: rules.LayerUser, Conditions: rules.Condition{Field: "merchant", Op: "contains", Value: key}, Actions: rules.Actions{Category: &cat}, Enabled: true}, "")
		if err != nil {
			return out, fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		e, _, err := s.ApplyToPast(ctx, tx, actor, r)
		if err != nil {
			return out, err
		}
		out = append(out, e)
	}
	return out, nil
}
