//go:build integration

package counterparties_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/alec-jensen/sofar/internal/counterparties"
	"github.com/alec-jensen/sofar/internal/events"
	"github.com/alec-jensen/sofar/internal/testkit"
)

type env struct {
	t   *testing.T
	db  *sql.DB
	svc *counterparties.Service
}

func setup(t *testing.T) *env {
	t.Helper()
	db := testkit.DB(t)
	return &env{t: t, db: db, svc: counterparties.New(db, events.NewRegistry())}
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

func (e *env) create(name string, aliases ...string) counterparties.Counterparty {
	var c counterparties.Counterparty
	e.tx(func(tx *sql.Tx) {
		var err error
		if c, err = e.svc.Create(context.Background(), tx, events.ActorUser, name, aliases...); err != nil {
			e.t.Fatal(err)
		}
	})
	return c
}

func (e *env) resolve(payer string) counterparties.Match {
	m, err := e.svc.Resolve(context.Background(), e.db, payer)
	if err != nil {
		e.t.Fatal(err)
	}
	return m
}

func TestS12b_E3_AliasMergeAcrossPlatforms(t *testing.T) {
	e := setup(t)
	zelle := e.create("Dad", "ZELLE FROM JOHN SMITH")
	venmo := e.create("John (venmo)", "@jsmith88")
	if m := e.resolve("VENMO PAYMENT FROM @jsmith88"); m.Counterparty == nil || m.Counterparty.ID != venmo.ID {
		t.Fatalf("handle resolves: %+v", m)
	}
	e.tx(func(tx *sql.Tx) {
		if err := e.svc.Merge(context.Background(), tx, events.ActorUser, venmo.ID, zelle.ID); err != nil {
			t.Fatal(err)
		}
	})
	if m := e.resolve("VENMO PAYMENT FROM @jsmith88"); m.Counterparty == nil || m.Counterparty.ID != zelle.ID {
		t.Fatalf("after the merge both platforms are Dad: %+v", m)
	}
	if m := e.resolve("Zelle payment from Smith,John"); m.Counterparty == nil || m.Counterparty.ID != zelle.ID || m.Score != 1 {
		t.Fatalf("Last,First is the same person: %+v", m)
	}
	list, _ := e.svc.List(context.Background(), e.db)
	if len(list) != 1 || len(list[0].Aliases) != 2 {
		t.Fatalf("one person, two aliases: %+v", list)
	}
}

func TestS12b_E4_HandleChangeHeals(t *testing.T) {
	e := setup(t)
	dad := e.create("Dad", "@dadj")
	if m := e.resolve("VENMO FROM @johnsmith"); m.Counterparty != nil {
		t.Fatal("an unknown handle is unknown")
	}
	// the user says this new handle is Dad: every later payment resolves
	e.tx(func(tx *sql.Tx) {
		if _, err := e.svc.AddAlias(context.Background(), tx, events.ActorUser, dad.ID, "VENMO FROM @johnsmith"); err != nil {
			t.Fatal(err)
		}
	})
	if m := e.resolve("VENMO PAYMENT FROM @johnsmith"); m.Counterparty == nil || m.Counterparty.ID != dad.ID {
		t.Fatalf("healed: %+v", m)
	}
	if m := e.resolve("VENMO PAYMENT FROM @dadj"); m.Counterparty == nil || m.Counterparty.ID != dad.ID {
		t.Fatal("the old handle still works")
	}
}

func TestS12b_E9_MaskedNamesDisambiguated(t *testing.T) {
	e := setup(t)
	smith := e.create("John Smith", "ZELLE FROM JOHN SMITH")
	e.create("John Stone", "ZELLE FROM JOHN STONE")
	m := e.resolve("ZELLE FROM JOHN S")
	if !m.Ambiguous() || len(m.Candidates) != 2 {
		t.Fatalf("both fit, so the system does not choose: %+v", m)
	}
	// a masked name that only one person fits resolves
	if m = e.resolve("ZELLE FROM JOHN SM"); m.Counterparty == nil || m.Counterparty.ID != smith.ID {
		t.Fatalf("%+v", m)
	}
	// the user names the masked string once: it is now unambiguous
	e.tx(func(tx *sql.Tx) {
		if _, err := e.svc.AddAlias(context.Background(), tx, events.ActorUser, smith.ID, "ZELLE FROM JOHN S"); err != nil {
			t.Fatal(err)
		}
	})
	if m = e.resolve("ZELLE FROM JOHN S"); m.Counterparty == nil || m.Counterparty.ID != smith.ID || m.Score != 1 {
		t.Fatalf("an exact alias wins outright: %+v", m)
	}
}

func TestS12b_PolicySplitUndoRedo(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	c := e.create("Roommate", "ZELLE FROM AMY LEE", "ZELLE FROM BEN ORTIZ")
	var ev int64
	e.tx(func(tx *sql.Tx) {
		if err := e.svc.SetPolicy(ctx, tx, events.ActorUser, c.ID, counterparties.PolicyNever); err != nil {
			t.Fatal(err)
		}
		tx.QueryRowContext(ctx, `SELECT max(id) FROM events`).Scan(&ev)
	})
	if got, _ := e.svc.Get(ctx, e.db, c.ID); got.Policy != "never" {
		t.Fatalf("%+v", got)
	}
	var undo events.Event
	e.tx(func(tx *sql.Tx) {
		var err error
		if undo, err = e.svc.Undo(ctx, tx, events.ActorUser, ev); err != nil {
			t.Fatal(err)
		}
	})
	if got, _ := e.svc.Get(ctx, e.db, c.ID); got.Policy != "ask" {
		t.Fatalf("undone: %+v", got)
	}
	e.tx(func(tx *sql.Tx) {
		if _, err := e.svc.Redo(ctx, tx, events.ActorUser, undo.ID); err != nil {
			t.Fatal(err)
		}
	})
	if got, _ := e.svc.Get(ctx, e.db, c.ID); got.Policy != "never" {
		t.Fatal("redone")
	}
	// two people were merged by mistake: split one out, then undo the split
	var split counterparties.Counterparty
	var splitEv int64
	e.tx(func(tx *sql.Tx) {
		var err error
		if split, err = e.svc.Split(ctx, tx, events.ActorUser, c.ID, "ben ortiz", "Ben"); err != nil {
			t.Fatal(err)
		}
		tx.QueryRowContext(ctx, `SELECT max(id) FROM events`).Scan(&splitEv)
	})
	if m := e.resolve("ZELLE FROM BEN ORTIZ"); m.Counterparty == nil || m.Counterparty.ID != split.ID {
		t.Fatalf("Ben is his own person now: %+v", m)
	}
	e.tx(func(tx *sql.Tx) {
		if _, err := e.svc.Undo(ctx, tx, events.ActorUser, splitEv); err != nil {
			t.Fatal(err)
		}
	})
	if m := e.resolve("ZELLE FROM BEN ORTIZ"); m.Counterparty == nil || m.Counterparty.ID != c.ID {
		t.Fatalf("back together: %+v", m)
	}
	if err := func() error {
		tx, _ := e.db.Begin()
		defer tx.Rollback()
		return e.svc.SetPolicy(ctx, tx, events.ActorUser, c.ID, "sometimes")
	}(); err == nil {
		t.Fatal("unknown policy")
	}
}
