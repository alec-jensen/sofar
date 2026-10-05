// Package budgetcfg holds the effective-dated budget configuration: the
// general savings rule, the emergency fund, and goals (S7, S8, S17). A change
// applies from a month onward and never rewrites earlier months.
package budgetcfg

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/alec-jensen/sofar/internal/budget"
	"github.com/alec-jensen/sofar/internal/categories"
	"github.com/alec-jensen/sofar/internal/civil"
	"github.com/alec-jensen/sofar/internal/events"
	"github.com/alec-jensen/sofar/internal/money"
)

// Event kinds.
const (
	KindConfigSet  = "config.set"
	KindGoalCreate = "goal.create"
	KindGoalChange = "goal.change"
	Subject        = "config"
)

type (
	// ConfigSet records a new config version.
	ConfigSet struct {
		VersionID int64  `json:"version_id"`
		Kind      string `json:"kind"`
		From      string `json:"from"`
	}
	// GoalCreate records a new goal.
	GoalCreate struct {
		GoalID string `json:"goal_id"`
		Name   string `json:"name"`
	}
	// GoalChange records an edit, completion, pause or abandonment.
	GoalChange struct {
		GoalID string `json:"goal_id"`
		Change string `json:"change"`
	}
)

var (
	// ErrInvalid means a bad value.
	ErrInvalid = errors.New("budgetcfg: invalid request")
	// ErrNotFound means no such goal or version.
	ErrNotFound = errors.New("budgetcfg: not found")
)

// EFConfig is the emergency fund configuration (S8).
type EFConfig struct {
	Months         int          `json:"months"`          // N in the target, default 3
	TargetOverride *money.Cents `json:"target_override"` // a fixed target in cents
	Line           money.Cents  `json:"line"`            // the monthly standing contribution
}

// Service reads and writes budget configuration.
type Service struct {
	DB         *sql.DB
	Reg        *events.Registry
	Categories *categories.Service
	Now        func() time.Time
	Home       *time.Location
}

// New builds a Service and registers its event payloads.
func New(db *sql.DB, reg *events.Registry, cats *categories.Service, home *time.Location) *Service {
	reg.Register(KindConfigSet, 1, ConfigSet{})
	reg.Register(KindGoalCreate, 1, GoalCreate{})
	reg.Register(KindGoalChange, 1, GoalChange{})
	if home == nil {
		home = time.UTC
	}
	return &Service{DB: db, Reg: reg, Categories: cats, Now: time.Now, Home: home}
}

func (s *Service) thisMonth() civil.YearMonth { return civil.DateOf(s.Now().In(s.Home)).Month() }

func md(ym civil.YearMonth) time.Time { return time.Date(ym.Y, ym.M, 1, 0, 0, 0, 0, time.UTC) }

// Version is a stored config version.
type Version struct {
	ID      int64
	Kind    string
	From    civil.YearMonth
	Payload json.RawMessage
}

// Versions loads every live config version, oldest first.
func Versions(ctx context.Context, q events.DBTX) ([]Version, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, kind, effective_month, payload FROM config_versions WHERE deleted_at IS NULL ORDER BY effective_month, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Version
	for rows.Next() {
		var v Version
		var from time.Time
		var payload []byte
		if err = rows.Scan(&v.ID, &v.Kind, &from, &payload); err != nil {
			return nil, err
		}
		v.From, v.Payload = civil.DateOf(from).Month(), payload
		out = append(out, v)
	}
	return out, rows.Err()
}

// savingsPayload is the stored savings rule.
type savingsPayload struct {
	Fixed          int64 `json:"fixed"`
	BasisPoints    int64 `json:"basis_points"`
	UseBasisPoints bool  `json:"use_basis_points"`
}

// SavingsAt resolves the savings rule in force for a month.
func SavingsAt(vs []Version, ym civil.YearMonth) budget.SavingsRule {
	var best *Version
	for i := range vs {
		if vs[i].Kind == "savings_rule" && !ym.Before(vs[i].From) && (best == nil || !vs[i].From.Before(best.From)) {
			best = &vs[i]
		}
	}
	if best == nil {
		return budget.SavingsRule{}
	}
	var p savingsPayload
	json.Unmarshal(best.Payload, &p)
	return budget.SavingsRule{Fixed: money.Cents(p.Fixed), BasisPoints: p.BasisPoints, UseBasisPoints: p.UseBasisPoints}
}

// EFAt resolves the emergency fund config in force for a month.
func EFAt(vs []Version, ym civil.YearMonth) EFConfig {
	cfg := EFConfig{Months: 3}
	var best *Version
	for i := range vs {
		if vs[i].Kind == "ef" && !ym.Before(vs[i].From) && (best == nil || !vs[i].From.Before(best.From)) {
			best = &vs[i]
		}
	}
	if best != nil {
		json.Unmarshal(best.Payload, &cfg)
		if cfg.Months <= 0 {
			cfg.Months = 3
		}
	}
	return cfg
}

func (s *Service) addVersion(ctx context.Context, tx *sql.Tx, actor events.Actor, kind string, from civil.YearMonth, payload any) (int64, error) {
	raw, _ := json.Marshal(payload)
	var id int64
	if err := tx.QueryRowContext(ctx, `INSERT INTO config_versions (kind, effective_month, payload) VALUES ($1,$2,$3) RETURNING id`, kind, md(from), raw).Scan(&id); err != nil {
		return 0, err
	}
	_, err := events.Append(ctx, tx, s.Reg, events.New{Actor: actor, Kind: KindConfigSet, SubjectType: Subject, SubjectID: kind, Payload: ConfigSet{VersionID: id, Kind: kind, From: from.String()}})
	return id, err
}

// SetSavings sets the general savings rule from a month onward: a fixed amount
// or a percentage (basis points) of the effective plan. There is no floor (S7).
func (s *Service) SetSavings(ctx context.Context, tx *sql.Tx, actor events.Actor, from civil.YearMonth, rule budget.SavingsRule) (int64, error) {
	if rule.Fixed < 0 || rule.BasisPoints < 0 || rule.BasisPoints > 10000 {
		return 0, fmt.Errorf("%w: savings must be non-negative, and a percentage at most 100", ErrInvalid)
	}
	return s.addVersion(ctx, tx, actor, "savings_rule", from, savingsPayload{Fixed: int64(rule.Fixed), BasisPoints: rule.BasisPoints, UseBasisPoints: rule.UseBasisPoints})
}

// SetEF sets the emergency fund config from a month onward. Changing the
// target is not a withdrawal (S8).
func (s *Service) SetEF(ctx context.Context, tx *sql.Tx, actor events.Actor, from civil.YearMonth, cfg EFConfig) (int64, error) {
	if cfg.Months < 1 || cfg.Months > 24 || cfg.Line < 0 || (cfg.TargetOverride != nil && *cfg.TargetOverride < 0) {
		return 0, fmt.Errorf("%w: the emergency fund needs 1 to 24 months and non-negative amounts", ErrInvalid)
	}
	return s.addVersion(ctx, tx, actor, "ef", from, cfg)
}

// UndoVersion removes a config version (undo of a set).
func (s *Service) UndoVersion(ctx context.Context, tx *sql.Tx, actor events.Actor, eventID int64) (events.Event, error) {
	target, err := events.Get(ctx, tx, eventID)
	if err != nil {
		return events.Event{}, err
	}
	p, err := s.Reg.Decode(target)
	cs, ok := p.(*ConfigSet)
	if err != nil || !ok {
		return events.Event{}, ErrInvalid
	}
	u, err := events.Undo(ctx, tx, actor, eventID, "")
	if err != nil {
		return u, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE config_versions SET deleted_at=now() WHERE id=$1`, cs.VersionID)
	return u, err
}

// RedoVersion restores a config version removed by UndoVersion.
func (s *Service) RedoVersion(ctx context.Context, tx *sql.Tx, actor events.Actor, undoID int64) (events.Event, error) {
	undo, err := events.Get(ctx, tx, undoID)
	if err != nil || undo.Reverses == nil {
		return events.Event{}, ErrInvalid
	}
	orig, err := events.Get(ctx, tx, *undo.Reverses)
	if err != nil {
		return events.Event{}, err
	}
	p, err := s.Reg.Decode(orig)
	cs, ok := p.(*ConfigSet)
	if err != nil || !ok {
		return events.Event{}, ErrInvalid
	}
	r, err := events.Redo(ctx, tx, actor, undoID, "")
	if err != nil {
		return r, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE config_versions SET deleted_at=NULL WHERE id=$1`, cs.VersionID)
	return r, err
}

// ---- goals ----

// GoalVersion is one effective-dated state of a goal.
type GoalVersion struct {
	From       string      `json:"from"`
	Target     money.Cents `json:"target"`
	Monthly    money.Cents `json:"monthly"`
	Paused     bool        `json:"paused"`
	TargetDate *string     `json:"target_date"`
}

// Goal is a savings goal: its own envelope and its own category in the Goals group.
type Goal struct {
	ID          string
	Name        string
	CategoryID  string
	Priority    int
	CreatedFrom civil.YearMonth
	CompletedOn *civil.Date
	AbandonedOn *civil.Date
	Versions    []GoalVersion
}

// At returns the goal's state in force for a month.
func (g Goal) At(ym civil.YearMonth) (GoalVersion, bool) {
	var best GoalVersion
	found := false
	for _, v := range g.Versions {
		from, err := civil.ParseYearMonth(v.From)
		if err != nil || ym.Before(from) {
			continue
		}
		if !found || v.From >= best.From {
			best, found = v, true
		}
	}
	return best, found
}

// Active reports whether the goal contributes in a month: started, not paused,
// not completed or abandoned before it (S7).
func (g Goal) Active(ym civil.YearMonth) bool {
	if ym.Before(g.CreatedFrom) {
		return false
	}
	if g.CompletedOn != nil && g.CompletedOn.Month().Before(ym) {
		return false
	}
	if g.AbandonedOn != nil && g.AbandonedOn.Month().Before(ym) {
		return false
	}
	v, ok := g.At(ym)
	return ok && !v.Paused
}

// Line builds the engine line for a month, or false when the goal has none.
func (g Goal) Line(ym civil.YearMonth) (budget.Line, bool) {
	if !g.Active(ym) {
		return budget.Line{}, false
	}
	v, _ := g.At(ym)
	return budget.Line{ID: g.ID, Label: g.Name, Amount: v.Monthly, Target: v.Target}, true
}

// DerivedDate is when the goal finishes at its monthly amount (S7: never both given).
func (g Goal) DerivedDate(ym civil.YearMonth, balance money.Cents) (civil.YearMonth, bool) {
	v, ok := g.At(ym)
	if !ok || v.Monthly <= 0 {
		return civil.YearMonth{}, false
	}
	left := v.Target - balance
	if left <= 0 {
		return ym, true
	}
	months := int((left + v.Monthly - 1) / v.Monthly)
	return ym.AddMonths(months), true
}

// GoalParams creates a goal. Give either Monthly or TargetDate; the other is derived.
type GoalParams struct {
	Name       string
	Target     money.Cents
	Monthly    money.Cents
	TargetDate *civil.YearMonth
	Priority   int
}

func monthsBetween(a, b civil.YearMonth) int { return (b.Y*12 + int(b.M)) - (a.Y*12 + int(a.M)) }

// CreateGoal adds a goal with its own category in the Goals group (S1a, S7).
func (s *Service) CreateGoal(ctx context.Context, tx *sql.Tx, actor events.Actor, p GoalParams) (Goal, error) {
	name := strings.Join(strings.Fields(p.Name), " ")
	if name == "" || p.Target <= 0 {
		return Goal{}, fmt.Errorf("%w: a goal needs a name and a target", ErrInvalid)
	}
	if (p.Monthly > 0) == (p.TargetDate != nil) {
		return Goal{}, fmt.Errorf("%w: give a monthly amount or a date, not both and not neither", ErrInvalid)
	}
	from := s.thisMonth()
	monthly := p.Monthly
	var tdate *string
	if p.TargetDate != nil {
		months := monthsBetween(from, *p.TargetDate)
		if months < 1 {
			return Goal{}, fmt.Errorf("%w: the date must be after this month", ErrInvalid)
		}
		monthly = (p.Target + money.Cents(months) - 1) / money.Cents(months)
		d := p.TargetDate.String()
		tdate = &d
	}
	cat, err := s.Categories.CreateCategory(ctx, tx, actor, "sv_goals", name)
	if err != nil {
		return Goal{}, err
	}
	if p.Priority == 0 {
		p.Priority = 100
	}
	versions, _ := json.Marshal([]GoalVersion{{From: from.String(), Target: p.Target, Monthly: monthly, TargetDate: tdate}})
	var id string
	if err = tx.QueryRowContext(ctx, `INSERT INTO goals (name, category_id, versions, priority, created_month) VALUES ($1,$2,$3,$4,$5) RETURNING id`, name, cat.ID, versions, p.Priority, md(from)).Scan(&id); err != nil {
		return Goal{}, err
	}
	if _, err = events.Append(ctx, tx, s.Reg, events.New{Actor: actor, Kind: KindGoalCreate, SubjectType: "goal", SubjectID: id, Payload: GoalCreate{GoalID: id, Name: name}}); err != nil {
		return Goal{}, err
	}
	return s.GetGoal(ctx, tx, id)
}

const goalCols = `id, name, category_id, priority, created_month, completed_on, abandoned_on, versions`

func scanGoal(r interface{ Scan(...any) error }) (Goal, error) {
	var g Goal
	var created time.Time
	var done, gone sql.NullTime
	var versions []byte
	if err := r.Scan(&g.ID, &g.Name, &g.CategoryID, &g.Priority, &created, &done, &gone, &versions); err != nil {
		return g, err
	}
	g.CreatedFrom = civil.DateOf(created).Month()
	if done.Valid {
		d := civil.DateOf(done.Time)
		g.CompletedOn = &d
	}
	if gone.Valid {
		d := civil.DateOf(gone.Time)
		g.AbandonedOn = &d
	}
	return g, json.Unmarshal(versions, &g.Versions)
}

// ListGoals returns every goal, by priority.
func ListGoals(ctx context.Context, q events.DBTX) ([]Goal, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+goalCols+` FROM goals ORDER BY priority, created_month, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Goal
	for rows.Next() {
		g, err := scanGoal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// GetGoal returns one goal.
func (s *Service) GetGoal(ctx context.Context, q events.DBTX, id string) (Goal, error) {
	g, err := scanGoal(q.QueryRowContext(ctx, `SELECT `+goalCols+` FROM goals WHERE id::text=$1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return g, ErrNotFound
	}
	return g, err
}

// GoalEdit changes a goal from this month onward; nil fields stay as they are.
type GoalEdit struct {
	Target     *money.Cents
	Monthly    *money.Cents
	TargetDate *civil.YearMonth // re-derives the monthly amount
	Paused     *bool
}

// EditGoal appends an effective-dated version. A date-based goal that fell
// behind is the user's call: raise the monthly amount, or let the date slip
// (give a later TargetDate); nothing adjusts on its own (S7).
func (s *Service) EditGoal(ctx context.Context, tx *sql.Tx, actor events.Actor, id string, e GoalEdit) (Goal, error) {
	g, err := s.GetGoal(ctx, tx, id)
	if err != nil {
		return g, err
	}
	now := s.thisMonth()
	cur, _ := g.At(now)
	next := cur
	next.From = now.String()
	if e.Target != nil {
		if *e.Target <= 0 {
			return g, fmt.Errorf("%w: the target must be positive", ErrInvalid)
		}
		next.Target = *e.Target
	}
	if e.Paused != nil {
		next.Paused = *e.Paused
	}
	switch {
	case e.Monthly != nil && e.TargetDate != nil:
		return g, fmt.Errorf("%w: give a monthly amount or a date, not both", ErrInvalid)
	case e.Monthly != nil:
		if *e.Monthly < 0 {
			return g, ErrInvalid
		}
		next.Monthly, next.TargetDate = *e.Monthly, nil
	case e.TargetDate != nil:
		months := monthsBetween(now, *e.TargetDate)
		if months < 1 {
			return g, fmt.Errorf("%w: the date must be after this month", ErrInvalid)
		}
		next.Monthly = (next.Target + money.Cents(months) - 1) / money.Cents(months)
		d := e.TargetDate.String()
		next.TargetDate = &d
	}
	// replace a version already written for this month, otherwise append
	vs := g.Versions[:0:0]
	for _, v := range g.Versions {
		if v.From != next.From {
			vs = append(vs, v)
		}
	}
	vs = append(vs, next)
	sort.SliceStable(vs, func(i, j int) bool { return vs[i].From < vs[j].From })
	raw, _ := json.Marshal(vs)
	if _, err = tx.ExecContext(ctx, `UPDATE goals SET versions=$2 WHERE id::text=$1`, id, raw); err != nil {
		return g, err
	}
	if _, err = events.Append(ctx, tx, s.Reg, events.New{Actor: actor, Kind: KindGoalChange, SubjectType: "goal", SubjectID: id, Payload: GoalChange{GoalID: id, Change: "edit"}}); err != nil {
		return g, err
	}
	return s.GetGoal(ctx, tx, id)
}

// CompleteGoal ends a goal's contribution from the following month. The freed
// contribution lands in the pot, because the fixed line simply disappears (S7).
func (s *Service) CompleteGoal(ctx context.Context, tx *sql.Tx, actor events.Actor, id string, on civil.Date) error {
	return s.finish(ctx, tx, actor, id, on, "completed_on", "complete")
}

// AbandonGoal ends a goal; the user decides where its balance goes (a move event).
func (s *Service) AbandonGoal(ctx context.Context, tx *sql.Tx, actor events.Actor, id string, on civil.Date) error {
	return s.finish(ctx, tx, actor, id, on, "abandoned_on", "abandon")
}

func (s *Service) finish(ctx context.Context, tx *sql.Tx, actor events.Actor, id string, on civil.Date, col, change string) error {
	res, err := tx.ExecContext(ctx, `UPDATE goals SET `+col+`=$2 WHERE id::text=$1`, id, on.String())
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	_, err = events.Append(ctx, tx, s.Reg, events.New{Actor: actor, Kind: KindGoalChange, SubjectType: "goal", SubjectID: id, Payload: GoalChange{GoalID: id, Change: change}})
	return err
}
