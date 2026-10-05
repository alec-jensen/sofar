package jobs

import (
	"context"
	"database/sql"
	"time"

	"github.com/rs/zerolog"
)

// schedulerLock is the Postgres advisory lock that makes one process the
// scheduler at a time.
const schedulerLock = 731891

// Periodic describes a recurring job kind. NextKeys returns every run that
// should exist by now but may not yet (catch-up, A7): given when the kind last
// completed, the current time and the home time zone, list the keyed runs.
// Keys must be stable so a re-run enqueues nothing new.
type Periodic struct {
	Kind     string
	NextKeys func(lastDone, now time.Time, home *time.Location) []Key
}

// Key is one scheduled run.
type Key struct {
	Key   string
	RunAt time.Time
}

// Scheduler enqueues the due runs of every registered periodic kind.
type Scheduler struct {
	DB       *sql.DB
	Log      zerolog.Logger
	Home     *time.Location
	Now      func() time.Time
	Periodic []Periodic
}

// NewScheduler builds a Scheduler.
func NewScheduler(db *sql.DB, log zerolog.Logger, home *time.Location) *Scheduler {
	return &Scheduler{DB: db, Log: log, Home: home, Now: time.Now}
}

// Add registers a periodic kind.
func (s *Scheduler) Add(p Periodic) { s.Periodic = append(s.Periodic, p) }

// Tick enqueues missing runs. It returns false without doing anything if
// another process holds the scheduler lock.
func (s *Scheduler) Tick(ctx context.Context) (bool, error) {
	conn, err := s.DB.Conn(ctx)
	if err != nil {
		return false, err
	}
	defer conn.Close()
	var got bool
	if err = conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, schedulerLock).Scan(&got); err != nil || !got {
		return false, err
	}
	defer conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, schedulerLock)
	return true, s.enqueueDue(ctx)
}

func (s *Scheduler) enqueueDue(ctx context.Context) error {
	now := s.Now()
	for _, p := range s.Periodic {
		var last sql.NullTime
		if err := s.DB.QueryRowContext(ctx, `SELECT max(done_at) FROM jobs WHERE kind=$1`, p.Kind).Scan(&last); err != nil {
			return err
		}
		tx, err := s.DB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		for _, k := range p.NextKeys(last.Time, now, s.Home) {
			if err = Enqueue(ctx, tx, p.Kind, k.Key, k.RunAt, nil); err != nil {
				tx.Rollback()
				return err
			}
		}
		if err = tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// Run ticks until ctx ends.
func (s *Scheduler) Run(ctx context.Context, every time.Duration) {
	for ctx.Err() == nil {
		if _, err := s.Tick(ctx); err != nil && ctx.Err() == nil {
			s.Log.Error().Err(err).Msg("scheduler")
		}
		select {
		case <-ctx.Done():
		case <-time.After(every):
		}
	}
}
