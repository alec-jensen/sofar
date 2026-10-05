//go:build integration

package jobs_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/alec-jensen/sofar/internal/jobs"
	"github.com/alec-jensen/sofar/internal/testkit"
)

var t0 = time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)

func enqueue(t *testing.T, db *sql.DB, kind, key string, at time.Time) {
	t.Helper()
	tx, _ := db.Begin()
	if err := jobs.Enqueue(context.Background(), tx, kind, key, at, map[string]int{"n": 1}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func count(t *testing.T, db *sql.DB, where string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM jobs WHERE `+where, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestJobs_EnqueueIsIdempotentByKey(t *testing.T) {
	db := testkit.DB(t)
	enqueue(t, db, "x", "k1", t0)
	enqueue(t, db, "x", "k1", t0)
	enqueue(t, db, "x", "k2", t0)
	if n := count(t, db, "true"); n != 2 {
		t.Fatalf("want 2 jobs, got %d", n)
	}
}

func TestJobs_RollbackDropsEnqueuedJob(t *testing.T) {
	db := testkit.DB(t)
	tx, _ := db.Begin()
	if err := jobs.Enqueue(context.Background(), tx, "x", "k", t0, nil); err != nil {
		t.Fatal(err)
	}
	tx.Rollback()
	if n := count(t, db, "true"); n != 0 {
		t.Fatalf("a rolled-back transaction must not leave a job, got %d", n)
	}
}

func TestJobs_RunsDueJobsOnceAndSkipsFuture(t *testing.T) {
	db := testkit.DB(t)
	q := jobs.NewQueue(db, zerolog.Nop())
	q.Now = func() time.Time { return t0 }
	ran := 0
	q.Register("x", func(ctx context.Context, j jobs.Job) error { ran++; return nil })
	enqueue(t, db, "x", "due", t0.Add(-time.Minute))
	enqueue(t, db, "x", "later", t0.Add(time.Hour))
	for i := 0; i < 3; i++ {
		q.RunOne(context.Background())
	}
	if ran != 1 {
		t.Fatalf("ran %d times, want 1", ran)
	}
	if n := count(t, db, "done_at IS NOT NULL"); n != 1 {
		t.Fatalf("done = %d", n)
	}
}

func TestJobs_FailureRetriesWithBackoff(t *testing.T) {
	db := testkit.DB(t)
	q := jobs.NewQueue(db, zerolog.Nop())
	now := t0
	q.Now = func() time.Time { return now }
	fail := true
	q.Register("x", func(ctx context.Context, j jobs.Job) error {
		if fail {
			return errors.New("boom")
		}
		return nil
	})
	enqueue(t, db, "x", "k", t0)
	q.RunOne(context.Background())
	var attempts int
	var lastErr sql.NullString
	var runAt time.Time
	db.QueryRow(`SELECT attempts, last_error, run_at FROM jobs`).Scan(&attempts, &lastErr, &runAt)
	if attempts != 1 || lastErr.String != "boom" || !runAt.Equal(t0.Add(jobs.Backoff(1))) {
		t.Fatalf("after failure: attempts=%d err=%q run_at=%v", attempts, lastErr.String, runAt)
	}
	if ran, _ := q.RunOne(context.Background()); ran {
		t.Fatal("must not retry before the backoff elapses")
	}
	now = now.Add(time.Hour)
	fail = false
	if ran, _ := q.RunOne(context.Background()); !ran {
		t.Fatal("should retry after the backoff")
	}
	if n := count(t, db, "done_at IS NOT NULL"); n != 1 {
		t.Fatal("retry should complete the job")
	}
}

func TestJobs_PanicIsAFailureNotACrash(t *testing.T) {
	db := testkit.DB(t)
	q := jobs.NewQueue(db, zerolog.Nop())
	q.Now = func() time.Time { return t0 }
	q.Register("x", func(ctx context.Context, j jobs.Job) error { panic("oops") })
	enqueue(t, db, "x", "k", t0)
	q.RunOne(context.Background())
	var msg string
	db.QueryRow(`SELECT last_error FROM jobs`).Scan(&msg)
	if msg == "" {
		t.Fatal("panic should be recorded")
	}
}

func TestJobs_BackoffGrowsAndCaps(t *testing.T) {
	if jobs.Backoff(1) >= jobs.Backoff(2) || jobs.Backoff(50) != 6*time.Hour {
		t.Fatalf("backoff: %v %v %v", jobs.Backoff(1), jobs.Backoff(2), jobs.Backoff(50))
	}
}

// daily06 is a test periodic kind: one run per local day at 06:00.
func daily06(lastDone, now time.Time, home *time.Location) []jobs.Key {
	var keys []jobs.Key
	local := now.In(home)
	day := time.Date(local.Year(), local.Month(), local.Day(), 6, 0, 0, 0, home)
	if day.After(now) {
		day = day.AddDate(0, 0, -1)
	}
	// catch up only the most recent missed slot, never a burst
	keys = append(keys, jobs.Key{Key: fmt.Sprintf("daily:%s", day.Format("2006-01-02")), RunAt: day})
	return keys
}

func TestJobs_CatchUpEnqueuesMissedKeys(t *testing.T) {
	db := testkit.DB(t)
	home, _ := time.LoadLocation("America/Chicago")
	s := jobs.NewScheduler(db, zerolog.Nop(), home)
	s.Add(jobs.Periodic{Kind: "daily", NextKeys: daily06})
	s.Now = func() time.Time { return t0 } // 07:00 Chicago on Oct 5
	if got, err := s.Tick(context.Background()); err != nil || !got {
		t.Fatalf("tick: %v %v", got, err)
	}
	if n := count(t, db, "key='daily:2026-10-05'"); n != 1 {
		t.Fatal("today's run should be enqueued")
	}
	// Server was down for three days: the next tick adds only the latest slot.
	s.Now = func() time.Time { return t0.AddDate(0, 0, 3) }
	s.Tick(context.Background())
	s.Tick(context.Background())
	if n := count(t, db, "kind='daily'"); n != 2 {
		t.Fatalf("want exactly 2 jobs (no burst, no duplicates), got %d", n)
	}
}

func TestJobs_OnlyOneSchedulerHoldsLock(t *testing.T) {
	db := testkit.DB(t)
	s := jobs.NewScheduler(db, zerolog.Nop(), time.UTC)
	started := make(chan struct{})
	release := make(chan struct{})
	s.Add(jobs.Periodic{Kind: "slow", NextKeys: func(time.Time, time.Time, *time.Location) []jobs.Key {
		close(started)
		<-release
		return nil
	}})
	done := make(chan bool)
	go func() { got, _ := s.Tick(context.Background()); done <- got }()
	<-started
	other := jobs.NewScheduler(db, zerolog.Nop(), time.UTC)
	if got, err := other.Tick(context.Background()); err != nil || got {
		t.Fatalf("a second scheduler must not run while the lock is held: %v %v", got, err)
	}
	close(release)
	if !<-done {
		t.Fatal("the first scheduler should have run")
	}
	if got, _ := other.Tick(context.Background()); !got {
		t.Fatal("the lock should be free afterwards")
	}
}
