package categorize

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/alec-jensen/sofar/internal/events"
	"github.com/alec-jensen/sofar/internal/ledger"
	"github.com/alec-jensen/sofar/internal/normalize"
	"github.com/alec-jensen/sofar/internal/rules"
)

// Source names what produced a suggestion.
const (
	SourceUserRule    = "user_rule"
	SourceLearnedRule = "learned_rule"
	SourceSeedRule    = "seed_rule"
	SourceModel       = "model"
	SourceMemory      = "memory"
	SourcePrior       = "prior"
	SourceNone        = "none"
)

// Alternative is another plausible category.
type Alternative struct {
	Category   string  `json:"category"`
	Confidence float64 `json:"confidence"`
	Source     string  `json:"source"`
}

// Suggestion is one category proposal with its reasoning.
type Suggestion struct {
	TxnID        string
	Category     string
	Source       string
	Confidence   float64
	Reason       string
	RuleID       string
	Alternatives []Alternative
	Flags        []string
}

// Flags raised on a card.
const (
	FlagUnusualAmount   = "unusual_amount"
	FlagNewMerchant     = "new_merchant"
	FlagPossibleDup     = "possible_duplicate"
	FlagPossibleReimb   = "possible_reimbursement"
	FlagAmbiguousVendor = "ambiguous_merchant"
)

// minModelExamples is how many labels the model needs before it is trusted.
const minModelExamples = 12

// state is what arbitration needs, loaded once per batch.
type state struct {
	rules   []rules.Rule
	byID    map[string]rules.Rule
	prob    map[string]int // rule id -> probation matches left
	model   *Model
	cal     Calibrator
	memory  Memory
	history map[string][]int64 // merchant key -> confirmed amounts
	known   map[string]bool    // merchant keys with at least one user label
	kinds   map[string]string
	dups    map[string]bool
	exc     map[[2]string]bool // txn, rule
}

func (s *Service) loadState(ctx context.Context, q events.DBTX, set *txnSet) (*state, error) {
	st := &state{byID: map[string]rules.Rule{}, prob: map[string]int{}, history: map[string][]int64{}, known: map[string]bool{}, kinds: set.kinds, dups: map[string]bool{}, exc: map[[2]string]bool{}, cal: Identity}
	var err error
	if st.rules, err = s.Rules.List(ctx, q); err != nil {
		return nil, err
	}
	for _, r := range st.rules {
		st.byID[r.ID] = r
	}
	rows, err := q.QueryContext(ctx, `SELECT id, probation_left FROM rules WHERE archived_at IS NULL AND probation_left > 0`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		var n int
		rows.Scan(&id, &n)
		st.prob[id] = n
	}
	rows.Close()
	rows, err = q.QueryContext(ctx, `SELECT txn_id, rule_id FROM rule_exceptions`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var a, b string
		rows.Scan(&a, &b)
		st.exc[[2]string{a, b}] = true
	}
	rows.Close()

	// model and calibrator: the latest snapshot
	var blob, metrics []byte
	err = q.QueryRowContext(ctx, `SELECT weights, metrics FROM model_snapshots WHERE kind='categorize' ORDER BY version DESC LIMIT 1`).Scan(&blob, &metrics)
	if err == nil {
		if m, derr := DecodeModel(blob); derr == nil {
			st.model = m
			var meta struct{ A, B float64 }
			if json.Unmarshal(metrics, &meta) == nil && (meta.A != 0 || meta.B != 0) {
				st.cal = Calibrator{meta.A, meta.B}
			}
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	// memory and history from what the user has confirmed
	fp := map[string]int{}
	for _, t := range set.txns {
		key := normalize.MerchantKey(t.Description)
		if t.ReviewedAt != nil && t.CategoryID != "" {
			fi := s.featureInput(t, set.kinds[t.AccountID], st)
			st.memory.Add(Features(fi), t.CategoryID, int64(t.Amount.Abs()), key)
			st.history[key] = append(st.history[key], int64(t.Amount.Abs()))
			st.known[key] = true
		}
		fp[fmt.Sprintf("%s|%d|%d|%s", t.AccountID, t.PostedOn.D+31*int(t.PostedOn.M), t.Amount, key)]++
	}
	for _, t := range set.txns {
		k := fmt.Sprintf("%s|%d|%d|%s", t.AccountID, t.PostedOn.D+31*int(t.PostedOn.M), t.Amount, normalize.MerchantKey(t.Description))
		if fp[k] > 1 {
			st.dups[t.ID] = true
		}
	}
	return st, nil
}

func medianOf(xs []int64) int64 {
	if len(xs) == 0 {
		return 0
	}
	c := append([]int64(nil), xs...)
	sort.Slice(c, func(i, j int) bool { return c[i] < c[j] })
	return c[len(c)/2]
}

func (s *Service) featureInput(t ledger.Txn, kind string, st *state) FeatureInput {
	c := normalize.Clean(t.Description)
	fi := FeatureInput{Merchant: c.Key, Description: c.Raw, Payee: t.Payee, Memo: t.Memo, Processor: c.Processor, Account: t.AccountID, AccountKind: kind, MCC: t.MCC,
		Amount: int64(t.Amount.Abs()), Inflow: t.Amount > 0, Weekday: int(t.PostedOn.Weekday()), DayOfMonth: t.PostedOn.D}
	if st != nil {
		fi.MerchantMedian = medianOf(st.history[c.Key])
	}
	return fi
}

// ruleConfidence rates a rule's suggestion by layer, and by probation.
func (st *state) ruleConfidence(r rules.Rule) (float64, string) {
	switch r.Layer {
	case rules.LayerUser:
		return 0.98, SourceUserRule
	case rules.LayerLearned:
		if st.prob[r.ID] > 0 {
			return 0.75, SourceLearnedRule
		}
		return 0.92, SourceLearnedRule
	}
	if r.Stage == rules.StagePre {
		return 0.8, SourceSeedRule
	}
	if r.Priority >= 200 {
		return 0.4, SourcePrior // keywords are the starter prior
	}
	return 0.7, SourceSeedRule
}

// suggest arbitrates every layer into one suggestion with a calibrated
// confidence, a reason and up to three alternatives (S18a). Rules by layer
// beat the statistical layers; among the weaker sources the most confident wins.
func (s *Service) suggest(t ledger.Txn, st *state) Suggestion {
	kind := st.kinds[t.AccountID]
	rt := RuleTxn(t, kind)
	live := make([]rules.Rule, 0, len(st.rules))
	for _, r := range st.rules {
		if !st.exc[[2]string{t.ID, r.ID}] {
			live = append(live, r)
		}
	}
	res, _ := rules.Evaluate(rt, live)
	fi := s.featureInput(t, kind, st)
	feats := Features(fi)

	out := Suggestion{TxnID: t.ID, Source: SourceNone}
	var cands []Alternative
	add := func(c string, conf float64, src string) {
		if c != "" {
			cands = append(cands, Alternative{c, conf, src})
		}
	}
	if res.Category != "" {
		r := st.byID[res.Winners["category"]]
		conf, src := st.ruleConfidence(r)
		out.RuleID = r.ID
		add(res.Category, conf, src)
		out.Reason = fmt.Sprintf("rule: %s", r.Name)
	}
	// other matching rules are alternatives
	for _, id := range res.Matched {
		if r := st.byID[id]; r.Actions.Category != nil && *r.Actions.Category != res.Category {
			conf, src := st.ruleConfidence(r)
			add(*r.Actions.Category, conf*0.8, src)
		}
	}
	var modelClass string
	if st.model != nil && st.model.Trained >= minModelExamples {
		p := st.model.Predict(feats)
		modelClass = p.Class
		for i, tp := range p.Top {
			conf := st.cal.Apply(tp.Prob)
			if conf > 0.97 {
				conf = 0.97
			}
			if i == 0 {
				add(tp.Class, conf, SourceModel)
			} else {
				add(tp.Class, conf, SourceModel)
			}
		}
	}
	ns := st.memory.Nearest(feats, 5)
	memClass, share, why := Vote(ns)
	if memClass != "" && len(ns) > 0 {
		conf := share * ns[0].Similarity
		add(memClass, conf, SourceMemory)
		if out.Reason == "" {
			out.Reason = why
		}
	}
	_ = modelClass

	if len(cands) == 0 {
		// unmatched: Other in the bucket the amount's sign suggests (S1a)
		out.Category, out.Confidence = "other_spending", 0.1
		if t.Amount > 0 {
			out.Category = "gift_other"
		}
		out.Reason = "no rule or history matches yet"
	} else {
		// user and learned rules decide outright; otherwise the most confident source
		best := 0
		for i, c := range cands {
			if c.Source == SourceUserRule || c.Source == SourceLearnedRule {
				best = i
				break
			}
			if c.Confidence > cands[best].Confidence {
				best = i
			}
		}
		pick := cands[best]
		out.Category, out.Confidence, out.Source = pick.Category, pick.Confidence, pick.Source
		if pick.Source == SourceModel {
			out.Reason = "learned from your past reviews"
		} else if pick.Source == SourceMemory && out.Reason == "" {
			out.Reason = why
		} else if pick.Source == SourcePrior {
			out.Reason = "a guess from the merchant name"
		}
		seen := map[string]bool{out.Category: true}
		sort.SliceStable(cands, func(i, j int) bool { return cands[i].Confidence > cands[j].Confidence })
		for _, c := range cands {
			if !seen[c.Category] && len(out.Alternatives) < 3 {
				seen[c.Category] = true
				out.Alternatives = append(out.Alternatives, c)
			}
		}
	}

	// flags
	key := fi.Merchant
	if !st.known[key] {
		out.Flags = append(out.Flags, FlagNewMerchant)
	} else if h := st.history[key]; len(h) >= 5 {
		if m := medianOf(h); m > 0 && (fi.Amount > m*3 || fi.Amount*3 < m) {
			out.Flags = append(out.Flags, FlagUnusualAmount)
		}
	}
	if st.dups[t.ID] {
		out.Flags = append(out.Flags, FlagPossibleDup)
	}
	return out
}

// Triage orders suggestions for review: uncertain first, then unusual amounts,
// then new merchants, then routine items (S18a).
func triageRank(sg Suggestion) int {
	switch {
	case sg.Confidence < 0.6 || sg.Source == SourceNone:
		return 0
	case hasFlag(sg.Flags, FlagUnusualAmount):
		return 1
	case hasFlag(sg.Flags, FlagNewMerchant):
		return 2
	}
	return 3
}

func hasFlag(fs []string, f string) bool {
	for _, x := range fs {
		if x == f {
			return true
		}
	}
	return false
}

// QueueItem is a transaction waiting for review with its suggestion.
type QueueItem struct {
	Txn        ledger.Txn
	Suggestion Suggestion
	GroupKey   string // merchant, for review by merchant
	Rank       int
}

// Queue returns the unreviewed transactions in triage order, with live
// suggestions. Suggestions stay live until the user confirms (S11).
func (s *Service) Queue(ctx context.Context, limit int) ([]QueueItem, error) {
	set, err := s.loadTxns(ctx, s.DB)
	if err != nil {
		return nil, err
	}
	st, err := s.loadState(ctx, s.DB, set)
	if err != nil {
		return nil, err
	}
	var out []QueueItem
	for _, t := range set.txns {
		if t.ReviewedAt != nil || t.Pending || t.Hidden {
			continue
		}
		sg := s.suggest(t, st)
		out = append(out, QueueItem{Txn: t, Suggestion: sg, GroupKey: normalize.MerchantKey(t.Description), Rank: triageRank(sg)})
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Rank != b.Rank {
			return a.Rank < b.Rank
		}
		if a.Suggestion.Confidence != b.Suggestion.Confidence {
			return a.Suggestion.Confidence < b.Suggestion.Confidence
		}
		return a.Txn.PostedOn.After(b.Txn.PostedOn)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Refresh recomputes suggestions for every unreviewed transaction and stores
// them, logging each new or changed suggestion before any label arrives
// (prequential evaluation). Reviewed transactions are never touched.
func (s *Service) Refresh(ctx context.Context, tx *sql.Tx) (changed int, err error) {
	set, err := s.loadTxns(ctx, tx)
	if err != nil {
		return 0, err
	}
	st, err := s.loadState(ctx, tx, set)
	if err != nil {
		return 0, err
	}
	version := 0
	if st.model != nil {
		version = st.model.Version
	}
	live := map[string]bool{}
	for _, t := range set.txns {
		if t.ReviewedAt != nil || t.Pending || t.Hidden {
			continue
		}
		sg := s.suggest(t, st)
		live[t.ID] = true
		alts, _ := json.Marshal(sg.Alternatives)
		flags, _ := json.Marshal(append([]string{}, sg.Flags...))
		var oldCat, oldSrc sql.NullString
		tx.QueryRowContext(ctx, `SELECT category_id, source FROM suggestions WHERE txn_id=$1`, t.ID).Scan(&oldCat, &oldSrc)
		var rid any
		if sg.RuleID != "" {
			rid = sg.RuleID
		}
		if _, err = tx.ExecContext(ctx, `
			INSERT INTO suggestions (txn_id, category_id, source, confidence, reason, alternatives, rule_id, flags, model_version, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,now())
			ON CONFLICT (txn_id) DO UPDATE SET category_id=EXCLUDED.category_id, source=EXCLUDED.source, confidence=EXCLUDED.confidence, reason=EXCLUDED.reason,
			  alternatives=EXCLUDED.alternatives, rule_id=EXCLUDED.rule_id, flags=EXCLUDED.flags, model_version=EXCLUDED.model_version, updated_at=now()`,
			t.ID, sg.Category, sg.Source, sg.Confidence, sg.Reason, alts, rid, flags, version); err != nil {
			return changed, err
		}
		if oldCat.String != sg.Category || oldSrc.String != sg.Source {
			changed++
			if _, err = tx.ExecContext(ctx, `INSERT INTO suggestion_log (txn_id, category_id, source, confidence, model_version, at) VALUES ($1,$2,$3,$4,$5,$6)`,
				t.ID, sg.Category, sg.Source, sg.Confidence, version, s.Now()); err != nil {
				return changed, err
			}
		}
	}
	// answered or vanished transactions keep no live suggestion
	rows, err := tx.QueryContext(ctx, `SELECT txn_id FROM suggestions`)
	if err != nil {
		return changed, err
	}
	var stale []string
	for rows.Next() {
		var id string
		rows.Scan(&id)
		if !live[id] {
			stale = append(stale, id)
		}
	}
	rows.Close()
	for _, id := range stale {
		if _, err = tx.ExecContext(ctx, `DELETE FROM suggestions WHERE txn_id=$1`, id); err != nil {
			return changed, err
		}
	}
	return changed, nil
}

// Retrain refits the model from the user's labels (never from the system's own
// guesses, invariant 12), with older labels decayed, and stores it as the next
// snapshot version. Rolling back is selecting an earlier version.
func (s *Service) Retrain(ctx context.Context, tx *sql.Tx) (*Model, error) {
	set, err := s.loadTxns(ctx, tx)
	if err != nil {
		return nil, err
	}
	byID := map[string]ledger.Txn{}
	for _, t := range set.txns {
		byID[t.ID] = t
	}
	st := &state{history: map[string][]int64{}}
	for _, t := range set.txns {
		if t.ReviewedAt != nil {
			st.history[normalize.MerchantKey(t.Description)] = append(st.history[normalize.MerchantKey(t.Description)], int64(t.Amount.Abs()))
		}
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT txn_id, final, weight, at FROM label_events
		WHERE NOT undone AND scope <> 'this_time' AND label_source IN ('user','legacy-import') AND weight > 0 ORDER BY at, id`)
	if err != nil {
		return nil, err
	}
	var train []Example
	for rows.Next() {
		var id, final string
		var w float64
		var at time.Time
		if err = rows.Scan(&id, &final, &w, &at); err != nil {
			rows.Close()
			return nil, err
		}
		t, ok := byID[id]
		if !ok {
			continue
		}
		train = append(train, Example{Feats: Features(s.featureInput(t, set.kinds[t.AccountID], st)), Class: final, Weight: s.decayed(label{weight: w, at: at})})
	}
	rows.Close()
	var next int
	tx.QueryRowContext(ctx, `SELECT coalesce(max(version),0)+1 FROM model_snapshots WHERE kind='categorize'`).Scan(&next)
	m := Fit(train, next, 6, 1)
	blob, err := m.Encode()
	if err != nil {
		return nil, err
	}
	metrics, _ := json.Marshal(map[string]any{"examples": len(train), "classes": len(m.Classes), "A": 1.0, "B": 0.0})
	_, err = tx.ExecContext(ctx, `INSERT INTO model_snapshots (kind, version, weights, trained_through, metrics) VALUES ('categorize',$1,$2,$3,$4)`, next, blob, s.Now(), metrics)
	return m, err
}
