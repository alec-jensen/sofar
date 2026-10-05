// Package testkit gives integration tests a fresh, migrated Postgres database.
// It uses SOFAR_TEST_DATABASE_URL when set (creating a uniquely named database
// per call) and otherwise starts an embedded Postgres once per process.
package testkit

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"

	"github.com/alec-jensen/sofar/internal/store"
)

var (
	once     sync.Once
	adminURL string
	startErr error
)

func admin(t testing.TB) string {
	once.Do(func() {
		if u := os.Getenv("SOFAR_TEST_DATABASE_URL"); u != "" {
			adminURL = u
			return
		}
		cache := filepath.Join(os.TempDir(), "sofar-embedded-pg")
		if startErr = os.MkdirAll(cache, 0o700); startErr != nil {
			return
		}
		runtime, err := os.MkdirTemp(cache, "run-")
		if err != nil {
			startErr = err
			return
		}
		pg := embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().
			Version(embeddedpostgres.V17).Port(55439).Database("postgres").
			Username("sofar_test").Password("local-test-only").
			CachePath(cache).BinariesPath(filepath.Join(cache, "binaries")).
			RuntimePath(runtime).DataPath(filepath.Join(runtime, "data")).
			BinaryRepositoryURL("https://repo.maven.apache.org/maven2").StartTimeout(90 * time.Second))
		if startErr = pg.Start(); startErr != nil {
			return
		}
		adminURL = "postgres://sofar_test:local-test-only@localhost:55439/postgres?sslmode=disable"
	})
	if startErr != nil {
		t.Fatalf("test database: %v", startErr)
	}
	return adminURL
}

// URLFor swaps the database name in a connection URL.
func URLFor(base, name string) string {
	u, err := url.Parse(base)
	if err != nil {
		panic(err)
	}
	u.Path = "/" + name
	return u.String()
}

// Empty returns a connection to a brand-new, unmigrated database that is
// dropped when the test ends, plus its connection URL.
func Empty(t testing.TB) (*sql.DB, string) {
	t.Helper()
	base := admin(t)
	ctx := context.Background()
	root, err := store.Open(ctx, base)
	if err != nil {
		t.Fatalf("connect to test database server: %v", err)
	}
	var b [6]byte
	rand.Read(b[:])
	name := "sofar_t_" + hex.EncodeToString(b[:])
	if _, err = root.ExecContext(ctx, fmt.Sprintf(`CREATE DATABASE %q`, name)); err != nil {
		root.Close()
		t.Fatalf("create test database: %v", err)
	}
	dsn := URLFor(base, name)
	d, err := store.Open(ctx, dsn)
	if err != nil {
		root.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		d.Close()
		root.ExecContext(context.Background(), fmt.Sprintf(`DROP DATABASE IF EXISTS %q WITH (FORCE)`, name))
		root.Close()
	})
	return d, dsn
}

// DB returns a fresh, fully migrated database.
func DB(t testing.TB) *sql.DB {
	t.Helper()
	d, _ := Empty(t)
	if err := store.Migrate(context.Background(), d); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return d
}
