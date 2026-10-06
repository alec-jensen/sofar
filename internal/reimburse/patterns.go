package reimburse

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"

	"github.com/alec-jensen/sofar/internal/civil"
	"github.com/alec-jensen/sofar/internal/events"
	"github.com/alec-jensen/sofar/internal/links"
)

// Pattern states.
const (
	Probation = "probation"
	Active    = "active"
	Demoted   = "demoted"
	Disabled  = "disabled"
)

// KindPatternDemoted is the event emitted when a pattern loses its auto-tentative right.
const KindPatternDemoted = "reimburse.pattern_demoted"

// PatternDemoted records why a pattern stopped linking on its own (the owner is notified).
type PatternDemoted struct {
	PatternID string `json:"pattern_id"`
	Reason    string `json:"reason"`
}

// Pattern is a learned or authored reimbursement habit: "Dad + dining: about half, within 3 days".
type Pattern struct {
	ID             string
	CounterpartyID string
	CategoryID     string
	MerchantKey    string
	ShareRatio     float64
	LagMin, LagMax int
	Support        int
	Streak         int
	Strikes        int
	State          string
	UserAuthored   bool
}

// Describe is the human-readable form shown with its provenance.
func (p Pattern) Describe(payer string) string {
	return fmt.Sprintf("%s + %s: about %d%%, within %d days (%d confirmed, %d strikes)", payer, orAny(p.CategoryID, p.MerchantKey), int(math.Round(p.ShareRatio*100)), p.LagMax, p.Support, p.Strikes)
}

func orAny(a, b string) string {
	if a != "" {
		return a
	}
	if b != "" {
		return b
	}
	return "anything"
}

const patternCols = `id, counterparty_id, category_id, merchant_key, share_ratio, lag_min, lag_max, support, streak, strikes, state, user_authored`

func scanPattern(r interface{ Scan(...any) error }) (Pattern, error) {
	var p Pattern
	err := r.Scan(&p.ID, &p.CounterpartyID, &p.CategoryID, &p.MerchantKey, &p.ShareRatio, &p.LagMin, &p.LagMax, &p.Support, &p.Streak, &p.Strikes, &p.State, &p.UserAuthored)
	return p, err
}

// Patterns lists every pattern, strongest first.
func (s *Service) Patterns(ctx context.Context, q events.DBTX) ([]Pattern, error) {
	rs, err := q.QueryContext(ctx, `SELECT `+patternCols+` FROM reimbursement_patterns ORDER BY (state='active') DESC, support DESC, created_at`)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var out []Pattern
	for rs.Next() {
		p, err := scanPattern(rs)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rs.Err()
}

// Example is one confirmed pair, the unit patterns are induced from.
type example struct {
	Counterparty string
	Category     string
	Merchant     string
	Amount       int64
	Total        int64
	Lag          int
	On           civil.Date
}

func (e example) ratio() float64 {
	if e.Total == 0 {
		return 0
	}
	return float64(e.Amount) / float64(e.Total)
}

// learn records a confirmed pair (a confirm, or a manual link, which is a
// stronger positive). A pattern is induced from consistent pairs: the same
// payer and category and a share ratio within tolerance. It stays in probation
// until three confirmations in a row, with no strike in between (S12b).
func (s *Service) learn(ctx context.Context, tx *sql.Tx, ex example) (*Pattern, error) {
	if ex.Counterparty == "" || ex.Total <= 0 {
		return nil, nil
	}
	if up, ok := s.userPatternFor(ctx, tx, ex.Counterparty, ex.Category); ok {
		// the user's own habit wins; a learned one that disagrees is not built, and the owner is asked
		if math.Abs(up.ShareRatio-ex.ratio()) > s.Cfg.PatternRatioTolerance {
			_, err := events.Append(ctx, tx, s.Reg, events.New{Actor: events.ActorSystem, Kind: KindPatternConflict, SubjectType: "reimburse", SubjectID: up.ID, Payload: PatternDemoted{PatternID: up.ID, Reason: "recent paybacks differ from the habit you wrote: update it?"}})
			return nil, err
		}
		return &up, nil
	}
	tol := s.ratioTolerance(ctx, tx, ex.Counterparty)
	rs, err := tx.QueryContext(ctx, `SELECT `+patternCols+` FROM reimbursement_patterns WHERE counterparty_id::text=$1 AND category_id=$2 AND state <> 'disabled' ORDER BY id`, ex.Counterparty, ex.Category)
	if err != nil {
		return nil, err
	}
	var match *Pattern
	for rs.Next() {
		p, err := scanPattern(rs)
		if err != nil {
			rs.Close()
			return nil, err
		}
		if math.Abs(p.ShareRatio-ex.ratio()) <= tol && match == nil {
			cp := p
			match = &cp
		}
	}
	rs.Close()
	if match == nil {
		var p Pattern
		err = tx.QueryRowContext(ctx, `INSERT INTO reimbursement_patterns (counterparty_id, category_id, merchant_key, share_ratio, lag_min, lag_max, last_confirmed_on)
			VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING `+patternCols,
			ex.Counterparty, ex.Category, "", ex.ratio(), max(0, ex.Lag), max(1, ex.Lag), ex.On.String()).Scan(&p.ID, &p.CounterpartyID, &p.CategoryID, &p.MerchantKey, &p.ShareRatio, &p.LagMin, &p.LagMax, &p.Support, &p.Streak, &p.Strikes, &p.State, &p.UserAuthored)
		return &p, err
	}
	n := float64(match.Support)
	match.ShareRatio = (match.ShareRatio*n + ex.ratio()) / (n + 1)
	match.Support++
	match.Streak++
	match.LagMin, match.LagMax = min(match.LagMin, max(0, ex.Lag)), max(match.LagMax, ex.Lag)
	if match.State == Probation && match.Streak >= s.Cfg.StreakToGraduate {
		match.State = Active
	}
	if match.State == Demoted && match.Streak >= s.Cfg.StreakToGraduate {
		match.State = Active // earned again
	}
	_, err = tx.ExecContext(ctx, `UPDATE reimbursement_patterns SET share_ratio=$2, support=$3, streak=$4, lag_min=$5, lag_max=$6, state=$7, last_confirmed_on=$8, updated_at=now() WHERE id=$1`,
		match.ID, match.ShareRatio, match.Support, match.Streak, match.LagMin, match.LagMax, match.State, ex.On.String())
	return match, err
}

// strike puts a pattern back to suggest-only: one rejection or unlink is enough,
// and it must earn graduation again.
func (s *Service) strike(ctx context.Context, tx *sql.Tx, actor events.Actor, patternID, reason string) error {
	if patternID == "" {
		return nil
	}
	var was string
	err := tx.QueryRowContext(ctx, `WITH old AS (SELECT state FROM reimbursement_patterns WHERE id::text=$1),
		upd AS (UPDATE reimbursement_patterns SET streak=0, strikes=strikes+1, state=CASE WHEN state='active' THEN 'probation' ELSE state END, updated_at=now() WHERE id::text=$1)
		SELECT state FROM old`, patternID).Scan(&was)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if was != Active {
		return nil
	}
	return s.emitDemoted(ctx, tx, actor, patternID, reason)
}

func (s *Service) emitDemoted(ctx context.Context, tx *sql.Tx, actor events.Actor, id, reason string) error {
	_, err := events.Append(ctx, tx, s.Reg, events.New{Actor: actor, Kind: KindPatternDemoted, SubjectType: "reimburse", SubjectID: id, Payload: PatternDemoted{PatternID: id, Reason: reason}})
	return err
}

// checkPrecision demotes a pattern whose recent auto-tentative links were
// mostly wrong (S12b evaluation and safety); the owner is told through the event.
func (s *Service) checkPrecision(ctx context.Context, tx *sql.Tx, patternID string) error {
	rs, err := tx.QueryContext(ctx, `SELECT outcome FROM link_events WHERE pattern_id::text=$1 AND outcome IN ('confirmed','rejected','unlinked') ORDER BY id DESC LIMIT 10`, patternID)
	if err != nil {
		return err
	}
	var good, total int
	for rs.Next() {
		var o string
		rs.Scan(&o)
		total++
		if o == "confirmed" {
			good++
		}
	}
	rs.Close()
	if total >= 5 && float64(good)/float64(total) < s.Cfg.PrecisionFloor {
		res, err := tx.ExecContext(ctx, `UPDATE reimbursement_patterns SET state='demoted', streak=0, updated_at=now() WHERE id::text=$1 AND state='active'`, patternID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			return s.emitDemoted(ctx, tx, events.ActorSystem, patternID, "its recent precision fell below the floor")
		}
	}
	return nil
}

// PatternsFor returns the usable patterns of one payer.
func (s *Service) PatternsFor(ctx context.Context, q events.DBTX, cp string) ([]Pattern, error) {
	all, err := s.Patterns(ctx, q)
	if err != nil {
		return nil, err
	}
	var out []Pattern
	for _, p := range all {
		if p.CounterpartyID == cp && p.State != Disabled {
			out = append(out, p)
		}
	}
	return out, nil
}

// SetPatternState lets the owner disable, re-enable or demote a pattern.
func (s *Service) SetPatternState(ctx context.Context, tx *sql.Tx, id, state string) error {
	switch state {
	case Probation, Disabled:
	default:
		return fmt.Errorf("%w: a pattern can be set to probation or disabled", ErrInvalid)
	}
	res, err := tx.ExecContext(ctx, `UPDATE reimbursement_patterns SET state=$2, streak=0, updated_at=now() WHERE id::text=$1`, id, state)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Profile is a payer's card (S12b): how they pay.
type Profile struct {
	Paybacks    int
	Others      int // deposits the user said were not reimbursements
	MedianLag   int
	MedianShare float64
	Spread      float64        // mean absolute deviation of their share
	MaxLag      int            // the longest ordinary lag (90th percentile)
	Categories  map[string]int // confirmed paybacks per expense category
	Reliability float64        // share of their receivables fully repaid
	Mixed       bool           // both reimbursements and other money: never auto-link
}

// Profile builds a counterparty's card from the user's own labels.
func (s *Service) Profile(ctx context.Context, q events.DBTX, cp string) (Profile, error) {
	var pr Profile
	pr.Categories = map[string]int{}
	rs, err := q.QueryContext(ctx, `SELECT lag_days, amount, expense_total, category_id FROM link_events e WHERE counterparty_id::text=$1 AND outcome IN ('confirmed','manual')
		AND NOT EXISTS (SELECT 1 FROM link_events u WHERE u.link_id = e.link_id AND u.outcome IN ('unlinked','rejected')) ORDER BY id`, cp)
	if err != nil {
		return pr, err
	}
	var lags []int
	var shares []float64
	for rs.Next() {
		var lag sql.NullInt64
		var a, t sql.NullInt64
		var cat string
		rs.Scan(&lag, &a, &t, &cat)
		pr.Paybacks++
		pr.Categories[cat]++
		lags = append(lags, int(lag.Int64))
		if t.Int64 > 0 {
			shares = append(shares, float64(a.Int64)/float64(t.Int64))
		}
	}
	rs.Close()
	sort.Ints(lags)
	sort.Float64s(shares)
	if len(lags) > 0 {
		pr.MedianLag = lags[len(lags)/2]
		pr.MaxLag = lags[min(len(lags)-1, len(lags)*9/10)]
	}
	if len(shares) > 0 {
		pr.MedianShare = shares[len(shares)/2]
		for _, sh := range shares {
			pr.Spread += math.Abs(sh - pr.MedianShare)
		}
		pr.Spread /= float64(len(shares))
	}
	if err = q.QueryRowContext(ctx, `SELECT count(*) FROM payer_labels WHERE counterparty_id::text=$1 AND label='other'`, cp).Scan(&pr.Others); err != nil {
		return pr, err
	}
	var all, paid int
	q.QueryRowContext(ctx, `SELECT count(*), count(*) FILTER (WHERE r.written_off_at IS NULL AND coalesce((SELECT sum(l.amount) FROM links l WHERE l.receivable_id=r.id AND l.state IN ('tentative','confirmed')),0) >= r.amount)
		FROM receivables r WHERE r.counterparty_id::text=$1 AND r.deleted_at IS NULL`, cp).Scan(&all, &paid)
	if all > 0 {
		pr.Reliability = float64(paid) / float64(all)
	}
	pr.Mixed = pr.Others > 0 && pr.Paybacks > 0
	return pr, nil
}

// logEvent records a decision or a label with the pair's features.
type logEntry struct {
	Payment, Expense, Receivable, Pattern, Counterparty string
	LinkID                                              int64
	Source, Outcome                                     string
	Confidence                                          float64
	Features                                            any
	Amount, ExpenseTotal                                int64
	Lag                                                 int
	Category                                            string
}

func nul(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (s *Service) log(ctx context.Context, tx *sql.Tx, e logEntry) error {
	f, _ := json.Marshal(e.Features)
	if e.Features == nil {
		f = []byte("{}")
	}
	var link any
	if e.LinkID != 0 {
		link = e.LinkID
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO link_events (payment_txn_id, expense_txn_id, receivable_id, link_id, pattern_id, counterparty_id, amount, expense_total, lag_days, category_id, source, outcome, confidence, features, model_version)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
		e.Payment, nul(e.Expense), nul(e.Receivable), link, nul(e.Pattern), nul(e.Counterparty), e.Amount, e.ExpenseTotal, e.Lag, e.Category, e.Source, e.Outcome, e.Confidence, string(f), ModelVersion)
	return err
}

// patternOf finds the pattern a link was made under, from its decision log.
func (s *Service) patternOf(ctx context.Context, q events.DBTX, linkID int64) (patternID string) {
	q.QueryRowContext(ctx, `SELECT coalesce(pattern_id::text,'') FROM link_events WHERE link_id=$1 AND pattern_id IS NOT NULL ORDER BY id DESC LIMIT 1`, linkID).Scan(&patternID)
	return
}

// context of a link for training: its payer, category and numbers.
func (s *Service) linkExample(ctx context.Context, tx *sql.Tx, l links.Link) (example, string, error) {
	rows, err := queryRows(ctx, tx, ` AND r.id = ANY($1::uuid[])`, "{"+l.PaymentID+","+l.ExpenseID+"}")
	if err != nil {
		return example{}, "", err
	}
	var pay, exp row
	for _, x := range rows {
		if x.ID == l.PaymentID {
			pay = x
		} else {
			exp = x
		}
	}
	m, err := s.Cps.Resolve(ctx, tx, pay.payerText())
	if err != nil {
		return example{}, "", err
	}
	cp := ""
	if m.Counterparty != nil {
		cp = m.Counterparty.ID
	}
	return example{Counterparty: cp, Category: exp.Category, Merchant: exp.merchant(), Amount: l.Amount, Total: -exp.Amount, Lag: pay.Date.DaysSince(exp.Date), On: pay.Date}, cp, nil
}

// Confirm accepts a tentative link: the label changes, the math does not, and
// the pair counts as a positive example.
func (s *Service) Confirm(ctx context.Context, tx *sql.Tx, actor events.Actor, linkID int64) (links.Link, error) {
	l, err := s.Links.Confirm(ctx, tx, actor, linkID)
	if err != nil {
		return l, err
	}
	ex, cp, err := s.linkExample(ctx, tx, l)
	if err != nil {
		return l, err
	}
	p, err := s.learn(ctx, tx, ex)
	if err != nil {
		return l, err
	}
	pid := s.patternOf(ctx, tx, linkID)
	if pid == "" && p != nil {
		pid = p.ID
	}
	if err = s.log(ctx, tx, logEntry{Payment: l.PaymentID, Expense: l.ExpenseID, Pattern: pid, Counterparty: cp, LinkID: linkID, Source: "user", Outcome: "confirmed", Amount: l.Amount, ExpenseTotal: ex.Total, Lag: ex.Lag, Category: ex.Category}); err != nil {
		return l, err
	}
	if pid != "" {
		err = s.checkPrecision(ctx, tx, pid)
	}
	return l, err
}

// Reject drops a suggested or tentative link. It is a negative example and a
// strike on the pattern behind it.
func (s *Service) Reject(ctx context.Context, tx *sql.Tx, actor events.Actor, linkID int64) (links.Link, error) {
	pid := s.patternOf(ctx, tx, linkID)
	l, err := s.Links.Reject(ctx, tx, actor, linkID)
	if err != nil {
		return l, err
	}
	ex, cp, err := s.linkExample(ctx, tx, l)
	if err != nil {
		return l, err
	}
	if err = s.log(ctx, tx, logEntry{Payment: l.PaymentID, Expense: l.ExpenseID, Pattern: pid, Counterparty: cp, LinkID: linkID, Source: "user", Outcome: "rejected", Amount: l.Amount, ExpenseTotal: ex.Total, Lag: ex.Lag, Category: ex.Category}); err != nil {
		return l, err
	}
	if err = s.strike(ctx, tx, actor, pid, "a link made under it was rejected"); err != nil {
		return l, err
	}
	if pid != "" {
		return l, s.checkPrecision(ctx, tx, pid)
	}
	return l, s.strikeMatching(ctx, tx, actor, ex, "a link like it was rejected")
}

// Unlink removes a link, even a confirmed one: a strong negative and a strike.
func (s *Service) Unlink(ctx context.Context, tx *sql.Tx, actor events.Actor, linkID int64) (links.Link, error) {
	pid := s.patternOf(ctx, tx, linkID)
	l, err := s.Links.Remove(ctx, tx, actor, linkID)
	if err != nil {
		return l, err
	}
	ex, cp, err := s.linkExample(ctx, tx, l)
	if err != nil {
		return l, err
	}
	if err = s.log(ctx, tx, logEntry{Payment: l.PaymentID, Expense: l.ExpenseID, Pattern: pid, Counterparty: cp, LinkID: linkID, Source: "user", Outcome: "unlinked", Amount: l.Amount, ExpenseTotal: ex.Total, Lag: ex.Lag, Category: ex.Category}); err != nil {
		return l, err
	}
	if pid != "" {
		if err = s.strike(ctx, tx, actor, pid, "a link made under it was removed"); err != nil {
			return l, err
		}
		return l, s.checkPrecision(ctx, tx, pid)
	}
	return l, s.strikeMatching(ctx, tx, actor, ex, "a link like it was removed")
}

// strikeMatching strikes the active pattern a manual link would have matched.
func (s *Service) strikeMatching(ctx context.Context, tx *sql.Tx, actor events.Actor, ex example, reason string) error {
	if ex.Counterparty == "" {
		return nil
	}
	ps, err := s.PatternsFor(ctx, tx, ex.Counterparty)
	if err != nil {
		return err
	}
	for _, p := range ps {
		if p.CategoryID == ex.Category && math.Abs(p.ShareRatio-ex.ratio()) <= s.Cfg.PatternRatioTolerance {
			return s.strike(ctx, tx, actor, p.ID, reason)
		}
	}
	return nil
}

// ManualLink is a link the user made themselves (the link picker): a strong
// positive example that counts toward the pattern.
func (s *Service) ManualLink(ctx context.Context, tx *sql.Tx, actor events.Actor, p links.Params) (links.Link, error) {
	p.State, p.CreatedBy = links.Confirmed, links.ByUser
	l, err := s.Links.Create(ctx, tx, actor, p)
	if err != nil {
		return l, err
	}
	ex, cp, err := s.linkExample(ctx, tx, l)
	if err != nil {
		return l, err
	}
	pat, err := s.learn(ctx, tx, ex)
	if err != nil {
		return l, err
	}
	pid := ""
	if pat != nil {
		pid = pat.ID
	}
	err = s.log(ctx, tx, logEntry{Payment: l.PaymentID, Expense: l.ExpenseID, Pattern: pid, Counterparty: cp, LinkID: l.ID, Source: "manual", Outcome: "manual", Amount: l.Amount, ExpenseTotal: ex.Total, Lag: ex.Lag, Category: ex.Category})
	return l, err
}

// ClassifyOther records that a deposit is not a reimbursement (a gift, an
// allowance, income). It trains the payer's reimbursement-versus-other split;
// a payer with both is never auto-linked.
func (s *Service) ClassifyOther(ctx context.Context, tx *sql.Tx, txnID string) error {
	rows, err := queryRows(ctx, tx, ` AND r.id = $1`, txnID)
	if err != nil || len(rows) == 0 {
		return ErrNotFound
	}
	m, err := s.Cps.Resolve(ctx, tx, rows[0].payerText())
	if err != nil {
		return err
	}
	var cp any
	if m.Counterparty != nil {
		cp = m.Counterparty.ID
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO payer_labels (txn_id, counterparty_id, label) VALUES ($1,$2,'other') ON CONFLICT (txn_id) DO UPDATE SET label='other', counterparty_id=EXCLUDED.counterparty_id`, txnID, cp); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE held_payments SET state='resolved', resolved_at=now() WHERE txn_id=$1`, txnID)
	return err
}

// ratioTolerance is how far a share may sit from a payer's habit: the global
// tolerance, widened to twice how much this payer's own share varies (tips and
// rounding), never beyond ten points.
func (s *Service) ratioTolerance(ctx context.Context, q events.DBTX, cp string) float64 {
	tol := s.Cfg.PatternRatioTolerance
	if cp == "" {
		return tol
	}
	if pr, err := s.Profile(ctx, q, cp); err == nil && pr.Paybacks >= 3 {
		tol = math.Max(tol, math.Min(0.10, 2*pr.Spread))
	}
	return tol
}
