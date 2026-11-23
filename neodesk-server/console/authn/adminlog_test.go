package authn

import (
	"context"
	"slices"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// TestFirstAdminLogsTheCredentialsPath: the log line about a made-up
// password of the first account names the file it was written to, in
// KEYS_PATH when that is set (BUGS.md AN6).
func TestFirstAdminLogsTheCredentialsPath(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		files := newMemFS()
		files.place(fileVector{Path: "/keys", Dir: true})
		files.place(fileVector{Path: "/data", Dir: true})
		s, _, logs := testService(t, db, func(o *Options) {
			o.Settings = Settings{KeysPath: "/keys", DataDir: "/data"}
			o.FS = files
		})
		created, err := s.EnsureDefaultAdmin(context.Background())
		if err != nil || !created {
			t.Fatalf("first account: %v, %v", created, err)
		}
		if !s.fs.Exists("/keys/.admin_credentials") {
			t.Fatal("no credentials file in KEYS_PATH")
		}
		want := []string{"info", "[AUTH] Generated admin credentials written to /keys/.admin_credentials — change password immediately"}
		if lines := logs.take(); !slices.ContainsFunc(lines, func(line []string) bool { return slices.Equal(line, want) }) {
			t.Errorf("no line %q among %q", want[1], lines)
		}
	})
}

// TestForcedAdminPasswordKeepsThePasswordRule: an installer's forced
// update does not write a DEFAULT_ADMIN_PASSWORD under 8 characters over
// the admin's password, and the log says why (L94).
func TestForcedAdminPasswordKeepsThePasswordRule(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		s, _, logs := testService(t, db, func(o *Options) {
			o.Settings = Settings{DefaultAdminUsername: "admin", DefaultAdminPassword: "short7!", ForcePasswordUpdate: true}
		})
		addUser(t, s, "admin", "Long-Pass-1", "admin", "local")
		before, err := getUserByUsername(ctx, db, "admin")
		if err != nil || before == nil {
			t.Fatalf("admin: %v, %v", before, err)
		}
		if _, err := s.EnsureDefaultAdmin(ctx); err != nil {
			t.Fatal(err)
		}
		after, _ := getUserByUsername(ctx, db, "admin")
		if after.PasswordHash.String != before.PasswordHash.String {
			t.Fatal("a forced update wrote the 7-character password")
		}
		want := []string{"error", "[AUTH] The configured admin password is not used: Password must be at least 8 characters"}
		if lines := logs.take(); !slices.ContainsFunc(lines, func(line []string) bool { return slices.Equal(line, want) }) {
			t.Errorf("no line %q among %q", want[1], lines)
		}
	})
}
