//go:build integration

package categorize_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/rs/zerolog"

	"github.com/alec-jensen/sofar/internal/categorize"
	"github.com/alec-jensen/sofar/internal/events"
	"github.com/alec-jensen/sofar/internal/jobs"
)

func TestS18a_BatchByMerchantCallsOutOutliers(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	for i := 0; i < 6; i++ {
		e.buy("CHEVRON 0097", fmt.Sprintf("2026-08-%02d", i+1), 4000+int64(i)*50, "fuel", true)
	}
	n1 := e.buy("CHEVRON 0097", "2026-09-10", 4100, "", false)
	n2 := e.buy("CHEVRON 0097", "2026-09-11", 3900, "", false)
	odd := e.buy("CHEVRON 0097", "2026-09-12", 31000, "", false)
	e.buy("KROGER 12", "2026-09-12", 5000, "", false)
	var res categorize.BatchResult
	e.tx(func(tx *sql.Tx) {
		var err error
		res, err = e.svc.ReviewBatch(ctx, tx, events.ActorUser, "chevron", "fuel", categorize.ScopeMerchant, false, "b1")
		if err != nil {
			t.Fatal(err)
		}
	})
	if len(res.Confirmed) != 2 || len(res.Outliers) != 1 || res.Outliers[0] != odd {
		t.Fatalf("the usual two are confirmed and the 310 dollar fill-up is called out: %+v", res)
	}
	for _, id := range []string{n1, n2} {
		if g, _ := e.led.Get(ctx, id); g.CategoryID != "fuel" || g.ReviewedAt == nil {
			t.Fatalf("confirmed: %+v", g)
		}
	}
	if g, _ := e.led.Get(ctx, odd); g.ReviewedAt != nil {
		t.Fatal("an outlier stays in the queue")
	}
	// the user can still include it on purpose
	e.tx(func(tx *sql.Tx) {
		r, err := e.svc.ReviewBatch(ctx, tx, events.ActorUser, "chevron", "fuel", categorize.ScopeThisTime, true, "b2")
		if err != nil || len(r.Confirmed) != 1 {
			t.Fatalf("%+v %v", r, err)
		}
	})
	e.tx(func(tx *sql.Tx) {
		if _, err := e.svc.ReviewBatch(ctx, tx, events.ActorUser, "", "fuel", "", false, ""); err == nil {
			t.Fatal("a batch needs a merchant")
		}
	})
}

func TestS18a_E15_BackfillLabeledLikeEverythingElse(t *testing.T) {
	e := setup(t)
	// two years of history arrive at once, and are reviewed the same way as today's
	old := e.buy("TORCHYS TACOS #5521", "2024-03-02", 3500, "", false)
	now := e.buy("TORCHYS TACOS #5522", "2026-09-30", 3600, "", false)
	a := e.review(old, "dining_out", categorize.ScopeMerchant, func(in *categorize.ReviewInput) { in.SessionID = "old" })
	b := e.review(now, "dining_out", categorize.ScopeMerchant, func(in *categorize.ReviewInput) { in.SessionID = "new" })
	if a.Weight != b.Weight || a.Weight == 0 {
		t.Fatalf("a backfilled label weighs the same as a fresh one: %v vs %v", a.Weight, b.Weight)
	}
	if len(e.learned()) != 1 {
		t.Fatalf("and learns the same rule, once: %+v", e.learned())
	}
}

func TestS18a_ConfirmWithoutSuggestionIsRefused(t *testing.T) {
	e := setup(t)
	id := e.buy("ZXQ HOLDINGS 4412", "2026-09-01", 1500, "", false)
	e.tx(func(tx *sql.Tx) {
		if _, err := e.svc.Confirm(context.Background(), tx, events.ActorUser, categorize.ReviewInput{TxnID: id}); err == nil {
			t.Fatal("nothing to confirm")
		}
	})
}

func TestS18a_NightlyJobIsIdempotent(t *testing.T) {
	e := setup(t)
	for i := 0; i < 3; i++ {
		id := e.buy(fmt.Sprintf("TORCHYS TACOS #%d", i), fmt.Sprintf("2026-09-%02d", i+1), 1500, "", false)
		e.review(id, "dining_out", categorize.ScopeMerchant, func(in *categorize.ReviewInput) { in.SessionID = fmt.Sprintf("n%d", i) })
	}
	e.buy("TORCHYS TACOS #9", "2026-09-20", 1500, "", false)
	h := categorize.NightlyHandler(e.svc, zerolog.Nop())
	for i := 0; i < 2; i++ {
		if err := h(context.Background(), jobs.Job{Kind: categorize.NightlyKind, Key: "categorize.nightly:2026-10-05"}); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	e.db.QueryRow(`SELECT count(*) FROM suggestions`).Scan(&n)
	if n != 1 {
		t.Fatalf("the one waiting transaction has one live suggestion: %d", n)
	}
	var logged int
	e.db.QueryRow(`SELECT count(*) FROM suggestion_log`).Scan(&logged)
	if logged != 1 {
		t.Fatalf("a repeated run logs nothing new: %d", logged)
	}
}
