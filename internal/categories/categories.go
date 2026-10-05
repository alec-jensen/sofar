// Package categories manages the bucket -> group -> category tree (S1a):
// custom groups and categories, renames that keep ids, deletes that reassign,
// and merges. Transactions follow through ledger remap events.
package categories

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/alec-jensen/sofar/internal/events"
	"github.com/alec-jensen/sofar/internal/ledger"
)

// Event kinds written by this package (subject type "category").
const (
	KindCreateGroup = "category.create_group"
	KindCreate      = "category.create"
	KindRename      = "category.rename"
	KindRenameGroup = "category.rename_group"
	KindDeleteGroup = "category.delete_group"
)

// Payloads.
type (
	// CreateGroup records a new custom group.
	CreateGroup struct{ ID, Name, Bucket string }
	// Create records a new custom category.
	Create struct{ ID, GroupID, Name string }
	// Rename records a name change; the id never changes.
	Rename struct{ ID, Name string }
	// DeleteGroup records a group removal.
	DeleteGroup struct{ ID string }
)

// Register adds this package's payloads to an event registry.
func Register(r *events.Registry) {
	r.Register(KindCreateGroup, 1, CreateGroup{})
	r.Register(KindCreate, 1, Create{})
	r.Register(KindRename, 1, Rename{})
	r.Register(KindRenameGroup, 1, Rename{})
	r.Register(KindDeleteGroup, 1, DeleteGroup{})
}

var (
	// ErrInvalid means a bad name, bucket or reference.
	ErrInvalid = errors.New("categories: invalid request")
	// ErrNotFound means no such category or group.
	ErrNotFound = errors.New("categories: not found")
	// ErrProtected means a system category cannot be removed.
	ErrProtected = errors.New("categories: this category is part of the system and cannot be removed")
	// ErrGroupNotEmpty means a group still has categories.
	ErrGroupNotEmpty = errors.New("categories: move or delete the categories in this group first")
)

// fallback is the "other" category of each bucket, where deleted categories'
// transactions go by default (S1a).
var fallback = map[string]string{
	"income": "gift_other", "bills": "other_bills", "subscriptions": "other_subscriptions",
	"savings": "general_savings", "spending": "other_spending",
}

// Fallback returns the default reassignment target for a bucket, or "".
func Fallback(bucket string) string { return fallback[bucket] }

func isFallback(id string) bool {
	for _, f := range fallback {
		if f == id {
			return true
		}
	}
	return false
}

// Category is a leaf of the tree.
type Category struct {
	ID, GroupID, Name string
	Sort              int
	IsDefault         bool
}

// Group holds categories within one bucket.
type Group struct {
	ID, Name, Bucket string
	Sort             int
	IsDefault        bool
	Categories       []Category
}

// Service edits the category tree.
type Service struct {
	DB     *sql.DB
	Ledger *ledger.Service
	Reg    *events.Registry
}

// New builds a Service and registers its event payloads.
func New(db *sql.DB, l *ledger.Service, reg *events.Registry) *Service {
	Register(reg)
	return &Service{DB: db, Ledger: l, Reg: reg}
}

// List returns every live group with its live categories, in display order.
func (s *Service) List(ctx context.Context) ([]Group, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT g.id, g.name, g.bucket, g.sort, g.is_default, coalesce(c.id,''), coalesce(c.name,''), coalesce(c.sort,0), coalesce(c.is_default,false)
		FROM category_groups g LEFT JOIN categories c ON c.group_id = g.id AND c.deleted_at IS NULL
		WHERE g.deleted_at IS NULL
		ORDER BY array_position(ARRAY['income','bills','subscriptions','savings','spending','neutral'], g.bucket), g.sort, g.id, c.sort, c.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Group
	for rows.Next() {
		var g Group
		var c Category
		if err = rows.Scan(&g.ID, &g.Name, &g.Bucket, &g.Sort, &g.IsDefault, &c.ID, &c.Name, &c.Sort, &c.IsDefault); err != nil {
			return nil, err
		}
		if len(out) == 0 || out[len(out)-1].ID != g.ID {
			out = append(out, g)
		}
		if c.ID != "" {
			c.GroupID = g.ID
			out[len(out)-1].Categories = append(out[len(out)-1].Categories, c)
		}
	}
	return out, rows.Err()
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func slug(name string) string {
	s := strings.Trim(slugRe.ReplaceAllString(strings.ToLower(name), "_"), "_")
	if s == "" {
		s = "x"
	}
	if len(s) > 40 {
		s = s[:40]
	}
	return s
}

func cleanName(name string) (string, error) {
	name = strings.Join(strings.Fields(name), " ")
	if name == "" || len(name) > 60 {
		return "", fmt.Errorf("%w: names are 1 to 60 characters", ErrInvalid)
	}
	return name, nil
}

func newID(prefix, name string) string {
	var b [3]byte
	rand.Read(b[:])
	return prefix + slug(name) + "_" + hex.EncodeToString(b[:])
}

func (s *Service) record(ctx context.Context, tx *sql.Tx, actor events.Actor, kind, subject string, payload any) (events.Event, error) {
	return events.Append(ctx, tx, s.Reg, events.New{Actor: actor, Kind: kind, SubjectType: ledger.SubjectCategory, SubjectID: subject, Payload: payload})
}

// CreateGroup adds a custom group to a bucket. The neutral bucket is system-only.
func (s *Service) CreateGroup(ctx context.Context, tx *sql.Tx, actor events.Actor, name, bucket string) (Group, error) {
	name, err := cleanName(name)
	if err != nil {
		return Group{}, err
	}
	if fallback[bucket] == "" {
		return Group{}, fmt.Errorf("%w: groups live in income, bills, subscriptions, savings or spending", ErrInvalid)
	}
	id := newID("g_", name)
	var sort int
	tx.QueryRowContext(ctx, `SELECT coalesce(max(sort),0)+10 FROM category_groups WHERE bucket=$1`, bucket).Scan(&sort)
	if _, err = tx.ExecContext(ctx, `INSERT INTO category_groups (id, name, bucket, sort) VALUES ($1,$2,$3,$4)`, id, name, bucket, sort); err != nil {
		return Group{}, err
	}
	_, err = s.record(ctx, tx, actor, KindCreateGroup, id, CreateGroup{ID: id, Name: name, Bucket: bucket})
	return Group{ID: id, Name: name, Bucket: bucket, Sort: sort}, err
}

// CreateCategory adds a custom category. It must be placed in a live group (S1a).
func (s *Service) CreateCategory(ctx context.Context, tx *sql.Tx, actor events.Actor, groupID, name string) (Category, error) {
	name, err := cleanName(name)
	if err != nil {
		return Category{}, err
	}
	var bucket string
	if err = tx.QueryRowContext(ctx, `SELECT bucket FROM category_groups WHERE id=$1 AND deleted_at IS NULL`, groupID).Scan(&bucket); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Category{}, fmt.Errorf("%w: a category must be placed in a group", ErrInvalid)
		}
		return Category{}, err
	}
	if bucket == "neutral" {
		return Category{}, fmt.Errorf("%w: neutral categories are system-only", ErrInvalid)
	}
	id := newID("c_", name)
	var sort int
	tx.QueryRowContext(ctx, `SELECT coalesce(max(sort),0)+10 FROM categories WHERE group_id=$1`, groupID).Scan(&sort)
	if _, err = tx.ExecContext(ctx, `INSERT INTO categories (id, group_id, name, sort) VALUES ($1,$2,$3,$4)`, id, groupID, name, sort); err != nil {
		return Category{}, err
	}
	_, err = s.record(ctx, tx, actor, KindCreate, id, Create{ID: id, GroupID: groupID, Name: name})
	return Category{ID: id, GroupID: groupID, Name: name, Sort: sort}, err
}

// RenameCategory changes the display name. The id, and so every rule and
// transaction pointing at it, is untouched.
func (s *Service) RenameCategory(ctx context.Context, tx *sql.Tx, actor events.Actor, id, name string) error {
	return s.rename(ctx, tx, actor, "categories", KindRename, id, name)
}

// RenameGroup changes a group's display name.
func (s *Service) RenameGroup(ctx context.Context, tx *sql.Tx, actor events.Actor, id, name string) error {
	return s.rename(ctx, tx, actor, "category_groups", KindRenameGroup, id, name)
}

func (s *Service) rename(ctx context.Context, tx *sql.Tx, actor events.Actor, table, kind, id, name string) error {
	name, err := cleanName(name)
	if err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE `+table+` SET name=$2 WHERE id=$1 AND deleted_at IS NULL`, id, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	_, err = s.record(ctx, tx, actor, kind, id, Rename{ID: id, Name: name})
	return err
}

type info struct{ bucket string }

func (s *Service) live(ctx context.Context, tx *sql.Tx, id string) (info, error) {
	var i info
	err := tx.QueryRowContext(ctx, `SELECT g.bucket FROM categories c JOIN category_groups g ON g.id=c.group_id WHERE c.id=$1 AND c.deleted_at IS NULL`, id).Scan(&i.bucket)
	if errors.Is(err, sql.ErrNoRows) {
		return i, ErrNotFound
	}
	return i, err
}

// Delete removes a category. Its transactions move to reassignTo, which
// defaults to "other" in the same bucket (S1a). System categories and the
// bucket fallbacks cannot be deleted.
func (s *Service) Delete(ctx context.Context, tx *sql.Tx, actor events.Actor, id, reassignTo string) (events.Event, error) {
	from, err := s.live(ctx, tx, id)
	if err != nil {
		return events.Event{}, err
	}
	if from.bucket == "neutral" || isFallback(id) {
		return events.Event{}, ErrProtected
	}
	if reassignTo == "" {
		reassignTo = fallback[from.bucket]
	}
	if reassignTo == id {
		return events.Event{}, fmt.Errorf("%w: choose a different category to move transactions to", ErrInvalid)
	}
	if _, err = s.live(ctx, tx, reassignTo); err != nil {
		return events.Event{}, fmt.Errorf("%w: the reassignment target does not exist", ErrInvalid)
	}
	return s.remap(ctx, tx, actor, ledger.KindCategoryDelete, id, reassignTo, true)
}

// Merge moves everything from one category into another and retires the first.
func (s *Service) Merge(ctx context.Context, tx *sql.Tx, actor events.Actor, from, to string) (events.Event, error) {
	f, err := s.live(ctx, tx, from)
	if err != nil {
		return events.Event{}, err
	}
	if f.bucket == "neutral" || isFallback(from) {
		return events.Event{}, ErrProtected
	}
	if from == to {
		return events.Event{}, fmt.Errorf("%w: cannot merge a category into itself", ErrInvalid)
	}
	if _, err = s.live(ctx, tx, to); err != nil {
		return events.Event{}, fmt.Errorf("%w: the surviving category does not exist", ErrInvalid)
	}
	// Rules move to the survivor in M6; the hook is the remap event itself.
	return s.remap(ctx, tx, actor, ledger.KindCategoryMerge, from, to, true)
}

func (s *Service) remap(ctx context.Context, tx *sql.Tx, actor events.Actor, kind, from, to string, retire bool) (events.Event, error) {
	e, err := events.Append(ctx, tx, s.Reg, events.New{Actor: actor, Kind: kind, SubjectType: ledger.SubjectCategory, SubjectID: from, Payload: ledger.CategoryRemap{From: from, To: to}})
	if err != nil {
		return e, err
	}
	if err = s.Ledger.RebuildAll(ctx, tx); err != nil {
		return e, err
	}
	if retire {
		_, err = tx.ExecContext(ctx, `UPDATE categories SET deleted_at=now() WHERE id=$1`, from)
	}
	return e, err
}

// DeleteGroup removes an empty custom group.
func (s *Service) DeleteGroup(ctx context.Context, tx *sql.Tx, actor events.Actor, id string) error {
	var isDefault bool
	if err := tx.QueryRowContext(ctx, `SELECT is_default FROM category_groups WHERE id=$1 AND deleted_at IS NULL`, id).Scan(&isDefault); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if isDefault {
		return ErrProtected
	}
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM categories WHERE group_id=$1 AND deleted_at IS NULL`, id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return ErrGroupNotEmpty
	}
	if _, err := tx.ExecContext(ctx, `UPDATE category_groups SET deleted_at=now() WHERE id=$1`, id); err != nil {
		return err
	}
	_, err := s.record(ctx, tx, actor, KindDeleteGroup, id, DeleteGroup{ID: id})
	return err
}

// Undo reverses a merge or delete: the category comes back and its
// transactions return to it. Other overlay events are undone through the ledger.
func (s *Service) Undo(ctx context.Context, tx *sql.Tx, actor events.Actor, eventID int64) (events.Event, error) {
	target, err := events.Get(ctx, tx, eventID)
	if err != nil {
		return events.Event{}, err
	}
	if target.Kind != ledger.KindCategoryMerge && target.Kind != ledger.KindCategoryDelete {
		return events.Event{}, fmt.Errorf("%w: only merges and deletes can be undone here", ErrInvalid)
	}
	u, err := events.Undo(ctx, tx, actor, eventID, "")
	if err != nil {
		return u, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE categories SET deleted_at=NULL WHERE id=$1`, target.SubjectID); err != nil {
		return u, err
	}
	return u, s.Ledger.RebuildAll(ctx, tx)
}
