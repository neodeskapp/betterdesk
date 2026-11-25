package users_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/routetest"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

type userSession struct {
	ID        string `json:"id"`
	Current   bool   `json:"current"`
	IPAddress string `json:"ipAddress"`
}

// sessionsOf lists an account's sessions as the user's page reads them.
func sessionsOf(t *testing.T, a *routetest.Agent, id int64) []userSession {
	t.Helper()
	got := a.Do("GET", fmt.Sprintf("/api/users/%d/sessions", id), "")
	var out struct {
		Success bool `json:"success"`
		Data    struct {
			Sessions []userSession `json:"sessions"`
		} `json:"data"`
	}
	if got.Status != http.StatusOK || json.Unmarshal([]byte(got.Body), &out) != nil || !out.Success {
		t.Fatalf("sessions of %d: %d %s", id, got.Status, got.Body)
	}
	return out.Data.Sessions
}

// signedIn reports whether a browser's session still works.
func signedIn(t *testing.T, a *routetest.Agent) (bool, string) {
	t.Helper()
	got := a.Do("GET", "/api/auth/verify", "")
	return got.Status == http.StatusOK, got.Body
}

const endedByAdmin = `{"success":false,"error":"An administrator signed you out. Please sign in again.","code":"session_ended_by_admin"}`

// TestAnAdminEndsTheSessionsOfAnAccount: with user.edit the user's page
// lists the account's sessions and ends one or all of them; the browser
// whose session ended is told why at its next request, and the audit log
// names who ended whose session from where.
func TestAnAdminEndsTheSessionsOfAnAccount(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		w := world(t, storetest.Prepare(t, db, storetest.GoServerFirst), routetest.Options{})
		operator := routetest.RoleIDs["operator"]
		first, second, third := w.As("operator"), w.As("operator"), w.As("operator")
		admin := w.As("admin")

		list := sessionsOf(t, admin, operator)
		if len(list) != 3 {
			t.Fatalf("three sessions, listed: %+v", list)
		}
		for _, s := range list {
			if s.Current || len(s.ID) != 32 || s.IPAddress != "127.0.0.1" {
				t.Errorf("entry %+v", s)
			}
		}
		// Ending one entry ends exactly one of the three browsers
		expect(t, "end one", admin.Do("DELETE", fmt.Sprintf("/api/users/%d/sessions/%s", operator, list[0].ID), ""), http.StatusOK, `{"success":true}`)
		var left []*routetest.Agent
		out := 0
		for _, a := range []*routetest.Agent{first, second, third} {
			got := a.Do("GET", "/api/auth/verify", "")
			switch {
			case got.Status == http.StatusOK:
				left = append(left, a)
			case got.Body == endedByAdmin:
				out++
			default:
				t.Errorf("a browser: %d %s", got.Status, got.Body)
			}
		}
		if out != 1 || len(left) != 2 {
			t.Fatalf("ending one entry ended %d browsers and left %d", out, len(left))
		}
		expectBody := func(name string, a *routetest.Agent, want string) {
			t.Helper()
			got := a.Do("GET", "/api/auth/verify", "")
			if got.Status != http.StatusUnauthorized || got.Body != want {
				t.Errorf("%s: %d %s", name, got.Status, got.Body)
			}
		}
		second, third = left[0], left[1]
		if n := len(sessionsOf(t, admin, operator)); n != 2 {
			t.Errorf("%d sessions left, want 2", n)
		}
		expect(t, "end all", admin.Do("POST", fmt.Sprintf("/api/users/%d/sessions/end-all", operator), `{}`), http.StatusOK, `{"success":true,"ended":2}`)
		expectBody("second", second, endedByAdmin)
		expectBody("third", third, endedByAdmin)
		if ok, body := signedIn(t, admin); !ok {
			t.Errorf("the admin's own session: %s", body)
		}
		if n := len(sessionsOf(t, admin, operator)); n != 0 {
			t.Errorf("%d sessions left after ending all", n)
		}
		ended := 0
		for _, row := range w.AuditRows() {
			if strings.HasPrefix(row, "session_ended_by_admin|Ended session ") && strings.Contains(row, " of e2e_operator") &&
				strings.HasSuffix(row, fmt.Sprintf("|127.0.0.1|%d", routetest.RoleIDs["admin"])) {
				ended++
			}
		}
		if ended != 3 {
			t.Errorf("%d audit entries for three ended sessions: %v", ended, w.AuditRows())
		}
		// An unknown account and an unknown session
		expect(t, "unknown account", admin.Do("GET", "/api/users/999/sessions", ""), http.StatusNotFound, `{"success":false,"error":"User not found"}`)
		expect(t, "unknown session", admin.Do("DELETE", fmt.Sprintf("/api/users/%d/sessions/%s", operator, strings.Repeat("a", 32)), ""), http.StatusNotFound, `{"success":false,"error":"Session not found"}`)
	})
}

// TestEndingAnAccountsSessionsNeedsThePermissionThatManagesUsers: without
// user.edit the routes refuse, and with it an account above the caller's
// own stays out of reach.
func TestEndingAnAccountsSessionsNeedsThePermissionThatManagesUsers(t *testing.T) {
	w := world(t, nil, routetest.Options{Roles: routetest.Overrides{"server_admin": {"user.edit": true}, "viewer": {"user.view": true}}})
	operator, admin := routetest.RoleIDs["operator"], routetest.RoleIDs["admin"]
	victim := w.As("operator")
	adminBrowser := w.As("admin")
	viewer := w.As("viewer")
	id := sessionsOf(t, adminBrowser, operator)[0].ID
	for what, got := range map[string]routetest.Answer{
		"list":    viewer.Do("GET", fmt.Sprintf("/api/users/%d/sessions", operator), ""),
		"end":     viewer.Do("DELETE", fmt.Sprintf("/api/users/%d/sessions/%s", operator, id), ""),
		"end all": viewer.Do("POST", fmt.Sprintf("/api/users/%d/sessions/end-all", operator), `{}`),
	} {
		if got.Status != http.StatusForbidden {
			t.Errorf("a viewer, %s: %d %s", what, got.Status, got.Body)
		}
	}
	if ok, body := signedIn(t, victim); !ok {
		t.Errorf("a refused request ended the session: %s", body)
	}
	// server_admin has user.edit here, but the admin account is above it
	serverAdmin := w.As("server_admin")
	if n := len(sessionsOf(t, serverAdmin, operator)); n != 1 {
		t.Errorf("server_admin lists %d sessions of the operator", n)
	}
	got := serverAdmin.Do("POST", fmt.Sprintf("/api/users/%d/sessions/end-all", admin), `{}`)
	expect(t, "an account above the caller's", got, http.StatusForbidden, `{"success":false,"error":"Cannot assign a role higher than your own"}`)
	if ok, body := signedIn(t, adminBrowser); !ok {
		t.Errorf("the admin's session after a refused end: %s", body)
	}
	// The admin's own session is not ended from the user's page
	own := ""
	for _, s := range sessionsOf(t, adminBrowser, admin) {
		if s.Current {
			own = s.ID
		}
	}
	expect(t, "own current session", adminBrowser.Do("DELETE", fmt.Sprintf("/api/users/%d/sessions/%s", admin, own), ""),
		http.StatusBadRequest, `{"success":false,"error":"This is the session of this browser. Use Sign out.","code":"current_session"}`)
}
