//go:build integration

package categorize_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/alec-jensen/sofar/internal/categorize"
	"github.com/alec-jensen/sofar/internal/civil"
	"github.com/alec-jensen/sofar/internal/events"
	"github.com/alec-jensen/sofar/internal/ledger"
	"github.com/alec-jensen/sofar/internal/rules"
	"github.com/alec-jensen/sofar/internal/testkit"
)

type env struct {
	t    *testing.T
	db   *sql.DB
	svc  *categorize.Service
	led  *ledger.Service
	acct string
}

func setup(t *testing.T) *env {
	t.Helper()
	db := testkit.DB(t)
	reg := events.NewRegistry()
	led := ledger.New(db, reg)
	return &env{t: t, db: db, svc: categorize.New(db, led, reg), led: led, acct: testkit.Account(t, db, "checking")}
}

func sp(s string) *string { return &s }

func rule(cat, merchant string) rules.Rule {
	return rules.Rule{Name: merchant, Layer: rules.LayerUser, Conditions: rules.Condition{Field: "merchant", Op: "contains", Value: merchant}, Actions: rules.Actions{Category: sp(cat)}, Enabled: true}
}

func (e *env) tx(fn func(tx *sql.Tx)) {
	e.t.Helper()
	tx, _ := e.db.Begin()
	defer tx.Rollback()
	fn(tx)
	if err := tx.Commit(); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) buy(desc, date string, cents int64, category string, reviewed bool) string {
	id := testkit.RawTxn(e.t, e.db, e.acct, testkit.Raw{PostedOn: date, Amount: -cents, Description: desc})
	if category != "" {
		e.tx(func(tx *sql.Tx) {
			e.led.Apply(context.Background(), tx, events.ActorUser, id, ledger.KindSetCategory, ledger.SetCategory{TxnID: id, CategoryID: &category}, "")
			if reviewed {
				e.led.Apply(context.Background(), tx, events.ActorUser, id, ledger.KindSetReviewed, ledger.SetReviewed{TxnID: id, Reviewed: true}, "")
			}
		})
	}
	return id
}

func (e *env) addRule(r rules.Rule) rules.Rule {
	var out rules.Rule
	e.tx(func(tx *sql.Tx) {
		var err error
		if out, err = e.svc.Rules.Create(context.Background(), tx, r, ""); err != nil {
			e.t.Fatal(err)
		}
	})
	return out
}

func TestS18a_BacktestWritesNothing(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	reviewed := e.buy("TORCHYS TACOS #12", "2026-09-01", 1500, "groceries", true)
	e.buy("TORCHYS TACOS #98", "2026-09-05", 1800, "", false)
	e.buy("KROGER #44", "2026-09-06", 5000, "", false)
	e.addRule(rule("groceries", "kroger"))
	var before int
	e.db.QueryRow(`SELECT count(*) FROM events`).Scan(&before)
	changes, err := e.svc.Backtest(ctx, rule("dining_out", "torchys"), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 2 {
		t.Fatalf("both taco rows would be decided differently: %+v", changes)
	}
	var sawReviewed bool
	for _, c := range changes {
		if c.TxnID == reviewed {
			sawReviewed = c.Reviewed && c.To == "dining_out"
		}
	}
	if !sawReviewed {
		t.Fatalf("a reviewed row is listed (and flagged reviewed): %+v", changes)
	}
	var after, ruleCount int
	e.db.QueryRow(`SELECT count(*) FROM events`).Scan(&after)
	e.db.QueryRow(`SELECT count(*) FROM rules`).Scan(&ruleCount)
	if after != before || ruleCount != 1 {
		t.Fatal("a backtest writes nothing")
	}
	total, sample, err := e.svc.Preview(ctx, rule("dining_out", "torchys"), 1)
	if err != nil || total != 2 || len(sample) != 1 {
		t.Fatalf("preview: matches 2 existing transactions, shows the limit: %d %v %v", total, sample, err)
	}
}

func TestS18a_ApplyToPastUndoable(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	a := e.buy("TORCHYS TACOS #12", "2026-09-01", 1500, "groceries", true)
	b := e.buy("TORCHYS TACOS #98", "2026-09-05", 1800, "", false)
	var ev events.Event
	var n int
	e.tx(func(tx *sql.Tx) {
		var err error
		if ev, n, err = e.svc.ApplyToPast(ctx, tx, events.ActorUser, rule("dining_out", "torchys")); err != nil {
			t.Fatal(err)
		}
	})
	if n != 2 {
		t.Fatalf("applied to %d", n)
	}
	cat := func(id string) string { g, _ := e.led.Get(ctx, id); return g.CategoryID }
	if cat(a) != "dining_out" || cat(b) != "dining_out" {
		t.Fatalf("explicit apply-to-past changes reviewed rows too: %q %q", cat(a), cat(b))
	}
	e.tx(func(tx *sql.Tx) {
		if _, err := e.svc.UndoApplyPast(ctx, tx, events.ActorUser, ev.ID); err != nil {
			t.Fatal(err)
		}
	})
	if cat(a) != "groceries" || cat(b) != "" {
		t.Fatalf("one undo restores everything: %q %q", cat(a), cat(b))
	}
	noCat := rules.Rule{Name: "x", Layer: rules.LayerUser, Conditions: rules.Condition{Field: "merchant", Op: "contains", Value: "x"}, Actions: rules.Actions{Tags: []string{"t"}}}
	tx, _ := e.db.Begin()
	defer tx.Rollback()
	if _, _, err := e.svc.ApplyToPast(ctx, tx, events.ActorUser, noCat); err == nil {
		t.Error("only category rules can be applied to the past")
	}
}

func TestS18a_ExplainReproducesTheDecision(t *testing.T) {
	e := setup(t)
	id := e.buy("TORCHYS TACOS #12", "2026-09-01", 1500, "", false)
	e.addRule(rule("dining_out", "torchys"))
	e.addRule(rule("groceries", "tacos"))
	d, err := e.svc.Explain(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if d.Result.Category == "" || len(d.Trace.Steps) != 2 {
		t.Fatalf("%+v", d)
	}
	if _, err = e.svc.Explain(context.Background(), "00000000-0000-0000-0000-000000000000"); err != categorize.ErrNotFound {
		t.Errorf("unknown transaction: %v", err)
	}
}

func TestS18a_ShadowedRuleDetected(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.buy("TORCHYS TACOS #12", "2026-09-01", 1500, "", false)
	broad := e.addRule(rule("dining_out", "tacos"))
	narrow := rule("groceries", "torchys tacos")
	narrow.Layer = rules.LayerSeed // a lower layer, so the user rule always wins first
	shadowed := e.addRule(narrow)
	unrelated := e.addRule(rule("travel", "southwest"))
	health, err := e.svc.Health(ctx)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]categorize.RuleHealth{}
	for _, h := range health {
		by[h.RuleID] = h
	}
	has := func(h categorize.RuleHealth, issue string) bool {
		for _, i := range h.Issues {
			if i == issue {
				return true
			}
		}
		return false
	}
	if !has(by[shadowed.ID], "shadowed") || !has(by[shadowed.ID], "conflicting") || !has(by[broad.ID], "conflicting") {
		t.Fatalf("the seed rule never decides and disagrees with the user rule: %+v", by[shadowed.ID])
	}
	if has(by[broad.ID], "shadowed") || !has(by[unrelated.ID], "stale") {
		t.Fatalf("broad: %+v unrelated: %+v", by[broad.ID], by[unrelated.ID])
	}
}

func TestS18a_E6_LowPrecisionAutoDemoted(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.buy("TORCHYS TACOS #12", "2026-09-01", 1500, "", false)
	r := rule("dining_out", "torchys")
	r.Layer = rules.LayerLearned
	learned := e.addRule(r)
	stale := rule("travel", "southwest")
	stale.Layer = rules.LayerLearned
	staleRule := e.addRule(stale)
	e.db.Exec(`INSERT INTO rule_stats (rule_id, hits, accepted, corrected) VALUES ($1, 10, 4, 6)`, learned.ID)
	var archived, demoted []string
	e.tx(func(tx *sql.Tx) {
		var err error
		if archived, demoted, err = e.svc.ArchiveStale(ctx, tx); err != nil {
			t.Fatal(err)
		}
	})
	if len(demoted) != 1 || demoted[0] != learned.ID {
		t.Fatalf("a learned rule corrected 60%% of the time is demoted: %v", demoted)
	}
	if len(archived) != 1 || archived[0] != staleRule.ID {
		t.Fatalf("a learned rule that never matches is archived: %v", archived)
	}
	list, _ := e.svc.Rules.List(ctx, e.db)
	if len(list) != 1 || list[0].Enabled {
		t.Fatalf("the demoted rule stays listed but disabled: %+v", list)
	}
	_ = civil.Date{}
	_ = time.Now
}
