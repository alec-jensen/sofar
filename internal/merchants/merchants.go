// Package merchants keeps merchant entities and their aliases. A bank
// descriptor is cleaned, looked up by alias, and attached to a merchant, so
// rules and learning survive descriptor drift. Merging and splitting are the
// user's decisions; clustering only proposes them. spec: S18a.
package merchants

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/alec-jensen/sofar/internal/events"
	"github.com/alec-jensen/sofar/internal/normalize"
)

// Event kinds (subject type "merchant").
const (
	KindMerge = "merchant.merge"
	KindAlias = "merchant.alias"
	Subject   = "merchant"
)

type (
	// Merge records one merchant folded into another; Aliases lets undo restore them.
	Merge struct {
		From    string   `json:"from"`
		To      string   `json:"to"`
		Aliases []string `json:"aliases"`
	}
	// Alias records an alias pointed at a merchant.
	Alias struct {
		Alias      string `json:"alias"`
		MerchantID string `json:"merchant_id"`
		Was        string `json:"was,omitempty"`
	}
)

var (
	// ErrNotFound means no such merchant.
	ErrNotFound = errors.New("merchants: not found")
	// ErrInvalid means a bad request.
	ErrInvalid = errors.New("merchants: invalid request")
)

// ambiguousSeed lists merchants that sell across categories: the amount, the
// account or the day decides, so one merchant rule is not enough (S18a).
var ambiguousSeed = map[string]bool{
	"amazon": true, "walmart": true, "target": true, "costco": true, "sam s club": true, "chevron": true, "shell": true, "exxon": true,
	"cvs": true, "walgreens": true, "seven eleven": true, "kroger": true, "paypal": true, "venmo": true, "apple": true, "google": true,
}

// SeedAmbiguous reports whether a merchant key is on the built-in list of merchants that sell across categories.
func SeedAmbiguous(key string) bool { return ambiguousSeed[key] }

// Merchant is a merchant entity.
type Merchant struct {
	ID        string
	Name      string
	Key       string
	Ambiguous bool
	Aliases   []string
}

// Service manages merchants.
type Service struct {
	DB  *sql.DB
	Reg *events.Registry
}

// New builds a Service and registers its event payloads.
func New(db *sql.DB, reg *events.Registry) *Service {
	reg.Register(KindMerge, 1, Merge{})
	reg.Register(KindAlias, 1, Alias{})
	return &Service{DB: db, Reg: reg}
}

// Resolve finds or creates the merchant for a bank description and records the
// cleaned key as an alias. created is true when a new merchant was made.
func (s *Service) Resolve(ctx context.Context, q interface {
	events.DBTX
}, description string) (m Merchant, created bool, err error) {
	c := normalize.Clean(description)
	if c.Key == "" {
		return Merchant{}, false, ErrInvalid
	}
	err = q.QueryRowContext(ctx, `SELECT m.id, m.name, m.canonical_key, m.ambiguous FROM merchant_aliases a JOIN merchants m ON m.id = a.merchant_id WHERE a.alias=$1`, c.Key).
		Scan(&m.ID, &m.Name, &m.Key, &m.Ambiguous)
	if err == nil {
		return m, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return m, false, err
	}
	// a merchant whose canonical key matches but has no alias row (older data)
	err = q.QueryRowContext(ctx, `SELECT id, name, canonical_key, ambiguous FROM merchants WHERE canonical_key=$1`, c.Key).Scan(&m.ID, &m.Name, &m.Key, &m.Ambiguous)
	if errors.Is(err, sql.ErrNoRows) {
		err = q.QueryRowContext(ctx, `INSERT INTO merchants (name, canonical_key, ambiguous) VALUES ($1,$2,$3) RETURNING id, name, canonical_key, ambiguous`,
			c.Name, c.Key, ambiguousSeed[c.Key]).Scan(&m.ID, &m.Name, &m.Key, &m.Ambiguous)
		created = true
	}
	if err != nil {
		return m, false, err
	}
	_, err = q.ExecContext(ctx, `INSERT INTO merchant_aliases (alias, merchant_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, c.Key, m.ID)
	return m, created, err
}

// Get returns a merchant with its aliases.
func (s *Service) Get(ctx context.Context, q events.DBTX, id string) (Merchant, error) {
	var m Merchant
	err := q.QueryRowContext(ctx, `SELECT id, name, canonical_key, ambiguous FROM merchants WHERE id::text=$1`, id).Scan(&m.ID, &m.Name, &m.Key, &m.Ambiguous)
	if errors.Is(err, sql.ErrNoRows) {
		return m, ErrNotFound
	}
	if err != nil {
		return m, err
	}
	rows, err := q.QueryContext(ctx, `SELECT alias FROM merchant_aliases WHERE merchant_id=$1 ORDER BY alias`, m.ID)
	if err != nil {
		return m, err
	}
	defer rows.Close()
	for rows.Next() {
		var a string
		rows.Scan(&a)
		m.Aliases = append(m.Aliases, a)
	}
	return m, rows.Err()
}

// SetAmbiguous marks a merchant as selling across categories (or not).
func (s *Service) SetAmbiguous(ctx context.Context, tx *sql.Tx, id string, v bool) error {
	res, err := tx.ExecContext(ctx, `UPDATE merchants SET ambiguous=$2 WHERE id::text=$1`, id, v)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// AddAlias points an alias at a merchant: the user telling sofar that a drifted
// descriptor is the same place (alias healing). Rules keyed on the merchant id
// keep working.
func (s *Service) AddAlias(ctx context.Context, tx *sql.Tx, actor events.Actor, alias, merchantID string) error {
	key := normalize.Normalize(alias)
	if key == "" {
		return ErrInvalid
	}
	if _, err := s.Get(ctx, tx, merchantID); err != nil {
		return err
	}
	var was sql.NullString
	tx.QueryRowContext(ctx, `SELECT merchant_id FROM merchant_aliases WHERE alias=$1`, key).Scan(&was)
	if _, err := tx.ExecContext(ctx, `INSERT INTO merchant_aliases (alias, merchant_id) VALUES ($1,$2) ON CONFLICT (alias) DO UPDATE SET merchant_id=EXCLUDED.merchant_id`, key, merchantID); err != nil {
		return err
	}
	_, err := events.Append(ctx, tx, s.Reg, events.New{Actor: actor, Kind: KindAlias, SubjectType: Subject, SubjectID: merchantID, Payload: Alias{Alias: key, MerchantID: merchantID, Was: was.String}})
	return err
}

// MergeInto folds one merchant into another: every alias moves, and the old
// merchant row is removed. Undo restores it.
func (s *Service) MergeInto(ctx context.Context, tx *sql.Tx, actor events.Actor, from, to string) (events.Event, error) {
	if from == to {
		return events.Event{}, fmt.Errorf("%w: cannot merge a merchant into itself", ErrInvalid)
	}
	src, err := s.Get(ctx, tx, from)
	if err != nil {
		return events.Event{}, err
	}
	if _, err = s.Get(ctx, tx, to); err != nil {
		return events.Event{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE merchant_aliases SET merchant_id=$2 WHERE merchant_id=$1`, from, to); err != nil {
		return events.Event{}, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM merchants WHERE id=$1`, from); err != nil {
		return events.Event{}, err
	}
	return events.Append(ctx, tx, s.Reg, events.New{Actor: actor, Kind: KindMerge, SubjectType: Subject, SubjectID: to, Payload: Merge{From: from, To: to, Aliases: src.Aliases}})
}

// Proposal is a suggested merge.
type Proposal struct {
	Names   []string
	Members []string // merchant ids, same order as Names
}

// ProposeMerges clusters merchant names by similarity. It never merges.
func (s *Service) ProposeMerges(ctx context.Context, threshold float64) ([]Proposal, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, canonical_key FROM merchants ORDER BY canonical_key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	idOf := map[string]string{}
	var keys []string
	for rows.Next() {
		var id, key string
		if err = rows.Scan(&id, &key); err != nil {
			return nil, err
		}
		idOf[key] = id
		keys = append(keys, key)
	}
	var out []Proposal
	for _, g := range normalize.ClusterAliases(keys, threshold) {
		p := Proposal{Names: g}
		for _, k := range g {
			p.Members = append(p.Members, idOf[k])
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Split moves one alias off a merchant onto a new merchant of its own.
func (s *Service) Split(ctx context.Context, tx *sql.Tx, actor events.Actor, alias string) (Merchant, error) {
	key := normalize.Normalize(alias)
	var from string
	if err := tx.QueryRowContext(ctx, `SELECT merchant_id FROM merchant_aliases WHERE alias=$1`, key).Scan(&from); err != nil {
		return Merchant{}, ErrNotFound
	}
	var m Merchant
	if err := tx.QueryRowContext(ctx, `INSERT INTO merchants (name, canonical_key, ambiguous) VALUES ($1,$2,$3) ON CONFLICT (canonical_key) DO UPDATE SET name=EXCLUDED.name RETURNING id, name, canonical_key, ambiguous`,
		strings.Title(key), key, ambiguousSeed[key]).Scan(&m.ID, &m.Name, &m.Key, &m.Ambiguous); err != nil {
		return Merchant{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE merchant_aliases SET merchant_id=$2 WHERE alias=$1`, key, m.ID); err != nil {
		return Merchant{}, err
	}
	_, err := events.Append(ctx, tx, s.Reg, events.New{Actor: actor, Kind: KindAlias, SubjectType: Subject, SubjectID: m.ID, Payload: Alias{Alias: key, MerchantID: m.ID, Was: from}})
	return m, err
}
