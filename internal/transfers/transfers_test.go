//go:build integration

package transfers_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/alec-jensen/sofar/internal/events"
	"github.com/alec-jensen/sofar/internal/ledger"
	"github.com/alec-jensen/sofar/internal/testkit"
	"github.com/alec-jensen/sofar/internal/transfers"
)

type env struct {
	t   *testing.T
	db  *sql.DB
	led *ledger.Service
	svc *transfers.Service
}

func setup(t *testing.T) *env {
	t.Helper()
	db := testkit.DB(t)
	reg := events.NewRegistry()
	led := ledger.New(db, reg)
	svc := transfers.New(db, led)
	svc.Now = func() time.Time { return time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC) }
	return &env{t: t, db: db, led: led, svc: svc}
}

func (e *env) account(name, kind, subtype string) string {
	id := testkit.AccountOfKind(e.t, e.db, name, kind)
	if subtype != "" {
		if _, err := e.db.Exec(`UPDATE accounts SET subtype=$2 WHERE id=$1`, id, subtype); err != nil {
			e.t.Fatal(err)
		}
	}
	return id
}

func (e *env) row(acct, date string, cents int64, desc string) string {
	return testkit.RawTxn(e.t, e.db, acct, testkit.Raw{PostedOn: date, TransactedOn: date, Amount: cents, Description: desc})
}

func (e *env) tx(fn func(tx *sql.Tx)) {
	e.t.Helper()
	tx, _ := e.db.Begin()
	defer tx.Rollback()
	fn(tx)
	if err := tx.Commit(); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) pair() int {
	e.t.Helper()
	var n int
	e.tx(func(tx *sql.Tx) {
		var err error
		if n, err = e.svc.Pair(context.Background(), tx); err != nil {
			e.t.Fatal(err)
		}
	})
	return n
}

func (e *env) cat(id string) string {
	g, err := e.led.Get(context.Background(), id)
	if err != nil {
		e.t.Fatal(err)
	}
	return g.CategoryID
}

func TestS12_AutoPairWithinThreeDays(t *testing.T) {
	e := setup(t)
	chk := e.account("checking", "budget", "checking")
	sav := e.account("savings", "budget", "savings")
	out := e.row(chk, "2026-10-01", -50000, "ONLINE TRANSFER TO SAVINGS")
	in := e.row(sav, "2026-10-04", 50000, "TRANSFER FROM CHECKING")
	late := e.row(chk, "2026-10-10", -7000, "MOVE")
	tooFar := e.row(sav, "2026-10-14", 7000, "MOVE")
	if n := e.pair(); n != 1 {
		t.Fatalf("one pair, three days apart: %d", n)
	}
	if e.cat(out) != "transfer" || e.cat(in) != "transfer" {
		t.Fatalf("both sides are neutral: %q %q", e.cat(out), e.cat(in))
	}
	if e.cat(late) != "" || e.cat(tooFar) != "" {
		t.Fatal("four days apart is not a pair")
	}
	if g, _ := e.led.Get(context.Background(), out); g.Bucket != "neutral" || g.ReviewedAt != nil {
		t.Fatalf("neutral but still waiting for review: %+v", g)
	}
	if n := e.pair(); n != 0 {
		t.Fatalf("pairing twice changes nothing: %d", n)
	}
	var system int
	e.db.QueryRow(`SELECT count(*) FROM events WHERE actor='system' AND kind='overlay.set_category'`).Scan(&system)
	if system != 2 {
		t.Fatalf("pairing is recorded as the system, one event per side: %d", system)
	}
}

func TestS12_PairUndoAndDissolveNeverRepair(t *testing.T) {
	e := setup(t)
	chk := e.account("checking", "budget", "checking")
	sav := e.account("savings", "budget", "savings")
	a := e.row(chk, "2026-10-01", -2500, "VENMO CASHOUT")
	b := e.row(sav, "2026-10-01", 2500, "DEPOSIT")
	e.pair()
	e.tx(func(tx *sql.Tx) {
		if err := e.svc.Unpair(context.Background(), tx, events.ActorUser, a); err != nil {
			t.Fatal(err)
		}
	})
	if e.cat(a) != "" || e.cat(b) != "" {
		t.Fatalf("not a transfer after all: %q %q", e.cat(a), e.cat(b))
	}
	if n := e.pair(); n != 0 {
		t.Fatal("a dissolved pair is never made again")
	}
	e.tx(func(tx *sql.Tx) {
		if err := e.svc.Unpair(context.Background(), tx, events.ActorUser, a); err != transfers.ErrNotFound {
			t.Fatalf("nothing left to dissolve: %v", err)
		}
	})
}

func TestS12_LearnedAccountPairWidensWindow(t *testing.T) {
	e := setup(t)
	chk := e.account("checking", "budget", "checking")
	sav := e.account("savings", "budget", "savings")
	for _, d := range []string{"2026-08-01", "2026-08-15", "2026-09-01"} {
		e.row(chk, d, -10000, "TRANSFER")
		e.row(sav, d, 10000, "TRANSFER")
	}
	if e.pair() != 3 {
		t.Fatal("three clean pairs")
	}
	a := e.row(chk, "2026-10-01", -10000, "TRANSFER")
	b := e.row(sav, "2026-10-06", 10000, "TRANSFER")
	if e.pair() != 1 || e.cat(a) != "transfer" || e.cat(b) != "transfer" {
		t.Fatal("five days apart pairs for an account pair seen three times")
	}
}

func TestS12_OneSidedTransferAsks(t *testing.T) {
	e := setup(t)
	chk := e.account("checking", "budget", "checking")
	lone := e.row(chk, "2026-10-05", -30000, "ONLINE TRANSFER TO XXXX1234")
	fresh := e.row(chk, "2026-10-19", -1000, "ONLINE TRANSFER")
	plain := e.row(chk, "2026-10-05", -1000, "TORCHYS TACOS")
	asks, err := e.svc.Asks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(asks) != 1 || asks[0].Txn.ID != lone || asks[0].Default != "neutral" || len(asks[0].Options) != 3 {
		t.Fatalf("one ask: the old transfer-looking row (a fresh one may still pair, a taco is not a transfer): %+v", asks)
	}
	_, _ = fresh, plain
	e.tx(func(tx *sql.Tx) {
		if _, err := e.svc.Resolve(context.Background(), tx, events.ActorUser, lone, "neutral"); err != nil {
			t.Fatal(err)
		}
	})
	if e.cat(lone) != "transfer" {
		t.Fatalf("neutral: %q", e.cat(lone))
	}
	if asks, _ := e.svc.Asks(context.Background()); len(asks) != 0 {
		t.Fatalf("asked once: %+v", asks)
	}
	e.tx(func(tx *sql.Tx) {
		if _, err := e.svc.Resolve(context.Background(), tx, events.ActorUser, lone, "maybe"); err == nil {
			t.Fatal("unknown choice")
		}
	})
}

func TestS12_AskAnsweredSpendingStaysSpendingAndIsNotAskedAgain(t *testing.T) {
	e := setup(t)
	chk := e.account("checking", "budget", "checking")
	lone := e.row(chk, "2026-10-05", -30000, "XFER TO FRIEND")
	e.tx(func(tx *sql.Tx) {
		if _, err := e.svc.Resolve(context.Background(), tx, events.ActorUser, lone, "spending"); err != nil {
			t.Fatal(err)
		}
	})
	if e.cat(lone) != "" {
		t.Fatal("spending is the default: no category set")
	}
	if asks, _ := e.svc.Asks(context.Background()); len(asks) != 0 {
		t.Fatal("answered")
	}
}

func TestS12_InvestmentContributionIsSavingsTransfer(t *testing.T) {
	e := setup(t)
	chk := e.account("checking", "budget", "checking")
	inv := e.account("brokerage", "tracked", "investment")
	// linked: the pair is a savings transfer on both sides
	a := e.row(chk, "2026-10-01", -20000, "ACH TRANSFER")
	b := e.row(inv, "2026-10-02", 20000, "DEPOSIT")
	e.pair()
	if e.cat(a) != "investments" || e.cat(b) != "investments" {
		t.Fatalf("savings transfer, category investments: %q %q", e.cat(a), e.cat(b))
	}
	if g, _ := e.led.Get(context.Background(), a); g.Bucket != "savings" {
		t.Fatalf("it counts toward savings: %+v", g)
	}
	// unlinked: a lone contribution asks with savings pre-selected
	lone := e.row(chk, "2026-10-05", -15000, "VANGUARD BUY INVESTMENT")
	asks, _ := e.svc.Asks(context.Background())
	if len(asks) != 1 || asks[0].Txn.ID != lone || asks[0].Default != "savings" {
		t.Fatalf("%+v", asks)
	}
	e.tx(func(tx *sql.Tx) {
		if _, err := e.svc.Resolve(context.Background(), tx, events.ActorUser, lone, "savings"); err != nil {
			t.Fatal(err)
		}
	})
	if e.cat(lone) != "investments" {
		t.Fatalf("%q", e.cat(lone))
	}
}

func TestS13_CardPaymentLinkedIsTransfer(t *testing.T) {
	e := setup(t)
	chk := e.account("checking", "budget", "checking")
	card := e.account("visa", "budget", "credit_card")
	pay := e.row(chk, "2026-10-10", -125000, "CHASE CARD PAYMENT")
	got := e.row(card, "2026-10-10", 125000, "PAYMENT THANK YOU")
	e.pair()
	if e.cat(pay) != "credit_card_payment" || e.cat(got) != "credit_card_payment" {
		t.Fatalf("a payment to a linked card is a transfer: %q %q", e.cat(pay), e.cat(got))
	}
	if g, _ := e.led.Get(context.Background(), pay); g.Bucket != "neutral" {
		t.Fatalf("%+v", g)
	}
}

func TestS13_CardPaymentUnlinkedIsSpending(t *testing.T) {
	e := setup(t)
	chk := e.account("checking", "budget", "checking")
	pay := e.row(chk, "2026-10-10", -125000, "CHASE CARD PAYMENT")
	if e.pair() != 0 || e.cat(pay) != "" {
		t.Fatal("no card account to pair with")
	}
	if asks, _ := e.svc.Asks(context.Background()); len(asks) != 0 {
		t.Fatal("it is spending by default, not a question")
	}
	if g, _ := e.led.Get(context.Background(), pay); g.Bucket == "neutral" {
		t.Fatalf("the purchases are invisible, so the payment is spending: %+v", g)
	}
}
