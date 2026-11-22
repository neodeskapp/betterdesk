package api

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/auth"
	"github.com/unitronix/betterdesk-server/db"
)

// TestAddressBookKeepsToPermissionsAndScope: saving an address book writes
// tags only onto devices in the caller's scope and only while its role
// holds device.edit as overrides decide it; reading one fills in the
// organization's stored passwords only for a caller with device.connect;
// the RustDesk device list follows an override that takes device.view away
// (RV185).
func TestAddressBookKeepsToPermissionsAndScope(t *testing.T) {
	srv := scopedFixture(t)
	database := srv.db
	key := map[string]string{"X-API-Key": testOperatorKey}
	tags := func(id string) string {
		t.Helper()
		p, err := database.GetPeer(id)
		if err != nil || p == nil {
			t.Fatalf("GetPeer %s: %v", id, err)
		}
		return p.Tags
	}

	ab := `{"data":"{\"tags\":[\"synced\"],\"peers\":[{\"id\":\"PEERAAA1\",\"tags\":[\"synced\"]},{\"id\":\"PEERBBB2\",\"tags\":[\"synced\"]}]}"}`
	if status, body := listenerCall(t, srv, http.MethodPost, "/api/ab", ab, key); status != http.StatusOK {
		t.Fatalf("POST /api/ab: %d %s", status, body)
	}
	if got := tags("PEERAAA1"); got != "rv" {
		t.Errorf("tags of the device outside the scope: %q, want rv", got)
	}
	if got := tags("PEERBBB2"); got != "synced" {
		t.Errorf("tags of the device in the scope: %q, want synced", got)
	}

	if status, body := listenerCall(t, srv, http.MethodPost, "/api/peers/list", "", key); status != http.StatusOK || !strings.Contains(body, "PEERBBB2") {
		t.Fatalf("POST /api/peers/list: %d %s, want PEERBBB2 listed", status, body)
	}
	for _, perm := range []string{auth.PermDeviceEdit, auth.PermDeviceView} {
		if err := database.SetRolePermission(auth.RoleOperator, perm, false); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.UpdatePeerTags("PEERBBB2", "rv"); err != nil {
		t.Fatal(err)
	}
	if status, body := listenerCall(t, srv, http.MethodPost, "/api/ab", ab, key); status != http.StatusOK {
		t.Fatalf("POST /api/ab without device.edit: %d %s", status, body)
	}
	if got := tags("PEERBBB2"); got != "rv" {
		t.Errorf("tags after a save by a role whose device.edit was taken away: %q, want rv", got)
	}
	if status, body := listenerCall(t, srv, http.MethodPost, "/api/peers/list", "", key); status != http.StatusOK || strings.Contains(body, "PEERBBB2") {
		t.Errorf("POST /api/peers/list without device.view: %d %s, want no devices", status, body)
	}
}

// TestAddressBookPasswordsNeedConnect: the organization's stored connect
// password lands in GET /api/ab only for a member that holds device.connect
// (RV185).
func TestAddressBookPasswordsNeedConnect(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	if err := srv.InitPeerCredentialVault("address-book-vault-test-secret-0123456789"); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateOrganization(&db.Organization{ID: "org-a", Name: "Acme", Slug: "acme", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	for _, u := range []*db.User{
		{Username: "rv_view", PasswordHash: "hash", Role: auth.RoleViewer},
		{Username: "rv_op", PasswordHash: "hash", Role: auth.RoleOperator},
	} {
		if err := database.CreateUser(u); err != nil {
			t.Fatal(err)
		}
		if _, err := database.LinkUserToOrg("org-a", u.ID, db.OrgRoleUser); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.SaveOrgAddressBook("org-a", "legacy", `{"tags":[],"peers":[{"id":"PEERBBB2","alias":"B"}]}`, "admin"); err != nil {
		t.Fatal(err)
	}
	if status, body := asConsole(t, srv, http.MethodPut, "/api/org/org-a/peer-credentials/PEERBBB2", `{"password":"stored-connect-pw"}`); status != http.StatusOK {
		t.Fatalf("store the connect password: %d %s", status, body)
	}

	_, body := callAs(t, srv, Identity{Username: "rv_op", Role: auth.RoleOperator}, http.MethodGet, "/api/ab", "")
	if !strings.Contains(body, "stored-connect-pw") {
		t.Errorf("GET /api/ab of an operator: %s, want the stored password", body)
	}
	_, body = callAs(t, srv, Identity{Username: "rv_view", Role: auth.RoleViewer}, http.MethodGet, "/api/ab", "")
	if strings.Contains(body, "stored-connect-pw") || !strings.Contains(body, "PEERBBB2") {
		t.Errorf("GET /api/ab of a viewer: %s, want the entry without the password", body)
	}
}
