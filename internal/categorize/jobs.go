package categorize

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/rs/zerolog"

	"github.com/alec-jensen/sofar/internal/civil"
	"github.com/alec-jensen/sofar/internal/jobs"
)

// NightlyKind is the job kind of the nightly categorization upkeep.
const NightlyKind = "categorize.nightly"

// NightlyKey is the idempotency key for one day's run.
func NightlyKey(d civil.Date) string { return "categorize.nightly:" + d.String() }

// NightlyPeriodic runs the upkeep at 03:30 home time. After downtime it lists
// only today's run: every step is derived from the data, so one run catches up.
func NightlyPeriodic() jobs.Periodic {
	return jobs.Periodic{
		Kind: NightlyKind,
		NextKeys: func(lastDone, now time.Time, home *time.Location) []jobs.Key {
			local := now.In(home)
			d := civil.DateOf(local)
			at := time.Date(d.Y, d.M, d.D, 3, 30, 0, 0, home)
			if local.Before(at) {
				d = d.AddDays(-1)
				at = time.Date(d.Y, d.M, d.D, 3, 30, 0, 0, home)
			}
			return []jobs.Key{{Key: NightlyKey(d), RunAt: at}}
		},
	}
}

// NightlyHandler archives stale rules, retrains the model, refreshes the live
// suggestions and records the first-pass accuracy so the trend can be read
// later. Each step is idempotent, so a repeated run changes nothing.
func NightlyHandler(s *Service, log zerolog.Logger) jobs.Handler {
	return func(ctx context.Context, j jobs.Job) error {
		tx, err := s.DB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if err = s.nightly(ctx, tx, log); err != nil {
			return err
		}
		return tx.Commit()
	}
}

func (s *Service) nightly(ctx context.Context, tx *sql.Tx, log zerolog.Logger) error {
	archived, demoted, err := s.ArchiveStale(ctx, tx)
	if err != nil {
		return fmt.Errorf("archive stale rules: %w", err)
	}
	if _, err = s.Retrain(ctx, tx); err != nil {
		return fmt.Errorf("retrain: %w", err)
	}
	changed, err := s.Refresh(ctx, tx)
	if err != nil {
		return fmt.Errorf("refresh suggestions: %w", err)
	}
	ev, err := s.Evaluate(ctx)
	if err != nil {
		return fmt.Errorf("evaluate: %w", err)
	}
	log.Info().Int("archived", len(archived)).Int("demoted", len(demoted)).Int("suggestions_changed", changed).
		Int("labeled", ev.Total).Float64("first_pass", ev.FirstPassAccuracy()).Msg("categorize nightly")
	return nil
}
