// Package counterparties knows the people who pay the user back (S12b): one
// counterparty per person, however many platforms, spellings or handles they
// use. Aliases are fuzzy, merges and splits are undoable, and a policy says
// whether their deposits are always, never or maybe reimbursements. Payer
// strings are untrusted bank text: stored, compared, never logged.
package counterparties

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"

	"github.com/alec-jensen/sofar/internal/events"
)

// Event kinds (subject type "counterparty").
const (
	KindCreate = "counterparty.create"
	KindAlias  = "counterparty.alias"
	KindMerge  = "counterparty.merge"
	KindSplit  = "counterparty.split"
	KindUpdate = "counterparty.update"
	Subject    = "counterparty"
)

type (
	// CreateEvt records a new counterparty.
	CreateEvt struct {
		ID string `json:"id"`
	}
	// AliasEvt records an alias given to a counterparty (From is its previous owner, if it moved).
	AliasEvt struct {
		AliasID int64  `json:"alias_id"`
		Key     string `json:"key"`
		To      string `json:"to"`
		From    string `json:"from"`
	}
	// MergeEvt records that From was folded into To, with the aliases that moved.
	MergeEvt struct {
		From    string  `json:"from"`
		To      string  `json:"to"`
		Aliases []int64 `json:"aliases"`
	}
	// SplitEvt records an alias moved from one counterparty to a new one.
	SplitEvt struct {
		AliasID int64  `json:"alias_id"`
		From    string `json:"from"`
		New     string `json:"new"`
	}
	// UpdateEvt records a change of name, policy or auto-net.
	UpdateEvt struct {
		ID    string `json:"id"`
		Field string `json:"field"`
		From  string `json:"from"`
		To    string `json:"to"`
	}
)

var (
	// ErrNotFound means no such counterparty.
	ErrNotFound = errors.New("counterparties: not found")
	// ErrInvalid means a bad request.
	ErrInvalid = errors.New("counterparties: invalid request")
)

// Policies.
const (
	PolicyAsk                = "ask"
	PolicyAlwaysReimbursment = "always_reimbursement"
	PolicyNever              = "never"
)

// Counterparty is one person.
type Counterparty struct {
	ID      string
	Name    string
	Policy  string
	AutoNet bool
	Aliases []string
}

// Service manages counterparties.
type Service struct {
	DB  *sql.DB
	Reg *events.Registry
}

// New builds a Service and registers its event payloads.
func New(db *sql.DB, reg *events.Registry) *Service {
	reg.Register(KindCreate, 1, CreateEvt{})
	reg.Register(KindAlias, 1, AliasEvt{})
	reg.Register(KindMerge, 1, MergeEvt{})
	reg.Register(KindSplit, 1, SplitEvt{})
	reg.Register(KindUpdate, 1, UpdateEvt{})
	return &Service{DB: db, Reg: reg}
}

func (s *Service) emit(ctx context.Context, tx *sql.Tx, actor events.Actor, kind, subject string, payload any) error {
	_, err := events.Append(ctx, tx, s.Reg, events.New{Actor: actor, Kind: kind, SubjectType: Subject, SubjectID: subject, Payload: payload})
	return err
}

// Create adds a counterparty and optional first aliases (payer strings or handles).
func (s *Service) Create(ctx context.Context, tx *sql.Tx, actor events.Actor, name string, aliases ...string) (Counterparty, error) {
	if name == "" || len(name) > 120 {
		return Counterparty{}, fmt.Errorf("%w: a counterparty needs a name", ErrInvalid)
	}
	var id string
	if err := tx.QueryRowContext(ctx, `INSERT INTO counterparties (name) VALUES ($1) RETURNING id`, name).Scan(&id); err != nil {
		return Counterparty{}, err
	}
	if err := s.emit(ctx, tx, actor, KindCreate, id, CreateEvt{ID: id}); err != nil {
		return Counterparty{}, err
	}
	for _, a := range aliases {
		if _, err := s.AddAlias(ctx, tx, actor, id, a); err != nil {
			return Counterparty{}, err
		}
	}
	return s.Get(ctx, tx, id)
}

// Get returns one counterparty with its aliases.
func (s *Service) Get(ctx context.Context, q events.DBTX, id string) (Counterparty, error) {
	var c Counterparty
	err := q.QueryRowContext(ctx, `SELECT id, name, policy, auto_net FROM counterparties WHERE id::text=$1 AND deleted_at IS NULL AND merged_into IS NULL`, id).Scan(&c.ID, &c.Name, &c.Policy, &c.AutoNet)
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrNotFound
	}
	if err != nil {
		return c, err
	}
	rows, err := q.QueryContext(ctx, `SELECT alias_key FROM counterparty_aliases WHERE counterparty_id=$1 ORDER BY id`, id)
	if err != nil {
		return c, err
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		rows.Scan(&k)
		c.Aliases = append(c.Aliases, k)
	}
	return c, rows.Err()
}

// List returns every live counterparty, by name.
func (s *Service) List(ctx context.Context, q events.DBTX) ([]Counterparty, error) {
	rows, err := q.QueryContext(ctx, `SELECT id FROM counterparties WHERE deleted_at IS NULL AND merged_into IS NULL ORDER BY lower(name), id`)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	var out []Counterparty
	for _, id := range ids {
		c, err := s.Get(ctx, q, id)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// AddAlias teaches the system that a payer string is this person. If another
// counterparty already owns the alias it moves (the user is the authority).
func (s *Service) AddAlias(ctx context.Context, tx *sql.Tx, actor events.Actor, id, payer string) (int64, error) {
	n := ParseName(payer)
	if n.Empty() {
		return 0, fmt.Errorf("%w: nothing to recognize in that name", ErrInvalid)
	}
	if _, err := s.Get(ctx, tx, id); err != nil {
		return 0, err
	}
	key := n.Key()
	var aliasID int64
	var from sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT id, counterparty_id::text FROM counterparty_aliases WHERE alias_key=$1`, key).Scan(&aliasID, &from)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		err = tx.QueryRowContext(ctx, `INSERT INTO counterparty_aliases (alias_key, counterparty_id, platform) VALUES ($1,$2,$3) RETURNING id`, key, id, string(PlatformOf(payer))).Scan(&aliasID)
	case err == nil && from.String == id:
		return aliasID, nil
	case err == nil:
		_, err = tx.ExecContext(ctx, `UPDATE counterparty_aliases SET counterparty_id=$2 WHERE id=$1`, aliasID, id)
	}
	if err != nil {
		return 0, err
	}
	return aliasID, s.emit(ctx, tx, actor, KindAlias, id, AliasEvt{AliasID: aliasID, Key: key, To: id, From: from.String})
}

// Match is the outcome of resolving a payer string.
type Match struct {
	Counterparty *Counterparty
	Score        float64
	// Candidates lists every counterparty that fits equally well; more than one
	// means the name is ambiguous (a masked "JOHN S") and the user decides.
	Candidates []Counterparty
}

// Ambiguous reports whether several people fit and none was chosen.
func (m Match) Ambiguous() bool { return m.Counterparty == nil && len(m.Candidates) > 1 }

// Resolve finds the counterparty a payer string belongs to, if any. An exact
// alias wins outright; otherwise the best fuzzy fit wins unless two different
// people fit equally well.
func (s *Service) Resolve(ctx context.Context, q events.DBTX, payer string) (Match, error) {
	n := ParseName(payer)
	if n.Empty() {
		return Match{}, nil
	}
	rows, err := q.QueryContext(ctx, `SELECT a.alias_key, a.counterparty_id FROM counterparty_aliases a
		JOIN counterparties c ON c.id = a.counterparty_id AND c.deleted_at IS NULL AND c.merged_into IS NULL`)
	if err != nil {
		return Match{}, err
	}
	best := map[string]float64{}
	var top float64
	for rows.Next() {
		var key, cp string
		if err = rows.Scan(&key, &cp); err != nil {
			rows.Close()
			return Match{}, err
		}
		if sc := Similarity(n, ParseKey(key)); sc > 0 {
			if sc > best[cp] {
				best[cp] = sc
			}
			if sc > top {
				top = sc
			}
		}
	}
	rows.Close()
	var ids []string
	for id, sc := range best {
		if sc == top {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	var m Match
	m.Score = top
	for _, id := range ids {
		c, err := s.Get(ctx, q, id)
		if err != nil {
			return m, err
		}
		m.Candidates = append(m.Candidates, c)
	}
	if len(ids) == 1 {
		m.Counterparty = &m.Candidates[0]
	}
	return m, nil
}

// SetPolicy sets always, never or ask.
func (s *Service) SetPolicy(ctx context.Context, tx *sql.Tx, actor events.Actor, id, policy string) error {
	switch policy {
	case PolicyAsk, PolicyAlwaysReimbursment, PolicyNever:
	default:
		return fmt.Errorf("%w: policy", ErrInvalid)
	}
	return s.update(ctx, tx, actor, id, "policy", policy)
}

// SetAutoNet turns netting receivables immediately on or off for this person.
func (s *Service) SetAutoNet(ctx context.Context, tx *sql.Tx, actor events.Actor, id string, on bool) error {
	return s.update(ctx, tx, actor, id, "auto_net", fmt.Sprint(on))
}

// Rename changes the display name.
func (s *Service) Rename(ctx context.Context, tx *sql.Tx, actor events.Actor, id, name string) error {
	if name == "" || len(name) > 120 {
		return fmt.Errorf("%w: a counterparty needs a name", ErrInvalid)
	}
	return s.update(ctx, tx, actor, id, "name", name)
}

func (s *Service) update(ctx context.Context, tx *sql.Tx, actor events.Actor, id, field, to string) error {
	c, err := s.Get(ctx, tx, id)
	if err != nil {
		return err
	}
	from := map[string]string{"policy": c.Policy, "name": c.Name, "auto_net": fmt.Sprint(c.AutoNet)}[field]
	if err = s.write(ctx, tx, id, field, to); err != nil {
		return err
	}
	return s.emit(ctx, tx, actor, KindUpdate, id, UpdateEvt{ID: id, Field: field, From: from, To: to})
}

func (s *Service) write(ctx context.Context, tx *sql.Tx, id, field, v string) error {
	var err error
	switch field {
	case "policy":
		_, err = tx.ExecContext(ctx, `UPDATE counterparties SET policy=$2 WHERE id::text=$1`, id, v)
	case "name":
		_, err = tx.ExecContext(ctx, `UPDATE counterparties SET name=$2 WHERE id::text=$1`, id, v)
	case "auto_net":
		_, err = tx.ExecContext(ctx, `UPDATE counterparties SET auto_net=$2::boolean WHERE id::text=$1`, id, v)
	}
	return err
}

// Merge folds one counterparty into another (the same person on two
// platforms): the aliases move, and references to the old one follow it.
func (s *Service) Merge(ctx context.Context, tx *sql.Tx, actor events.Actor, from, to string) error {
	if from == to {
		return fmt.Errorf("%w: pick two different people", ErrInvalid)
	}
	if _, err := s.Get(ctx, tx, from); err != nil {
		return err
	}
	if _, err := s.Get(ctx, tx, to); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM counterparty_aliases WHERE counterparty_id::text=$1 ORDER BY id`, from)
	if err != nil {
		return err
	}
	var moved []int64
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		moved = append(moved, id)
	}
	rows.Close()
	if _, err = tx.ExecContext(ctx, `UPDATE counterparty_aliases SET counterparty_id=$2 WHERE counterparty_id::text=$1`, from, to); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE counterparties SET merged_into=$2 WHERE id::text=$1`, from, to); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE receivables SET counterparty_id=$2 WHERE counterparty_id::text=$1`, from, to); err != nil {
		return err
	}
	return s.emit(ctx, tx, actor, KindMerge, to, MergeEvt{From: from, To: to, Aliases: moved})
}

// Split moves one alias out of a counterparty into a new one (two people who
// were merged by mistake).
func (s *Service) Split(ctx context.Context, tx *sql.Tx, actor events.Actor, id string, aliasKey, newName string) (Counterparty, error) {
	var aliasID int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM counterparty_aliases WHERE counterparty_id::text=$1 AND alias_key=$2`, id, aliasKey).Scan(&aliasID)
	if errors.Is(err, sql.ErrNoRows) {
		return Counterparty{}, ErrNotFound
	}
	if err != nil {
		return Counterparty{}, err
	}
	if newName == "" {
		return Counterparty{}, fmt.Errorf("%w: name the new person", ErrInvalid)
	}
	var nid string
	if err = tx.QueryRowContext(ctx, `INSERT INTO counterparties (name) VALUES ($1) RETURNING id`, newName).Scan(&nid); err != nil {
		return Counterparty{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE counterparty_aliases SET counterparty_id=$2 WHERE id=$1`, aliasID, nid); err != nil {
		return Counterparty{}, err
	}
	if err = s.emit(ctx, tx, actor, KindSplit, nid, SplitEvt{AliasID: aliasID, From: id, New: nid}); err != nil {
		return Counterparty{}, err
	}
	return s.Get(ctx, tx, nid)
}

// Undo reverses any counterparty event.
func (s *Service) Undo(ctx context.Context, tx *sql.Tx, actor events.Actor, eventID int64) (events.Event, error) {
	return s.flip(ctx, tx, actor, eventID, false)
}

// Redo reverses an undo.
func (s *Service) Redo(ctx context.Context, tx *sql.Tx, actor events.Actor, undoID int64) (events.Event, error) {
	return s.flip(ctx, tx, actor, undoID, true)
}

func (s *Service) flip(ctx context.Context, tx *sql.Tx, actor events.Actor, id int64, redo bool) (events.Event, error) {
	target, err := events.Get(ctx, tx, id)
	if err != nil {
		return events.Event{}, err
	}
	orig := target
	var out events.Event
	if redo {
		if target.Kind != events.KindUndo || target.Reverses == nil {
			return events.Event{}, events.ErrNotUndone
		}
		if orig, err = events.Get(ctx, tx, *target.Reverses); err != nil {
			return events.Event{}, err
		}
		out, err = events.Redo(ctx, tx, actor, id, "")
	} else {
		out, err = events.Undo(ctx, tx, actor, id, "")
	}
	if err != nil {
		return out, err
	}
	p, err := s.Reg.Decode(orig)
	if err != nil {
		return out, err
	}
	apply := redo // redo re-applies the original change
	switch v := p.(type) {
	case *CreateEvt:
		if apply {
			_, err = tx.ExecContext(ctx, `UPDATE counterparties SET deleted_at=NULL WHERE id::text=$1`, v.ID)
		} else {
			_, err = tx.ExecContext(ctx, `UPDATE counterparties SET deleted_at=now() WHERE id::text=$1`, v.ID)
		}
	case *AliasEvt:
		switch {
		case apply && v.From == "":
			_, err = tx.ExecContext(ctx, `INSERT INTO counterparty_aliases (id, alias_key, counterparty_id) VALUES ($1,$2,$3) ON CONFLICT (id) DO UPDATE SET counterparty_id=EXCLUDED.counterparty_id`, v.AliasID, v.Key, v.To)
		case apply:
			_, err = tx.ExecContext(ctx, `UPDATE counterparty_aliases SET counterparty_id=$2 WHERE id=$1`, v.AliasID, v.To)
		case v.From == "":
			_, err = tx.ExecContext(ctx, `DELETE FROM counterparty_aliases WHERE id=$1`, v.AliasID)
		default:
			_, err = tx.ExecContext(ctx, `UPDATE counterparty_aliases SET counterparty_id=$2 WHERE id=$1`, v.AliasID, v.From)
		}
	case *MergeEvt:
		if apply {
			_, err = tx.ExecContext(ctx, `UPDATE counterparty_aliases SET counterparty_id=$2 WHERE id = ANY($1::bigint[])`, pgInts(v.Aliases), v.To)
			if err == nil {
				_, err = tx.ExecContext(ctx, `UPDATE counterparties SET merged_into=$2 WHERE id::text=$1`, v.From, v.To)
			}
		} else {
			_, err = tx.ExecContext(ctx, `UPDATE counterparties SET merged_into=NULL WHERE id::text=$1`, v.From)
			if err == nil {
				_, err = tx.ExecContext(ctx, `UPDATE counterparty_aliases SET counterparty_id=$2 WHERE id = ANY($1::bigint[])`, pgInts(v.Aliases), v.From)
			}
		}
	case *SplitEvt:
		if apply {
			_, err = tx.ExecContext(ctx, `UPDATE counterparties SET deleted_at=NULL WHERE id::text=$1`, v.New)
			if err == nil {
				_, err = tx.ExecContext(ctx, `UPDATE counterparty_aliases SET counterparty_id=$2 WHERE id=$1`, v.AliasID, v.New)
			}
		} else {
			_, err = tx.ExecContext(ctx, `UPDATE counterparty_aliases SET counterparty_id=$2 WHERE id=$1`, v.AliasID, v.From)
			if err == nil {
				_, err = tx.ExecContext(ctx, `UPDATE counterparties SET deleted_at=now() WHERE id::text=$1`, v.New)
			}
		}
	case *UpdateEvt:
		to := v.From
		if apply {
			to = v.To
		}
		err = s.write(ctx, tx, v.ID, v.Field, to)
	}
	return out, err
}

func pgInts(v []int64) string {
	s := "{"
	for i, x := range v {
		if i > 0 {
			s += ","
		}
		s += fmt.Sprint(x)
	}
	return s + "}"
}
