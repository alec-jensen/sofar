//go:build integration

package categories_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/alec-jensen/sofar/internal/categories"
	"github.com/alec-jensen/sofar/internal/events"
	"github.com/alec-jensen/sofar/internal/ledger"
	"github.com/alec-jensen/sofar/internal/testkit"
)

type env struct {
	t    *testing.T
	db   *sql.DB
	led  *ledger.Service
	cats *categories.Service
	acct string
}

func setup(t *testing.T) *env {
	t.Helper()
	db := testkit.DB(t)
	reg := events.NewRegistry()
	led := ledger.New(db, reg)
	return &env{t: t, db: db, led: led, cats: categories.New(db, led, reg), acct: testkit.Account(t, db, "checking")}
}

// do runs fn in a transaction and commits it.
func (e *env) do(fn func(tx *sql.Tx)) {
	e.t.Helper()
	tx, err := e.db.Begin()
	if err != nil {
		e.t.Fatal(err)
	}
	defer tx.Rollback()
	fn(tx)
	if err = tx.Commit(); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) categorize(cat string) string {
	e.t.Helper()
	id := testkit.RawTxn(e.t, e.db, e.acct, testkit.Raw{Amount: -100})
	e.do(func(tx *sql.Tx) {
		if _, err := e.led.Apply(context.Background(), tx, events.ActorUser, id, ledger.KindSetCategory, ledger.SetCategory{TxnID: id, CategoryID: &cat}, ""); err != nil {
			e.t.Fatal(err)
		}
	})
	return id
}

func (e *env) category(txn string) string {
	g, err := e.led.Get(context.Background(), txn)
	if err != nil {
		e.t.Fatal(err)
	}
	return g.CategoryID
}

func TestS1a_CustomGroupAndCategoryRules(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	tx, _ := e.db.Begin()
	defer tx.Rollback()
	if _, err := e.cats.CreateGroup(ctx, tx, events.ActorUser, "pets", "neutral"); !errors.Is(err, categories.ErrInvalid) {
		t.Errorf("a custom group in the neutral bucket must be refused: %v", err)
	}
	if _, err := e.cats.CreateCategory(ctx, tx, events.ActorUser, "no_such_group", "pets"); !errors.Is(err, categories.ErrInvalid) {
		t.Errorf("a category must be placed in a group: %v", err)
	}
	if _, err := e.cats.CreateCategory(ctx, tx, events.ActorUser, "nt_transfers", "mine"); !errors.Is(err, categories.ErrInvalid) {
		t.Errorf("neutral categories are system-only: %v", err)
	}
	if _, err := e.cats.CreateGroup(ctx, tx, events.ActorUser, "  ", "spending"); !errors.Is(err, categories.ErrInvalid) {
		t.Errorf("empty names are refused: %v", err)
	}
	g, err := e.cats.CreateGroup(ctx, tx, events.ActorUser, "Pets & animals", "spending")
	if err != nil {
		t.Fatal(err)
	}
	c, err := e.cats.CreateCategory(ctx, tx, events.ActorUser, g.ID, "Vet bills")
	if err != nil {
		t.Fatal(err)
	}
	tx.Commit()
	groups, err := e.cats.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, gr := range groups {
		if gr.ID == g.ID {
			found = gr.Bucket == "spending" && len(gr.Categories) == 1 && gr.Categories[0].ID == c.ID && gr.Categories[0].Name == "Vet bills"
		}
	}
	if !found {
		t.Fatalf("custom group and category should be listed: %+v", groups)
	}
	if groups[0].Bucket != "income" || groups[len(groups)-1].Bucket != "neutral" {
		t.Errorf("groups are ordered income first, neutral last: %s ... %s", groups[0].Bucket, groups[len(groups)-1].Bucket)
	}
}

func TestS1a_RenameKeepsID(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	txn := e.categorize("groceries")
	e.do(func(tx *sql.Tx) {
		if err := e.cats.RenameCategory(ctx, tx, events.ActorUser, "groceries", "food shopping"); err != nil {
			t.Fatal(err)
		}
	})
	if e.category(txn) != "groceries" {
		t.Error("renaming must not change the id")
	}
	var name string
	e.db.QueryRow(`SELECT name FROM categories WHERE id='groceries'`).Scan(&name)
	if name != "food shopping" {
		t.Errorf("name = %q", name)
	}
	tx, _ := e.db.Begin()
	defer tx.Rollback()
	if err := e.cats.RenameCategory(ctx, tx, events.ActorUser, "no_such", "x"); !errors.Is(err, categories.ErrNotFound) {
		t.Errorf("renaming a missing category: %v", err)
	}
}

func TestS1a_DeleteRequiresReassignment(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	var custom categories.Category
	e.do(func(tx *sql.Tx) {
		g, _ := e.cats.CreateGroup(ctx, tx, events.ActorUser, "stuff", "spending")
		var err error
		if custom, err = e.cats.CreateCategory(ctx, tx, events.ActorUser, g.ID, "hobby"); err != nil {
			t.Fatal(err)
		}
	})
	a, b := e.categorize(custom.ID), e.categorize(custom.ID)

	tx, _ := e.db.Begin()
	if _, err := e.cats.Delete(ctx, tx, events.ActorUser, custom.ID, "no_such_category"); !errors.Is(err, categories.ErrInvalid) {
		t.Errorf("an unknown reassignment target must be refused: %v", err)
	}
	if _, err := e.cats.Delete(ctx, tx, events.ActorUser, custom.ID, custom.ID); !errors.Is(err, categories.ErrInvalid) {
		t.Errorf("reassigning to itself must be refused: %v", err)
	}
	if _, err := e.cats.Delete(ctx, tx, events.ActorUser, "other_spending", ""); !errors.Is(err, categories.ErrProtected) {
		t.Errorf("the bucket fallback cannot be deleted: %v", err)
	}
	if _, err := e.cats.Delete(ctx, tx, events.ActorUser, "transfer", ""); !errors.Is(err, categories.ErrProtected) {
		t.Errorf("system categories cannot be deleted: %v", err)
	}
	tx.Rollback()

	// default target: "other" in the same bucket
	var del events.Event
	e.do(func(tx *sql.Tx) {
		var err error
		if del, err = e.cats.Delete(ctx, tx, events.ActorUser, custom.ID, ""); err != nil {
			t.Fatal(err)
		}
	})
	if e.category(a) != "other_spending" || e.category(b) != "other_spending" {
		t.Fatalf("transactions should move to other_spending, got %q and %q", e.category(a), e.category(b))
	}
	groups, _ := e.cats.List(ctx)
	for _, g := range groups {
		for _, c := range g.Categories {
			if c.ID == custom.ID {
				t.Error("a deleted category must not be listed")
			}
		}
	}
	// undo brings the category and its transactions back
	e.do(func(tx *sql.Tx) {
		if _, err := e.cats.Undo(ctx, tx, events.ActorUser, del.ID); err != nil {
			t.Fatal(err)
		}
	})
	if e.category(a) != custom.ID {
		t.Fatalf("undo should return transactions to the category, got %q", e.category(a))
	}
}

func TestS1a_DeleteWithExplicitTarget(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	txn := e.categorize("dining_out")
	e.do(func(tx *sql.Tx) {
		if _, err := e.cats.Delete(ctx, tx, events.ActorUser, "dining_out", "groceries"); err != nil {
			t.Fatal(err)
		}
	})
	if e.category(txn) != "groceries" {
		t.Fatalf("got %q", e.category(txn))
	}
}

func TestS1a_MergeMovesTransactions(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	a, b := e.categorize("fuel"), e.categorize("auto_transit")
	split := testkit.RawTxn(t, e.db, e.acct, testkit.Raw{Amount: -1000})
	e.do(func(tx *sql.Tx) {
		_, err := e.led.Apply(ctx, tx, events.ActorUser, split, ledger.KindSplit, ledger.Split{TxnID: split, Parts: []ledger.SplitPart{{Amount: -600, CategoryID: "fuel"}, {Amount: -400, CategoryID: "groceries"}}}, "")
		if err != nil {
			t.Fatal(err)
		}
	})
	e.do(func(tx *sql.Tx) {
		if _, err := e.cats.Merge(ctx, tx, events.ActorUser, "fuel", "auto_transit"); err != nil {
			t.Fatal(err)
		}
	})
	if e.category(a) != "auto_transit" || e.category(b) != "auto_transit" {
		t.Errorf("both should now be auto_transit: %q %q", e.category(a), e.category(b))
	}
	g, _ := e.led.Get(ctx, split)
	if g.Splits[0].CategoryID != "auto_transit" || g.Splits[1].CategoryID != "groceries" {
		t.Errorf("split parts follow the merge: %+v", g.Splits)
	}
	// chains resolve: merge auto_transit into shopping, a should follow
	e.do(func(tx *sql.Tx) {
		if _, err := e.cats.Merge(ctx, tx, events.ActorUser, "auto_transit", "shopping"); err != nil {
			t.Fatal(err)
		}
	})
	if e.category(a) != "shopping" {
		t.Errorf("merge chains must resolve, got %q", e.category(a))
	}
	// a category set after the merge on the retired id still lands on the survivor
	late := e.categorize("fuel")
	if e.category(late) != "shopping" {
		t.Errorf("a retired id resolves to the survivor, got %q", e.category(late))
	}
	tx, _ := e.db.Begin()
	defer tx.Rollback()
	if _, err := e.cats.Merge(ctx, tx, events.ActorUser, "shopping", "shopping"); !errors.Is(err, categories.ErrInvalid) {
		t.Errorf("self merge: %v", err)
	}
}

func TestS1a_DeleteGroupRequiresEmpty(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	var g categories.Group
	var c categories.Category
	e.do(func(tx *sql.Tx) {
		g, _ = e.cats.CreateGroup(ctx, tx, events.ActorUser, "temp", "savings")
		c, _ = e.cats.CreateCategory(ctx, tx, events.ActorUser, g.ID, "inner")
	})
	tx, _ := e.db.Begin()
	if err := e.cats.DeleteGroup(ctx, tx, events.ActorUser, g.ID); !errors.Is(err, categories.ErrGroupNotEmpty) {
		t.Errorf("non-empty group: %v", err)
	}
	if err := e.cats.DeleteGroup(ctx, tx, events.ActorUser, "sv_core"); !errors.Is(err, categories.ErrProtected) {
		t.Errorf("default groups are protected: %v", err)
	}
	tx.Rollback()
	e.do(func(tx *sql.Tx) {
		if _, err := e.cats.Delete(ctx, tx, events.ActorUser, c.ID, ""); err != nil {
			t.Fatal(err)
		}
		if err := e.cats.DeleteGroup(ctx, tx, events.ActorUser, g.ID); err != nil {
			t.Fatal(err)
		}
	})
}
