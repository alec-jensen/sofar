// Package jobs is sofar's in-repo job queue on Postgres (ADR-0011): jobs are
// keyed so they are idempotent, enqueued in the caller's transaction, picked
// with FOR UPDATE SKIP LOCKED, and retried with backoff. A scheduler adds the
// periodic runs that are due, catching up after downtime. spec: A1, A7.
package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

// Job is one unit of work handed to a handler.
type Job struct {
	ID       int64
	Kind     string
	Key      string
	Attempts int
	Payload  json.RawMessage
}

// Handler runs a job. Returning an error schedules a retry with backoff.
// Handlers must be idempotent: a job can run more than once.
type Handler func(ctx context.Context, j Job) error

// Enqueue inserts a job inside the caller's transaction. A job with the same
// key is ignored, which makes enqueueing idempotent.
func Enqueue(ctx context.Context, tx *sql.Tx, kind, key string, runAt time.Time, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if payload == nil {
		raw = []byte("{}")
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO jobs (kind, key, run_at, payload) VALUES ($1,$2,$3,$4) ON CONFLICT (key) DO NOTHING`, kind, key, runAt, raw)
	return err
}

// Queue runs registered handlers against the jobs table.
type Queue struct {
	DB   *sql.DB
	Log  zerolog.Logger
	Now  func() time.Time
	Lock time.Duration // how long a picked job is held before another worker may retry it
	// OnResult, if set, is told how each attempt ended (for metrics).
	OnResult func(kind string, ok bool)

	mu       sync.Mutex
	handlers map[string]Handler
}

// NewQueue builds a Queue with production defaults.
func NewQueue(db *sql.DB, log zerolog.Logger) *Queue {
	return &Queue{DB: db, Log: log, Now: time.Now, Lock: 10 * time.Minute, handlers: map[string]Handler{}}
}

// Register sets the handler for a kind.
func (q *Queue) Register(kind string, h Handler) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.handlers[kind] = h
}

func (q *Queue) handler(kind string) Handler {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.handlers[kind]
}

// Backoff is the retry delay after the given number of failed attempts.
func Backoff(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	d := 30 * time.Second << min(attempts-1, 10)
	if d > 6*time.Hour {
		d = 6 * time.Hour
	}
	return d
}

// RunOne picks one due job, runs it and records the outcome. It reports
// whether a job was run.
func (q *Queue) RunOne(ctx context.Context) (bool, error) {
	now := q.Now()
	tx, err := q.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var j Job
	err = tx.QueryRowContext(ctx, `
		SELECT id, kind, key, attempts, payload FROM jobs
		WHERE done_at IS NULL AND run_at <= $1 AND (locked_until IS NULL OR locked_until < $1)
		ORDER BY run_at, id LIMIT 1 FOR UPDATE SKIP LOCKED`, now).Scan(&j.ID, &j.Kind, &j.Key, &j.Attempts, &j.Payload)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	// Hold the job past this transaction so a crashed worker's job is retried
	// only after the lock expires.
	if _, err = tx.ExecContext(ctx, `UPDATE jobs SET locked_until=$2, attempts=attempts+1 WHERE id=$1`, j.ID, now.Add(q.Lock)); err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	j.Attempts++

	h := q.handler(j.Kind)
	var runErr error
	if h == nil {
		runErr = fmt.Errorf("no handler for %q", j.Kind)
	} else {
		runErr = q.safely(ctx, h, j)
	}
	if q.OnResult != nil {
		q.OnResult(j.Kind, runErr == nil)
	}
	if runErr == nil {
		_, err = q.DB.ExecContext(ctx, `UPDATE jobs SET done_at=$2, locked_until=NULL, last_error=NULL WHERE id=$1`, j.ID, q.Now())
		return true, err
	}
	q.Log.Warn().Str("kind", j.Kind).Int("attempt", j.Attempts).Msg("job failed")
	_, err = q.DB.ExecContext(ctx, `UPDATE jobs SET locked_until=NULL, run_at=$2, last_error=$3 WHERE id=$1`, j.ID, q.Now().Add(Backoff(j.Attempts)), truncate(runErr.Error(), 500))
	return true, err
}

func (q *Queue) safely(ctx context.Context, h Handler, j Job) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return h(ctx, j)
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// Run processes jobs until ctx ends, polling when idle.
func (q *Queue) Run(ctx context.Context, poll time.Duration) {
	for ctx.Err() == nil {
		ran, err := q.RunOne(ctx)
		if err != nil && ctx.Err() == nil {
			q.Log.Error().Err(err).Msg("job queue")
		}
		if ran && err == nil {
			continue
		}
		select {
		case <-ctx.Done():
		case <-time.After(poll):
		}
	}
}
