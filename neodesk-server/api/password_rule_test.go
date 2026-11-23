package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
	"github.com/neodeskapp/neodesk/neodesk-server/db"
)

// TestAccountRoutesKeepThePasswordRule: the API's account and
// organization account routes refuse a new password under 8 characters
// and change nothing, and take any password of 8 (L94).
func TestAccountRoutesKeepThePasswordRule(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	handler := srv.InternalHandler()
	call := func(method, path, body string) (int, string) {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "127.0.0.1:50000"
		req = req.WithContext(WithIdentity(req.Context(), Identity{Username: "apikey:console-bridge", Role: auth.RoleAdmin}))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code, strings.TrimSpace(rec.Body.String())
	}
	const tooShort = `{"error":"Password must be at least 8 characters"}`

	if status, body := call(http.MethodPost, "/api/users", `{"username":"rule_short","password":"sixsix7"}`); status != http.StatusBadRequest || body != tooShort {
		t.Errorf("create with 7 characters: %d %s", status, body)
	}
	if u, _ := database.GetUser("rule_short"); u != nil {
		t.Error("the account with a 7-character password was created")
	}
	if status, body := call(http.MethodPost, "/api/users", `{"username":"rule_ok","password":"alllower"}`); status != http.StatusCreated {
		t.Fatalf("create with 8 lower-case letters: %d %s", status, body)
	}
	user, err := database.GetUser("rule_ok")
	if err != nil || user == nil {
		t.Fatalf("rule_ok: %v, %v", user, err)
	}
	if status, body := call(http.MethodPut, "/api/users/"+itoa(uint64(user.ID)), `{"password":"sixsix7"}`); status != http.StatusBadRequest || body != tooShort {
		t.Errorf("change to 7 characters: %d %s", status, body)
	}
	if after, _ := database.GetUser("rule_ok"); after.PasswordHash != user.PasswordHash {
		t.Error("the refused password was stored")
	}

	if err := database.CreateOrganization(&db.Organization{ID: "org-rule", Name: "Rule", Slug: "rule", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if status, body := call(http.MethodPost, "/api/org/org-rule/users", `{"username":"org_short","password":"sixsix7"}`); status != http.StatusBadRequest || body != tooShort {
		t.Errorf("organization account with 7 characters: %d %s", status, body)
	}
	if u, _ := database.GetOrgUserByUsername("org-rule", "org_short"); u != nil {
		t.Error("the organization account with a 7-character password was created")
	}
	orgUser := &db.OrgUser{ID: "ou-rule", OrgID: "org-rule", Username: "org_ok", Role: "user", PasswordHash: mustHash(t, "org-pass-1"), CreatedAt: time.Now().UTC()}
	if err := database.CreateOrgUser(orgUser); err != nil {
		t.Fatal(err)
	}
	if status, body := call(http.MethodPut, "/api/org/org-rule/users/ou-rule", `{"password":"sixsix7","display_name":"Changed"}`); status != http.StatusBadRequest || body != tooShort {
		t.Errorf("organization account changed to 7 characters: %d %s", status, body)
	}
	if after, _ := database.GetOrgUser("ou-rule"); after.PasswordHash != orgUser.PasswordHash || after.DisplayName == "Changed" {
		t.Error("the refused change was stored")
	}
}
