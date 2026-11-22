package console

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/authn"
)

func adminHash(t *testing.T, w *world) string {
	t.Helper()
	var hash string
	if err := w.db.QueryRow(context.Background(), "SELECT password_hash FROM users WHERE username = 'e2e_admin'").Scan(&hash); err != nil {
		t.Fatal(err)
	}
	return hash
}

// An installer asks for a new admin password with FORCE_PASSWORD_UPDATE.
// The console acts on it at start.
func TestStartLooksAfterTheFirstAccount(t *testing.T) {
	env := map[string]string{
		"DEFAULT_ADMIN_USERNAME": "e2e_admin",
		"DEFAULT_ADMIN_PASSWORD": "Start-Test-1!pass",
		"FORCE_PASSWORD_UPDATE":  "true",
	}
	w := newWorld(t, setup{env: env})
	if err := w.s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if hash := adminHash(t, w); !strings.HasPrefix(hash, "pbkdf2-sha256$") {
		t.Errorf("the admin's password was not set at start: %q", hash)
	}
}

// The other way an installer asks is a file in the data folder. It works
// once: the console deletes it.
func TestStartTakesTheForceFileOfTheDataFolder(t *testing.T) {
	env := map[string]string{"DEFAULT_ADMIN_USERNAME": "e2e_admin", "DEFAULT_ADMIN_PASSWORD": "Start-Test-1!pass"}
	w := newWorld(t, setup{env: env})
	file := filepath.Join(w.s.cfg.DataDir, ".force_password_update")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := w.s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, err := os.Stat(file)
	if taken, changed := os.IsNotExist(err), adminHash(t, w) != "x"; !taken || !changed {
		t.Errorf("file taken: %v, password set: %v", taken, changed)
	}
}

// In a container the signal server makes the first account. When none
// appears the console does not start, and makes none of its own.
func TestStartFailsWithoutTheFirstAccount(t *testing.T) {
	w := newWorld(t, setup{env: map[string]string{"DOCKER": "true", "DOCKER_ADMIN_BOOTSTRAP_WAIT_MS": "1000"}})
	if _, err := w.db.Exec(context.Background(), "DELETE FROM users"); err != nil {
		t.Fatal(err)
	}
	err := w.s.Start(context.Background())
	if !errors.Is(err, authn.ErrNoBootstrapAdmin) {
		t.Fatalf("Start = %v, want the missing first account", err)
	}
	if w.s.Addr() != "" {
		t.Error("the console listens although it did not start")
	}
	var users int
	if err := w.db.QueryRow(context.Background(), "SELECT COUNT(*) FROM users").Scan(&users); err != nil || users != 0 {
		t.Errorf("%d accounts after a failed start (%v)", users, err)
	}
}
