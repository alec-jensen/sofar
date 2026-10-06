package api

import (
	"context"
	"database/sql"
	"net/http"
	"strconv"

	"github.com/alec-jensen/sofar/internal/categorize"
	"github.com/alec-jensen/sofar/internal/events"
	"github.com/alec-jensen/sofar/internal/httpx"
)

type altDTO struct {
	Category   string  `json:"category"`
	Confidence float64 `json:"confidence"`
	Source     string  `json:"source"`
}

type suggestionDTO struct {
	Category     string   `json:"category"`
	Source       string   `json:"source"`
	Confidence   float64  `json:"confidence"`
	Reason       string   `json:"reason"`
	RuleID       *string  `json:"rule_id"`
	Alternatives []altDTO `json:"alternatives"`
	Flags        []string `json:"flags"`
}

type queueItemDTO struct {
	Transaction txnDTO         `json:"transaction"`
	Suggestion  *suggestionDTO `json:"suggestion"`
	GroupKey    string         `json:"group_key"`
	// Audit is true when audit mode withheld the suggestion on purpose.
	Audit bool `json:"audit"`
}

func toSuggestionDTO(sg categorize.Suggestion) *suggestionDTO {
	if sg.Category == "" {
		return nil
	}
	d := &suggestionDTO{Category: sg.Category, Source: sg.Source, Confidence: sg.Confidence, Reason: sg.Reason, RuleID: ptr(sg.RuleID), Alternatives: []altDTO{}, Flags: []string{}}
	for _, a := range sg.Alternatives {
		d.Alternatives = append(d.Alternatives, altDTO{a.Category, a.Confidence, a.Source})
	}
	if sg.Flags != nil {
		d.Flags = sg.Flags
	}
	return d
}

func (s *server) reviewQueue(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 500 {
			httpx.Fail(w, http.StatusBadRequest, "invalid_input", "limit is between 1 and 500.")
			return
		}
		limit = n
	}
	q, err := s.Categorize.Queue(r.Context(), 0)
	if err != nil {
		s.fail(w, err)
		return
	}
	items := []queueItemDTO{}
	for i, it := range q {
		if i >= limit {
			break
		}
		d := queueItemDTO{Transaction: toTxnDTO(it.Txn), GroupKey: it.GroupKey, Audit: it.Hidden}
		if !it.Hidden {
			d.Suggestion = toSuggestionDTO(it.Suggestion)
		}
		items = append(items, d)
	}
	httpx.Respond(w, map[string]any{"items": items, "total": len(q)})
}

type reviewBody struct {
	Category  string `json:"category"`
	Scope     string `json:"scope"`
	SessionID string `json:"session_id"`
	DwellMS   int    `json:"dwell_ms"`
}

type promptDTO struct {
	Kind   string  `json:"kind"`
	RuleID *string `json:"rule_id"`
	Detail string  `json:"detail"`
}

type proposalDTO struct {
	MerchantKey string `json:"merchant_key"`
	Unreviewed  int    `json:"unreviewed"`
	Labeled     int    `json:"labeled"`
	Suggested   string `json:"suggested_scope"`
}

type reviewResultDTO struct {
	Transaction  txnDTO      `json:"transaction"`
	Weight       float64     `json:"weight"`
	LearnedRules []string    `json:"learned_rules"`
	Prompts      []promptDTO `json:"prompts"`
	Proposal     proposalDTO `json:"proposal"`
}

func toPrompts(ps []categorize.Prompt) []promptDTO {
	out := []promptDTO{}
	for _, p := range ps {
		out = append(out, promptDTO{p.Kind, ptr(p.RuleID), p.Detail})
	}
	return out
}

func (s *server) reviewConfirm(w http.ResponseWriter, r *http.Request) {
	var in reviewBody
	if httpx.Decode(r, &in) != nil {
		httpx.Fail(w, http.StatusBadRequest, "invalid_input", "Invalid request.")
		return
	}
	s.finishReview(w, r, in, true)
}

func (s *server) reviewRecategorize(w http.ResponseWriter, r *http.Request) {
	var in reviewBody
	if httpx.Decode(r, &in) != nil || in.Category == "" {
		httpx.Fail(w, http.StatusBadRequest, "invalid_input", "Choose a category.")
		return
	}
	s.finishReview(w, r, in, false)
}

func (s *server) finishReview(w http.ResponseWriter, r *http.Request, in reviewBody, confirm bool) {
	id := r.PathValue("txn")
	if _, err := s.Ledger.Get(r.Context(), id); err != nil {
		s.fail(w, err)
		return
	}
	ri := categorize.ReviewInput{TxnID: id, Final: in.Category, Scope: categorize.Scope(in.Scope), SessionID: in.SessionID, DwellMS: in.DwellMS}
	var res categorize.ReviewResult
	if !s.withTx(w, r, func(ctx context.Context, tx *sql.Tx) (err error) {
		if confirm {
			res, err = s.Categorize.Confirm(ctx, tx, events.ActorUser, ri)
			return
		}
		ri.Suggested, ri.Source, ri.Confidence, ri.RuleID = s.liveSuggestion(ctx, tx, id)
		res, err = s.Categorize.Review(ctx, tx, events.ActorUser, ri)
		return
	}) {
		return
	}
	t, err := s.Ledger.Get(r.Context(), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	p, err := s.Categorize.Proposal(r.Context(), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	learned := res.LearnedRules
	if learned == nil {
		learned = []string{}
	}
	httpx.Respond(w, reviewResultDTO{Transaction: toTxnDTO(t), Weight: res.Weight, LearnedRules: learned, Prompts: toPrompts(res.Prompts),
		Proposal: proposalDTO{p.MerchantKey, p.Unreviewed, p.Labeled, string(p.Suggested)}})
}

// liveSuggestion is what the card showed, so a correction records what it corrected.
func (s *server) liveSuggestion(ctx context.Context, tx *sql.Tx, id string) (cat, source string, conf float64, ruleID string) {
	var c, so, ru sql.NullString
	if tx.QueryRowContext(ctx, `SELECT category_id, source, confidence, rule_id FROM suggestions WHERE txn_id=$1`, id).Scan(&c, &so, &conf, &ru) != nil {
		return "", "", 0, ""
	}
	return c.String, so.String, conf, ru.String
}

func (s *server) reviewBatch(w http.ResponseWriter, r *http.Request) {
	var in struct {
		reviewBody
		MerchantKey     string `json:"merchant_key"`
		IncludeOutliers bool   `json:"include_outliers"`
	}
	if httpx.Decode(r, &in) != nil || in.Category == "" || in.MerchantKey == "" {
		httpx.Fail(w, http.StatusBadRequest, "invalid_input", "Choose a merchant and a category.")
		return
	}
	var res categorize.BatchResult
	if !s.withTx(w, r, func(ctx context.Context, tx *sql.Tx) (err error) {
		res, err = s.Categorize.ReviewBatch(ctx, tx, events.ActorUser, in.MerchantKey, in.Category, categorize.Scope(in.Scope), in.IncludeOutliers, in.SessionID)
		return
	}) {
		return
	}
	confirmed, outliers := res.Confirmed, res.Outliers
	if confirmed == nil {
		confirmed = []string{}
	}
	if outliers == nil {
		outliers = []string{}
	}
	httpx.Respond(w, map[string]any{"confirmed": confirmed, "outliers": outliers, "prompts": toPrompts(res.Prompts)})
}

func (s *server) categoryName(r *http.Request, id string) (string, bool) {
	groups, err := s.Categories.List(r.Context())
	if err != nil {
		return "", false
	}
	for _, g := range groups {
		for _, c := range g.Categories {
			if c.ID == id {
				return c.Name, true
			}
		}
	}
	return "", false
}

// categoryProposals answers "move these merchants into it?" for a new category.
func (s *server) categoryProposals(w http.ResponseWriter, r *http.Request) {
	name, ok := s.categoryName(r, r.PathValue("id"))
	if !ok {
		httpx.Fail(w, http.StatusNotFound, "not_found", "Not found.")
		return
	}
	ps, err := s.Categorize.ProposeForCategory(r.Context(), name)
	if err != nil {
		s.fail(w, err)
		return
	}
	items := []map[string]any{}
	for _, p := range ps {
		items = append(items, map[string]any{"merchant_key": p.Key, "count": p.Count, "current_category": ptr(p.Current)})
	}
	httpx.Respond(w, map[string]any{"items": items})
}

func (s *server) moveMerchants(w http.ResponseWriter, r *http.Request) {
	var in struct {
		MerchantKeys []string `json:"merchant_keys"`
	}
	if httpx.Decode(r, &in) != nil || len(in.MerchantKeys) == 0 {
		httpx.Fail(w, http.StatusBadRequest, "invalid_input", "Choose at least one merchant.")
		return
	}
	id := r.PathValue("id")
	if _, ok := s.categoryName(r, id); !ok {
		httpx.Fail(w, http.StatusNotFound, "not_found", "Not found.")
		return
	}
	var evs []events.Event
	if !s.withTx(w, r, func(ctx context.Context, tx *sql.Tx) (err error) {
		evs, err = s.Categorize.MoveMerchants(ctx, tx, events.ActorUser, id, in.MerchantKeys)
		return
	}) {
		return
	}
	ids := []int64{}
	for _, e := range evs {
		ids = append(ids, e.ID)
	}
	httpx.Respond(w, map[string]any{"event_ids": ids})
}
