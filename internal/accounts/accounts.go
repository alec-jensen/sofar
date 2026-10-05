// Package accounts lists and edits accounts: kind (budget, tracked,
// balance_only, liability), display name override and active flag. Changes are
// recorded as events. spec: S13.
package accounts

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/alec-jensen/sofar/internal/events"
)

// KindUpdate is the event kind recorded for an account change.
const KindUpdate = "account.update"

// Update is the payload: nil fields are unchanged.
type Update struct {
	AccountID           string  `json:"account_id"`
	Kind                *string `json:"kind,omitempty"`
	DisplayNameOverride *string `json:"display_name_override,omitempty"`
	Active              *bool   `json:"active,omitempty"`
}

// Account is one account as the API shows it.
type Account struct {
	ID                  string
	Provider            string
	Name                string
	DisplayNameOverride string
	Institution         string
	Mask                string
	Currency            string
	Kind                string
	Active              bool
	CreatedAt           time.Time
}

// DisplayName is the name to show: the override, else the provider's name.
func (a Account) DisplayName() string {
	if a.DisplayNameOverride != "" {
		return a.DisplayNameOverride
	}
	return a.Name
}

var (
	// ErrNotFound means no such account.
	ErrNotFound = errors.New("accounts: not found")
	// ErrInvalid means a bad value.
	ErrInvalid = errors.New("accounts: invalid value")
)

// Service reads and edits accounts.
type Service struct {
	DB  *sql.DB
	Reg *events.Registry
}

// New builds a Service and registers its event payload.
func New(db *sql.DB, reg *events.Registry) *Service {
	reg.Register(KindUpdate, 1, Update{})
	return &Service{DB: db, Reg: reg}
}

const cols = `id, provider, name, coalesce(display_name_override,''), institution, mask, currency, kind, active, created_at`

func scan(r interface{ Scan(...any) error }) (Account, error) {
	var a Account
	err := r.Scan(&a.ID, &a.Provider, &a.Name, &a.DisplayNameOverride, &a.Institution, &a.Mask, &a.Currency, &a.Kind, &a.Active, &a.CreatedAt)
	return a, err
}

// List returns every account, active first.
func (s *Service) List(ctx context.Context) ([]Account, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+cols+` FROM accounts ORDER BY active DESC, kind, lower(name), id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Account
	for rows.Next() {
		a, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Get returns one account.
func (s *Service) Get(ctx context.Context, id string) (Account, error) {
	a, err := scan(s.DB.QueryRowContext(ctx, `SELECT `+cols+` FROM accounts WHERE id::text=$1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	return a, err
}

// Update applies a change inside tx and records an event.
func (s *Service) Update(ctx context.Context, tx *sql.Tx, actor events.Actor, id string, u Update) (Account, error) {
	u.AccountID = id
	if u.Kind != nil {
		switch *u.Kind {
		case "budget", "tracked", "balance_only", "liability":
		default:
			return Account{}, fmt.Errorf("%w: unknown account kind %q", ErrInvalid, *u.Kind)
		}
	}
	if u.DisplayNameOverride != nil {
		n := strings.Join(strings.Fields(*u.DisplayNameOverride), " ")
		if len(n) > 80 {
			return Account{}, fmt.Errorf("%w: names are at most 80 characters", ErrInvalid)
		}
		u.DisplayNameOverride = &n
	}
	var name any
	if u.DisplayNameOverride != nil && *u.DisplayNameOverride != "" {
		name = *u.DisplayNameOverride
	}
	res, err := tx.ExecContext(ctx, `
		UPDATE accounts SET kind = coalesce($2, kind), active = coalesce($3, active),
		  display_name_override = CASE WHEN $4::boolean THEN $5 ELSE display_name_override END
		WHERE id::text = $1`, id, u.Kind, u.Active, u.DisplayNameOverride != nil, name)
	if err != nil {
		return Account{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Account{}, ErrNotFound
	}
	if _, err = events.Append(ctx, tx, s.Reg, events.New{Actor: actor, Kind: KindUpdate, SubjectType: "account", SubjectID: id, Payload: u}); err != nil {
		return Account{}, err
	}
	a, err := scan(tx.QueryRowContext(ctx, `SELECT `+cols+` FROM accounts WHERE id::text=$1`, id))
	return a, err
}
