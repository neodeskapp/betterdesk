package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
	"github.com/neodeskapp/neodesk/neodesk-server/config"
	"github.com/neodeskapp/neodesk/neodesk-server/db"
	"github.com/neodeskapp/neodesk/neodesk-server/peer"
)

func TestAssignUserToOrgAcceptsNumericOrgID(t *testing.T) {
	cfg := config.DefaultConfig()
	database := testSetupDB(t)
	defer database.Close()

	if err := database.CreateOrganization(&db.Organization{
		ID:        "42",
		Name:      "Acme",
		Slug:      "acme",
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	user := &db.User{Username: "alice", PasswordHash: "hash", Role: "operator"}
	if err := database.CreateUser(user); err != nil {
		t.Fatal(err)
	}

	cfg.APIPort = 19887
	srv := New(cfg, database, peer.NewMap(), nil, "test")
	if err := srv.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()
	time.Sleep(100 * time.Millisecond)

	body := bytes.NewBufferString(`{"org_id":42,"role":"operator"}`)
	req, err := http.NewRequest("POST", fmt.Sprintf("http://127.0.0.1:%d/api/users/%d/organizations", cfg.APIPort, user.ID), body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	testAuthReq(req)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d", resp.StatusCode)
	}

	var orgUser db.OrgUser
	if err := json.NewDecoder(resp.Body).Decode(&orgUser); err != nil {
		t.Fatal(err)
	}
	if orgUser.OrgID != "42" || orgUser.ServerUserID != user.ID || orgUser.Role != db.OrgRoleUser {
		t.Fatalf("unexpected org user: %+v", orgUser)
	}
}

// TestOrgPolicyAndInviteOfAnUnknownOrgAre404: saving a policy of an
// organization that does not exist, or inviting into one, is answered 404,
// not as a server error; a known organization works as before (BUGS C5).
func TestOrgPolicyAndInviteOfAnUnknownOrgAre404(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	if err := database.CreateOrganization(&db.Organization{ID: "org-a", Name: "Acme", Slug: "acme", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		method, path, body string
		want               int
	}{
		{http.MethodPut, "/api/org/no-such-org/policy/connection", `{"x":1}`, http.StatusNotFound},
		{http.MethodPost, "/api/org/no-such-org/invite", `{"email":"a@example.org"}`, http.StatusNotFound},
		{http.MethodPut, "/api/org/no-such-org/policy/nonsense", `{"x":1}`, http.StatusBadRequest},
		{http.MethodPut, "/api/org/org-a/policy/connection", `{"x":1}`, http.StatusOK},
		{http.MethodPost, "/api/org/org-a/invite", `{"email":"a@example.org"}`, http.StatusCreated},
	} {
		if status, body := asConsole(t, srv, c.method, c.path, c.body); status != c.want {
			t.Errorf("%s %s: %d %s, want %d", c.method, c.path, status, body, c.want)
		}
	}
}

// TestListOrganizationsSuperAdminSeesAll: the organization list skips the
// membership filter for both super admin roles, admin and super_admin;
// other roles see the organizations they belong to (BUGS G15).
func TestListOrganizationsSuperAdminSeesAll(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	for _, org := range []*db.Organization{
		{ID: "org-a", Name: "Acme", Slug: "acme", CreatedAt: time.Now().UTC()},
		{ID: "org-b", Name: "Beta", Slug: "beta", CreatedAt: time.Now().UTC()},
	} {
		if err := database.CreateOrganization(org); err != nil {
			t.Fatal(err)
		}
	}
	member := &db.User{Username: "member", PasswordHash: "hash", Role: auth.RoleOperator}
	if err := database.CreateUser(member); err != nil {
		t.Fatal(err)
	}
	if _, err := database.LinkUserToOrg("org-b", member.ID, "operator"); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		id   Identity
		want string
	}{
		{Identity{Username: "root", Role: auth.RoleSuperAdmin}, "org-a,org-b"},
		{Identity{Username: "boss", Role: auth.RoleAdmin}, "org-a,org-b"},
		{Identity{Username: "member", Role: auth.RoleOperator}, "org-b"},
	} {
		status, body := callAs(t, srv, c.id, http.MethodGet, "/api/org", "")
		var list struct {
			Organizations []db.Organization `json:"organizations"`
		}
		if err := json.Unmarshal([]byte(body), &list); status != http.StatusOK || err != nil {
			t.Fatalf("list as %s: %d %s", c.id.Role, status, body)
		}
		var ids []string
		for _, org := range list.Organizations {
			ids = append(ids, org.ID)
		}
		sort.Strings(ids)
		if got := strings.Join(ids, ","); got != c.want {
			t.Errorf("organizations of %s: %q, want %q", c.id.Role, got, c.want)
		}
	}
}

func TestListUserOrganizationsIncludesMembershipShape(t *testing.T) {
	cfg := config.DefaultConfig()
	database := testSetupDB(t)
	defer database.Close()

	orgs := []db.Organization{
		{ID: "org-a", Name: "Acme", Slug: "acme", CreatedAt: time.Now().UTC()},
		{ID: "org-b", Name: "Beta", Slug: "beta", CreatedAt: time.Now().UTC()},
	}
	for i := range orgs {
		if err := database.CreateOrganization(&orgs[i]); err != nil {
			t.Fatal(err)
		}
	}

	user := &db.User{Username: "alice", PasswordHash: "hash", Role: "operator"}
	if err := database.CreateUser(user); err != nil {
		t.Fatal(err)
	}
	if _, err := database.LinkUserToOrg("org-a", user.ID, "operator"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.LinkUserToOrg("org-b", user.ID, "admin"); err != nil {
		t.Fatal(err)
	}

	cfg.APIPort = 19888
	srv := New(cfg, database, peer.NewMap(), nil, "test")
	if err := srv.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()
	time.Sleep(100 * time.Millisecond)

	req, err := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d/api/users/%d/organizations", cfg.APIPort, user.ID), nil)
	if err != nil {
		t.Fatal(err)
	}
	testAuthReq(req)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	var body struct {
		Organizations []struct {
			ID      string `json:"id"`
			OrgID   string `json:"org_id"`
			Name    string `json:"name"`
			OrgName string `json:"org_name"`
			Role    string `json:"role"`
		} `json:"organizations"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}

	if len(body.Organizations) != 2 {
		t.Fatalf("expected 2 organizations, got %d", len(body.Organizations))
	}
	rolesByOrg := map[string]string{}
	for _, org := range body.Organizations {
		if org.ID == "" || org.OrgID == "" || org.Name == "" || org.OrgName == "" {
			t.Fatalf("missing compatibility fields in org response: %+v", org)
		}
		rolesByOrg[org.OrgID] = org.Role
	}
	if rolesByOrg["org-a"] != "operator" || rolesByOrg["org-b"] != "admin" {
		t.Fatalf("unexpected org roles: %+v", rolesByOrg)
	}
}
