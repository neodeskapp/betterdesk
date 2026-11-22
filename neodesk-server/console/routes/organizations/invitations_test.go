package organizations_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/routes/internal/routetest"
)

const code = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// The invitation page's two routes: lookup names the organization of a
// code and the console account of the session, null signed out; a code of
// the wrong form, and one the API server does not know, get the same 404,
// the first without a call; redeem gives the organization and the username
// of the new sign-in and writes the audit log, and says why it refused
// (BUGS L42).
func TestInvitationLookupAndRedeem(t *testing.T) {
	api := routetest.NewAPI()
	w := routetest.New(t, routetest.Options{API: api})
	visitor, admin := w.SignedOut(), w.As("admin")

	api.On("POST /api/org-invitations/lookup", http.StatusOK, `{"org_id":"o1","org_name":"Acme","org_slug":"acme","expires_at":"2026-12-01T00:00:00Z"}`)
	want(t, "lookup signed out", visitor.Do("POST", "/api/invitations/lookup", `{"token":"`+strings.ToUpper(code)+`"}`), 200,
		`{"success":true,"organization":{"name":"Acme","slug":"acme"},"expires_at":"2026-12-01T00:00:00Z","account":null}`)
	calls(t, "lookup", api, `POST /api/org-invitations/lookup {"token":"`+code+`"}`)
	want(t, "lookup signed in", admin.Do("POST", "/api/invitations/lookup", `{"token":"`+code+`"}`), 200,
		`{"success":true,"organization":{"name":"Acme","slug":"acme"},"expires_at":"2026-12-01T00:00:00Z","account":{"username":"e2e_admin"}}`)
	api.Calls()

	const invalid = `{"success":false,"error":"organizations.invite_invalid"}`
	for _, body := range []string{`{"token":"nope"}`, `{"token":12}`, `{}`, `[]`} {
		if got := visitor.Do("POST", "/api/invitations/lookup", body); got.Status != 404 || !strings.Contains(got.Body, `"success":false`) {
			t.Errorf("lookup of %s: %d %s, want 404", body, got.Status, got.Body)
		}
	}
	calls(t, "codes of the wrong form", api)
	api.On("POST /api/org-invitations/lookup", http.StatusNotFound, `{"error":"invitation not found"}`)
	if got := visitor.Do("POST", "/api/invitations/lookup", `{"token":"`+code+`"}`); got.Status != 404 || got.Body == "" || strings.Contains(got.Body, "invitation not found") {
		t.Errorf("lookup of an unknown code: %d %s, want the console's 404", got.Status, got.Body)
	}
	api.Calls()

	api.On("POST /api/org-invitations/redeem", http.StatusCreated, `{"user":{"id":"ou1","username":"anna","role":"user"},"invitation_id":"inv-1","org":{"id":"o1","name":"Acme","slug":"acme"}}`)
	want(t, "redeem", visitor.Do("POST", "/api/invitations/redeem", `{"token":"`+code+`","username":"anna","password":"long-enough-1","role":"owner"}`), 201,
		`{"success":true,"organization":{"name":"Acme","slug":"acme"},"username":"anna"}`)
	calls(t, "redeem", api, `POST /api/org-invitations/redeem {"token":"`+code+`","username":"anna","password":"long-enough-1"}`)
	rows := w.AuditRows()
	if len(rows) == 0 || !strings.HasPrefix(rows[len(rows)-1], "org_invitation_redeemed|Organization o1: sign-in anna made with invitation inv-1|") {
		t.Errorf("audit rows %q", rows)
	}

	api.On("POST /api/org-invitations/redeem", http.StatusConflict, `{"error":"username already exists in this organization"}`)
	if got := visitor.Do("POST", "/api/invitations/redeem", `{"token":"`+code+`","username":"anna","password":"long-enough-1"}`); got.Status != 409 || !strings.Contains(got.Body, "organizations.invite_username_taken") {
		t.Errorf("a taken username: %d %s", got.Status, got.Body)
	}
	api.On("POST /api/org-invitations/redeem", http.StatusBadRequest, `{"error":"Password must be at least 8 characters"}`)
	want(t, "a short password", visitor.Do("POST", "/api/invitations/redeem", `{"token":"`+code+`","username":"anna","password":"short"}`), 400,
		`{"success":false,"error":"Password must be at least 8 characters"}`)
	api.On("POST /api/org-invitations/redeem", http.StatusNotFound, `{"error":"invitation not found"}`)
	want(t, "a used code", visitor.Do("POST", "/api/invitations/redeem", `{"token":"`+code+`","username":"anna","password":"long-enough-1"}`), 404, invalid)
	api.Calls()
	want(t, "a code of the wrong form", visitor.Do("POST", "/api/invitations/redeem", `{"token":"x","username":"anna","password":"long-enough-1"}`), 404, invalid)
	calls(t, "a code of the wrong form", api)
}

// Lookup and redeem count together against 20 requests in 15 minutes per
// address (BUGS L42).
func TestInvitationRoutesAreLimited(t *testing.T) {
	api := routetest.NewAPI().On("POST /api/org-invitations/lookup", http.StatusNotFound, `{"error":"invitation not found"}`)
	w := routetest.New(t, routetest.Options{API: api, Env: map[string]string{"RATE_LIMIT_MAX": "1000"}})
	visitor := w.SignedOut()
	for i := 0; i < 20; i++ {
		path := "/api/invitations/lookup"
		if i%2 == 1 {
			path = "/api/invitations/redeem"
		}
		if got := visitor.Do("POST", path, `{"token":"`+code+`"}`); got.Status != 404 {
			t.Fatalf("request %d: %d %s", i+1, got.Status, got.Body)
		}
	}
	if got := visitor.Do("POST", "/api/invitations/redeem", `{"token":"`+code+`"}`); got.Status != 429 {
		t.Errorf("the 21st request: %d %s, want 429", got.Status, got.Body)
	}
}

// Creating and revoking an invitation pass the API server's answers on
// (the code in the creation's answer) and are written to the audit log
// (BUGS L42).
func TestInvitationsAreAudited(t *testing.T) {
	api := routetest.NewAPI()
	w := routetest.New(t, routetest.Options{API: api})
	admin := w.As("admin")
	api.On("POST /api/org/o1/invite", http.StatusCreated, `{"id":"inv-1","org_id":"o1","token":"`+code+`","email":"guest@example.invalid","role":"user","expires_at":"2026-12-01T00:00:00Z"}`)
	want(t, "invite", admin.Do("POST", "/api/panel/org/o1/invite", `{"email":"guest@example.invalid"}`), 201,
		`{"id":"inv-1","org_id":"o1","token":"`+code+`","email":"guest@example.invalid","role":"user","expires_at":"2026-12-01T00:00:00Z"}`)
	api.On("DELETE /api/org/o1/invitations/inv-1", http.StatusNoContent, ``)
	if got := admin.Do("DELETE", "/api/panel/org/o1/invitations/inv-1", ""); got.Status != 204 {
		t.Errorf("revoke: %d %s", got.Status, got.Body)
	}
	api.On("DELETE /api/org/o1/invitations/inv-2", http.StatusConflict, `{"error":"invitation already used"}`)
	want(t, "revoke a used one", admin.Do("DELETE", "/api/panel/org/o1/invitations/inv-2", ""), 409, `{"error":"invitation already used"}`)
	want(t, "an odd invitation id", admin.Do("DELETE", "/api/panel/org/o1/invitations/a$b", ""), 400, `{"error":"Invalid invitationId"}`)
	if got := w.As("operator").Do("DELETE", "/api/panel/org/o1/invitations/inv-1", ""); got.Status != 403 {
		t.Errorf("revoke without org.manage_users: %d %s", got.Status, got.Body)
	}
	joined := strings.Join(w.AuditRows(), "\n")
	for _, entry := range []string{
		"org_invitation_created|Organization o1: invitation inv-1 for guest@example.invalid, valid until 2026-12-01T00:00:00Z|",
		"org_invitation_revoked|Organization o1: invitation inv-1 revoked|",
	} {
		if !strings.Contains(joined, entry) {
			t.Errorf("no audit entry %q in\n%s", entry, joined)
		}
	}
	if strings.Contains(joined, "inv-2") {
		t.Errorf("a refused revocation was audited:\n%s", joined)
	}
}
