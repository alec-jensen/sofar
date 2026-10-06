// Package matching runs everything that relates transactions to each other
// after new data arrives: pair the two sides of transfers, match incoming
// payments to expenses and receivables, advance held payments, and let stale
// reimbursement habits decay. It is one idempotent keyed job, enqueued in the
// same transaction as the change that caused it, and run again nightly so
// catch-up after downtime is safe.
package matching

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/rs/zerolog"

	"github.com/alec-jensen/sofar/internal/civil"
	"github.com/alec-jensen/sofar/internal/jobs"
	"github.com/alec-jensen/sofar/internal/reimburse"
	"github.com/alec-jensen/sofar/internal/transfers"
)

// Kinds of job.
const (
	RunKind     = "matching.run"
	NightlyKind = "matching.nightly"
)

// DecayAfterDays is how long a reimbursement habit may go unconfirmed before it drops to suggest-only.
const DecayAfterDays = 120

// Service orchestrates the matchers.
type Service struct {
	DB        *sql.DB
	Transfers *transfers.Service
	Reimburse *reimburse.Service
	Now       func() time.Time
}

// New builds a Service.
func New(db *sql.DB, t *transfers.Service, r *reimburse.Service) *Service {
	return &Service{DB: db, Transfers: t, Reimburse: r, Now: time.Now}
}

// Enqueue schedules a run in the caller's transaction. Runs requested within the
// same minute share a key, so a burst of changes causes one run.
func (s *Service) Enqueue(ctx context.Context, tx *sql.Tx) error {
	now := s.Now().UTC()
	return jobs.Enqueue(ctx, tx, RunKind, fmt.Sprintf("%s:%s", RunKind, now.Format("200601021504")), now, nil)
}

// Run does one pass: pair transfers, process payments, sweep held ones. Every
// step is idempotent, so running twice changes nothing.
func (s *Service) Run(ctx context.Context, tx *sql.Tx) error {
	if _, err := s.Transfers.Pair(ctx, tx); err != nil {
		return fmt.Errorf("pair transfers: %w", err)
	}
	if _, err := s.Reimburse.Process(ctx, tx); err != nil {
		return fmt.Errorf("match payments: %w", err)
	}
	if err := s.Reimburse.Sweep(ctx, tx); err != nil {
		return fmt.Errorf("sweep held payments: %w", err)
	}
	return nil
}

// Nightly is Run plus the slow housekeeping: habits that were not reconfirmed decay.
func (s *Service) Nightly(ctx context.Context, tx *sql.Tx) error {
	if err := s.Run(ctx, tx); err != nil {
		return err
	}
	if _, err := s.Reimburse.DecayPatterns(ctx, tx, DecayAfterDays); err != nil {
		return fmt.Errorf("decay patterns: %w", err)
	}
	return nil
}

// Handler runs a job of either kind.
func Handler(s *Service, log zerolog.Logger) jobs.Handler {
	return func(ctx context.Context, j jobs.Job) error {
		tx, err := s.DB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if j.Kind == NightlyKind {
			err = s.Nightly(ctx, tx)
		} else {
			err = s.Run(ctx, tx)
		}
		if err != nil {
			return err
		}
		return tx.Commit()
	}
}

// NightlyPeriodic runs at 03:45 home time. After downtime only today's run is
// listed: every step is derived from the data, so one run catches up.
func NightlyPeriodic() jobs.Periodic {
	return jobs.Periodic{
		Kind: NightlyKind,
		NextKeys: func(lastDone, now time.Time, home *time.Location) []jobs.Key {
			local := now.In(home)
			d := civil.DateOf(local)
			at := time.Date(d.Y, d.M, d.D, 3, 45, 0, 0, home)
			if local.Before(at) {
				d = d.AddDays(-1)
				at = time.Date(d.Y, d.M, d.D, 3, 45, 0, 0, home)
			}
			return []jobs.Key{{Key: NightlyKind + ":" + d.String(), RunAt: at}}
		},
	}
}
