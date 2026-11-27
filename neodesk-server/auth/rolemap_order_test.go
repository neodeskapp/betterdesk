package auth

import "testing"

// WD-33: server_admin and global_admin share a level. A person in two
// mapped groups with those roles gets the role of the entry that comes
// first in the map, at every sign-in, for LDAP and single sign-on alike.
func TestRoleMapTieGoesToTheFirstEntry(t *testing.T) {
	for _, c := range []struct{ raw, want string }{
		{"CN=Servers,DC=ex,DC=com=server_admin|CN=Global,DC=ex,DC=com=global_admin", RoleServerAdmin},
		{"CN=Global,DC=ex,DC=com=global_admin\nCN=Servers,DC=ex,DC=com=server_admin", RoleGlobalAdmin},
	} {
		entries := parseGroupRoleEntries(c.raw)
		groups := []string{"CN=Global,DC=ex,DC=com", "CN=Servers,DC=ex,DC=com"}
		oidc := &OIDCProvider{config: &OIDCConfig{GroupRoleMap: "Servers=server_admin|Global=global_admin", DefaultRole: RoleViewer}}
		if c.want == RoleGlobalAdmin {
			oidc.config.GroupRoleMap = "Global=global_admin|Servers=server_admin"
		}
		for i := 0; i < 50; i++ {
			if role, ok := ldapMappedRole(entries, groups, nil); !ok || role != c.want {
				t.Fatalf("LDAP %q, sign-in %d: %q, want %q", c.raw, i, role, c.want)
			}
			if role := oidc.resolveRole([]string{"Global", "Servers"}); role != c.want {
				t.Fatalf("SSO %q, sign-in %d: %q, want %q", oidc.config.GroupRoleMap, i, role, c.want)
			}
		}
	}
	// A higher role still wins wherever its entry is
	entries := parseGroupRoleEntries("CN=Ops,DC=ex=operator|CN=Admins,DC=ex=super_admin")
	if role, _ := ldapMappedRole(entries, []string{"CN=Ops,DC=ex", "CN=Admins,DC=ex"}, nil); role != RoleSuperAdmin {
		t.Fatalf("higher role: %q", role)
	}
	// A group named twice keeps its last role at the place of its first
	entries = parseGroupRoleEntries("A=viewer|B=operator|A=admin")
	if len(entries) != 2 || entries[0] != (groupRole{"A", "admin"}) || entries[1] != (groupRole{"B", "operator"}) {
		t.Fatalf("entries %+v", entries)
	}
}
