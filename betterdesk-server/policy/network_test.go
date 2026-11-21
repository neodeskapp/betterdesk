package policy

import (
	"path/filepath"
	"testing"

	"github.com/unitronix/betterdesk-server/db"
)

func TestNetworkResolverForceRelay(t *testing.T) {
	database, err := db.OpenSQLite(filepath.Join(t.TempDir(), "policy-test.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := database.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	if err := database.CreateOrganization(&db.Organization{ID: "org1", Name: "Test Org"}); err != nil {
		t.Fatalf("CreateOrganization: %v", err)
	}
	if err := database.SetOrgSetting("org1", networkPolicyKey, `{"block_direct_p2p":true}`); err != nil {
		t.Fatalf("SetOrgSetting: %v", err)
	}
	if err := database.AssignDeviceToOrg(&db.OrgDevice{OrgID: "org1", DeviceID: "device123"}); err != nil {
		t.Fatalf("AssignDeviceToOrg: %v", err)
	}

	r := NewNetworkResolver(database)
	if !r.ShouldForceRelay("device123") {
		t.Fatal("expected block_direct_p2p to force relay")
	}
	if r.ShouldForceRelay("unknown") {
		t.Fatal("unknown device should not force relay")
	}
}

func TestNetworkResolverAllowedRelayServers(t *testing.T) {
	database, err := db.OpenSQLite(filepath.Join(t.TempDir(), "policy-relay.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := database.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	if err := database.CreateOrganization(&db.Organization{ID: "org2", Name: "Relay Org"}); err != nil {
		t.Fatalf("CreateOrganization: %v", err)
	}
	if err := database.SetOrgSetting("org2", networkPolicyKey, `{"allowed_relay_servers":["relay.example.com:21117"]}`); err != nil {
		t.Fatalf("SetOrgSetting: %v", err)
	}
	if err := database.AssignDeviceToOrg(&db.OrgDevice{OrgID: "org2", DeviceID: "devA"}); err != nil {
		t.Fatalf("AssignDeviceToOrg: %v", err)
	}

	r := NewNetworkResolver(database)
	got := r.ResolveRelay("198.51.100.1:21117", "devA")
	if got != "relay.example.com:21117" {
		t.Fatalf("ResolveRelay = %q, want org allowlist entry", got)
	}
}

func TestRelayAllowedComparesHostAndPortExactly(t *testing.T) {
	cases := []struct {
		entry, relay string
		want         bool
	}{
		{"relay.corp", "relay.corp:21117", true},
		{"relay.corp", "RELAY.corp", true},
		{"relay.corp", "relay.corp.attacker.net:21117", false},
		{"relay.corp:21117", "relay.corp:21117", true},
		{"relay.corp:21117", "relay.corp:211170", false},
		{"relay.corp:21117", "relay.corp:21118", false},
		{"relay.corp:21117", "relay.corp", false},
		{"[2001:db8::1]:21117", "[2001:db8::1]:21117", true},
		{"2001:db8::1", "[2001:db8::1]:21117", true},
		{"10.0.0.1", "10.0.0.10:21117", false},
	}
	for _, tc := range cases {
		if got := relayAllowed(tc.entry, tc.relay); got != tc.want {
			t.Errorf("relayAllowed(%q, %q) = %v, want %v", tc.entry, tc.relay, got, tc.want)
		}
	}
}

func TestResolveRelayRefusesHostThatOnlyStartsWithAllowedOne(t *testing.T) {
	database, err := db.OpenSQLite(filepath.Join(t.TempDir(), "policy-prefix.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := database.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := database.CreateOrganization(&db.Organization{ID: "org3", Name: "Prefix Org"}); err != nil {
		t.Fatal(err)
	}
	if err := database.SetOrgSetting("org3", networkPolicyKey, `{"allowed_relay_servers":["relay.corp"]}`); err != nil {
		t.Fatal(err)
	}
	if err := database.AssignDeviceToOrg(&db.OrgDevice{OrgID: "org3", DeviceID: "devP"}); err != nil {
		t.Fatal(err)
	}

	r := NewNetworkResolver(database)
	if got := r.ResolveRelay("relay.corp.attacker.net:21117", "devP"); got != "relay.corp" {
		t.Fatalf("ResolveRelay = %q, want the allowlist entry instead of the look-alike host", got)
	}
	if got := r.ResolveRelay("relay.corp:21117", "devP"); got != "relay.corp:21117" {
		t.Fatalf("ResolveRelay = %q, want the allowed relay kept", got)
	}
}
