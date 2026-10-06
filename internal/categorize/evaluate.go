package categorize

import (
	"context"
	"fmt"
	"hash/fnv"
	"sort"
	"time"
)

// Bucket is one bar of the calibration curve.
type Bucket struct {
	Lo, Hi   float64
	N        int
	Accepted int
}

// Accuracy is the share of this bucket where the user agreed.
func (b Bucket) Accuracy() float64 {
	if b.N == 0 {
		return 0
	}
	return float64(b.Accepted) / float64(b.N)
}

// SourceStat is acceptance for one suggestion source.
type SourceStat struct{ N, Accepted int }

// MerchantStat counts misses per merchant.
type MerchantStat struct {
	Key      string
	N, Wrong int
}

// WeekPoint is one point of the accuracy trend.
type WeekPoint struct {
	Week     string
	N        int
	Accepted int
}

// Evaluation is the prequential report: every suggestion was stored before its
// label, so agreement with the first label is an honest first-pass accuracy (S18a).
type Evaluation struct {
	Total, Accepted int
	BySource        map[string]SourceStat
	Calibration     []Bucket
	Confusion       map[string]map[string]int // suggested -> final, only disagreements
	WorstMerchants  []MerchantStat
	Trend           []WeekPoint
}

// FirstPassAccuracy is the overall agreement rate.
func (e Evaluation) FirstPassAccuracy() float64 {
	if e.Total == 0 {
		return 0
	}
	return float64(e.Accepted) / float64(e.Total)
}

// Evaluate compares each transaction's first label with the suggestion that was
// live just before it. A transaction that never had a suggestion is skipped.
func (s *Service) Evaluate(ctx context.Context) (Evaluation, error) {
	ev := Evaluation{BySource: map[string]SourceStat{}, Confusion: map[string]map[string]int{}}
	for i := 0; i < 10; i++ {
		ev.Calibration = append(ev.Calibration, Bucket{Lo: float64(i) / 10, Hi: float64(i+1) / 10})
	}
	rows, err := s.DB.QueryContext(ctx, `
		WITH first_label AS (
		  SELECT DISTINCT ON (txn_id) txn_id, final, at, merchant_key
		  FROM label_events WHERE NOT undone AND label_source IN ('user','legacy-import') ORDER BY txn_id, at, id
		)
		SELECT f.final, f.at, f.merchant_key, sl.category_id, sl.source, sl.confidence
		FROM first_label f
		JOIN LATERAL (SELECT category_id, source, confidence FROM suggestion_log WHERE txn_id=f.txn_id AND at <= f.at ORDER BY at DESC, id DESC LIMIT 1) sl ON true`)
	if err != nil {
		return ev, err
	}
	defer rows.Close()
	merchants := map[string]*MerchantStat{}
	weeks := map[string]*WeekPoint{}
	for rows.Next() {
		var final, key, sugg, source string
		var at time.Time
		var conf float64
		if err = rows.Scan(&final, &at, &key, &sugg, &source, &conf); err != nil {
			return ev, err
		}
		ok := sugg == final
		ev.Total++
		st := ev.BySource[source]
		st.N++
		m := merchants[key]
		if m == nil {
			m = &MerchantStat{Key: key}
			merchants[key] = m
		}
		m.N++
		y, w := at.ISOWeek()
		wk := fmt.Sprintf("%04d-W%02d", y, w)
		wp := weeks[wk]
		if wp == nil {
			wp = &WeekPoint{Week: wk}
			weeks[wk] = wp
		}
		wp.N++
		bi := int(conf * 10)
		if bi > 9 {
			bi = 9
		}
		ev.Calibration[bi].N++
		if ok {
			ev.Accepted++
			st.Accepted++
			wp.Accepted++
			ev.Calibration[bi].Accepted++
		} else {
			m.Wrong++
			if ev.Confusion[sugg] == nil {
				ev.Confusion[sugg] = map[string]int{}
			}
			ev.Confusion[sugg][final]++
		}
		ev.BySource[source] = st
	}
	for _, m := range merchants {
		if m.Wrong > 0 {
			ev.WorstMerchants = append(ev.WorstMerchants, *m)
		}
	}
	sort.Slice(ev.WorstMerchants, func(i, j int) bool {
		a, b := ev.WorstMerchants[i], ev.WorstMerchants[j]
		if a.Wrong != b.Wrong {
			return a.Wrong > b.Wrong
		}
		return a.Key < b.Key
	})
	if len(ev.WorstMerchants) > 10 {
		ev.WorstMerchants = ev.WorstMerchants[:10]
	}
	for _, w := range weeks {
		ev.Trend = append(ev.Trend, *w)
	}
	sort.Slice(ev.Trend, func(i, j int) bool { return ev.Trend[i].Week < ev.Trend[j].Week })
	return ev, rows.Err()
}

// Disagreement is a confirmed transaction the model now strongly disagrees with.
type Disagreement struct {
	TxnID       string
	Description string
	Confirmed   string
	ModelSays   string
	Confidence  float64
}

// RetroReview lists confirmed transactions the current model is confident
// should have been something else: possible past mistakes ("label noise") for
// the user to look at. It never changes anything.
func (s *Service) RetroReview(ctx context.Context, minConfidence float64) ([]Disagreement, error) {
	set, err := s.loadTxns(ctx, s.DB)
	if err != nil {
		return nil, err
	}
	st, err := s.loadState(ctx, s.DB, set)
	if err != nil {
		return nil, err
	}
	if st.model == nil || st.model.Trained < minModelExamples {
		return nil, nil
	}
	var out []Disagreement
	for _, t := range set.txns {
		if t.ReviewedAt == nil || t.CategoryID == "" {
			continue
		}
		p := st.model.Predict(Features(s.featureInput(t, set.kinds[t.AccountID], st)))
		if conf := st.cal.Apply(p.Prob); p.Class != t.CategoryID && conf >= minConfidence {
			out = append(out, Disagreement{t.ID, t.Description, t.CategoryID, p.Class, conf})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Confidence > out[j].Confidence })
	return out, nil
}

// auditHidden reports whether audit mode hides the suggestion on this
// high-confidence item. It is a deterministic share of items, so reloading the
// queue does not change the answer (S18a audit mode, off by default).
func auditHidden(txnID string, share float64, confidence float64) bool {
	if share <= 0 || confidence < 0.95 {
		return false
	}
	h := fnv.New32a()
	h.Write([]byte(txnID))
	return float64(h.Sum32()%10000)/10000 < share
}
