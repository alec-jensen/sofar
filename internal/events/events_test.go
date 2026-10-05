//go:build integration

package events_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"testing"
	"time"

	"pgregory.net/rapid"

	"github.com/alec-jensen/sofar/internal/events"
	"github.com/alec-jensen/sofar/internal/testkit"
)

type noteSet struct {
	Note string `json:"note"`
}

func registry() *events.Registry {
	r := events.NewRegistry()
	r.Register("overlay.set_note", 1, noteSet{})
	return r
}

type fataler interface {
	Helper()
	Fatal(args ...any)
}

func appendNote(t fataler, q events.DBTX, reg *events.Registry, n int) events.Event {
	t.Helper()
	e, err := events.Append(context.Background(), q, reg, events.New{Actor: events.ActorUser, Kind: "overlay.set_note", SubjectType: "txn", SubjectID: "t1", Payload: noteSet{Note: fmt.Sprint(n)}})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestEvents_IdempotencyKeyReturnsOriginal(t *testing.T) {
	db := testkit.DB(t)
	reg := registry()
	ctx := context.Background()
	n := events.New{Actor: events.ActorUser, Kind: "overlay.set_note", SubjectType: "txn", SubjectID: "t1", Payload: noteSet{"a"}, IdempotencyKey: "k1"}
	first, err := events.Append(ctx, db, reg, n)
	if err != nil || first.Replayed {
		t.Fatalf("first append: %v replayed=%v", err, first.Replayed)
	}
	n.Payload = noteSet{"different"}
	again, err := events.Append(ctx, db, reg, n)
	if err != nil || !again.Replayed || again.ID != first.ID || string(again.Payload) != string(first.Payload) {
		t.Fatalf("duplicate key should return the original: %+v %v", again, err)
	}
	var count int
	db.QueryRow(`SELECT count(*) FROM events`).Scan(&count)
	if count != 1 {
		t.Fatalf("want 1 stored event, got %d", count)
	}
	// events without a key are never deduplicated
	appendNote(t, db, reg, 1)
	appendNote(t, db, reg, 1)
	db.QueryRow(`SELECT count(*) FROM events`).Scan(&count)
	if count != 3 {
		t.Fatalf("want 3 stored events, got %d", count)
	}
}

func TestEvents_UnknownKindRefused(t *testing.T) {
	db := testkit.DB(t)
	_, err := events.Append(context.Background(), db, registry(), events.New{Actor: events.ActorSystem, Kind: "nope", Payload: noteSet{}})
	if !errors.Is(err, events.ErrUnknownKind) {
		t.Fatalf("want ErrUnknownKind, got %v", err)
	}
}

func TestEvents_PayloadVersioningKeepsOldEventsDecoding(t *testing.T) {
	type v1 struct {
		Note string `json:"note"`
	}
	type v2 struct {
		Text string   `json:"text"`
		Tags []string `json:"tags"`
	}
	db := testkit.DB(t)
	ctx := context.Background()
	old := events.NewRegistry()
	old.Register("overlay.set_note", 1, v1{})
	e1, err := events.Append(ctx, db, old, events.New{Actor: events.ActorUser, Kind: "overlay.set_note", Payload: v1{Note: "hi"}})
	if err != nil {
		t.Fatal(err)
	}
	now := events.NewRegistry()
	now.Register("overlay.set_note", 1, v1{})
	now.Register("overlay.set_note", 2, v2{})
	e2, err := events.Append(ctx, db, now, events.New{Actor: events.ActorUser, Kind: "overlay.set_note", Payload: v2{Text: "yo", Tags: []string{"a"}}})
	if err != nil {
		t.Fatal(err)
	}
	got1, err := now.Decode(e1)
	if err != nil || got1.(*v1).Note != "hi" {
		t.Fatalf("v1 event: %v %v", got1, err)
	}
	got2, err := now.Decode(e2)
	if err != nil || got2.(*v2).Text != "yo" {
		t.Fatalf("v2 event: %v %v", got2, err)
	}
	var head struct {
		V int `json:"_v"`
	}
	json.Unmarshal(e2.Payload, &head)
	if head.V != 2 {
		t.Fatalf("new events carry the latest version, got %d", head.V)
	}
}

func TestEvents_UndoRedoBasics(t *testing.T) {
	db := testkit.DB(t)
	reg := registry()
	ctx := context.Background()
	e := appendNote(t, db, reg, 1)
	u, err := events.Undo(ctx, db, events.ActorUser, e.ID, "")
	if err != nil || u.Reverses == nil || *u.Reverses != e.ID || u.SubjectID != "t1" {
		t.Fatalf("undo: %+v %v", u, err)
	}
	if _, err = events.Undo(ctx, db, events.ActorUser, e.ID, ""); !errors.Is(err, events.ErrAlreadyUndone) {
		t.Fatalf("double undo: %v", err)
	}
	if got, _ := events.Effective(ctx, db, events.Filter{}); len(got) != 0 {
		t.Fatalf("undone event must not be effective: %v", got)
	}
	if _, err = events.Redo(ctx, db, events.ActorUser, e.ID, ""); !errors.Is(err, events.ErrNotUndone) {
		t.Fatalf("redo of a non-undo: %v", err)
	}
	if _, err = events.Redo(ctx, db, events.ActorUser, u.ID, ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := events.Effective(ctx, db, events.Filter{}); len(got) != 1 || got[0].ID != e.ID {
		t.Fatalf("redo must bring it back: %v", got)
	}
	if _, err = events.Redo(ctx, db, events.ActorUser, u.ID, ""); !errors.Is(err, events.ErrNotUndone) {
		t.Fatalf("an undo that was already redone cannot be redone again: %v", err)
	}
	if _, err = events.Undo(ctx, db, events.ActorUser, e.ID, ""); err != nil {
		t.Fatalf("undo after redo: %v", err)
	}
	if _, err = events.Undo(ctx, db, events.ActorUser, 999999, ""); !errors.Is(err, events.ErrNotFound) {
		t.Fatalf("missing event: %v", err)
	}
}

func TestEvents_EffectiveFilters(t *testing.T) {
	db := testkit.DB(t)
	reg := registry()
	reg.Register("overlay.hide", 1, struct{}{})
	ctx := context.Background()
	base := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	for i, tc := range []struct{ kind, subject string }{{"overlay.set_note", "a"}, {"overlay.hide", "a"}, {"overlay.set_note", "b"}} {
		if _, err := events.Append(ctx, db, reg, events.New{Actor: events.ActorUser, Kind: tc.kind, SubjectType: "txn", SubjectID: tc.subject, At: base.Add(time.Duration(i) * time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	count := func(f events.Filter) int {
		got, err := events.Effective(ctx, db, f)
		if err != nil {
			t.Fatal(err)
		}
		return len(got)
	}
	if n := count(events.Filter{SubjectID: "a"}); n != 2 {
		t.Errorf("subject a: %d", n)
	}
	if n := count(events.Filter{Kinds: []string{"overlay.hide"}}); n != 1 {
		t.Errorf("kind hide: %d", n)
	}
	if n := count(events.Filter{Since: base.Add(time.Hour)}); n != 2 {
		t.Errorf("since: %d", n)
	}
	if n := count(events.Filter{Until: base.Add(time.Hour)}); n != 1 {
		t.Errorf("until: %d", n)
	}
	if n := count(events.Filter{}); n != 3 {
		t.Errorf("all: %d", n)
	}
}

type model struct {
	inForce map[int64]bool
	undoOf  map[int64]int64 // undo event id -> target, for undos still in force
}

func (m model) effective() []int64 {
	var out []int64
	for id, ok := range m.inForce {
		if ok {
			out = append(out, id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// TestA9_Inv4_UndoRedoRestoresState drives random sequences of appends, undos
// and redos against a simple model and checks the effective set after every step.
func TestA9_Inv4_UndoRedoRestoresState(t *testing.T) {
	d := testkit.DB(t)
	reg := registry()
	ctx := context.Background()
	rapid.Check(t, func(rt *rapid.T) {
		tx, err := d.BeginTx(ctx, nil)
		if err != nil {
			rt.Fatal(err)
		}
		defer tx.Rollback()
		m := model{inForce: map[int64]bool{}, undoOf: map[int64]int64{}}
		check := func() {
			got, err := events.Effective(ctx, tx, events.Filter{})
			if err != nil {
				rt.Fatal(err)
			}
			var ids []int64
			for _, e := range got {
				ids = append(ids, e.ID)
			}
			sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
			if fmt.Sprint(ids) != fmt.Sprint(m.effective()) {
				rt.Fatalf("effective = %v, want %v", ids, m.effective())
			}
		}
		steps := rapid.IntRange(1, 25).Draw(rt, "steps")
		for i := 0; i < steps; i++ {
			switch rapid.IntRange(0, 2).Draw(rt, "op") {
			case 0:
				e := appendNote(rt, tx, reg, i)
				m.inForce[e.ID] = true
			case 1: // undo a random in-force event
				ids := m.effective()
				if len(ids) == 0 {
					continue
				}
				id := rapid.SampledFrom(ids).Draw(rt, "undo")
				u, err := events.Undo(ctx, tx, events.ActorUser, id, "")
				if err != nil {
					rt.Fatal(err)
				}
				m.inForce[id] = false
				m.undoOf[u.ID] = id
			case 2: // redo a random undo in force
				var undos []int64
				for u := range m.undoOf {
					undos = append(undos, u)
				}
				if len(undos) == 0 {
					continue
				}
				sort.Slice(undos, func(i, j int) bool { return undos[i] < undos[j] })
				u := rapid.SampledFrom(undos).Draw(rt, "redo")
				if _, err := events.Redo(ctx, tx, events.ActorUser, u, ""); err != nil {
					rt.Fatal(err)
				}
				m.inForce[m.undoOf[u]] = true
				delete(m.undoOf, u)
			}
			check()
		}
	})
}
