// Package store owns the Postgres connection and schema migrations.
package store

import (
	"context"
	"database/sql"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// Open connects to Postgres and checks the connection. It does not migrate;
// call Migrate for that.
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
	return d, nil
}
