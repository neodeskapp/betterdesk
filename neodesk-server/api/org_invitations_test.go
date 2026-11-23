package api

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
	"github.com/neodeskapp/neodesk/neodesk-server/db"
)

// An invitation end to end, as the console calls the API server: its code
// is in the answer that creates it and nowhere else (the table keeps a
// hash, the list leaves it out); lookup names the organization of a usable
// code; redeeming makes an organization sign-in that signs in at
// /api/org/login and uses the invitation once; a refused username or
// password uses nothing; revoking stops an open invitation and keeps a
// used one; an invitation lives 1 to 720 hours; and the two redeem routes
// answer only the console (BUGS L42).
func TestOrganizationInvitationIsRedeemedOnce(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	srv.SetJWTManager(auth.NewJWTManager("org-invitation-test-secret", time.Hour))
	if err := database.CreateOrganization(&db.Organization{ID: "org-i", Name: "Invites", Slug: "invites", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	invite := func() (id, code string) {
		t.Helper()
		status, body := asConsole(t, srv, http.MethodPost, "/api/org/org-i/invite", `{"email":"guest@example.invalid"}`)
		var out struct {
			ID    string `json:"id"`
			Token string `json:"token"`
		}
		if err := json.Unmarshal([]byte(body), &out); err != nil || status != http.StatusCreated || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(out.Token) {
			t.Fatalf("invite: %d %s", status, body)
		}
		return out.ID, out.Token
	}
	call := func(path, body string) (int, string) {
		t.Helper()
		return asConsole(t, srv, http.MethodPost, path, body)
	}
	redeem := func(code, username, password string) (int, string) {
		t.Helper()
		b, _ := json.Marshal(map[string]string{"token": code, "username": username, "password": password})
		return call("/api/org-invitations/redeem", string(b))
	}
	lookup := func(code string) int {
		t.Helper()
		status, _ := call("/api/org-invitations/lookup", `{"token":"`+code+`"}`)
		return status
	}

	firstID, first := invite()
	invitations, _ := database.ListOrgInvitations("org-i")
	if len(invitations) != 1 || invitations[0].Token == first || invitations[0].Token != invitationHash(first) {
		t.Fatalf("stored invitation %+v: want the code's hash", invitations)
	}
	if status, body := asConsole(t, srv, http.MethodGet, "/api/org/org-i/invitations", ""); status != http.StatusOK || strings.Contains(body, `"token"`) {
		t.Errorf("the list: %d %s, want no token", status, body)
	}
	if status, body := call("/api/org-invitations/lookup", `{"token":"`+first+`"}`); status != http.StatusOK || !strings.Contains(body, `"org_name":"Invites"`) || !strings.Contains(body, `"org_slug":"invites"`) {
		t.Errorf("lookup: %d %s", status, body)
	}
	for _, code := range []string{invitationHash(first), "nope", strings.Repeat("0", 64), ""} {
		if status := lookup(code); status != http.StatusNotFound {
			t.Errorf("lookup of %q: %d, want 404", code, status)
		}
	}

	for _, c := range []struct{ username, password string }{{"anna", "short"}, {"a", "long-enough-1"}, {"anna smith", "long-enough-1"}} {
		if status, body := redeem(first, c.username, c.password); status != http.StatusBadRequest {
			t.Errorf("redeem as %q with %q: %d %s, want 400", c.username, c.password, status, body)
		}
	}
	if status := lookup(first); status != http.StatusOK {
		t.Errorf("a refused redemption used the invitation: lookup %d", status)
	}
	status, body := redeem(first, "anna", "long-enough-1")
	if status != http.StatusCreated || !strings.Contains(body, `"username":"anna"`) || !strings.Contains(body, `"email":"guest@example.invalid"`) || !strings.Contains(body, `"slug":"invites"`) {
		t.Fatalf("redeem: %d %s", status, body)
	}
	if status, _ := orgSignIn(t, srv, "org-i", "anna", "long-enough-1"); status != http.StatusOK {
		t.Errorf("sign-in with the redeemed account: %d", status)
	}
	if status, body := redeem(first, "bert", "long-enough-1"); status != http.StatusNotFound {
		t.Errorf("redeem again: %d %s, want 404", status, body)
	}
	if status := lookup(first); status != http.StatusNotFound {
		t.Errorf("lookup of a used invitation: %d, want 404", status)
	}

	secondID, second := invite()
	if status, body := redeem(second, "anna", "long-enough-1"); status != http.StatusConflict {
		t.Errorf("redeem with a taken username: %d %s, want 409", status, body)
	}
	if status := lookup(second); status != http.StatusOK {
		t.Errorf("a taken username used the invitation: lookup %d", status)
	}
	if status, body := asConsole(t, srv, http.MethodDelete, "/api/org/org-i/invitations/"+secondID, ""); status != http.StatusNoContent {
		t.Errorf("revoke: %d %s", status, body)
	}
	if status := lookup(second); status != http.StatusNotFound {
		t.Errorf("lookup of a revoked invitation: %d, want 404", status)
	}
	if status, body := asConsole(t, srv, http.MethodDelete, "/api/org/org-i/invitations/"+secondID, ""); status != http.StatusNotFound {
		t.Errorf("revoke again: %d %s, want 404", status, body)
	}
	if status, body := asConsole(t, srv, http.MethodDelete, "/api/org/org-i/invitations/"+firstID, ""); status != http.StatusConflict {
		t.Errorf("revoke a used invitation: %d %s, want 409", status, body)
	}

	for _, hours := range []string{"-1", "721"} {
		if status, body := asConsole(t, srv, http.MethodPost, "/api/org/org-i/invite", `{"expires_in_hours":`+hours+`}`); status != http.StatusBadRequest {
			t.Errorf("an invitation for %s hours: %d %s, want 400", hours, status, body)
		}
	}
	_, third := invite()
	if status, body := listenerCall(t, srv, http.MethodPost, "/api/org-invitations/lookup", `{"token":"`+third+`"}`, map[string]string{"X-API-Key": testAPIKey}); status != http.StatusForbidden {
		t.Errorf("lookup over the listener with an admin key: %d %s, want 403", status, body)
	}
	if status, body := listenerCall(t, srv, http.MethodPost, "/api/org-invitations/redeem", `{"token":"`+third+`","username":"carl","password":"long-enough-1"}`, map[string]string{"X-API-Key": testAPIKey}); status != http.StatusForbidden {
		t.Errorf("redeem over the listener with an admin key: %d %s, want 403", status, body)
	}
}
