//go:build integration

package rollover_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/alec-jensen/sofar/internal/events"
	"github.com/alec-jensen/sofar/internal/jobs"
	"github.com/alec-jensen/sofar/internal/ledger"
	"github.com/alec-jensen/sofar/internal/recompute"
	"github.com/alec-jensen/sofar/internal/rollover"
	"github.com/alec-jensen/sofar/internal/testkit"
)

func setup(t *testing.T, now time.Time) (*sql.DB, *recompute.Service, *jobs.Scheduler, *jobs.Queue, string) {
	t.Helper()
	db := testkit.DB(t)
	reg := events.NewRegistry()
	led := ledger.New(db, reg)
	rec := recompute.New(db, reg, led, time.UTC)
	rec.Now = func() time.Time { return now }
	acct := testkit.Account(t, db, "checking")
	sched := jobs.NewScheduler(db, zerolog.Nop(), time.UTC)
	sched.Now = rec.Now
	sched.Add(rollover.Periodic())
	q := jobs.NewQueue(db, zerolog.Nop())
	q.Now = rec.Now
	return db, rec, sched, q, acct
}

func count(t *testing.T, db *sql.DB, where string) int {
	var n int
	db.QueryRow(`SELECT count(*) FROM jobs WHERE ` + where).Scan(&n)
	return n
}

func TestS15_RolloverCatchUpAfterDowntime(t *testing.T) {
	now := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)
	db, _, sched, _, _ := setup(t, now)
	ctx := context.Background()
	// the server was down across the 1st: one tick enqueues November's run, once
	sched.Tick(ctx)
	sched.Tick(ctx)
	if n := count(t, db, "key='rollover:2026-11'"); n != 1 {
		t.Fatalf("one rollover for the current month: %d", n)
	}
	var runAt time.Time
	db.QueryRow(`SELECT run_at FROM jobs WHERE key='rollover:2026-11'`).Scan(&runAt)
	if !runAt.Equal(time.Date(2026, 11, 1, 0, 5, 0, 0, time.UTC)) {
		t.Fatalf("scheduled for 00:05 on the 1st: %v", runAt)
	}
	// before 00:05 on the 1st, the previous month's key is still the latest
	early := time.Date(2026, 12, 1, 0, 2, 0, 0, time.UTC)
	sched.Now = func() time.Time { return early }
	sched.Tick(ctx)
	if n := count(t, db, "key='rollover:2026-12'"); n != 0 {
		t.Fatal("not due yet")
	}
}

func TestS15_RolloverIdempotentByKeyAndDigest(t *testing.T) {
	now := time.Date(2026, 5, 2, 9, 0, 0, 0, time.UTC)
	db, rec, sched, q, acct := setup(t, now)
	ctx := context.Background()
	for _, m := range []string{"2026-03", "2026-04"} {
		id := testkit.RawTxn(t, db, acct, testkit.Raw{PostedOn: m + "-01", Amount: 400000, Description: "ACME PAYROLL"})
		tx, _ := db.Begin()
		rec.Ledger.Apply(ctx, tx, events.ActorUser, id, ledger.KindSetCategory, ledger.SetCategory{TxnID: id, CategoryID: strp("salary")}, "")
		rec.Ledger.Apply(ctx, tx, events.ActorUser, id, ledger.KindSetReviewed, ledger.SetReviewed{TxnID: id, Reviewed: true}, "")
		tx.Commit()
	}
	var sent []string
	q.Register(rollover.Kind, rollover.Handler(rec, func(ctx context.Context, title, body string) error {
		sent = append(sent, body)
		return nil
	}))
	sched.Tick(ctx)
	for i := 0; i < 3; i++ {
		q.RunOne(ctx)
	}
	sched.Tick(ctx) // the same key again: nothing new
	q.RunOne(ctx)
	if len(sent) != 1 || !strings.HasPrefix(sent[0], "may plan: $4000") || !strings.Contains(sent[0], "uncategorized:") {
		t.Fatalf("one digest: %v", sent)
	}
	var done int
	db.QueryRow(`SELECT count(*) FROM jobs WHERE done_at IS NOT NULL`).Scan(&done)
	if done != 1 {
		t.Fatalf("done jobs: %d", done)
	}
}

func TestS16_ServerNeverChoosesCoverSource(t *testing.T) {
	now := time.Date(2026, 5, 2, 9, 0, 0, 0, time.UTC)
	db, rec, sched, q, acct := setup(t, now)
	ctx := context.Background()
	testkit.RawTxn(t, db, acct, testkit.Raw{PostedOn: "2026-04-10", Amount: -900000, Description: "HUGE PURCHASE"})
	q.Register(rollover.Kind, rollover.Handler(rec, nil))
	sched.Tick(ctx)
	q.RunOne(ctx)
	var covers, budgetEvents int
	db.QueryRow(`SELECT count(*) FROM events WHERE kind='budget.event'`).Scan(&budgetEvents)
	db.QueryRow(`SELECT count(*) FROM events WHERE payload->>'kind'='cover'`).Scan(&covers)
	if covers != 0 || budgetEvents != 0 {
		t.Fatalf("the rollover writes no decisions: %d covers, %d budget events", covers, budgetEvents)
	}
}

func strp(s string) *string { return &s }
