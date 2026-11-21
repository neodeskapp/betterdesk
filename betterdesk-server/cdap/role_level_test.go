package cdap

import (
	"testing"
	"time"
)

// RoleLevel orders every role auth knows the way auth does, so a super
// admin counts as admin and not as an unknown role (RV188).
func TestRoleLevelKnowsTheRolesOfAuth(t *testing.T) {
	for _, tc := range []struct {
		low, high string
	}{
		{"viewer", "operator"},
		{"operator", "global_admin"},
		{"operator", "server_admin"},
		{"global_admin", "admin"},
		{"global_admin", "super_admin"},
		{"pro", "viewer"},
		{"device", "viewer"},
	} {
		if RoleLevel(tc.low) >= RoleLevel(tc.high) {
			t.Errorf("level of %s (%d) is not below %s (%d)", tc.low, RoleLevel(tc.low), tc.high, RoleLevel(tc.high))
		}
	}
	if RoleLevel("super_admin") != RoleLevel("admin") {
		t.Errorf("super_admin %d, admin %d: want the same level", RoleLevel("super_admin"), RoleLevel("admin"))
	}
	if RoleLevel("guest_issuer") != RoleLevel("viewer") {
		t.Errorf("guest_issuer %d, viewer %d: want the same level", RoleLevel("guest_issuer"), RoleLevel("viewer"))
	}
	if RoleLevel("nosuchrole") != 0 {
		t.Errorf("unknown role: level %d, want 0", RoleLevel("nosuchrole"))
	}

	terminal := &Widget{Type: "terminal", ID: "t"}
	toggle := &Widget{Type: "toggle", ID: "s"}
	for _, tc := range []struct {
		role, action string
		w            *Widget
		want         bool
	}{
		{"super_admin", "execute", terminal, true},
		{"admin", "execute", terminal, true},
		{"global_admin", "execute", terminal, false},
		{"global_admin", "set", toggle, true},
		{"operator", "set", toggle, true},
		{"viewer", "set", toggle, false},
		{"guest_issuer", "query", toggle, true},
		{"pro", "query", toggle, false},
	} {
		if got := CheckWidgetPermission(tc.role, tc.action, tc.w); got != tc.want {
			t.Errorf("%s %s on %s: %v, want %v", tc.role, tc.action, tc.w.Type, got, tc.want)
		}
	}
}

// The effective role of several delegations is the highest one granted.
func TestEffectiveRoleIsTheHighestDelegation(t *testing.T) {
	ds := NewDelegationStore()
	later := time.Now().Add(time.Hour)
	for i, role := range []string{"operator", "admin", "operator"} {
		if err := ds.Add(&Delegation{ID: string(rune('a' + i)), Grantee: "vera", DeviceID: "CDAP1", Role: role, ExpiresAt: later}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 20; i++ {
		if got := ds.GetEffectiveRole("vera", "CDAP1", ""); got != "admin" {
			t.Fatalf("effective role %q, want admin", got)
		}
	}
	if got := ds.GetEffectiveRole("vera", "OTHER", ""); got != "" {
		t.Errorf("other device: %q, want none", got)
	}
}
