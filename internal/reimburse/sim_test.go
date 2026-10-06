//go:build integration

package reimburse_test

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/alec-jensen/sofar/internal/events"
	"github.com/alec-jensen/sofar/internal/links"
	"github.com/alec-jensen/sofar/internal/reimburse"
	"github.com/alec-jensen/sofar/internal/sim"
	"github.com/alec-jensen/sofar/internal/testkit"
)

type simResult struct {
	paybacks                      int
	autoTotal, autoCorrect        int
	suggested, suggestedCorrect   int
	linkedByUser, neverLinked     int
	falseLinks, allLinks, abstain int
}

// runSim replays a synthetic life day by day. Expenses and P2P paybacks arrive,
// the matcher runs, and a user oracle confirms what is right and rejects or
// corrects what is wrong.
func runSim(t *testing.T, seed int64) simResult {
	t.Helper()
	e := setup(t)
	e.dad()
	cfg := sim.DefaultConfig(seed)
	cfg.Months, cfg.OutageEvery, cfg.DuplicateRate, cfg.RotateRate = 8, 0, 0, 0
	cfg.PaybackRate = 0.5
	life := sim.Generate(cfg)

	type item struct {
		tx sim.Txn
		id string
	}
	byDay := map[string][]sim.Txn{}
	for _, tx := range life.Txns {
		if tx.Truth.Kind == sim.KindSpending || tx.Truth.Kind == sim.KindReimbursement {
			byDay[tx.Raw.Posted.String()] = append(byDay[tx.Raw.Posted.String()], tx)
		}
	}
	dbID := map[string]string{}  // sim id -> db id
	truth := map[string]string{} // db payment id -> db expense id (filled lazily by sim id)
	truthSim := map[string]string{}
	var res simResult
	linked := map[string]bool{}

	start := cfg.Start
	for dayN := 0; dayN < 8*31; dayN++ {
		d := start.AddDays(dayN)
		e.now = time.Date(d.Y, d.M, d.D, 12, 0, 0, 0, time.UTC)
		e.rcv.Now = func() time.Time { return e.now }
		txs := byDay[d.String()]
		sort.SliceStable(txs, func(i, j int) bool { return txs[i].Raw.ID < txs[j].Raw.ID })
		for _, tx := range txs {
			if tx.Truth.Kind == sim.KindSpending {
				id := testkit.RawTxn(t, e.db, e.acct, testkit.Raw{PostedOn: d.String(), TransactedOn: tx.Appears.String(), Amount: int64(tx.Raw.Amount), Description: tx.Raw.Description})
				e.setCategory(id, tx.Truth.Category, true)
				dbID[tx.Raw.ID] = id
			} else {
				id := testkit.RawTxn(t, e.db, e.acct, testkit.Raw{PostedOn: d.String(), TransactedOn: d.String(), Amount: int64(tx.Raw.Amount), Description: tx.Raw.Description})
				dbID[tx.Raw.ID] = id
				truthSim[id] = tx.Truth.Reimburses
				res.paybacks++
			}
		}
		_ = truth
		if len(txs) == 0 {
			continue
		}
		ds := e.process()
		for pay, dec := range ds {
			want, isPayback := truthSim[pay]
			if !isPayback || linked[pay] {
				continue
			}
			wantExp := dbID[want]
			var act links.Link
			for _, l := range e.activeLinks() {
				if l.PaymentID == pay {
					act = l
				}
			}
			switch dec.Kind {
			case reimburse.KindAuto:
				res.autoTotal++
				res.allLinks++
				if act.ExpenseID == wantExp {
					res.autoCorrect++
					e.tx(func(tx *sql.Tx) { e.svc.Confirm(context.Background(), tx, events.ActorUser, act.ID) })
				} else {
					res.falseLinks++
					e.tx(func(tx *sql.Tx) { e.svc.Reject(context.Background(), tx, events.ActorUser, act.ID) })
					e.userLink(pay, wantExp, &res)
				}
				linked[pay] = true
			case reimburse.KindSuggest:
				res.suggested++
				if dec.Chosen != nil && dec.Chosen.ExpenseID == wantExp {
					res.suggestedCorrect++
					res.allLinks++
					e.tx(func(tx *sql.Tx) {
						l, err := e.svc.AcceptSuggestion(context.Background(), tx, events.ActorUser, pay, 1)
						if err == nil {
							_, err = e.svc.Confirm(context.Background(), tx, events.ActorUser, l.ID)
						}
						if err != nil {
							t.Fatal(err)
						}
					})
					linked[pay] = true
				} else {
					e.userLink(pay, wantExp, &res)
					linked[pay] = true
				}
			default:
				res.abstain++
				if false {
					want := e.describe(wantExp)
					top := ""
					for _, o := range dec.Options {
						if o.ExpenseID == wantExp {
							top = fmt.Sprintf("truth option: p=%.2f %+v", o.P, o.Features)
						}
					}
					t.Logf("abstain #%d (%s): payment %d, truth expense %s; %s; reason: %s; options=%d", res.abstain, dec.Kind, e.amountOf(pay), want, top, dec.Reason, len(dec.Options))
				}
				if wantExp != "" && e.expenseVisible(wantExp) {
					e.userLink(pay, wantExp, &res)
					linked[pay] = true
				}
			}
		}
	}
	for pay := range truthSim {
		if !linked[pay] {
			res.neverLinked++
		}
	}
	return res
}

func (e *env) expenseVisible(id string) bool {
	var n int
	e.db.QueryRow(`SELECT count(*) FROM raw_transactions WHERE id=$1`, id).Scan(&n)
	return n == 1
}

// userLink is the user linking the right expense by hand (the link picker).
func (e *env) userLink(pay, exp string, res *simResult) {
	if exp == "" {
		return
	}
	e.tx(func(tx *sql.Tx) {
		if _, err := e.svc.ManualLink(context.Background(), tx, events.ActorUser, links.Params{PaymentID: pay, ExpenseID: exp}); err == nil {
			res.linkedByUser++
		}
	})
}

// TestA9_10_AutoTentativePrecisionGate: auto-tentative links are right at least
// 99% of the time across the standard seeds. A wrong link corrupts a month, so
// precision matters more than coverage; the numbers are logged for the report.
func TestA9_10_AutoTentativePrecisionGate(t *testing.T) {
	if testing.Short() {
		t.Skip("simulation")
	}
	var total simResult
	for _, seed := range []int64{1, 2, 3, 4, 5, 6} {
		r := runSim(t, seed)
		t.Logf("seed %d: %+v", seed, r)
		total.paybacks += r.paybacks
		total.autoTotal += r.autoTotal
		total.autoCorrect += r.autoCorrect
		total.suggested += r.suggested
		total.suggestedCorrect += r.suggestedCorrect
		total.falseLinks += r.falseLinks
		total.allLinks += r.allLinks
		total.neverLinked += r.neverLinked
		total.abstain += r.abstain
	}
	if total.autoTotal > 0 {
		prec := float64(total.autoCorrect) / float64(total.autoTotal)
		t.Logf("auto-tentative precision %.4f over %d links; suggestions right %d of %d; paybacks %d, abstained runs %d, never linked %d",
			prec, total.autoTotal, total.suggestedCorrect, total.suggested, total.paybacks, total.abstain, total.neverLinked)
		if prec < 0.99 {
			t.Errorf("auto-tentative precision %.4f is below the 0.99 gate", prec)
		}
	} else {
		t.Logf("no auto-tentative links were made over %d paybacks: precision is vacuously perfect", total.paybacks)
	}
	if total.paybacks == 0 {
		t.Fatal("the simulation produced no paybacks")
	}
	if rate := float64(total.falseLinks) / float64(max(1, total.allLinks)); rate > 0.01 {
		t.Errorf("false-link rate %.4f is above 1%%", rate)
	}
}

func (e *env) amountOf(id string) int64 {
	var a int64
	e.db.QueryRow(`SELECT amount FROM raw_transactions WHERE id=$1`, id).Scan(&a)
	return a
}

func (e *env) describe(id string) string {
	var a int64
	var d string
	var tr, po sql.NullTime
	e.db.QueryRow(`SELECT amount, description, transacted_on, posted_on FROM raw_transactions WHERE id=$1`, id).Scan(&a, &d, &tr, &po)
	return fmt.Sprintf("%d %s tr=%v po=%v", a, d, tr.Time.Format("01-02"), po.Time.Format("01-02"))
}
