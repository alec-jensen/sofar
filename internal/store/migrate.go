package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"

	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// migrationLock serializes migrations across processes and replicas.
const migrationLock = 731890

func provider(db *sql.DB) (*goose.Provider, error) {
	sub, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		return nil, err
	}
	return goose.NewProvider(goose.DialectPostgres, db, sub)
}

// Migrate applies every pending migration. Safe to call on every start.
func Migrate(ctx context.Context, db *sql.DB) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", migrationLock); err != nil {
		return err
	}
	defer conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", migrationLock)
	p, err := provider(db)
	if err != nil {
		return err
	}
	if _, err = p.Up(ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}

// Status describes one migration and whether it has been applied.
type Status struct {
	Version int64
	Source  string
	Applied bool
}

// MigrationStatus lists every known migration.
func MigrationStatus(ctx context.Context, db *sql.DB) ([]Status, error) {
	p, err := provider(db)
	if err != nil {
		return nil, err
	}
	rows, err := p.Status(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Status, 0, len(rows))
	for _, r := range rows {
		out = append(out, Status{Version: r.Source.Version, Source: r.Source.Path, Applied: r.State == goose.StateApplied})
	}
	return out, nil
}

// Current reports whether every embedded migration has been applied.
func Current(ctx context.Context, db *sql.DB) (bool, error) {
	st, err := MigrationStatus(ctx, db)
	if err != nil {
		return false, err
	}
	for _, s := range st {
		if !s.Applied {
			return false, nil
		}
	}
	return true, nil
}
