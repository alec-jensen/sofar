package reimburse

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/alec-jensen/sofar/internal/civil"
	"github.com/alec-jensen/sofar/internal/counterparties"
	"github.com/alec-jensen/sofar/internal/events"
)

// KindPatternConflict is emitted when a learned habit disagrees with one the
// user wrote: the user's wins, and the system asks what to do (S12b edge 13).
const KindPatternConflict = "reimburse.pattern_conflict"

// AuthorPattern lets the user write a habit by hand ("dad pays half of dining,
// within 3 days"). It is active at once and beats any learned pattern.
func (s *Service) AuthorPattern(ctx context.Context, tx *sql.Tx, cp, category string, ratio float64, lagMax int) (Pattern, error) {
	if ratio <= 0 || ratio > 1 || lagMax < 0 {
		return Pattern{}, fmt.Errorf("%w: a share between 0 and 100 percent", ErrInvalid)
	}
	if _, err := s.Cps.Get(ctx, tx, cp); err != nil {
		return Pattern{}, ErrNotFound
	}
	var p Pattern
	err := tx.QueryRowContext(ctx, `INSERT INTO reimbursement_patterns (counterparty_id, category_id, share_ratio, lag_max, support, streak, state, user_authored)
		VALUES ($1,$2,$3,$4,0,0,'active',true) RETURNING `+patternCols, cp, category, ratio, max(1, lagMax)).
		Scan(&p.ID, &p.CounterpartyID, &p.CategoryID, &p.MerchantKey, &p.ShareRatio, &p.LagMin, &p.LagMax, &p.Support, &p.Streak, &p.Strikes, &p.State, &p.UserAuthored)
	return p, err
}

// userPatternFor reports whether the user authored a pattern for this payer and category.
func (s *Service) userPatternFor(ctx context.Context, tx *sql.Tx, cp, category string) (Pattern, bool) {
	var p Pattern
	err := tx.QueryRowContext(ctx, `SELECT `+patternCols+` FROM reimbursement_patterns WHERE counterparty_id::text=$1 AND category_id=$2 AND user_authored AND state <> 'disabled' LIMIT 1`, cp, category).
		Scan(&p.ID, &p.CounterpartyID, &p.CategoryID, &p.MerchantKey, &p.ShareRatio, &p.LagMin, &p.LagMax, &p.Support, &p.Streak, &p.Strikes, &p.State, &p.UserAuthored)
	return p, err == nil
}

// DecayPatterns demotes active patterns that have not been reconfirmed for a
// long time: a payer who stopped reimbursing a category goes back to
// suggest-only and the owner is asked "update?" (S12b edge 1).
func (s *Service) DecayPatterns(ctx context.Context, tx *sql.Tx, afterDays int) ([]string, error) {
	cut := s.today().AddDays(-afterDays).String()
	rs, err := tx.QueryContext(ctx, `UPDATE reimbursement_patterns SET state='demoted', streak=0, updated_at=now()
		WHERE state='active' AND NOT user_authored AND last_confirmed_on IS NOT NULL AND last_confirmed_on < $1 RETURNING id::text`, cut)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rs.Next() {
		var id string
		rs.Scan(&id)
		ids = append(ids, id)
	}
	rs.Close()
	for _, id := range ids {
		if err = s.emitDemoted(ctx, tx, events.ActorSystem, id, "it has not been reconfirmed lately: has this changed?"); err != nil {
			return ids, err
		}
	}
	return ids, nil
}

// Retrain rebuilds every learned pattern from the user's current labels, after
// they have unlinked or rewritten links in bulk (S12b edge 12). A link that was
// later unlinked or rejected no longer counts as support.
func (s *Service) Retrain(ctx context.Context, tx *sql.Tx) error {
	ps, err := s.Patterns(ctx, tx)
	if err != nil {
		return err
	}
	for _, p := range ps {
		if p.UserAuthored {
			continue
		}
		rs, err := tx.QueryContext(ctx, `SELECT amount, expense_total FROM link_events e
			WHERE counterparty_id::text=$1 AND category_id=$2 AND outcome IN ('confirmed','manual') AND expense_total > 0
			  AND NOT EXISTS (SELECT 1 FROM link_events u WHERE u.link_id = e.link_id AND u.outcome IN ('unlinked','rejected'))`, p.CounterpartyID, p.CategoryID)
		if err != nil {
			return err
		}
		var n int
		var sum float64
		for rs.Next() {
			var a, t int64
			rs.Scan(&a, &t)
			if math.Abs(float64(a)/float64(t)-p.ShareRatio) <= s.Cfg.PatternRatioTolerance {
				n++
				sum += float64(a) / float64(t)
			}
		}
		rs.Close()
		state, streak, ratio := p.State, p.Streak, p.ShareRatio
		if n > 0 {
			ratio = sum / float64(n)
		}
		if n < s.Cfg.StreakToGraduate {
			if state == Active {
				state = Probation
			}
			streak = n
		} else {
			streak = min(streak, n)
		}
		if _, err = tx.ExecContext(ctx, `UPDATE reimbursement_patterns SET support=$2, streak=$3, state=$4, share_ratio=$5, updated_at=now() WHERE id=$1`, p.ID, n, streak, state, ratio); err != nil {
			return err
		}
	}
	return nil
}

// AgingThresholds personalizes "overdue" to each payer's own lag (S12b item 6):
// a payer who always pays in 2 days is nudged on day 5, one who takes 3 weeks
// is not. Payers without enough history use the defaults.
func (s *Service) AgingThresholds(ctx context.Context, q events.DBTX) func(cp *string) []int {
	cache := map[string][]int{}
	return func(cp *string) []int {
		if cp == nil {
			return nil
		}
		if t, ok := cache[*cp]; ok {
			return t
		}
		var t []int
		if pr, err := s.Profile(ctx, q, *cp); err == nil && pr.Paybacks >= 3 {
			t = []int{max(3, 2*pr.MedianLag+1)}
		}
		cache[*cp] = t
		return t
	}
}

// Prediction is the expense-side guess: this expense is likely to be paid back.
type Prediction struct {
	CounterpartyID string
	Name           string
	Amount         int64
	ExpectedBy     civil.Date
	PatternID      string
	Proven         bool // an active pattern; otherwise a probation one
}

// PredictExpense says whether a new expense is likely to be reimbursed, by
// whom, for about how much and by when ("likely reimbursed by dad, about $11
// within 3 days"), as a one-tap receivable suggestion.
func (s *Service) PredictExpense(ctx context.Context, q events.DBTX, expenseID string) ([]Prediction, error) {
	rows, err := queryRows(ctx, q, ` AND r.id = $1`, expenseID)
	if err != nil || len(rows) == 0 || !eligibleExpense(rows[0]) {
		return nil, err
	}
	e := rows[0]
	ps, err := s.Patterns(ctx, q)
	if err != nil {
		return nil, err
	}
	var out []Prediction
	for _, p := range ps {
		if p.State == Disabled || p.State == Demoted || p.CategoryID != e.Category || p.CategoryID == "" || p.Support < 2 && !p.UserAuthored {
			continue
		}
		c, err := s.Cps.Get(ctx, q, p.CounterpartyID)
		if err != nil {
			continue
		}
		out = append(out, Prediction{CounterpartyID: c.ID, Name: c.Name, Amount: int64(math.Round(p.ShareRatio * float64(-e.Amount))), ExpectedBy: e.Date.AddDays(p.LagMax), PatternID: p.ID, Proven: p.State == Active})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Proven && !out[j].Proven })
	return out, nil
}

// ReceiveLearn marks a receivable received by a real payment and teaches the
// system from it, like any other user-made link.
func (s *Service) ReceiveLearn(ctx context.Context, tx *sql.Tx, actor events.Actor, receivableID, paymentID string, amount int64) error {
	l, err := s.Recv.Receive(ctx, tx, actor, receivableID, paymentID, amount)
	if err != nil {
		return err
	}
	return s.learnFromLink(ctx, tx, l.ID, "manual")
}

// ReceiveCashLearn does the same for cash: no bank signal, but the label still
// trains the expense-side predictor (S12b edge 14).
func (s *Service) ReceiveCashLearn(ctx context.Context, tx *sql.Tx, actor events.Actor, receivableID, accountID string, on civil.Date, amount int64) error {
	l, _, err := s.Recv.MarkReceivedCash(ctx, tx, actor, receivableID, accountID, on, amount)
	if err != nil {
		return err
	}
	return s.learnFromLink(ctx, tx, l.ID, "manual")
}

func (s *Service) learnFromLink(ctx context.Context, tx *sql.Tx, linkID int64, source string) error {
	l, err := s.Links.Get(ctx, tx, linkID)
	if err != nil {
		return err
	}
	ex, cp, err := s.linkExample(ctx, tx, l)
	if err != nil {
		return err
	}
	if ex.Counterparty == "" && l.ReceivableID != nil {
		// a cash payback has no payer text: the receivable knows who owes it
		var c sql.NullString
		tx.QueryRowContext(ctx, `SELECT counterparty_id::text FROM receivables WHERE id::text=$1`, *l.ReceivableID).Scan(&c)
		ex.Counterparty, cp = c.String, c.String
	}
	pat, err := s.learn(ctx, tx, ex)
	if err != nil {
		return err
	}
	pid := ""
	if pat != nil {
		pid = pat.ID
	}
	return s.log(ctx, tx, logEntry{Payment: l.PaymentID, Expense: l.ExpenseID, Pattern: pid, Counterparty: cp, LinkID: l.ID, Source: source, Outcome: "manual", Amount: l.Amount, ExpenseTotal: ex.Total, Lag: ex.Lag, Category: ex.Category})
}

// Allowance is a payer whose deposits look like a schedule rather than paybacks.
type Allowance struct {
	Key      string
	Amount   int64
	Count    int
	Interval int // days between deposits
}

// AllowanceQuestions finds payers who send the same amount on a regular
// schedule with nothing linked ("allowance or reimbursement?", S12a edge 14).
// The question is only asked; it links nothing.
func (s *Service) AllowanceQuestions(ctx context.Context, q events.DBTX) ([]Allowance, error) {
	rows, err := queryRows(ctx, q, ` AND r.amount > 0 AND r.pending = false`)
	if err != nil {
		return nil, err
	}
	type group struct {
		amount int64
		dates  []civil.Date
	}
	by := map[string]*group{}
	for _, r := range rows {
		if r.Linked > 0 || !isP2P(r, false) {
			continue
		}
		var label string
		q.QueryRowContext(ctx, `SELECT label FROM payer_labels WHERE txn_id=$1`, r.ID).Scan(&label)
		if label != "" {
			continue
		}
		k := fmt.Sprintf("%s|%d", counterparties.ParseName(r.payerText()).Key(), r.Amount)
		if by[k] == nil {
			by[k] = &group{amount: r.Amount}
		}
		by[k].dates = append(by[k].dates, r.Date)
	}
	var out []Allowance
	for k, g := range by {
		if len(g.dates) < 3 {
			continue
		}
		sort.Slice(g.dates, func(i, j int) bool { return g.dates[i].Before(g.dates[j]) })
		gaps := make([]int, 0, len(g.dates)-1)
		for i := 1; i < len(g.dates); i++ {
			gaps = append(gaps, g.dates[i].DaysSince(g.dates[i-1]))
		}
		sort.Ints(gaps)
		med := gaps[len(gaps)/2]
		regular := med >= 7 && med <= 40
		for _, gp := range gaps {
			if math.Abs(float64(gp-med)) > float64(med)*0.25+2 {
				regular = false
			}
		}
		if regular {
			out = append(out, Allowance{Key: strings.SplitN(k, "|", 2)[0], Amount: g.amount, Count: len(g.dates), Interval: med})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}
