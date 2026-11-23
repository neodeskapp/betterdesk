// Package storetest gives tests a console database: SQLite always, and
// PostgreSQL too when NEODESK_TEST_POSTGRES_DSN names a server.
package storetest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// PostgresEnv is the environment variable with the DSN of a PostgreSQL
// server tests may use. Each test gets its own schema there.
const PostgresEnv = "NEODESK_TEST_POSTGRES_DSN"

// SQLite returns an empty SQLite database in the test's temp directory.
func SQLite(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.OpenSQLite(filepath.Join(t.TempDir(), "console.sqlite3"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// Postgres returns a database in a fresh schema of the server named by
// NEODESK_TEST_POSTGRES_DSN, or skips the test when it is not set.
func Postgres(t *testing.T) *store.DB {
	t.Helper()
	dsn := os.Getenv(PostgresEnv)
	if dsn == "" {
		t.Skipf("%s is not set", PostgresEnv)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	schema := "t_" + hex.EncodeToString(suffix)

	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to postgres: %v", err)
	}
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatalf("create schema: %v", err)
	}

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect to postgres: %v", err)
	}
	db := store.OpenPostgres(pool)
	t.Cleanup(func() {
		_ = db.Close()
		pool.Close()
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer dropCancel()
		_, _ = admin.Exec(dropCtx, "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
	})
	return db
}

// Each runs fn once per database: as subtest "sqlite", and as subtest
// "postgres" when a server is configured.
func Each(t *testing.T, fn func(t *testing.T, db *store.DB)) {
	t.Helper()
	t.Run("sqlite", func(t *testing.T) { fn(t, SQLite(t)) })
	t.Run("postgres", func(t *testing.T) { fn(t, Postgres(t)) })
}
