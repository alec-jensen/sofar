//go:build integration

package matching_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/alec-jensen/sofar/internal/counterparties"
	"github.com/alec-jensen/sofar/internal/events"
	"github.com/alec-jensen/sofar/internal/jobs"
	"github.com/alec-jensen/sofar/internal/ledger"
	"github.com/alec-jensen/sofar/internal/links"
	"github.com/alec-jensen/sofar/internal/manual"
	"github.com/alec-jensen/sofar/internal/matching"
	"github.com/alec-jensen/sofar/internal/receivables"
	"github.com/alec-jensen/sofar/internal/reimburse"
	"github.com/alec-jensen/sofar/internal/testkit"
	"github.com/alec-jensen/sofar/internal/transfers"
)

func TestA7_MatchingJobIsKeyedAndIdempotent(t *testing.T) {
	db := testkit.DB(t)
	reg := events.NewRegistry()
	led := ledger.New(db, reg)
	lnk := links.New(db, reg)
	cps := counterparties.New(db, reg)
	rcv := receivables.New(db, reg, lnk, cps, manual.New(db, reg, led))
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	rcv.Now = func() time.Time { return now }
	re := reimburse.New(db, reg, lnk, cps, rcv)
	re.Now = func() time.Time { return now }
	tr := transfers.New(db, led)
	tr.Now = func() time.Time { return now }
	svc := matching.New(db, tr, re)
	svc.Now = func() time.Time { return now }

	chk := testkit.Account(t, db, "checking")
	sav := testkit.Account(t, db, "savings")
	a := testkit.RawTxn(t, db, chk, testkit.Raw{PostedOn: "2026-10-10", TransactedOn: "2026-10-10", Amount: -5000, Description: "MOVE"})
	b := testkit.RawTxn(t, db, sav, testkit.Raw{PostedOn: "2026-10-10", TransactedOn: "2026-10-10", Amount: 5000, Description: "MOVE"})
	pay := testkit.RawTxn(t, db, chk, testkit.Raw{PostedOn: "2026-10-15", TransactedOn: "2026-10-15", Amount: 1100, Description: "VENMO PAYMENT FROM DAD"})

	// two requests in the same minute are one job
	for i := 0; i < 2; i++ {
		tx, _ := db.Begin()
		if err := svc.Enqueue(context.Background(), tx); err != nil {
			t.Fatal(err)
		}
		tx.Commit()
	}
	var n int
	db.QueryRow(`SELECT count(*) FROM jobs WHERE kind=$1`, matching.RunKind).Scan(&n)
	if n != 1 {
		t.Fatalf("one job: %d", n)
	}

	h := matching.Handler(svc, zerolog.Nop())
	for i := 0; i < 2; i++ {
		if err := h(context.Background(), jobs.Job{Kind: matching.RunKind, Key: "k"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{a, b} {
		if g, _ := led.Get(context.Background(), id); g.CategoryID != "transfer" {
			t.Fatalf("paired: %q", g.CategoryID)
		}
	}
	var held int
	db.QueryRow(`SELECT count(*) FROM held_payments WHERE txn_id=$1 AND state='awaiting_expense'`, pay).Scan(&held)
	if held != 1 {
		t.Fatal("the lone payment is held")
	}
	var events1 int
	db.QueryRow(`SELECT count(*) FROM events`).Scan(&events1)
	if err := h(context.Background(), jobs.Job{Kind: matching.NightlyKind, Key: "n"}); err != nil {
		t.Fatal(err)
	}
	var events2 int
	db.QueryRow(`SELECT count(*) FROM events`).Scan(&events2)
	if events1 != events2 {
		t.Fatalf("a repeated pass writes nothing new: %d vs %d", events1, events2)
	}
	_ = sql.ErrNoRows
}

func TestA7_NightlyKeysCatchUpToToday(t *testing.T) {
	p := matching.NightlyPeriodic()
	home := time.UTC
	got := p.NextKeys(time.Time{}, time.Date(2026, 10, 20, 9, 0, 0, 0, home), home)
	if len(got) != 1 || got[0].Key != "matching.nightly:2026-10-20" {
		t.Fatalf("%+v", got)
	}
	early := p.NextKeys(time.Time{}, time.Date(2026, 10, 20, 2, 0, 0, 0, home), home)
	if early[0].Key != "matching.nightly:2026-10-19" {
		t.Fatalf("before 03:45 the last run is yesterday's: %+v", early)
	}
}
