package reimburse

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/alec-jensen/sofar/internal/civil"
	"github.com/alec-jensen/sofar/internal/events"
	"github.com/alec-jensen/sofar/internal/links"
)

// Held states (S12a item 3).
const (
	AwaitingExpense = "awaiting_expense"
	UnlinkedReimb   = "unlinked_reimbursement"
	NeedsDecision   = "needs_decision"
	ResolvedHeld    = "resolved"
)

// Held is a payment that arrived before its expense, held outside income and
// outside the plan until the matching expense shows up or the user decides.
type Held struct {
	TxnID        string
	State        string
	Since        civil.Date
	GraceUntil   civil.Date
	HoldUntil    civil.Date
	Declared     bool
	HintMerchant string
	HintAmount   *int64
	Amount       int64
	Description  string
	Suggestions  int
}

// hold records an abstained payment as held. A payment that is not a person
// paying is not held (it is just an unclassified deposit).
func (s *Service) hold(ctx context.Context, tx *sql.Tx, pay row, d *Decision) error {
	if !isP2P(pay, d.Payer != nil) {
		return nil
	}
	grace := pay.Date.AddDays(s.Cfg.GraceDays)
	hold := grace.AddDays(s.Cfg.HoldDays)
	_, err := tx.ExecContext(ctx, `
		INSERT INTO held_payments (txn_id, state, since, grace_until, hold_until) VALUES ($1,'awaiting_expense',$2,$3,$4)
		ON CONFLICT (txn_id) DO UPDATE SET state='awaiting_expense', since=EXCLUDED.since, grace_until=EXCLUDED.grace_until, hold_until=EXCLUDED.hold_until, resolved_at=NULL
		WHERE held_payments.state='resolved' AND held_payments.resolved_at IS NOT NULL`, pay.ID, s.today().String(), grace.String(), hold.String())
	return err
}

// Sweep advances held payments by the calendar: after the grace window an
// awaiting payment becomes an unlinked reimbursement (the "owed to you" prompt),
// and after the 30-day hold it needs a decision. It also closes those whose
// payment has since been linked. It is idempotent and safe after downtime.
func (s *Service) Sweep(ctx context.Context, tx *sql.Tx) error {
	today := s.today().String()
	if _, err := tx.ExecContext(ctx, `UPDATE held_payments h SET state='resolved', resolved_at=now()
		WHERE h.state <> 'resolved' AND coalesce((SELECT sum(l.amount) FROM links l WHERE l.payment_txn_id=h.txn_id AND l.state IN ('tentative','confirmed')),0) > 0`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE held_payments SET state='needs_decision' WHERE state IN ('awaiting_expense','unlinked_reimbursement') AND hold_until < $1`, today); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE held_payments SET state='unlinked_reimbursement' WHERE state='awaiting_expense' AND grace_until < $1 AND NOT declared`, today)
	return err
}

// ListHeld returns payments still held, oldest first.
func (s *Service) ListHeld(ctx context.Context, q events.DBTX) ([]Held, error) {
	rs, err := q.QueryContext(ctx, `
		SELECT h.txn_id, h.state, h.since, h.grace_until, h.hold_until, h.declared, h.hint_merchant, h.hint_amount, r.amount, r.description,
		       (SELECT count(*) FROM link_suggestions ls WHERE ls.payment_txn_id = h.txn_id)
		FROM held_payments h JOIN raw_transactions r ON r.id = h.txn_id WHERE h.state <> 'resolved' ORDER BY h.since, h.txn_id`)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var out []Held
	for rs.Next() {
		var h Held
		var a, b, c sql.NullTime
		var hint sql.NullInt64
		if err = rs.Scan(&h.TxnID, &h.State, &a, &b, &c, &h.Declared, &h.HintMerchant, &hint, &h.Amount, &h.Description, &h.Suggestions); err != nil {
			return nil, err
		}
		h.Since, h.GraceUntil, h.HoldUntil = civil.DateOf(a.Time), civil.DateOf(b.Time), civil.DateOf(c.Time)
		if hint.Valid {
			h.HintAmount = &hint.Int64
		}
		out = append(out, h)
	}
	return out, rs.Err()
}

// Declare lets the user say "this payment was for something not posted yet",
// with optional hints. The placeholder binds to the expense when it appears
// (a tentative link, since the user wrote the intent), and does not age into a
// question while it waits.
func (s *Service) Declare(ctx context.Context, tx *sql.Tx, txnID, merchant string, approx *int64) error {
	rows, err := queryRows(ctx, tx, ` AND r.id = $1`, txnID)
	if err != nil || len(rows) == 0 || rows[0].Amount <= 0 {
		return ErrNotFound
	}
	merchant = strings.ToLower(strings.TrimSpace(merchant))
	if len(merchant) > 80 {
		return fmt.Errorf("%w: the hint is too long", ErrInvalid)
	}
	until := s.today().AddDays(60).String()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO held_payments (txn_id, state, since, grace_until, hold_until, declared, hint_merchant, hint_amount) VALUES ($1,'awaiting_expense',$2,$3,$3,true,$4,$5)
		ON CONFLICT (txn_id) DO UPDATE SET state='awaiting_expense', declared=true, hint_merchant=EXCLUDED.hint_merchant, hint_amount=EXCLUDED.hint_amount,
		  grace_until=EXCLUDED.grace_until, hold_until=EXCLUDED.hold_until, resolved_at=NULL`, txnID, s.today().String(), until, merchant, approx)
	return err
}

// bindDeclaration applies a declared placeholder: when exactly one expense in
// the window carries the hinted merchant (and is large enough), it is the match.
func (s *Service) bindDeclaration(ctx context.Context, q events.DBTX, w *world, d *Decision) {
	var hint string
	var approx sql.NullInt64
	err := q.QueryRowContext(ctx, `SELECT hint_merchant, hint_amount FROM held_payments WHERE txn_id=$1 AND declared AND state <> 'resolved'`, d.PaymentID).Scan(&hint, &approx)
	if err != nil || hint == "" {
		return
	}
	var hits []Option
	for _, o := range d.Options {
		e := w.byID[o.ExpenseID]
		text := strings.ToLower(e.Description + " " + e.Payee)
		if !strings.Contains(text, hint) || o.Features.Over || o.Features.TooEarly {
			continue
		}
		if approx.Valid && (-e.Amount < approx.Int64*9/10 || -e.Amount > approx.Int64*11/10) {
			continue
		}
		hits = append(hits, o)
	}
	if len(hits) == 1 {
		o := hits[0]
		o.Why = append(o.Why, "you said this was for "+hint)
		d.Chosen, d.Kind, d.Source = &o, KindAuto, "declaration"
		d.Reason = "bound to the expense you declared"
	}
}

// Dissolve removes every active link that involves a transaction that has
// vanished or been cancelled (a pending item that never posted). Receivables
// return to expected on their own, since their status is derived from links.
func (s *Service) Dissolve(ctx context.Context, tx *sql.Tx, actor events.Actor, txnID string) (int, error) {
	rs, err := tx.QueryContext(ctx, `SELECT id FROM links WHERE (payment_txn_id=$1 OR expense_txn_id=$1) AND state IN ('tentative','confirmed')`, txnID)
	if err != nil {
		return 0, err
	}
	var ids []int64
	for rs.Next() {
		var id int64
		rs.Scan(&id)
		ids = append(ids, id)
	}
	rs.Close()
	for _, id := range ids {
		l, err := s.Links.Remove(ctx, tx, actor, id)
		if err != nil {
			return 0, err
		}
		if err = s.log(ctx, tx, logEntry{Payment: l.PaymentID, Expense: l.ExpenseID, LinkID: id, Source: "system", Outcome: "dissolved", Amount: l.Amount}); err != nil {
			return 0, err
		}
	}
	if len(ids) > 0 {
		_, err = tx.ExecContext(ctx, `UPDATE held_payments SET state='awaiting_expense', resolved_at=NULL WHERE txn_id=$1 AND state='resolved'`, txnID)
	}
	return len(ids), err
}

// CarryOver moves the links of a pending transaction onto the posted one it
// became (called by the sync reconcile). If the posted amount is smaller than
// what was linked (a tip changed it), the newest links shrink to fit and the
// remainder simply stops being linked; if the payment side changed, the same
// applies to it.
func (s *Service) CarryOver(ctx context.Context, tx *sql.Tx, actor events.Actor, pendingID, postedID string) error {
	if _, err := tx.ExecContext(ctx, `UPDATE links SET expense_txn_id=$2, updated_at=now() WHERE expense_txn_id=$1`, pendingID, postedID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE links SET payment_txn_id=$2, updated_at=now() WHERE payment_txn_id=$1`, pendingID, postedID); err != nil {
		return err
	}
	for _, side := range []struct{ col, amt string }{{"expense_txn_id", "abs(r.amount)"}, {"payment_txn_id", "r.amount"}} {
		rs, err := tx.QueryContext(ctx, `SELECT l.id, l.amount, `+side.amt+` FROM links l JOIN raw_transactions r ON r.id=l.`+side.col+` WHERE l.`+side.col+`=$1 AND l.state IN ('tentative','confirmed') ORDER BY l.id DESC`, postedID)
		if err != nil {
			return err
		}
		type lk struct{ id, amount, cap int64 }
		var all []lk
		var total, capacity int64
		for rs.Next() {
			var x lk
			rs.Scan(&x.id, &x.amount, &x.cap)
			all = append(all, x)
			total += x.amount
			capacity = x.cap
		}
		rs.Close()
		for _, x := range all {
			if total <= capacity {
				break
			}
			cut := min(x.amount, total-capacity)
			total -= cut
			if cut == x.amount {
				if _, err = s.Links.Remove(ctx, tx, actor, x.id); err != nil {
					return err
				}
			} else if _, err = tx.ExecContext(ctx, `UPDATE links SET amount=amount-$2, updated_at=now() WHERE id=$1`, x.id, cut); err != nil {
				return err
			}
		}
	}
	return nil
}

// ensure the links package is referenced for its constants in this file's docs
var _ = links.Tentative
