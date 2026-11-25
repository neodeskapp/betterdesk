package db

import (
	"sort"
	"strings"
	"testing"
)

// TestLegacyChatPermissionOverridesAreRenamed: a role override for
// chat.access (before A242) becomes the same override for chat.support and
// chat.team at the next start, so a role keeps exactly what it had; an
// override the role already has for one of them stays as it is.
func TestLegacyChatPermissionOverridesAreRenamed(t *testing.T) {
	panelGroupStores(t, func(t *testing.T, database Database) {
		suffix := strings.ReplaceAll(t.Name(), "/", "-")
		denied, granted := "a242-denied-"+suffix, "a242-granted-"+suffix
		t.Cleanup(func() {
			for _, role := range []string{denied, granted} {
				for _, p := range []string{"chat.access", "chat.support", "chat.team"} {
					_ = database.DeleteRolePermission(role, p)
				}
			}
		})
		for _, o := range []struct {
			role, perm string
			granted    bool
		}{{denied, "chat.access", false}, {granted, "chat.access", true}, {granted, "chat.team", false}} {
			if err := database.SetRolePermission(o.role, o.perm, o.granted); err != nil {
				t.Fatal(err)
			}
		}
		if err := database.Migrate(); err != nil {
			t.Fatal(err)
		}
		read := func(role string) string {
			list, err := database.ListRolePermissions(role)
			if err != nil {
				t.Fatal(err)
			}
			var out []string
			for _, o := range list {
				v := "deny"
				if o.Granted {
					v = "grant"
				}
				out = append(out, o.Permission+"="+v)
			}
			sort.Strings(out)
			return strings.Join(out, " ")
		}
		if got, want := read(denied), "chat.support=deny chat.team=deny"; got != want {
			t.Errorf("a role without chat: %q, want %q", got, want)
		}
		if got, want := read(granted), "chat.support=grant chat.team=deny"; got != want {
			t.Errorf("a role with chat and its own Team chat override: %q, want %q", got, want)
		}
	})
}
