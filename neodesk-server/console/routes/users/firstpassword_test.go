package users_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/routetest"
)

// TestAResetInTheConsoleRemovesTheFirstPassword: setting an account's
// password on the Users page removes the first-run credentials file that
// holds its first password, as the account's own password change and the
// reset-password command do (L342, L308).
func TestAResetInTheConsoleRemovesTheFirstPassword(t *testing.T) {
	for _, c := range []struct{ name, method, path, body string }{
		{"reset", "POST", "/api/users/%d/reset-password", `{"newPassword":"Second-Pass-2!"}`},
		{"edit", "PATCH", "/api/users/%d", `{"password":"Second-Pass-2!"}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := world(t, nil, routetest.Options{})
			const id = 900
			if _, err := w.DB.Exec(context.Background(),
				`INSERT INTO users (id, username, password_hash, role) VALUES (?, 'founder', 'x', 'admin')`, id); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(w.DataDir, ".admin_credentials")
			if err := os.WriteFile(file, []byte("Admin Username: founder\nAdmin Password: First-Pass-1!\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			got := w.As("super_admin").Do(c.method, fmt.Sprintf(c.path, id), c.body)
			if got.Status != 200 {
				t.Fatalf("%s: %d %s", c.name, got.Status, got.Body)
			}
			if _, err := os.Stat(file); !os.IsNotExist(err) {
				t.Errorf("the first password is still in %s after the %s", file, c.name)
			}
			if _, err := os.Stat(filepath.Join(w.DataDir, ".admin_credentials.removed")); err != nil {
				t.Errorf("no note took its place: %v", err)
			}
		})
	}
}
