//go:build integration

package recurring_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/alec-jensen/sofar/internal/civil"
	"github.com/alec-jensen/sofar/internal/events"
	"github.com/alec-jensen/sofar/internal/ledger"
	"github.com/alec-jensen/sofar/internal/recurrence"
	"github.com/alec-jensen/sofar/internal/recurring"
	"github.com/alec-jensen/sofar/internal/testkit"
)

type env struct {
	t    *testing.T
	db   *sql.DB
	svc  *recurring.Service
	acct string
}

func setup(t *testing.T) *env {
	t.Helper()
	db := testkit.DB(t)
	reg := events.NewRegistry()
	led := ledger.New(db, reg)
	svc := recurring.New(db, reg, led, time.UTC)
	svc.Now = func() time.Time { return time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC) }
	return &env{t: t, db: db, svc: svc, acct: testkit.Account(t, db, "checking")}
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

func (e *env) charge(desc string, cents int64, dates ...string) []string {
	var ids []string
	for _, d := range dates {
		ids = append(ids, testkit.RawTxn(e.t, e.db, e.acct, testkit.Raw{PostedOn: d, Amount: -cents, Description: desc}))
	}
	return ids
}

func TestS5_DetectionAsksBeforeAdding(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.charge("City Water Utility", 4500, "2026-07-10", "2026-08-10", "2026-09-10")
	cs, err := e.svc.Candidates(ctx)
	if err != nil || len(cs) != 1 || cs[0].Key != "city water utility" {
		t.Fatalf("candidates: %+v %v", cs, err)
	}
	items, _ := e.svc.List(ctx, e.db)
	if len(items) != 0 {
		t.Fatal("detection only asks: nothing is added until the user confirms")
	}
	var item recurring.Item
	e.tx(func(tx *sql.Tx) {
		var err error
		if item, err = e.svc.Confirm(ctx, tx, events.ActorUser, cs[0], ""); err != nil {
			t.Fatal(err)
		}
	})
	if item.Kind != recurrence.ItemBill || item.Amount != 4500 || item.ActiveFrom != (civil.Date{Y: 2026, M: 10, D: 1}) {
		t.Fatalf("a confirmed bill starts this month: %+v", item)
	}
	if cs, _ = e.svc.Candidates(ctx); len(cs) != 0 {
		t.Fatalf("an answered candidate is not offered again: %+v", cs)
	}
}

func TestS5_NotRecurringIsRemembered(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.charge("Random Gym", 3000, "2026-07-03", "2026-08-03", "2026-09-03")
	var ev events.Event
	e.tx(func(tx *sql.Tx) {
		var err error
		if ev, err = e.svc.Dismiss(ctx, tx, events.ActorUser, "random gym", "out"); err != nil {
			t.Fatal(err)
		}
	})
	if cs, _ := e.svc.Candidates(ctx); len(cs) != 0 {
		t.Fatalf("dismissed: %+v", cs)
	}
	e.tx(func(tx *sql.Tx) {
		if _, err := e.svc.Undo(ctx, tx, events.ActorUser, ev.ID); err != nil {
			t.Fatal(err)
		}
	})
	if cs, _ := e.svc.Candidates(ctx); len(cs) != 1 {
		t.Fatalf("undoing the dismissal brings it back: %+v", cs)
	}
}

func TestS14_MarkAsRecurringBypassesThreshold(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	ids := e.charge("Zebra Annual Dues", 90000, "2026-03-10")
	// one charge, one year ago: detection would never find it
	if cs, _ := e.svc.Candidates(ctx); len(cs) != 0 {
		t.Fatalf("one charge is not a candidate: %+v", cs)
	}
	sched := recurrence.Schedule{Kind: recurrence.KindCalendar, Anchor: civil.Date{Y: 2026, M: 3, D: 10}, Calendar: recurrence.CalendarRule{Kind: recurrence.Monthly, Day: 10, EveryMonths: 12}}
	var item recurring.Item
	var ev events.Event
	e.tx(func(tx *sql.Tx) {
		var err error
		item, err = e.svc.MarkAsRecurring(ctx, tx, events.ActorUser, ids[0], recurring.MarkParams{Kind: recurrence.ItemBill, Schedule: sched, CategoryID: "memberships"})
		if err != nil {
			t.Fatal(err)
		}
		ev, _ = events.Get(ctx, tx, 1)
	})
	_ = ev
	if item.Amount != 90000 || item.Key != "zebra annual dues" || item.CategoryID != "memberships" || item.Monthly(civil.Date{Y: 2026, M: 10, D: 1}) {
		t.Fatalf("an annual bill is a sinking item: %+v", item)
	}
	var links int
	e.db.QueryRow(`SELECT count(*) FROM recurring_links WHERE item_id=$1`, item.ID).Scan(&links)
	if links != 1 {
		t.Fatalf("the transaction is attached: %d", links)
	}
}

func TestS14_MarkAsRecurringValidates(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	id := e.charge("Thing", 1000, "2026-09-01")[0]
	tx, _ := e.db.Begin()
	defer tx.Rollback()
	if _, err := e.svc.MarkAsRecurring(ctx, tx, events.ActorUser, id, recurring.MarkParams{Kind: recurrence.ItemBill}); err == nil {
		t.Error("a schedule is required")
	}
	if _, err := e.svc.MarkAsRecurring(ctx, tx, events.ActorUser, id, recurring.MarkParams{Kind: recurrence.ItemPaymentPlan, Schedule: recurrence.Schedule{Kind: recurrence.KindCalendar, Calendar: recurrence.CalendarRule{Kind: recurrence.Monthly, Day: 1}}}); err == nil {
		t.Error("a payment plan needs its installments")
	}
	if _, err := e.svc.MarkAsRecurring(ctx, tx, events.ActorUser, "00000000-0000-0000-0000-000000000000", recurring.MarkParams{}); err == nil {
		t.Error("unknown transaction")
	}
}

func TestS5_CreateAndEndAreUndoable(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	sched := recurrence.Schedule{Kind: recurrence.KindCalendar, Calendar: recurrence.CalendarRule{Kind: recurrence.Monthly, Day: 3}}
	var item recurring.Item
	e.tx(func(tx *sql.Tx) {
		var err error
		if item, err = e.svc.Create(ctx, tx, events.ActorUser, recurring.Params{Kind: recurrence.ItemBill, Name: "Water", Amount: 5000, Schedule: sched}); err != nil {
			t.Fatal(err)
		}
	})
	var end events.Event
	e.tx(func(tx *sql.Tx) {
		var err error
		if end, err = e.svc.EndItem(ctx, tx, events.ActorUser, item.ID, civil.Date{Y: 2026, M: 10, D: 1}); err != nil {
			t.Fatal(err)
		}
	})
	got, _ := e.svc.Get(ctx, e.db, item.ID)
	if got.EndedOn == nil {
		t.Fatal("ended")
	}
	var undo events.Event
	e.tx(func(tx *sql.Tx) {
		var err error
		if undo, err = e.svc.Undo(ctx, tx, events.ActorUser, end.ID); err != nil {
			t.Fatal(err)
		}
	})
	if got, _ = e.svc.Get(ctx, e.db, item.ID); got.EndedOn != nil {
		t.Fatal("undo clears the end")
	}
	e.tx(func(tx *sql.Tx) {
		if _, err := e.svc.Redo(ctx, tx, events.ActorUser, undo.ID); err != nil {
			t.Fatal(err)
		}
	})
	if got, _ = e.svc.Get(ctx, e.db, item.ID); got.EndedOn == nil {
		t.Fatal("redo ends it again")
	}
}
