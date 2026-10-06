//go:build integration

package categorize_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/alec-jensen/sofar/internal/categorize"
	"github.com/alec-jensen/sofar/internal/events"
)

func (e *env) refresh() int {
	e.t.Helper()
	var n int
	e.tx(func(tx *sql.Tx) {
		var err error
		if n, err = e.svc.Refresh(context.Background(), tx); err != nil {
			e.t.Fatal(err)
		}
	})
	return n
}

func (e *env) suggestion(txn string) (cat, source string, ok bool) {
	err := e.db.QueryRow(`SELECT category_id, source FROM suggestions WHERE txn_id=$1`, txn).Scan(&cat, &source)
	return cat, source, err == nil
}

func TestS18a_SuggestionLiveUntilConfirmed(t *testing.T) {
	e := setup(t)
	id := e.buy("TORCHYS TACOS #12", "2026-09-01", 1500, "", false)
	e.addRule(rule("dining_out", "torchys"))
	e.refresh()
	if cat, src, ok := e.suggestion(id); !ok || cat != "dining_out" || src != categorize.SourceUserRule {
		t.Fatalf("a rule suggests: %q %q %v", cat, src, ok)
	}
	// the rule changes: the suggestion follows, because it is still unreviewed
	list, _ := e.svc.Rules.List(context.Background(), e.db)
	e.tx(func(tx *sql.Tx) {
		r := list[0]
		c := "groceries"
		r.Actions.Category = &c
		if _, err := e.svc.Rules.Update(context.Background(), tx, r.ID, r); err != nil {
			t.Fatal(err)
		}
	})
	e.refresh()
	if cat, _, _ := e.suggestion(id); cat != "groceries" {
		t.Fatalf("suggestions stay live and follow the rules: %q", cat)
	}
	var logged int
	e.db.QueryRow(`SELECT count(*) FROM suggestion_log WHERE txn_id=$1`, id).Scan(&logged)
	if logged != 2 {
		t.Fatalf("every changed suggestion is logged before any label: %d", logged)
	}
	// confirming it removes the live suggestion
	e.review(id, "dining_out", categorize.ScopeThisTime, nil)
	if _, _, ok := e.suggestion(id); ok {
		t.Fatal("a confirmed transaction keeps no suggestion")
	}
}

func TestS18a_ReviewedNeverReevaluated(t *testing.T) {
	e := setup(t)
	id := e.buy("TORCHYS TACOS #12", "2026-09-01", 1500, "groceries", true)
	e.addRule(rule("dining_out", "torchys"))
	e.refresh()
	if _, _, ok := e.suggestion(id); ok {
		t.Fatal("a reviewed transaction gets no suggestion")
	}
	if g, _ := e.led.Get(context.Background(), id); g.CategoryID != "groceries" {
		t.Fatalf("and its category is untouched: %q", g.CategoryID)
	}
}

func TestS18a_SurpriseFlagOnOutlier(t *testing.T) {
	e := setup(t)
	for i := 0; i < 6; i++ {
		e.buy("CHEVRON 0097", fmt.Sprintf("2026-08-%02d", i+1), 4000+int64(i)*50, "fuel", true)
	}
	odd := e.buy("CHEVRON 0097", "2026-09-10", 31000, "", false)
	normal := e.buy("CHEVRON 0097", "2026-09-11", 4100, "", false)
	e.refresh()
	flags := func(id string) string {
		var f string
		e.db.QueryRow(`SELECT flags::text FROM suggestions WHERE txn_id=$1`, id).Scan(&f)
		return f
	}
	if f := flags(odd); f != `["unusual_amount"]` {
		t.Fatalf("an amount far outside the usual: %s", f)
	}
	if f := flags(normal); f != `[]` {
		t.Fatalf("a normal fill-up has no flag: %s", f)
	}
	if f := flags(e.buy("BRAND NEW PLACE", "2026-09-12", 1000, "", false)); f != "" {
		t.Logf("flags for a new merchant are only visible after refresh: %q", f)
	}
}

func TestS18a_TriageOrderUncertainFirst(t *testing.T) {
	e := setup(t)
	for i := 0; i < 6; i++ {
		e.buy("CHEVRON 0097", fmt.Sprintf("2026-08-%02d", i+1), 4000, "fuel", true)
	}
	e.addRule(rule("fuel", "chevron"))
	routine := e.buy("CHEVRON 0097", "2026-09-11", 4000, "", false)
	unusual := e.buy("CHEVRON 0097", "2026-09-10", 40000, "", false)
	unknown := e.buy("ZXQ HOLDINGS 4412", "2026-09-09", 9900, "", false)
	newKnown := e.buy("TORCHYS TACOS #1", "2026-09-08", 1500, "", false)
	e.addRule(rule("dining_out", "torchys"))
	q, err := e.svc.Queue(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, it := range q {
		order = append(order, it.Txn.ID)
	}
	want := []string{unknown, unusual, newKnown, routine}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("triage: uncertain, then unusual, then new merchant, then routine. got %v want %v", order, want)
		}
	}
}

func TestS18a_ModelLearnsFromReviewsAndRetrainsSnapshots(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	cats := map[string]string{"SHELL OIL": "fuel", "CHEVRON": "fuel", "KROGER": "groceries", "STARBUCKS": "dining_out"}
	n := 0
	for name, cat := range cats {
		for i := 0; i < 5; i++ {
			n++
			id := e.buy(fmt.Sprintf("%s #%d", name, 100+n), fmt.Sprintf("2026-08-%02d", 1+n%27), 3000+int64(n)*10, "", false)
			e.review(id, cat, categorize.ScopeThisTime, func(in *categorize.ReviewInput) { in.SessionID = fmt.Sprintf("s%d", n) })
		}
	}
	// scope this-time never trains, so there is nothing to fit yet
	e.tx(func(tx *sql.Tx) {
		if m, err := e.svc.Retrain(ctx, tx); err != nil || m.Trained != 0 {
			t.Fatalf("exceptions never train: %v %v", m, err)
		}
	})
	for name, cat := range cats {
		for i := 0; i < 4; i++ {
			n++
			id := e.buy(fmt.Sprintf("%s #%d", name, 100+n), fmt.Sprintf("2026-08-%02d", 1+n%27), 3000+int64(n)*10, "", false)
			e.review(id, cat, categorize.ScopeMerchant, func(in *categorize.ReviewInput) { in.SessionID = fmt.Sprintf("m%d", n) })
		}
	}
	var m *categorize.Model
	e.tx(func(tx *sql.Tx) {
		var err error
		if m, err = e.svc.Retrain(ctx, tx); err != nil {
			t.Fatal(err)
		}
	})
	if m.Trained < 12 || m.Version != 2 {
		t.Fatalf("trained on user labels, as the second snapshot: %+v", m.Trained)
	}
	// a merchant never labeled, with a familiar n-gram, now gets a model suggestion
	for _, r := range e.learned() {
		e.tx(func(tx *sql.Tx) { e.svc.Rules.Archive(ctx, tx, r.ID) })
	}
	id := e.buy("SHELL EXPRESS #4", "2026-09-20", 3300, "", false)
	e.refresh()
	cat, src, ok := e.suggestion(id)
	if !ok || cat != "fuel" {
		t.Fatalf("n-gram generalization: %q %q", cat, src)
	}
	var versions int
	e.db.QueryRow(`SELECT count(*) FROM model_snapshots WHERE kind='categorize'`).Scan(&versions)
	if versions != 2 {
		t.Fatalf("snapshots are versioned: %d", versions)
	}
	_ = events.ActorUser
}
