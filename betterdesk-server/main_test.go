package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/unitronix/betterdesk-server/config"
	"github.com/unitronix/betterdesk-server/db"
)

func TestWriteBootstrapAdminCredentialsDoesNotOverwriteExistingFile(t *testing.T) {
	dir := t.TempDir()
	credentialsPath := filepath.Join(dir, ".admin_credentials")
	original := []byte("Admin Username: admin\nAdmin Password: original-password\n")
	if err := os.WriteFile(credentialsPath, original, 0600); err != nil {
		t.Fatal(err)
	}

	gotPath, err := writeBootstrapAdminCredentials(dir, "admin", "replacement-password")
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != credentialsPath {
		t.Fatalf("credentials path = %q, want %q", gotPath, credentialsPath)
	}

	got, err := os.ReadFile(credentialsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("existing credentials were overwritten: %q", got)
	}
}

func TestReadBootstrapAdminPassword(t *testing.T) {
	dir := t.TempDir()
	credentialsPath := filepath.Join(dir, ".admin_credentials")
	if err := os.WriteFile(credentialsPath,
		[]byte("Admin Username: admin\nAdmin Password: shared-password\n"), 0600); err != nil {
		t.Fatal(err)
	}

	if got := readBootstrapAdminPassword(dir); got != "shared-password" {
		t.Fatalf("password = %q, want %q", got, "shared-password")
	}
}

// TestFirstAdminOnPostgresWritesItsCredentials: on PostgreSQL the first
// admin's generated password goes into .admin_credentials beside the key
// file, as the DSN names no folder; on SQLite it stays beside the database
// file (BUGS D8).
func TestFirstAdminOnPostgresWritesItsCredentials(t *testing.T) {
	keys, data := t.TempDir(), t.TempDir()
	keyFile := filepath.Join(keys, "id_ed25519")
	for _, dsn := range []string{"postgres://bd:made-up@db:5432/betterdesk?sslmode=disable", "POSTGRESQL://db/betterdesk"} {
		dir := bootstrapCredentialsDir(dsn, keyFile)
		path, err := writeBootstrapAdminCredentials(dir, "admin", "made-up-first-pass")
		if err != nil || path != filepath.Join(keys, ".admin_credentials") {
			t.Fatalf("credentials of %s: %q %v", dsn, path, err)
		}
		if got := readBootstrapAdminPassword(dir); got != "made-up-first-pass" {
			t.Errorf("password read back for %s: %q", dsn, got)
		}
	}
	if got := bootstrapCredentialsDir(filepath.Join(data, "db_v2.sqlite3"), keyFile); got != data {
		t.Errorf("folder on SQLite: %q, want %q", got, data)
	}
}

func TestResolveEnrollmentModeExplicitEnvironmentWins(t *testing.T) {
	mode, source := resolveEnrollmentMode(config.EnrollmentModeOpen, config.EnrollmentModeLocked, true)
	if mode != config.EnrollmentModeOpen || source != "environment" {
		t.Fatalf("resolveEnrollmentMode() = (%q, %q), want (%q, environment)",
			mode, source, config.EnrollmentModeOpen)
	}
}

func TestResolveEnrollmentModeDatabaseWinsWithoutExplicitEnvironment(t *testing.T) {
	mode, source := resolveEnrollmentMode(config.EnrollmentModeManaged, config.EnrollmentModeLocked, false)
	if mode != config.EnrollmentModeLocked || source != "database" {
		t.Fatalf("resolveEnrollmentMode() = (%q, %q), want (%q, database)",
			mode, source, config.EnrollmentModeLocked)
	}
}

func TestResolveEnrollmentModeIgnoresInvalidDatabaseValue(t *testing.T) {
	mode, source := resolveEnrollmentMode(config.EnrollmentModeManaged, "invalid", false)
	if mode != config.EnrollmentModeManaged || source != "configuration" {
		t.Fatalf("resolveEnrollmentMode() = (%q, %q), want (%q, configuration)",
			mode, source, config.EnrollmentModeManaged)
	}
}

func TestApplyEnrollmentModePersistsExplicitEnvironment(t *testing.T) {
	database, err := db.OpenSQLite(filepath.Join(t.TempDir(), "enrollment.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := database.SetConfig("enrollment_mode", config.EnrollmentModeLocked); err != nil {
		t.Fatal(err)
	}

	cfg := config.DefaultConfig()
	cfg.EnrollmentMode = config.EnrollmentModeOpen
	cfg.EnrollmentModeEnvOverride = true

	source, err := applyEnrollmentMode(cfg, database)
	if err != nil {
		t.Fatal(err)
	}
	if source != "environment" || cfg.EnrollmentMode != config.EnrollmentModeOpen {
		t.Fatalf("applyEnrollmentMode() source=%q mode=%q", source, cfg.EnrollmentMode)
	}
	stored, err := database.GetConfig("enrollment_mode")
	if err != nil {
		t.Fatal(err)
	}
	if stored != config.EnrollmentModeOpen {
		t.Fatalf("persisted enrollment mode = %q, want %q", stored, config.EnrollmentModeOpen)
	}
}

func TestApplyEnrollmentModeRestoresDatabaseWithoutOverride(t *testing.T) {
	database, err := db.OpenSQLite(filepath.Join(t.TempDir(), "enrollment.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := database.SetConfig("enrollment_mode", config.EnrollmentModeLocked); err != nil {
		t.Fatal(err)
	}

	cfg := config.DefaultConfig()
	cfg.EnrollmentMode = config.EnrollmentModeManaged

	source, err := applyEnrollmentMode(cfg, database)
	if err != nil {
		t.Fatal(err)
	}
	if source != "database" || cfg.EnrollmentMode != config.EnrollmentModeLocked {
		t.Fatalf("applyEnrollmentMode() source=%q mode=%q", source, cfg.EnrollmentMode)
	}
}
