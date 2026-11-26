package impersonate_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/routetest"
	"github.com/neodeskapp/neodesk/neodesk-server/console/session"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// The start opens a session of its own: the browser sees the console as
// the account, the admin's session stays as it was, and nothing of the
// account's own sessions changes: no limit counts it, none of them ends,
// it is not one of the account's signed-in devices, and the last sign-in
// stays.
func TestStartOpensASeparateSession(t *testing.T) {
	w, _ := newWorld(t, options{})
	// One session per account, and the operator signed in on a browser
	if err := w.Server.Deps().Sessions.SetSingleSession(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	operator := w.As("operator")
	exec(t, w, `UPDATE users SET last_login = '2026-01-02 03:04:05' WHERE id = ?`, idOperator)

	a := w.As("super_admin")
	adminSID := a.Cookie(sidCookie)
	res := a.Do("POST", fmt.Sprintf("/api/users/%d/impersonate", idOperator), startBody("Reproduce the ticket 4711"))
	if res.Status != 200 {
		t.Fatalf("start: %d %s", res.Status, res.Body)
	}
	if a.Cookie(sidCookie) != adminSID {
		t.Error("the start changed the admin's session cookie")
	}
	name, boot := whoAmI(t, a)
	if name != "e2e_operator" || !strings.Contains(boot.Body, `"impersonation":{"adminUsername":"e2e_super_admin","reason":"Reproduce the ticket 4711"`) {
		t.Fatalf("the bootstrap: %s", boot.Body)
	}
	if strings.Contains(boot.Body, strings.TrimPrefix(adminSID, "s%3A")[:10]) || strings.Contains(boot.Body, "adminSid") {
		t.Error("the bootstrap names the admin's session")
	}
	if active, _, by := impRow(t, w, idOperator); !active || by != idSuper {
		t.Errorf("the row: active %v, impersonated_by %d", active, by)
	}
	// The operator's own browser goes on
	if name, res := whoAmI(t, operator); name != "e2e_operator" {
		t.Errorf("the operator's own session: %d %s", res.Status, res.Body)
	}
	// It is no device of the operator's
	devices := operator.Do("GET", "/api/users/me/sessions", "")
	var list struct {
		Data struct {
			Sessions []json.RawMessage `json:"sessions"`
		} `json:"data"`
	}
	_ = json.Unmarshal([]byte(devices.Body), &list)
	if len(list.Data.Sessions) != 1 {
		t.Errorf("the operator's signed-in devices: %s", devices.Body)
	}
	// (as the other super admin: one session per account)
	if res := w.As("admin").Do("GET", fmt.Sprintf("/api/users/%d/sessions", idOperator), ""); strings.Count(res.Body, `"browser"`) != 1 {
		t.Errorf("the operator's devices on the user's page: %s", res.Body)
	}
	var last string
	_ = w.DB.QueryRow(context.Background(), `SELECT COALESCE(last_login, '') FROM users WHERE id = ?`, idOperator).Scan(&last)
	if !strings.HasPrefix(last, "2026-01-02") {
		t.Errorf("last sign-in: %q", last)
	}
	// The operator signing in again (one session per account) ends the
	// operator's other session, not the impersonation
	w.As("operator")
	if name, res := whoAmI(t, a); name != "e2e_operator" || !strings.Contains(res.Body, `"impersonation"`) {
		t.Errorf("after the operator signed in again: %d %s", res.Status, res.Body)
	}
	if got := auditAbout(t, w, idOperator); len(got) == 0 || !strings.HasPrefix(got[0], "impersonation_started|2|Viewed NeoDesk as e2e_operator for: Reproduce the ticket 4711") {
		t.Errorf("audit: %v", got)
	}
}

// A copy of the impersonation cookie is worth nothing without the admin's
// session it belongs to, and a forged one is no cookie.
func TestTheImpersonationCookieAloneIsWorthNothing(t *testing.T) {
	w, _ := newWorld(t, options{})
	a := start(t, w, "super_admin", idOperator)
	thief := w.SignedOut()
	thief.SetCookie(impCookie, a.Cookie(impCookie))
	if name, res := whoAmI(t, thief); name != "" || res.Status != http.StatusConflict || code(t, res) != "impersonation_ended" {
		t.Fatalf("the cookie alone: %d %s", res.Status, res.Body)
	}
	// With another account's session it is no better
	other := w.As("global_admin")
	other.SetCookie(impCookie, a.Cookie(impCookie))
	if name, res := whoAmI(t, other); name != "" || res.Status != http.StatusConflict || code(t, res) != "impersonation_ended" {
		t.Fatalf("with another session: %q %d %s", name, res.Status, res.Body)
	}
	// Both attempts ended it
	if active, _, _ := impRow(t, w, idOperator); active {
		t.Error("the impersonation is still active")
	}

	b := start(t, w, "super_admin", idOperator)
	forged := b.Cookie(impCookie)
	b.SetCookie(impCookie, forged[:len(forged)-4]+"AAAA")
	if name, _ := whoAmI(t, b); name != "e2e_super_admin" {
		t.Fatalf("a forged cookie: %q", name)
	}
}

// Stopping returns the browser to the admin's own session, and the old
// cookie is worth nothing afterwards: it neither views as the account nor
// runs anything as the admin.
func TestStopReturnsToTheAdmin(t *testing.T) {
	w, _ := newWorld(t, options{})
	a := start(t, w, "super_admin", idOperator)
	old := a.Cookie(impCookie)
	res := a.Do("POST", "/api/impersonation/stop", "{}")
	if res.Status != 200 || !strings.Contains(res.Body, `"stopped":true`) || a.Cookie(impCookie) != "" {
		t.Fatalf("stop: %d %s, cookie %q", res.Status, res.Body, a.Cookie(impCookie))
	}
	if name, _ := whoAmI(t, a); name != "e2e_super_admin" {
		t.Fatalf("after stop: %q", name)
	}
	if active, reason, _ := impRow(t, w, idOperator); active || reason != session.ReasonImpersonationStopped {
		t.Errorf("the row: %v %q", active, reason)
	}
	a.SetCookie(impCookie, old)
	res = a.Do("POST", "/api/users", `{"username":"made_by_replay","password":"long enough 1","role":"viewer"}`)
	if res.Status != http.StatusConflict || code(t, res) != "impersonation_ended" {
		t.Fatalf("a write with the old cookie: %d %s", res.Status, res.Body)
	}
	var n int
	_ = w.DB.QueryRow(context.Background(), `SELECT COUNT(*) FROM users WHERE username = 'made_by_replay'`).Scan(&n)
	if n != 0 {
		t.Fatal("the write ran as the admin")
	}
	if a.Cookie(impCookie) != "" {
		t.Error("the answer did not clear the old cookie")
	}
	if got := auditAbout(t, w, idOperator); !strings.HasPrefix(got[len(got)-1], "impersonation_stopped|2|Stopped viewing NeoDesk as e2e_operator") {
		t.Errorf("audit: %v", got)
	}
}

// It lasts 15 minutes and can be made longer once, to 30 minutes from
// the start; after that it ends at the next request.
func TestExtendOnceAndExpiry(t *testing.T) {
	w, _ := newWorld(t, options{})
	a := start(t, w, "super_admin", idOperator)
	var status struct {
		Data struct {
			StartedAt int64 `json:"started_at"`
			ExpiresAt int64 `json:"expires_at"`
			CanExtend bool  `json:"can_extend"`
		} `json:"data"`
	}
	_ = json.Unmarshal([]byte(a.Do("GET", "/api/impersonation", "").Body), &status)
	if status.Data.ExpiresAt-status.Data.StartedAt != 15*60*1000 || !status.Data.CanExtend {
		t.Fatalf("status: %+v", status.Data)
	}
	res := a.Do("POST", "/api/impersonation/extend", "{}")
	_ = json.Unmarshal([]byte(res.Body), &status)
	if res.Status != 200 || status.Data.ExpiresAt-status.Data.StartedAt != 30*60*1000 || status.Data.CanExtend {
		t.Fatalf("extend: %d %s", res.Status, res.Body)
	}
	if res := a.Do("POST", "/api/impersonation/extend", "{}"); res.Status != http.StatusConflict || code(t, res) != "impersonation_extended_once" {
		t.Fatalf("extend twice: %d %s", res.Status, res.Body)
	}
	moveExpiry(t, w, idOperator, time.Now().Add(-time.Second))
	if res := a.Do("GET", "/api/devices", ""); res.Status != http.StatusConflict || code(t, res) != "impersonation_ended" {
		t.Fatalf("after the time is up: %d %s", res.Status, res.Body)
	}
	if name, _ := whoAmI(t, a); name != "e2e_super_admin" {
		t.Fatalf("after the end: %q", name)
	}
	if got := auditAbout(t, w, idOperator); !strings.Contains(strings.Join(got, "\n"), "impersonation_ended|2|Stopped viewing NeoDesk as e2e_operator: The time to view NeoDesk as another account is up.") {
		t.Errorf("audit: %v", got)
	}
}

// Every request checks again that the admin may still view the account:
// it ends when the admin is demoted or disabled, loses the permission or
// signs out elsewhere, when the account is promoted or disabled, and when
// the account's sessions end (a new password).
func TestItEndsWhenTheRulesNoLongerHold(t *testing.T) {
	for _, c := range []struct {
		name   string
		change func(t *testing.T, w *routetest.World)
		status int
	}{
		{"the admin demoted", func(t *testing.T, w *routetest.World) {
			exec(t, w, `UPDATE users SET role = 'operator' WHERE id = ?`, idSuper)
		}, http.StatusConflict},
		{"the admin disabled", func(t *testing.T, w *routetest.World) {
			exec(t, w, `UPDATE users SET disabled = 1 WHERE id = ?`, idSuper)
		}, http.StatusConflict},
		{"the admin signed out everywhere", func(t *testing.T, w *routetest.World) {
			if _, err := w.Server.Deps().Sessions.EndAccount(context.Background(), idSuper, ""); err != nil {
				t.Fatal(err)
			}
		}, http.StatusUnauthorized},
		{"the account promoted", func(t *testing.T, w *routetest.World) {
			exec(t, w, `UPDATE users SET role = 'super_admin' WHERE id = ?`, idOperator)
		}, http.StatusConflict},
		{"the account given a permission the admin lacks", func(t *testing.T, w *routetest.World) {
			exec(t, w, `UPDATE users SET role = 'global_admin' WHERE id = ?`, idOperator)
		}, http.StatusConflict},
		{"the account disabled", func(t *testing.T, w *routetest.World) {
			exec(t, w, `UPDATE users SET disabled = 1 WHERE id = ?`, idOperator)
		}, http.StatusConflict},
		{"the account's sessions ended", func(t *testing.T, w *routetest.World) {
			if _, err := w.Server.Deps().Sessions.EndAccount(context.Background(), idOperator, ""); err != nil {
				t.Fatal(err)
			}
		}, http.StatusConflict},
	} {
		t.Run(c.name, func(t *testing.T) {
			w, _ := newWorld(t, options{})
			a := start(t, w, "super_admin", idOperator)
			if res := a.Do("GET", "/api/devices", ""); res.Status == http.StatusConflict {
				t.Fatalf("before the change: %d %s", res.Status, res.Body)
			}
			c.change(t, w)
			res := a.Do("GET", "/api/devices", "")
			if res.Status != c.status {
				t.Fatalf("%d %s, want %d", res.Status, res.Body, c.status)
			}
			if active, _, _ := impRow(t, w, idOperator); active {
				t.Error("the impersonation is still active")
			}
			if a.Cookie(impCookie) != "" {
				t.Error("the browser keeps the impersonation cookie")
			}
		})
	}
}

// Signing out during an impersonation ends it and signs the admin out.
func TestSignOutEndsItAndSignsTheAdminOut(t *testing.T) {
	w, _ := newWorld(t, options{})
	a := start(t, w, "super_admin", idOperator)
	if res := a.Do("POST", "/api/auth/logout", "{}"); res.Status != 200 {
		t.Fatalf("sign out: %d %s", res.Status, res.Body)
	}
	if res := a.Do("GET", "/api/console/bootstrap", ""); res.Status != 401 {
		t.Fatalf("after sign-out: %d %s", res.Status, res.Body)
	}
	if active, reason, _ := impRow(t, w, idOperator); active || reason != session.ReasonImpersonationStopped {
		t.Errorf("the row: %v %q", active, reason)
	}
	rows := strings.Join(w.AuditRows(), "\n")
	if !strings.Contains(rows, "logout|User logged out|127.0.0.1|2") || strings.Contains(rows, "logout|User logged out|127.0.0.1|5") {
		t.Errorf("audit: %s", rows)
	}
}

// Every API request of an impersonation has an audit entry of the admin
// about the account, refused or not, and an entry a route writes with
// LogAction names the admin too.
func TestEveryRequestIsAuditedForTheAdmin(t *testing.T) {
	w, _ := newWorld(t, options{})
	d := w.Server.Deps()
	w.Server.Router().Get("/api/zz-logs", func(rw http.ResponseWriter, r *http.Request) {
		_ = d.LogAction(r, "zz_probe", "looked")
		kit.OK(rw, r, nil)
	}, d.RequireAuth)
	a := start(t, w, "super_admin", idOperator)
	a.Do("GET", "/api/devices", "")
	a.Do("DELETE", "/api/devices/1", "")
	a.Do("GET", "/api/zz-logs", "")
	got := strings.Join(auditAbout(t, w, idOperator), "\n")
	for _, want := range []string{
		"impersonation_request|2|GET /api/devices",
		"impersonation_request|2|DELETE /api/devices/1 (refused: changes data)",
		"zz_probe|2|looked",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in\n%s", want, got)
		}
	}
	var n int
	_ = w.DB.QueryRow(context.Background(), `SELECT COUNT(*) FROM audit_log WHERE user_id = ? AND target_user_id IS NULL AND action <> 'login'`, idOperator).Scan(&n)
	if n != 0 {
		t.Errorf("%d entries name the operator as the one who acted", n)
	}
}

// A tab that still shows the impersonation after it ended runs nothing as
// the admin.
func TestATabThatStillViewsRunsNothingAsTheAdmin(t *testing.T) {
	w, _ := newWorld(t, options{})
	a := w.As("super_admin")
	res := a.Do("POST", "/api/users", `{"username":"from_a_stale_tab","password":"long enough 1","role":"viewer"}`, "X-ND-Impersonating", "1")
	if res.Status != http.StatusConflict || code(t, res) != "impersonation_ended" {
		t.Fatalf("%d %s", res.Status, res.Body)
	}
}

// The whole round on both databases: start, read, a refused write, the
// person's history, stop.
func TestOnBothDatabases(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w, _ := newWorld(t, options{db: db})
		a := start(t, w, "super_admin", idOperator)
		if res := a.Do("GET", "/api/devices", ""); res.Status != 200 {
			t.Fatalf("read: %d %s", res.Status, res.Body)
		}
		if res := a.Do("DELETE", "/api/devices/1", ""); res.Status != 403 || code(t, res) != "impersonation_read_only" {
			t.Fatalf("write: %d %s", res.Status, res.Body)
		}
		if active, _, by := impRow(t, w, idOperator); !active || by != idSuper {
			t.Fatalf("row: %v %d", active, by)
		}
		if res := a.Do("POST", "/api/impersonation/stop", "{}"); res.Status != 200 {
			t.Fatalf("stop: %d %s", res.Status, res.Body)
		}
		res := w.As("operator").Do("GET", "/api/users/me/security-events", "")
		if res.Status != 200 || !strings.Contains(res.Body, `"action":"impersonation_started","at":`) || !strings.Contains(res.Body, `"actor":"e2e_super_admin"`) {
			t.Fatalf("history: %d %s", res.Status, res.Body)
		}
	})
}
