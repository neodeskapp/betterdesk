package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/neodeskapp/neodesk/neodesk-server/config"
	"github.com/neodeskapp/neodesk/neodesk-server/db"
)

// eachEnrollmentDatabase runs test on a new SQLite database and, when
// NEODESK_TEST_POSTGRES_DSN names a server, on a new PostgreSQL
// database, both migrated.
func eachEnrollmentDatabase(t *testing.T, test func(t *testing.T, database db.Database)) {
	t.Run("sqlite", func(t *testing.T) {
		database, err := db.OpenSQLite(filepath.Join(t.TempDir(), "enrollment.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { database.Close() })
		if err := database.Migrate(); err != nil {
			t.Fatal(err)
		}
		test(t, database)
	})
	t.Run("postgres", func(t *testing.T) {
		dsn := os.Getenv("NEODESK_TEST_POSTGRES_DSN")
		if dsn == "" {
			t.Skip("NEODESK_TEST_POSTGRES_DSN is not set")
		}
		ctx := context.Background()
		name := fmt.Sprintf("ndgo_enrollment_%d", time.Now().UnixNano())
		admin, err := pgx.Connect(ctx, dsn)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
			admin.Close(ctx)
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, _ = admin.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
			admin.Close(ctx)
		})
		u, err := url.Parse(dsn)
		if err != nil {
			t.Fatal(err)
		}
		u.Path = "/" + name
		database, err := db.OpenPostgres(u.String())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { database.Close() })
		if err := database.Migrate(); err != nil {
			t.Fatal(err)
		}
		test(t, database)
	})
}

// startEnrollment does what a server start does with the enrollment mode:
// read the environment, then reconcile it with the mode the database holds.
// mode and force are the values of ENROLLMENT_MODE and
// ENROLLMENT_MODE_ENV_OVERRIDE, empty for unset.
func startEnrollment(t *testing.T, database db.Database, mode, force string) string {
	t.Helper()
	t.Setenv("ENROLLMENT_MODE", mode)
	t.Setenv("ENROLLMENT_MODE_ENV_OVERRIDE", force)
	cfg := config.DefaultConfig()
	cfg.LoadEnv()
	if _, err := applyEnrollmentMode(cfg, database); err != nil {
		t.Fatal(err)
	}
	return cfg.EnrollmentMode
}

// saveInConsole stores a mode as PUT /api/enrollment/mode does.
func saveInConsole(t *testing.T, database db.Database, mode string) {
	t.Helper()
	if err := database.SetConfig("enrollment_mode", mode); err != nil {
		t.Fatal(err)
	}
}

// L35: the Docker entrypoint passes managed as the default of a fresh
// volume at its first start only. The mode stays managed after a restart.
func TestEnrollmentModeOfTheFirstStartSurvivesARestart(t *testing.T) {
	eachEnrollmentDatabase(t, func(t *testing.T, database db.Database) {
		if got := startEnrollment(t, database, config.EnrollmentModeManaged, "N"); got != config.EnrollmentModeManaged {
			t.Fatalf("first start: mode %q, want managed", got)
		}
		if got := startEnrollment(t, database, "", ""); got != config.EnrollmentModeManaged {
			t.Fatalf("after a restart without ENROLLMENT_MODE: mode %q, want managed", got)
		}
	})
}

// L35, L36: ENROLLMENT_MODE is only the first default. A mode saved in the
// console stays across restarts although the environment still names
// another one.
func TestEnrollmentModeSavedInTheConsoleSurvivesARestart(t *testing.T) {
	eachEnrollmentDatabase(t, func(t *testing.T, database db.Database) {
		if got := startEnrollment(t, database, config.EnrollmentModeManaged, ""); got != config.EnrollmentModeManaged {
			t.Fatalf("first start: mode %q, want managed", got)
		}
		saveInConsole(t, database, config.EnrollmentModeOpen)
		if got := startEnrollment(t, database, config.EnrollmentModeManaged, ""); got != config.EnrollmentModeOpen {
			t.Fatalf("after a restart: mode %q, want the console's open", got)
		}
		if stored, _ := database.GetConfig("enrollment_mode"); stored != config.EnrollmentModeOpen {
			t.Fatalf("stored mode %q, want the console's open", stored)
		}
	})
}

// ENROLLMENT_MODE_ENV_OVERRIDE=Y makes the environment win at every start,
// over what the console saved, and stores the mode.
func TestEnrollmentModeForcedByTheEnvironmentWins(t *testing.T) {
	eachEnrollmentDatabase(t, func(t *testing.T, database db.Database) {
		saveInConsole(t, database, config.EnrollmentModeOpen)
		if got := startEnrollment(t, database, config.EnrollmentModeLocked, "Y"); got != config.EnrollmentModeLocked {
			t.Fatalf("forced start: mode %q, want locked", got)
		}
		if stored, _ := database.GetConfig("enrollment_mode"); stored != config.EnrollmentModeLocked {
			t.Fatalf("stored mode %q, want locked", stored)
		}
	})
}

// Without ENROLLMENT_MODE and without a saved mode the server runs open and
// stores nothing, so a value set later is still applied as the first default.
func TestEnrollmentModeWithoutAnySettingStoresNothing(t *testing.T) {
	eachEnrollmentDatabase(t, func(t *testing.T, database db.Database) {
		if got := startEnrollment(t, database, "", ""); got != config.EnrollmentModeOpen {
			t.Fatalf("start without a setting: mode %q, want open", got)
		}
		if stored, _ := database.GetConfig("enrollment_mode"); stored != "" {
			t.Fatalf("stored mode %q, want none", stored)
		}
		if got := startEnrollment(t, database, config.EnrollmentModeLocked, ""); got != config.EnrollmentModeLocked {
			t.Fatalf("start with ENROLLMENT_MODE set later: mode %q, want locked", got)
		}
	})
}
