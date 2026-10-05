//go:build integration

package merchants_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/alec-jensen/sofar/internal/events"
	"github.com/alec-jensen/sofar/internal/merchants"
	"github.com/alec-jensen/sofar/internal/testkit"
)

func setup(t *testing.T) (*merchants.Service, *sql.DB) {
	t.Helper()
	db := testkit.DB(t)
	return merchants.New(db, events.NewRegistry()), db
}

func tx(t *testing.T, db *sql.DB, fn func(*sql.Tx)) {
	t.Helper()
	x, _ := db.Begin()
	defer x.Rollback()
	fn(x)
	if err := x.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestS18a_ResolveGroupsDescriptorsByKey(t *testing.T) {
	s, db := setup(t)
	ctx := context.Background()
	var a, b, c merchants.Merchant
	var created bool
	tx(t, db, func(x *sql.Tx) {
		a, created, _ = s.Resolve(ctx, x, "TST* TORCHYS TACOS #12 DALLAS TX")
		if !created {
			t.Error("first sighting creates the merchant")
		}
		b, created, _ = s.Resolve(ctx, x, "TORCHYS TACOS #98 AUSTIN TX")
		if created {
			t.Error("a different store of the same chain is the same merchant")
		}
		c, _, _ = s.Resolve(ctx, x, "Amazon.com*2K4 AMZN.COM/BILL WA")
	})
	if a.ID != b.ID || a.ID == c.ID || a.Name != "Torchy's Tacos" {
		t.Fatalf("%+v %+v %+v", a, b, c)
	}
	if c.Ambiguous != true {
		t.Errorf("amazon sells across categories: %+v", c)
	}
	if a.Ambiguous {
		t.Error("a taco place is not ambiguous")
	}
}

func TestS18a_E1_DescriptorRenameHeals(t *testing.T) {
	s, db := setup(t)
	ctx := context.Background()
	var old merchants.Merchant
	tx(t, db, func(x *sql.Tx) { old, _, _ = s.Resolve(ctx, x, "SQ *BLUE BOTTLE COFFEE") })
	var drifted merchants.Merchant
	tx(t, db, func(x *sql.Tx) { drifted, _, _ = s.Resolve(ctx, x, "BLUE BOTTLE COFFEE SF CA") })
	if old.ID == drifted.ID {
		t.Skip("the cleaner already unified these")
	}
	props, err := s.ProposeMerges(ctx, 0.5)
	if err != nil || len(props) != 1 {
		t.Fatalf("the drift is proposed, not applied: %+v %v", props, err)
	}
	// the user says the new descriptor is the old merchant
	tx(t, db, func(x *sql.Tx) {
		if err := s.AddAlias(ctx, x, events.ActorUser, "blue bottle coffee sf", old.ID); err != nil {
			t.Fatal(err)
		}
	})
	var again merchants.Merchant
	tx(t, db, func(x *sql.Tx) { again, _, _ = s.Resolve(ctx, x, "BLUE BOTTLE COFFEE SF CA") })
	if again.ID != old.ID {
		t.Fatalf("future descriptors resolve to the original merchant, so its rules keep applying: %+v", again)
	}
}

func TestS18a_MergeAndSplit(t *testing.T) {
	s, db := setup(t)
	ctx := context.Background()
	var a, b merchants.Merchant
	tx(t, db, func(x *sql.Tx) {
		a, _, _ = s.Resolve(ctx, x, "Kwik Market N Little")
		b, _, _ = s.Resolve(ctx, x, "Kwik Mkt")
	})
	tx(t, db, func(x *sql.Tx) {
		if _, err := s.MergeInto(ctx, x, events.ActorUser, b.ID, a.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.MergeInto(ctx, x, events.ActorUser, a.ID, a.ID); err == nil {
			t.Error("self merge")
		}
	})
	got, err := s.Get(ctx, db, a.ID)
	if err != nil || len(got.Aliases) != 2 {
		t.Fatalf("both aliases now point here: %+v %v", got, err)
	}
	if _, err = s.Get(ctx, db, b.ID); err != merchants.ErrNotFound {
		t.Errorf("the merged merchant is gone: %v", err)
	}
	var split merchants.Merchant
	tx(t, db, func(x *sql.Tx) {
		var err error
		if split, err = s.Split(ctx, x, events.ActorUser, "kwik mkt"); err != nil {
			t.Fatal(err)
		}
	})
	if got, _ = s.Get(ctx, db, a.ID); len(got.Aliases) != 1 || split.ID == a.ID {
		t.Fatalf("an alias split off onto its own merchant: %+v", got)
	}
}
