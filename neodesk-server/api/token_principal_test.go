package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
	"github.com/neodeskapp/neodesk/neodesk-server/db"
)

// listenerCall sends a request through ListenerHandler, as the API port
// serves it, from a public address, and returns the status and the body.
func listenerCall(t *testing.T, srv *Server, method, path, body string, header map[string]string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "203.0.113.7:40000"
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	srv.ListenerHandler().ServeHTTP(rec, req)
	data, err := io.ReadAll(rec.Result().Body)
	if err != nil {
		t.Fatal(err)
	}
	return rec.Code, strings.TrimSpace(string(data))
}

func bearer(token string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + token}
}

func mustHash(t *testing.T, password string) string {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	return string(hash)
}

// TestOrgTokenIsNoServerRole: an organization sign-in answers a token that
// reaches its own organization's routes and nothing a server role guards,
// whatever the organization role, and it never stands for the server
// account of the same name (RV167).
func TestOrgTokenIsNoServerRole(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	srv.SetJWTManager(auth.NewJWTManager("org-token-test-secret", time.Hour))
	for _, org := range []*db.Organization{
		{ID: "org-a", Name: "Acme", Slug: "acme", CreatedAt: time.Now().UTC()},
		{ID: "org-b", Name: "Beta", Slug: "beta", CreatedAt: time.Now().UTC()},
	} {
		if err := database.CreateOrganization(org); err != nil {
			t.Fatal(err)
		}
	}
	for _, u := range []*db.OrgUser{
		{ID: "ou-1", OrgID: "org-a", Username: "orgboss", Role: "admin", PasswordHash: mustHash(t, "boss-pass-1"), CreatedAt: time.Now().UTC()},
		{ID: "ou-2", OrgID: "org-a", Username: "victim", Role: "user", PasswordHash: mustHash(t, "victim-pass-1"), CreatedAt: time.Now().UTC()},
		{ID: "ou-3", OrgID: "org-b", Username: "orgboss", Role: "user", PasswordHash: mustHash(t, "other-pass-1"), CreatedAt: time.Now().UTC()},
	} {
		if err := database.CreateOrgUser(u); err != nil {
			t.Fatal(err)
		}
	}
	server := &db.User{Username: "victim", PasswordHash: mustHash(t, "server-pass-1"), Role: auth.RoleOperator}
	if err := database.CreateUser(server); err != nil {
		t.Fatal(err)
	}
	if err := database.SaveAddressBook("victim", "legacy", `{"tags":["server-account-secret"],"peers":[]}`); err != nil {
		t.Fatal(err)
	}

	login := func(user, pass string) string {
		t.Helper()
		status, body := listenerCall(t, srv, http.MethodPost, "/api/org/login",
			`{"org_id":"org-a","username":"`+user+`","password":"`+pass+`"}`, nil)
		var out struct {
			Token string `json:"token"`
		}
		if err := json.Unmarshal([]byte(body), &out); status != http.StatusOK || err != nil || out.Token == "" {
			t.Fatalf("org login %s: %d %s", user, status, body)
		}
		return out.Token
	}
	boss := login("orgboss", "boss-pass-1")
	victim := login("victim", "victim-pass-1")

	for _, c := range []struct {
		token, method, path, body string
		want                      int
	}{
		{boss, http.MethodGet, "/api/keys", "", http.StatusForbidden},
		{boss, http.MethodPost, "/api/keys", `{"name":"x","role":"super_admin"}`, http.StatusForbidden},
		{boss, http.MethodGet, "/api/tokens", "", http.StatusForbidden},
		{boss, http.MethodPost, "/api/users", `{"username":"evil","password":"evil-pass-1","role":"super_admin"}`, http.StatusForbidden},
		{boss, http.MethodGet, "/api/peers", "", http.StatusForbidden},
		{boss, http.MethodGet, "/api/org/org-a", "", http.StatusOK},
		{boss, http.MethodGet, "/api/org/org-b", "", http.StatusForbidden},
		{boss, http.MethodGet, "/api/org/org-b/users", "", http.StatusForbidden},
		{victim, http.MethodGet, "/api/auth/me", "", http.StatusUnauthorized},
	} {
		if status, body := listenerCall(t, srv, c.method, c.path, c.body, bearer(c.token)); status != c.want {
			t.Errorf("%s %s with an org token: %d %s, want %d", c.method, c.path, status, body, c.want)
		}
	}

	status, body := listenerCall(t, srv, http.MethodGet, "/api/ab", "", bearer(victim))
	if strings.Contains(body, "server-account-secret") {
		t.Errorf("an org user named like a server account read its address book: %d %s", status, body)
	}

	// An organization sign-in reads its organization and the address book
	// only, also one whose stored organization role is admin (A169).
	for name, token := range map[string]string{"an org user": victim, "an org admin": boss} {
		for _, path := range []string{"/api/org/org-a/users", "/api/org/org-a/invitations"} {
			if status, body := listenerCall(t, srv, http.MethodGet, path, "", bearer(token)); status != http.StatusForbidden {
				t.Errorf("%s with the token of %s: %d %s, want 403", path, name, status, body)
			}
		}
	}

	status, body = listenerCall(t, srv, http.MethodGet, "/api/org", "", bearer(boss))
	if status != http.StatusOK || !strings.Contains(body, `"org-a"`) || strings.Contains(body, `"org-b"`) {
		t.Errorf("org list with an org token: %d %s, want org-a only", status, body)
	}

	if err := database.DeleteOrgUser("ou-1"); err != nil {
		t.Fatal(err)
	}
	if status, body := listenerCall(t, srv, http.MethodGet, "/api/org/org-a", "", bearer(boss)); status != http.StatusUnauthorized {
		t.Errorf("token of a deleted org user: %d %s, want 401", status, body)
	}
}
