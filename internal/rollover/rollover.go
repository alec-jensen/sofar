// Package rollover runs the monthly rollover job (S15, S16, S17). Everything
// the rollover "does" (sweeps, carries, planned credits) is derived inside the
// engine, so the job only triggers the recompute and sends the digest; running
// it twice, or late, changes nothing. It never picks a cover source and never
// edits goals (S16).
package rollover

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/alec-jensen/sofar/internal/civil"
	"github.com/alec-jensen/sofar/internal/jobs"
	"github.com/alec-jensen/sofar/internal/money"
	"github.com/alec-jensen/sofar/internal/recompute"
)

// Kind is the job kind.
const Kind = "rollover"

// Key is the idempotency key for a month's rollover.
func Key(ym civil.YearMonth) string { return fmt.Sprintf("rollover:%s", ym) }

// Periodic schedules the rollover at 00:05 home time on the 1st. After
// downtime it enqueues only the current month's run: earlier months' effects
// are derived, so one recompute catches everything up.
func Periodic() jobs.Periodic {
	return jobs.Periodic{
		Kind: Kind,
		NextKeys: func(lastDone, now time.Time, home *time.Location) []jobs.Key {
			local := now.In(home)
			ym := civil.DateOf(local).Month()
			at := time.Date(ym.Y, ym.M, 1, 0, 5, 0, 0, home)
			if local.Before(at) {
				ym = ym.Prev()
				at = time.Date(ym.Y, ym.M, 1, 0, 5, 0, 0, home)
			}
			return []jobs.Key{{Key: Key(ym), RunAt: at}}
		},
	}
}

// Notifier receives the digest. A nil Notifier means nobody is subscribed.
type Notifier func(ctx context.Context, title, body string) error

// Handler recomputes everything and sends the digest for the month the job's
// key names. The job's own month comes from its key, so a late run still
// describes the right month.
func Handler(rec *recompute.Service, notify Notifier) jobs.Handler {
	return func(ctx context.Context, j jobs.Job) error {
		ym, err := civil.ParseYearMonth(strings.TrimPrefix(j.Key, "rollover:"))
		if err != nil {
			return fmt.Errorf("rollover: bad key %q", j.Key)
		}
		if _, err = rec.Recompute(ctx); err != nil {
			return err
		}
		if notify == nil {
			return nil
		}
		title, body, ok, err := Digest(ctx, rec, ym)
		if err != nil || !ok {
			return err
		}
		return notify(ctx, title, body)
	}
}

var monthNames = []string{"", "january", "february", "march", "april", "may", "june", "july", "august", "september", "october", "november", "december"}

func dollars(c money.Cents) string {
	sign := ""
	if c < 0 {
		sign, c = "-", -c
	}
	return fmt.Sprintf("%s$%d", sign, c/100)
}

// Digest is the one notification: "october plan: $X (+/- $Y vs last month),
// uncategorized: $Z." ok is false when there is no snapshot to describe.
func Digest(ctx context.Context, rec *recompute.Service, ym civil.YearMonth) (title, body string, ok bool, err error) {
	cur, err := rec.GetSnapshot(ctx, rec.DB, ym)
	if err != nil {
		return "", "", false, nil
	}
	body = fmt.Sprintf("%s plan: %s", monthNames[ym.M], dollars(cur.State.Plan))
	if prev, perr := rec.GetSnapshot(ctx, rec.DB, ym.Prev()); perr == nil {
		diff := cur.State.Plan - prev.State.Plan
		sign := "+"
		if diff < 0 {
			sign, diff = "-", -diff
		}
		body += fmt.Sprintf(" (%s%s vs last month)", sign, dollars(diff))
	}
	body += fmt.Sprintf(", uncategorized: %s", dollars(cur.State.Pot))
	return "a new month", body, true, nil
}
