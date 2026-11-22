package authn

import (
	"context"
	"slices"
	"testing"

	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
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

// TestFirstAdminPasswordKeepsThePasswordRule: a configured admin password
// shorter than the password rule (8 characters) is not given to the first
// account, nor written over an existing one by a forced update; the
// account gets a made-up password instead and the log says why (L94).
func TestFirstAdminPasswordKeepsThePasswordRule(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		files := newMemFS()
		files.place(fileVector{Path: "/data", Dir: true})
		s, _, logs := testService(t, db, func(o *Options) {
			o.Settings = Settings{DefaultAdminUsername: "admin", DefaultAdminPassword: "short7!", DataDir: "/data"}
			o.FS = files
		})
		if created, err := s.EnsureDefaultAdmin(ctx); err != nil || !created {
			t.Fatalf("first account: %v, %v", created, err)
		}
		admin, err := getUserByUsername(ctx, db, "admin")
		if err != nil || admin == nil {
			t.Fatalf("admin: %v, %v", admin, err)
		}
		if ok, _ := s.VerifyPassword(ctx, "short7!", admin.PasswordHash.String); ok {
			t.Fatal("the first account got the 7-character password")
		}
		if !s.fs.Exists("/data/.admin_credentials") {
			t.Fatal("no made-up password in /data/.admin_credentials")
		}
		want := []string{"error", "[AUTH] The configured admin password is not used: Password must be at least 8 characters"}
		if lines := logs.take(); !slices.ContainsFunc(lines, func(line []string) bool { return slices.Equal(line, want) }) {
			t.Errorf("no line %q among %q", want[1], lines)
		}

		forced, _, _ := testService(t, db, func(o *Options) {
			o.Settings = Settings{DefaultAdminUsername: "admin", DefaultAdminPassword: "short7!", DataDir: "/data", ForcePasswordUpdate: true}
			o.FS = files
		})
		if _, err := forced.EnsureDefaultAdmin(ctx); err != nil {
			t.Fatal(err)
		}
		after, _ := getUserByUsername(ctx, db, "admin")
		if after.PasswordHash.String != admin.PasswordHash.String {
			t.Fatal("a forced update wrote the 7-character password")
		}
	})
}
