//go:build integration

package rules_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/alec-jensen/sofar/internal/rules"
	"github.com/alec-jensen/sofar/internal/testkit"
)

func sp(s string) *string { return &s }

func newRule(cat, merchant string) rules.Rule {
	return rules.Rule{Name: merchant, Layer: rules.LayerUser, Conditions: rules.Condition{Field: "merchant", Op: "contains", Value: merchant}, Actions: rules.Actions{Category: sp(cat)}, Enabled: true}
}

func run(t *testing.T, db *sql.DB, fn func(tx *sql.Tx)) {
	t.Helper()
	tx, _ := db.Begin()
	defer tx.Rollback()
	fn(tx)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestS18a_RuleVersionRollback(t *testing.T) {
	db := testkit.DB(t)
	st := &rules.Store{DB: db}
	ctx := context.Background()
	var r rules.Rule
	run(t, db, func(tx *sql.Tx) {
		var err error
		if r, err = st.Create(ctx, tx, newRule("groceries", "heb"), ""); err != nil {
			t.Fatal(err)
		}
		if r, err = st.Update(ctx, tx, r.ID, newRule("dining_out", "heb")); err != nil {
			t.Fatal(err)
		}
	})
	vs, _ := st.Versions(ctx, db, r.ID)
	if len(vs) != 2 {
		t.Fatalf("versions: %v", vs)
	}
	run(t, db, func(tx *sql.Tx) {
		if _, err := st.Rollback(ctx, tx, r.ID, 1); err != nil {
			t.Fatal(err)
		}
		if _, err := st.Rollback(ctx, tx, r.ID, 99); err != rules.ErrNotFound {
			t.Errorf("missing version: %v", err)
		}
	})
	list, _ := st.List(ctx, db)
	if len(list) != 1 || *list[0].Actions.Category != "groceries" {
		t.Fatalf("rolled back: %+v", list)
	}
	if vs, _ = st.Versions(ctx, db, r.ID); len(vs) != 3 {
		t.Fatalf("rollback is itself a version, history is kept: %v", vs)
	}
}

func TestS18a_InvalidRulesAreRefused(t *testing.T) {
	db := testkit.DB(t)
	st := &rules.Store{DB: db}
	tx, _ := db.Begin()
	defer tx.Rollback()
	bad := newRule("groceries", "heb")
	bad.Conditions = rules.Condition{Field: "description", Op: "regex", Value: "(?<=a)b"}
	if _, err := st.Create(context.Background(), tx, bad, ""); err == nil {
		t.Fatal("a non-RE2 regex is refused at the door")
	}
}

func TestS18a_ImportExportAndPacksNeverOverwriteUserRules(t *testing.T) {
	db := testkit.DB(t)
	st := &rules.Store{DB: db}
	ctx := context.Background()
	run(t, db, func(tx *sql.Tx) {
		st.Create(ctx, tx, newRule("groceries", "heb"), "")
		st.Create(ctx, tx, newRule("fuel", "shell"), "")
	})
	data, err := st.Export(ctx, db, "")
	if err != nil {
		t.Fatal(err)
	}
	// importing the user's own export into the same database adds nothing
	run(t, db, func(tx *sql.Tx) {
		added, skipped, err := st.Import(ctx, tx, data)
		if err != nil || added != 0 || skipped != 2 {
			t.Fatalf("duplicates are skipped: %d %d %v", added, skipped, err)
		}
	})
	// a pack: seed rules named "texas", one of which conflicts with a user rule
	packV1 := []byte(`{"version":1,"pack":"texas","rules":[
		{"name":"heb","layer":"user","stage":"default","conditions":{"field":"merchant","op":"contains","value":"heb"},"actions":{"category":"dining_out"},"priority":100,"enabled":true},
		{"name":"whataburger","layer":"user","stage":"default","conditions":{"field":"merchant","op":"contains","value":"whataburger"},"actions":{"category":"dining_out"},"priority":100,"enabled":true}]}`)
	run(t, db, func(tx *sql.Tx) {
		if added, _, err := st.Import(ctx, tx, packV1); err != nil || added != 2 {
			t.Fatalf("pack import: %d %v", added, err)
		}
	})
	list, _ := st.List(ctx, db)
	var userHeb, seedHeb bool
	for _, r := range list {
		if r.Name == "heb" && r.Layer == rules.LayerUser && *r.Actions.Category == "groceries" {
			userHeb = true
		}
		if r.Name == "heb" && r.Layer == rules.LayerSeed {
			seedHeb = true
		}
	}
	if !userHeb || !seedHeb {
		t.Fatalf("the user's rule is untouched and the pack's rule sits in the seed layer: %+v", list)
	}
	// re-importing a newer pack replaces only that pack's rules
	packV2 := []byte(`{"version":1,"pack":"texas","rules":[
		{"name":"whataburger","layer":"user","stage":"default","conditions":{"field":"merchant","op":"contains","value":"whataburger"},"actions":{"category":"dining_out"},"priority":100,"enabled":true}]}`)
	run(t, db, func(tx *sql.Tx) {
		if _, _, err := st.Import(ctx, tx, packV2); err != nil {
			t.Fatal(err)
		}
	})
	list, _ = st.List(ctx, db)
	if len(list) != 3 {
		t.Fatalf("2 user rules + the one pack rule: %d", len(list))
	}
	var bad = []byte(`{"version":1,"rules":[{"name":"x","layer":"user","stage":"default","conditions":{"field":"nope","op":"is","value":"a"},"actions":{"category":"a"}}]}`)
	tx, _ := db.Begin()
	defer tx.Rollback()
	if _, _, err := st.Import(ctx, tx, bad); err == nil {
		t.Error("an invalid rule in a file is refused")
	}
}

func TestS18a_ArchiveKeepsHistoryAndToggleWorks(t *testing.T) {
	db := testkit.DB(t)
	st := &rules.Store{DB: db}
	ctx := context.Background()
	var r rules.Rule
	run(t, db, func(tx *sql.Tx) {
		r, _ = st.Create(ctx, tx, newRule("groceries", "heb"), "")
		if err := st.SetEnabled(ctx, tx, r.ID, false); err != nil {
			t.Fatal(err)
		}
	})
	if list, _ := st.List(ctx, db); len(list) != 1 || list[0].Enabled {
		t.Fatalf("disabled but still listed: %+v", list)
	}
	run(t, db, func(tx *sql.Tx) {
		if err := st.Archive(ctx, tx, r.ID); err != nil {
			t.Fatal(err)
		}
	})
	if list, _ := st.List(ctx, db); len(list) != 0 {
		t.Fatal("archived rules are not listed")
	}
	var n int
	db.QueryRow(`SELECT count(*) FROM rule_versions WHERE rule_id::text=$1`, r.ID).Scan(&n)
	if n != 1 {
		t.Fatal("history stays")
	}
}
