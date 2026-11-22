package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/db"
)

// Organization roles change nothing for the console's accounts, so the
// routes no longer store one: a new organization account, a linked server
// account and an invitation get the default role whatever the request
// names, and a change of the role is ignored. A role stored before stays
// as it is and is still read (BUGS L119).
func TestOrganizationRolesAreNotStored(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	if err := database.CreateOrganization(&db.Organization{ID: "org-r", Name: "Roles", Slug: "roles", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"linked", "assigned"} {
		if err := database.CreateUser(&db.User{Username: name, PasswordHash: "x", Role: "operator"}); err != nil {
			t.Fatal(err)
		}
	}
	linked, _ := database.GetUser("linked")
	assigned, _ := database.GetUser("assigned")
	if err := database.CreateOrgUser(&db.OrgUser{ID: "ou-old", OrgID: "org-r", Username: "old-owner", Role: db.OrgRoleOwner, PasswordHash: "x", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}

	role := func(what string, status int, body string, wantStatus int) {
		t.Helper()
		var out struct {
			Role string `json:"role"`
		}
		if err := json.Unmarshal([]byte(body), &out); err != nil || status != wantStatus || out.Role != db.OrgRoleUser {
			t.Errorf("%s: %d %s, want %d with role user", what, status, body, wantStatus)
		}
	}
	status, body := asConsole(t, srv, http.MethodPost, "/api/org/org-r/users", `{"username":"newbie","password":"long-enough-1","role":"owner"}`)
	role("a new organization account named owner", status, body, http.StatusCreated)
	status, body = asConsole(t, srv, http.MethodPost, "/api/org/org-r/members", `{"user_id":`+strconv.FormatInt(linked.ID, 10)+`,"role":"admin"}`)
	role("a linked server account named admin", status, body, http.StatusCreated)
	status, body = asConsole(t, srv, http.MethodPost, "/api/users/"+strconv.FormatInt(assigned.ID, 10)+"/organizations", `{"org_id":"org-r","role":"operator"}`)
	role("a server account added from the Users page as operator", status, body, http.StatusCreated)
	status, body = asConsole(t, srv, http.MethodPost, "/api/org/org-r/invite", `{"role":"admin","expires_in_hours":1}`)
	role("an invitation named admin", status, body, http.StatusCreated)
	status, body = asConsole(t, srv, http.MethodPut, "/api/org/org-r/users/ou-old", `{"role":"user","display_name":"Old"}`)
	if status != http.StatusOK || !strings.Contains(body, `"role":"owner"`) || !strings.Contains(body, `"display_name":"Old"`) {
		t.Errorf("a change of role: %d %s, want the old role kept and the name changed", status, body)
	}

	users, err := database.ListOrgUsers("org-r")
	if err != nil {
		t.Fatal(err)
	}
	stored := map[string]string{}
	for _, u := range users {
		stored[u.Username] = u.Role
	}
	for name, want := range map[string]string{"newbie": "user", "linked": "user", "assigned": "user", "old-owner": "owner"} {
		if stored[name] != want {
			t.Errorf("%s has the stored role %q, want %q", name, stored[name], want)
		}
	}
	invitations, err := database.ListOrgInvitations("org-r")
	if err != nil || len(invitations) != 1 || invitations[0].Role != db.OrgRoleUser {
		t.Errorf("invitations %+v (%v), want one with role user", invitations, err)
	}
}

// The connection settings of an organization (policy, file transfer,
// clipboard, session length) were stored and applied nowhere; the settings
// route refuses them now and keeps every other key (BUGS L119).
func TestOrganizationConnectionSettingsAreRefused(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	if err := database.CreateOrganization(&db.Organization{ID: "org-s", Name: "Settings", Slug: "settings", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"connection_policy", "allow_file_transfer", "allow_clipboard", "max_session_duration_min"} {
		if status, body := asConsole(t, srv, http.MethodPut, "/api/org/org-s/settings", `{"key":"`+key+`","value":"1"}`); status != http.StatusBadRequest {
			t.Errorf("%s: %d %s, want 400", key, status, body)
		}
	}
	if status, body := asConsole(t, srv, http.MethodPut, "/api/org/org-s/settings", `{"key":"timezone","value":"Europe/Berlin"}`); status != http.StatusOK {
		t.Errorf("timezone: %d %s, want 200", status, body)
	}
	settings, err := database.ListOrgSettings("org-s")
	if err != nil || len(settings) != 1 || settings[0].Key != "timezone" {
		t.Errorf("stored settings %+v (%v), want timezone only", settings, err)
	}
}
