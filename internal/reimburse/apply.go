package reimburse

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/alec-jensen/sofar/internal/events"
	"github.com/alec-jensen/sofar/internal/links"
)

// Process decides every eligible incoming payment and writes the result: an
// auto-tentative link only in the two allowed cases, suggestions for the
// review queue, and held payments for the ones that abstain. It is idempotent:
// run it after any import, sync or change and nothing is done twice. A payment
// that already has a link is never touched.
func (s *Service) Process(ctx context.Context, tx *sql.Tx) ([]Decision, error) {
	ds, w, err := s.plan(ctx, tx, "")
	if err != nil {
		return nil, err
	}
	for i := range ds {
		if err = s.apply(ctx, tx, w, &ds[i]); err != nil {
			return ds, err
		}
	}
	return ds, nil
}

// OnExpense is the late-match trigger (S12a): run it whenever an expense
// arrives or posts. Payments that were held for lack of a counterpart get
// another look, and an expense that fits is suggested (or linked tentatively
// under a proven pattern). Nothing else changes.
func (s *Service) OnExpense(ctx context.Context, tx *sql.Tx) ([]Decision, error) {
	return s.Process(ctx, tx)
}

func (s *Service) apply(ctx context.Context, tx *sql.Tx, w *world, d *Decision) error {
	pay := w.byID[d.PaymentID]
	cp := ""
	if d.Payer != nil {
		cp = d.Payer.ID
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM link_suggestions WHERE payment_txn_id=$1`, d.PaymentID); err != nil {
		return err
	}
	switch d.Kind {
	case KindSkip:
		_, err := tx.ExecContext(ctx, `UPDATE held_payments SET state='resolved', resolved_at=now() WHERE txn_id=$1 AND state <> 'resolved'`, d.PaymentID)
		return err
	case KindAuto:
		o := d.Chosen
		by := links.ByAutoPattern
		if d.Source == "declaration" {
			by = links.ByUser // the user wrote the intent; the binding still waits for review
		}
		var rid *string
		if d.Source == "receivable" {
			by = links.ByAutoReceivable
			r := o.ReceivableID
			rid = &r
		}
		l, err := s.Links.Create(ctx, tx, events.ActorSystem, links.Params{PaymentID: d.PaymentID, ExpenseID: o.ExpenseID, ReceivableID: rid, Amount: o.Amount, State: links.Tentative, CreatedBy: by})
		if err != nil {
			return err
		}
		e := w.byID[o.ExpenseID]
		if err = s.log(ctx, tx, logEntry{Payment: d.PaymentID, Expense: o.ExpenseID, Receivable: o.ReceivableID, Pattern: d.PatternID, Counterparty: cp, LinkID: l.ID, Source: d.Source,
			Outcome: "auto_tentative", Confidence: o.P, Features: o.Features, Amount: o.Amount, ExpenseTotal: -e.Amount, Lag: pay.Date.DaysSince(e.Date), Category: e.Category}); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE held_payments SET state='resolved', resolved_at=now() WHERE txn_id=$1 AND state <> 'resolved'`, d.PaymentID)
		return err
	case KindSuggest, KindAmbiguous:
		shown := d.Options
		if len(shown) > 3 {
			shown = shown[:3]
		}
		for rank, o := range shown {
			if o.Features.Weak && d.Kind == KindSuggest {
				continue
			}
			why, _ := json.Marshal(o.Why)
			var rid, eid any
			if o.ReceivableID != "" {
				rid = o.ReceivableID
			}
			eid = o.ExpenseID
			if _, err := tx.ExecContext(ctx, `INSERT INTO link_suggestions (payment_txn_id, expense_txn_id, receivable_id, amount, confidence, reasons, rank) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
				d.PaymentID, eid, rid, o.Amount, o.P, string(why), rank+1); err != nil {
				return err
			}
		}
		return s.logOnce(ctx, tx, d, cp, "suggested")
	case KindAbstain:
		if err := s.hold(ctx, tx, pay, d); err != nil {
			return err
		}
		return s.logOnce(ctx, tx, d, cp, "abstained")
	}
	return nil
}

// logOnce records a decision only when it differs from the last one for the
// payment, so repeated runs do not flood the log.
func (s *Service) logOnce(ctx context.Context, tx *sql.Tx, d *Decision, cp, outcome string) error {
	exp := ""
	if d.Chosen != nil {
		exp = d.Chosen.ExpenseID
	} else if len(d.Options) > 0 && outcome == "suggested" {
		exp = d.Options[0].ExpenseID
	}
	var lastOutcome, lastExp string
	tx.QueryRowContext(ctx, `SELECT outcome, coalesce(expense_txn_id::text,'') FROM link_events WHERE payment_txn_id=$1 AND outcome IN ('suggested','abstained') ORDER BY id DESC LIMIT 1`, d.PaymentID).Scan(&lastOutcome, &lastExp)
	if lastOutcome == outcome && lastExp == exp {
		return nil
	}
	var feats any
	conf := 0.0
	if len(d.Options) > 0 {
		feats, conf = d.Options[0].Features, d.Options[0].P
	}
	return s.log(ctx, tx, logEntry{Payment: d.PaymentID, Expense: exp, Counterparty: cp, Source: d.Source, Outcome: outcome, Confidence: conf, Features: feats})
}

// AcceptSuggestion turns a suggestion into a link at the user's say-so: a
// tentative link that nets immediately, which the user then confirms or
// rejects like any other. (Confirming it is a separate, learning step.)
func (s *Service) AcceptSuggestion(ctx context.Context, tx *sql.Tx, actor events.Actor, paymentID string, rank int) (links.Link, error) {
	var exp, rec sql.NullString
	var amount int64
	err := tx.QueryRowContext(ctx, `SELECT expense_txn_id::text, receivable_id::text, amount FROM link_suggestions WHERE payment_txn_id=$1 AND rank=$2`, paymentID, rank).Scan(&exp, &rec, &amount)
	if err == sql.ErrNoRows {
		return links.Link{}, ErrNotFound
	}
	if err != nil {
		return links.Link{}, err
	}
	var rid *string
	if rec.Valid {
		rid = &rec.String
	}
	l, err := s.Links.Create(ctx, tx, actor, links.Params{PaymentID: paymentID, ExpenseID: exp.String, ReceivableID: rid, Amount: amount, State: links.Tentative, CreatedBy: links.ByUser})
	if err != nil {
		return l, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM link_suggestions WHERE payment_txn_id=$1`, paymentID); err != nil {
		return l, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE held_payments SET state='resolved', resolved_at=now() WHERE txn_id=$1 AND state <> 'resolved'`, paymentID)
	return l, err
}
