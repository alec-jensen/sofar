package db

import (
	"context"
	"database/sql"
	_ "embed"
	_ "github.com/jackc/pgx/v5/stdlib"
)

//go:embed schema.sql
var schema string

func Open(ctx context.Context, url string) (*sql.DB, error) {
	d, err := sql.Open("pgx", url)
	if err != nil {
		return nil, err
	}
	d.SetMaxOpenConns(10)
	if err = d.PingContext(ctx); err != nil {
		d.Close()
		return nil, err
	}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// Serialize startup migrations across server restarts/replicas.
	if _, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(731890)"); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, schema); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return d, nil
}
