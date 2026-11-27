package cli

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// With --reset-two-factor, reset-password turns the account's two-factor
// sign-in off and deletes its secret and recovery codes: an install whose
// only admin lost the authenticator and the codes gets back in from the
// server's shell. Without it, two-factor sign-in stays as it is, and the
// output is one line for the account.
func TestResetPasswordCanTurnOffTwoFactor(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "db_v2.sqlite3")
	env := map[string]string{"DB_PATH": file, "CONSOLE_DIR": root}
	run := func(args ...string) (int, string) {
		t.Helper()
		var out, errOut bytes.Buffer
		e := Env{Stdin: strings.NewReader("Neu-Passw0rd\n"), Stdout: &out, Stderr: &errOut, Getenv: func(k string) string { return env[k] }}
		status := resetPassword(args, e, resetOptions{random: rand.Reader, terminal: func(io.Reader) bool { return false },
			exists: func(p string) bool { _, err := os.Stat(p); return err == nil }, cwd: root})
		return status, out.String() + errOut.String()
	}
	if status, out := run("admin"); status != 0 || out != "Created admin user: admin\n" {
		t.Fatalf("first run: %d %q", status, out)
	}
	db, err := sql.Open("sqlite", "file:"+file)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	turnOn := func() {
		t.Helper()
		if _, err := db.Exec(`UPDATE users SET totp_enabled = 1, totp_secret = 'JBSWY3DPEHPK3PXP', totp_recovery_codes = '["a","b"]'`); err != nil {
			t.Fatal(err)
		}
	}
	twoFactor := func() (enabled bool, secret string, codes sql.NullString) {
		t.Helper()
		if err := db.QueryRow(`SELECT totp_enabled, COALESCE(totp_secret, ''), totp_recovery_codes FROM users WHERE username = 'admin'`).Scan(&enabled, &secret, &codes); err != nil {
			t.Fatal(err)
		}
		return
	}

	turnOn()
	status, out := run("admin")
	if want := "Password updated for admin; its console sessions were signed out.\n"; status != 0 || out != want {
		t.Fatalf("reset: %d %q, want %q", status, out, want)
	}
	if enabled, secret, _ := twoFactor(); !enabled || secret == "" {
		t.Fatal("a reset without --reset-two-factor turned two-factor sign-in off")
	}

	status, out = run("--reset-two-factor", "admin")
	if want := "Password updated and two-factor sign-in turned off for admin; its console sessions were signed out.\n"; status != 0 || out != want {
		t.Fatalf("reset with --reset-two-factor: %d %q, want %q", status, out, want)
	}
	if enabled, secret, codes := twoFactor(); enabled || secret != "" || codes.Valid {
		t.Fatalf("two-factor sign-in after --reset-two-factor: enabled %v, secret %q, codes %v", enabled, secret, codes)
	}
}
