package rules

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/alec-jensen/sofar/internal/civil"
	"github.com/alec-jensen/sofar/internal/events"
)

var (
	// ErrNotFound means no such rule or version.
	ErrNotFound = errors.New("rules: not found")
	// ErrInvalid means the rule failed validation.
	ErrInvalid = errors.New("rules: invalid rule")
)

// Store persists rules with versions.
type Store struct{ DB *sql.DB }

type stored struct {
	Name       string    `json:"name"`
	Layer      Layer     `json:"layer"`
	Stage      Stage     `json:"stage"`
	Conditions Condition `json:"conditions"`
	Actions    Actions   `json:"actions"`
	Priority   int       `json:"priority"`
	Enabled    bool      `json:"enabled"`
	Provenance string    `json:"provenance"`
	ValidFrom  *string   `json:"valid_from,omitempty"`
	ValidTo    *string   `json:"valid_to,omitempty"`
	Pack       string    `json:"pack,omitempty"`
}

func toStored(r Rule, pack string) stored {
	s := stored{Name: r.Name, Layer: r.Layer, Stage: r.Stage, Conditions: r.Conditions, Actions: r.Actions, Priority: r.Priority, Enabled: r.Enabled, Provenance: r.Provenance, Pack: pack}
	if r.ValidFrom != nil {
		v := r.ValidFrom.String()
		s.ValidFrom = &v
	}
	if r.ValidTo != nil {
		v := r.ValidTo.String()
		s.ValidTo = &v
	}
	return s
}

func (s stored) rule(id string, updated time.Time) (Rule, error) {
	r := Rule{ID: id, Name: s.Name, Layer: s.Layer, Stage: s.Stage, Conditions: s.Conditions, Actions: s.Actions, Priority: s.Priority, Enabled: s.Enabled, Provenance: s.Provenance, UpdatedAt: updated}
	for _, p := range []struct {
		in  *string
		out **civil.Date
	}{{s.ValidFrom, &r.ValidFrom}, {s.ValidTo, &r.ValidTo}} {
		if p.in != nil {
			d, err := civil.ParseDate(*p.in)
			if err != nil {
				return r, err
			}
			*p.out = &d
		}
	}
	return r, nil
}

// WithDefaults fills the layer, stage, priority and provenance a caller left empty.
func (r Rule) WithDefaults() Rule {
	fill(&r)
	return r
}

func fill(r *Rule) {
	if r.Stage == "" {
		r.Stage = StageDefault
	}
	if r.Layer == "" {
		r.Layer = LayerUser
	}
	if r.Priority == 0 {
		r.Priority = 100
	}
	if r.Provenance == "" {
		r.Provenance = string(r.Layer)
	}
}

func dateArg(d *civil.Date) any {
	if d == nil {
		return nil
	}
	return d.String()
}

// Create validates and stores a new rule at version 1. pack names a shared
// pack the rule belongs to, or is empty.
func (s *Store) Create(ctx context.Context, tx *sql.Tx, r Rule, pack string) (Rule, error) {
	fill(&r)
	if err := r.Validate(); err != nil {
		return Rule{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	cond, _ := json.Marshal(r.Conditions)
	act, _ := json.Marshal(r.Actions)
	var id string
	var updated time.Time
	err := tx.QueryRowContext(ctx, `
		INSERT INTO rules (name, layer, stage, conditions, actions, priority, enabled, provenance, valid_from, valid_to, pack)
		VALUES ($1,$2,$3,$4,$5,$6,true,$7,$8,$9,nullif($10,'')) RETURNING id, updated_at`,
		r.Name, string(r.Layer), string(r.Stage), cond, act, r.Priority, r.Provenance, dateArg(r.ValidFrom), dateArg(r.ValidTo), pack).Scan(&id, &updated)
	if err != nil {
		return Rule{}, err
	}
	r.ID, r.Enabled, r.UpdatedAt = id, true, updated
	snap, _ := json.Marshal(toStored(r, pack))
	_, err = tx.ExecContext(ctx, `INSERT INTO rule_versions (rule_id, version, snapshot) VALUES ($1,1,$2)`, id, snap)
	return r, err
}

// Update replaces a rule's content as a new version. Earlier versions stay
// available for rollback.
func (s *Store) Update(ctx context.Context, tx *sql.Tx, id string, r Rule) (Rule, error) {
	fill(&r)
	if err := r.Validate(); err != nil {
		return Rule{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	var version int
	var pack sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT version, pack FROM rules WHERE id::text=$1 AND archived_at IS NULL FOR UPDATE`, id).Scan(&version, &pack); err != nil {
		return Rule{}, ErrNotFound
	}
	r.ID = id
	return s.write(ctx, tx, r, version+1, pack.String)
}

func (s *Store) write(ctx context.Context, tx *sql.Tx, r Rule, version int, pack string) (Rule, error) {
	cond, _ := json.Marshal(r.Conditions)
	act, _ := json.Marshal(r.Actions)
	var updated time.Time
	err := tx.QueryRowContext(ctx, `
		UPDATE rules SET name=$2, layer=$3, stage=$4, conditions=$5, actions=$6, priority=$7, enabled=$8, provenance=$9, valid_from=$10, valid_to=$11, version=$12, updated_at=now()
		WHERE id::text=$1 RETURNING updated_at`,
		r.ID, r.Name, string(r.Layer), string(r.Stage), cond, act, r.Priority, r.Enabled, r.Provenance, dateArg(r.ValidFrom), dateArg(r.ValidTo), version).Scan(&updated)
	if err != nil {
		return Rule{}, err
	}
	r.UpdatedAt = updated
	snap, _ := json.Marshal(toStored(r, pack))
	_, err = tx.ExecContext(ctx, `INSERT INTO rule_versions (rule_id, version, snapshot) VALUES ($1,$2,$3)`, r.ID, version, snap)
	return r, err
}

// Rollback restores an earlier version as a new, latest version.
func (s *Store) Rollback(ctx context.Context, tx *sql.Tx, id string, version int) (Rule, error) {
	var snap []byte
	var current int
	var pack sql.NullString
	err := tx.QueryRowContext(ctx, `
		SELECT v.snapshot, r.version, r.pack FROM rule_versions v JOIN rules r ON r.id = v.rule_id
		WHERE r.id::text=$1 AND v.version=$2 FOR UPDATE OF r`, id, version).Scan(&snap, &current, &pack)
	if err != nil {
		return Rule{}, ErrNotFound
	}
	var st stored
	if err = json.Unmarshal(snap, &st); err != nil {
		return Rule{}, err
	}
	r, err := st.rule(id, time.Time{})
	if err != nil {
		return Rule{}, err
	}
	return s.write(ctx, tx, r, current+1, pack.String)
}

// Archive retires a rule without deleting it.
func (s *Store) Archive(ctx context.Context, tx *sql.Tx, id string) error {
	res, err := tx.ExecContext(ctx, `UPDATE rules SET archived_at=now(), enabled=false WHERE id::text=$1 AND archived_at IS NULL`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetEnabled turns a rule on or off.
func (s *Store) SetEnabled(ctx context.Context, tx *sql.Tx, id string, on bool) error {
	res, err := tx.ExecContext(ctx, `UPDATE rules SET enabled=$2, updated_at=now() WHERE id::text=$1 AND archived_at IS NULL`, id, on)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// List returns the live rules (enabled or not), newest first.
func (s *Store) List(ctx context.Context, q events.DBTX) ([]Rule, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, name, layer, stage, conditions, actions, priority, enabled, provenance, valid_from, valid_to, updated_at FROM rules WHERE archived_at IS NULL ORDER BY updated_at DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Rule
	for rows.Next() {
		var r Rule
		var layer, stage string
		var cond, act []byte
		var from, to sql.NullTime
		if err = rows.Scan(&r.ID, &r.Name, &layer, &stage, &cond, &act, &r.Priority, &r.Enabled, &r.Provenance, &from, &to, &r.UpdatedAt); err != nil {
			return nil, err
		}
		r.Layer, r.Stage = Layer(layer), Stage(stage)
		if err = json.Unmarshal(cond, &r.Conditions); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(act, &r.Actions); err != nil {
			return nil, err
		}
		if from.Valid {
			d := civil.DateOf(from.Time)
			r.ValidFrom = &d
		}
		if to.Valid {
			d := civil.DateOf(to.Time)
			r.ValidTo = &d
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Versions lists a rule's version numbers, newest first.
func (s *Store) Versions(ctx context.Context, q events.DBTX, id string) ([]int, error) {
	rows, err := q.QueryContext(ctx, `SELECT version FROM rule_versions WHERE rule_id::text=$1 ORDER BY version DESC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int
	for rows.Next() {
		var v int
		rows.Scan(&v)
		out = append(out, v)
	}
	return out, rows.Err()
}

// Exported is the JSON form of rules for backup, sharing and packs.
type Exported struct {
	Version int      `json:"version"`
	Pack    string   `json:"pack,omitempty"`
	Rules   []stored `json:"rules"`
}

// Export serializes live rules of the given layers (all when none are given).
func (s *Store) Export(ctx context.Context, q events.DBTX, pack string, layers ...Layer) ([]byte, error) {
	rs, err := s.List(ctx, q)
	if err != nil {
		return nil, err
	}
	out := Exported{Version: 1, Pack: pack}
	for _, r := range rs {
		keep := len(layers) == 0
		for _, l := range layers {
			keep = keep || r.Layer == l
		}
		if keep {
			out.Rules = append(out.Rules, toStored(r, pack))
		}
	}
	return json.MarshalIndent(out, "", "  ")
}

func fingerprint(st stored) string {
	st.Name, st.Pack, st.Provenance = "", "", ""
	raw, _ := json.Marshal(st)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:8])
}

// Import adds rules from an export. It never overwrites or removes the user's
// own rules: a rule identical to one already present is skipped, everything
// else is added with provenance "import". When the file names a pack, rules
// from the same earlier pack are replaced (and only those), and they join the
// seed layer so a shared pack never claims to be the user's own.
func (s *Store) Import(ctx context.Context, tx *sql.Tx, data []byte) (added, skipped int, err error) {
	var in Exported
	if err = json.Unmarshal(data, &in); err != nil {
		return 0, 0, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if len(in.Rules) > 5000 {
		return 0, 0, fmt.Errorf("%w: too many rules", ErrInvalid)
	}
	if in.Pack != "" {
		if _, err = tx.ExecContext(ctx, `UPDATE rules SET archived_at=now(), enabled=false WHERE pack=$1 AND archived_at IS NULL`, in.Pack); err != nil {
			return 0, 0, err
		}
	}
	have := map[string]bool{}
	existing, err := s.List(ctx, tx)
	if err != nil {
		return 0, 0, err
	}
	for _, r := range existing {
		have[fingerprint(toStored(r, ""))] = true
	}
	for _, st := range in.Rules {
		r, rerr := st.rule("", time.Time{})
		if rerr != nil {
			return added, skipped, fmt.Errorf("%w: %v", ErrInvalid, rerr)
		}
		if in.Pack != "" {
			r.Layer = LayerSeed
		}
		r.Provenance = "import"
		if have[fingerprint(toStored(r, ""))] {
			skipped++
			continue
		}
		if _, err = s.Create(ctx, tx, r, in.Pack); err != nil {
			return added, skipped, err
		}
		added++
	}
	return added, skipped, nil
}
