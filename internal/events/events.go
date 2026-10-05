// Package events is the append-only record of user and system decisions
// (architecture A2). Undo is a new event that reverses an earlier one; redo
// reverses the undo. Nothing is ever updated or deleted. spec: A6, S0 principle 3.
package events

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"
)

// Actor says who caused an event.
type Actor string

const (
	ActorUser         Actor = "user"
	ActorSystem       Actor = "system"
	ActorLegacyImport Actor = "legacy-import"
)

// KindUndo is the control event that reverses another event.
const KindUndo = "undo"

// Event is one stored decision.
type Event struct {
	ID             int64
	At             time.Time
	Actor          Actor
	Kind           string
	SubjectType    string
	SubjectID      string
	Payload        json.RawMessage
	Reverses       *int64
	IdempotencyKey string
	// Replayed is set when Append found an existing event with the same
	// idempotency key and returned it instead of writing a new one.
	Replayed bool
}

// New describes an event to append. Payload may be a registered struct, a
// json.RawMessage, or nil.
type New struct {
	Actor          Actor
	Kind           string
	SubjectType    string
	SubjectID      string
	Payload        any
	IdempotencyKey string
	At             time.Time // zero means now
}

// DBTX is satisfied by *sql.DB and *sql.Tx.
type DBTX interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

var (
	// ErrUnknownKind means the kind is not in the registry.
	ErrUnknownKind = errors.New("events: unknown event kind")
	// ErrNotFound means no such event.
	ErrNotFound = errors.New("events: no such event")
	// ErrAlreadyUndone means the event is already reversed; redo it instead.
	ErrAlreadyUndone = errors.New("events: already undone")
	// ErrNotUndone means redo was asked for something that is not an undo in force.
	ErrNotUndone = errors.New("events: not an undo in force")
)

const eventColumns = `id, at, actor, kind, subject_type, subject_id, payload, reverses, coalesce(idempotency_key, '')`

func scan(row interface{ Scan(...any) error }) (Event, error) {
	var e Event
	var actor string
	var payload []byte
	err := row.Scan(&e.ID, &e.At, &actor, &e.Kind, &e.SubjectType, &e.SubjectID, &payload, &e.Reverses, &e.IdempotencyKey)
	e.Actor, e.Payload = Actor(actor), payload
	return e, err
}

// Append writes an event. A repeated idempotency key returns the original
// event (with Replayed set), not an error (A6).
func Append(ctx context.Context, q DBTX, reg *Registry, n New) (Event, error) {
	if n.Kind != KindUndo && (reg == nil || !reg.Known(n.Kind)) {
		return Event{}, fmt.Errorf("%w: %q", ErrUnknownKind, n.Kind)
	}
	payload, err := reg.Encode(n.Kind, n.Payload)
	if err != nil {
		return Event{}, err
	}
	return insert(ctx, q, n, payload, nil)
}

func insert(ctx context.Context, q DBTX, n New, payload []byte, reverses *int64) (Event, error) {
	at := n.At
	if at.IsZero() {
		at = time.Now()
	}
	var key any
	if n.IdempotencyKey != "" {
		key = n.IdempotencyKey
	}
	row := q.QueryRowContext(ctx, `
		INSERT INTO events (at, actor, kind, subject_type, subject_id, payload, reverses, idempotency_key)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (idempotency_key) DO NOTHING
		RETURNING `+eventColumns, at, string(n.Actor), n.Kind, n.SubjectType, n.SubjectID, payload, reverses, key)
	e, err := scan(row)
	if errors.Is(err, sql.ErrNoRows) && n.IdempotencyKey != "" {
		e, err = scan(q.QueryRowContext(ctx, `SELECT `+eventColumns+` FROM events WHERE idempotency_key=$1`, n.IdempotencyKey))
		e.Replayed = true
	}
	return e, err
}

// Get loads one event.
func Get(ctx context.Context, q DBTX, id int64) (Event, error) {
	e, err := scan(q.QueryRowContext(ctx, `SELECT `+eventColumns+` FROM events WHERE id=$1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Event{}, ErrNotFound
	}
	return e, err
}

// reversal is one edge of the undo graph.
type reversal struct{ id, target int64 }

func reversals(ctx context.Context, q DBTX) ([]reversal, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, reverses FROM events WHERE reverses IS NOT NULL ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []reversal
	for rows.Next() {
		var r reversal
		if err = rows.Scan(&r.id, &r.target); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// reversedSet returns the ids that are currently reversed: the targets of
// undo events that are themselves in force. A reverser always has a higher id
// than its target, so evaluating from the highest id down is enough.
func reversedSet(rs []reversal) (reversed map[int64]bool, inForceUndo map[int64]bool) {
	reversed = map[int64]bool{}
	inForceUndo = map[int64]bool{}
	sort.Slice(rs, func(i, j int) bool { return rs[i].id > rs[j].id })
	for _, r := range rs {
		if !reversed[r.id] {
			inForceUndo[r.id] = true
			reversed[r.target] = true
		}
	}
	return reversed, inForceUndo
}

// Undo appends an event that reverses eventID. It refuses an event that is
// already undone (use Redo on that undo instead).
func Undo(ctx context.Context, q DBTX, actor Actor, eventID int64, key string) (Event, error) {
	target, err := Get(ctx, q, eventID)
	if err != nil {
		return Event{}, err
	}
	rs, err := reversals(ctx, q)
	if err != nil {
		return Event{}, err
	}
	reversed, _ := reversedSet(rs)
	if reversed[eventID] {
		return Event{}, ErrAlreadyUndone
	}
	id := eventID
	return insert(ctx, q, New{Actor: actor, Kind: KindUndo, SubjectType: target.SubjectType, SubjectID: target.SubjectID, IdempotencyKey: key}, []byte(`{}`), &id)
}

// Redo reverses an undo that is in force, putting the original event back.
func Redo(ctx context.Context, q DBTX, actor Actor, undoID int64, key string) (Event, error) {
	target, err := Get(ctx, q, undoID)
	if err != nil {
		return Event{}, err
	}
	if target.Kind != KindUndo {
		return Event{}, ErrNotUndone
	}
	rs, err := reversals(ctx, q)
	if err != nil {
		return Event{}, err
	}
	reversed, _ := reversedSet(rs)
	if reversed[undoID] {
		return Event{}, ErrNotUndone
	}
	id := undoID
	return insert(ctx, q, New{Actor: actor, Kind: KindUndo, SubjectType: target.SubjectType, SubjectID: target.SubjectID, IdempotencyKey: key}, []byte(`{}`), &id)
}

// Filter narrows Effective.
type Filter struct {
	SubjectType string
	SubjectID   string
	Kinds       []string
	Since       time.Time // inclusive, zero means no lower bound
	Until       time.Time // exclusive, zero means no upper bound
}

// Effective returns the events still in force (not reversed), excluding the
// undo events themselves, ordered by (at, id).
func Effective(ctx context.Context, q DBTX, f Filter) ([]Event, error) {
	rs, err := reversals(ctx, q)
	if err != nil {
		return nil, err
	}
	reversed, _ := reversedSet(rs)
	dead := make([]int64, 0, len(reversed))
	for id := range reversed {
		dead = append(dead, id)
	}
	sort.Slice(dead, func(i, j int) bool { return dead[i] < dead[j] })

	query := `SELECT ` + eventColumns + ` FROM events WHERE kind <> 'undo' AND NOT (id = ANY($1::bigint[]))`
	args := []any{int64Array(dead)}
	add := func(cond string, v any) {
		args = append(args, v)
		query += fmt.Sprintf(" AND "+cond, len(args))
	}
	if f.SubjectType != "" {
		add("subject_type = $%d", f.SubjectType)
	}
	if f.SubjectID != "" {
		add("subject_id = $%d", f.SubjectID)
	}
	if len(f.Kinds) > 0 {
		add("kind = ANY($%d::text[])", stringArray(f.Kinds))
	}
	if !f.Since.IsZero() {
		add("at >= $%d", f.Since)
	}
	if !f.Until.IsZero() {
		add("at < $%d", f.Until)
	}
	query += ` ORDER BY at, id`
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		e, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// IsInForce reports whether an event is currently in force.
func IsInForce(ctx context.Context, q DBTX, id int64) (bool, error) {
	if _, err := Get(ctx, q, id); err != nil {
		return false, err
	}
	rs, err := reversals(ctx, q)
	if err != nil {
		return false, err
	}
	reversed, _ := reversedSet(rs)
	return !reversed[id], nil
}
