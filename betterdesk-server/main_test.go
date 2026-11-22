package main

import (
	"os"
	"path/filepath"
	"strings"
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

	gotPath, wrote, err := writeBootstrapAdminCredentials(dir, "admin", "replacement-password")
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != credentialsPath {
		t.Fatalf("credentials path = %q, want %q", gotPath, credentialsPath)
	}
	if wrote {
		t.Fatal("reported the replacement password as written")
	}

	got, err := os.ReadFile(credentialsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("existing credentials were overwritten: %q", got)
	}
}

// TestBootstrapCredentialsFileWithoutPasswordGetsThePassword: a first
// admin's generated password is written to an .admin_credentials that
// exists without a password line, and the file's text is kept.
func TestBootstrapCredentialsFileWithoutPasswordGetsThePassword(t *testing.T) {
	dir := t.TempDir()
	credentialsPath := filepath.Join(dir, ".admin_credentials")
	original := "Admin Username: admin\n"
	if err := os.WriteFile(credentialsPath, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}

	gotPath, wrote, err := writeBootstrapAdminCredentials(dir, "admin", "generated-password")
	if err != nil || gotPath != credentialsPath || !wrote {
		t.Fatalf("write = %q, wrote %v, %v", gotPath, wrote, err)
	}
	if got := readBootstrapAdminPassword(dir); got != "generated-password" {
		t.Fatalf("password in the file = %q, want the generated one", got)
	}
	got, err := os.ReadFile(credentialsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(got), original) {
		t.Fatalf("file text not kept: %q", got)
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
		path, wrote, err := writeBootstrapAdminCredentials(dir, "admin", "made-up-first-pass")
		if err != nil || !wrote || path != filepath.Join(keys, ".admin_credentials") {
			t.Fatalf("credentials of %s: %q, wrote %v, %v", dsn, path, wrote, err)
		}
		if got := readBootstrapAdminPassword(dir); got != "made-up-first-pass" {
			t.Errorf("password read back for %s: %q", dsn, got)
		}
	}
	if got := bootstrapCredentialsDir(filepath.Join(data, "db_v2.sqlite3"), keyFile); got != data {
		t.Errorf("folder on SQLite: %q, want %q", got, data)
	}
}

// TestAttachPanelSyncUsesTheMainFileWithoutAuthDB: on a SQLite install
// without auth.db (a fresh one, where the console keeps its tables in the
// main file) the panel sync reads the main file; an auth.db that exists is
// read until it has been moved into the main file (BUGS D6).
func TestAttachPanelSyncUsesTheMainFileWithoutAuthDB(t *testing.T) {
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "db_v2.sqlite3")
	database, err := db.OpenSQLite(mainPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if err := database.Migrate(); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "data", "auth.db")
	if got := panelSyncPath(mainPath, missing); got != mainPath {
		t.Errorf("without auth.db: %q, want the main file %q", got, mainPath)
	}
	if got := panelSyncPath(mainPath, ""); got != mainPath {
		t.Errorf("without an auth.db path: %q, want the main file %q", got, mainPath)
	}
	authPath := filepath.Join(dir, "auth.db")
	if err := os.WriteFile(authPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := panelSyncPath(mainPath, authPath); got != authPath {
		t.Errorf("with an auth.db: %q, want %q", got, authPath)
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

// A reload reads the environment again; the entrypoint's default mode is
// marked not explicit there, so the mode set in the panel must stay.
func TestReloadKeepsPanelEnrollmentModeOverEnvironmentDefault(t *testing.T) {
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
	t.Setenv("ENROLLMENT_MODE", config.EnrollmentModeManaged)
	t.Setenv("ENROLLMENT_MODE_ENV_OVERRIDE", "N")

	cfg := config.DefaultConfig()
	cfg.EnrollmentMode = config.EnrollmentModeLocked
	if err := reloadEnvConfig(cfg, database); err != nil {
		t.Fatal(err)
	}
	if cfg.EnrollmentMode != config.EnrollmentModeLocked {
		t.Fatalf("enrollment mode after reload = %q, want the panel's %q", cfg.EnrollmentMode, config.EnrollmentModeLocked)
	}
}

// TestBlocklistOfTheStartHoldsTheStoredBans: the blocklist main starts
// with has the bans kept in the database, such as a revoked device's
// (L127).
func TestBlocklistOfTheStartHoldsTheStoredBans(t *testing.T) {
	database, err := db.OpenSQLite(filepath.Join(t.TempDir(), "db_v2.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := database.SaveBlocklistEntry(&db.BlocklistEntry{Kind: "id", Value: "123456789", Reason: "revoked via panel"}); err != nil {
		t.Fatal(err)
	}
	blocklist, err := openBlocklist("", database)
	if err != nil {
		t.Fatal(err)
	}
	if !blocklist.IsIDBlocked("123456789") {
		t.Fatal("the stored ban is not in the blocklist of the start")
	}
}

// TestInitialAdminPasswordKeepsThePasswordRule: an INIT_ADMIN_PASS under
// 8 characters is not given to the first account (L94).
func TestInitialAdminPasswordKeepsThePasswordRule(t *testing.T) {
	for given, want := range map[string]string{"": "", "sixsix7": "", "alllower": "alllower", "Long-Pass-1": "Long-Pass-1"} {
		if got := initialAdminPassword(given); got != want {
			t.Errorf("initialAdminPassword(%q) = %q, want %q", given, got, want)
		}
	}
}
