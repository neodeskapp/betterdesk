package db

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// pgTestDSN returns the DSN of the test PostgreSQL with the database
// replaced by dbname (when not empty) and the runtime parameters added,
// or skips the test when BETTERDESK_TEST_POSTGRES_DSN is not set.
func pgTestDSN(t *testing.T, dbname string, params map[string]string) string {
	t.Helper()
	dsn := os.Getenv("BETTERDESK_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("BETTERDESK_TEST_POSTGRES_DSN is not set")
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse the test DSN: %v", err)
	}
	if dbname != "" {
		u.Path = "/" + dbname
	}
	q := u.Query()
	for k, v := range params {
		q.Set(k, v)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// pgFreshDatabase creates an empty database on the test PostgreSQL, dropped
// when the test ends, and returns its name. Nothing of the shared test
// database (its tables in public, the console's tables) is in it.
func pgFreshDatabase(t *testing.T) string {
	t.Helper()
	admin := pgTestDSN(t, "", nil)
	name := fmt.Sprintf("bdgo_fresh_%d", time.Now().UnixNano())
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Fatalf("connect to the test PostgreSQL: %v", err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("create database %s: %v", name, err)
	}
	t.Cleanup(func() {
		conn, err := pgx.Connect(ctx, admin)
		if err != nil {
			t.Errorf("connect to drop database %s: %v", name, err)
			return
		}
		defer conn.Close(ctx)
		if _, err := conn.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)"); err != nil {
			t.Errorf("drop database %s: %v", name, err)
		}
	})
	return name
}

// openTestPostgres opens and migrates the PostgreSQL the DSN names, closed
// when the test ends.
func openTestPostgres(t *testing.T, dsn string) *PostgresDB {
	t.Helper()
	database, err := OpenPostgres(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if err := database.Migrate(); err != nil {
		t.Fatal(err)
	}
	return database
}

// freshStores runs a test on a new SQLite database and, when
// BETTERDESK_TEST_POSTGRES_DSN names a server, on a new PostgreSQL
// database: both hold only what Migrate creates.
func freshStores(t *testing.T, test func(t *testing.T, store Database)) {
	t.Run("sqlite", func(t *testing.T) { test(t, newTestDB(t)) })
	t.Run("postgres", func(t *testing.T) {
		name := pgFreshDatabase(t)
		test(t, openTestPostgres(t, pgTestDSN(t, name, nil)))
	})
}
