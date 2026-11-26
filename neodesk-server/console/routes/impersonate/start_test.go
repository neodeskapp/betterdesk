package impersonate_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/routetest"
)

// Every refusal of the start, each with its code: who may not be viewed,
// who may not view, and what the start asks for.
func TestStartRefusals(t *testing.T) {
	w, _ := newWorld(t, options{roles: routetest.Overrides{
		// A global admin who may impersonate, and an operator with a
		// permission the global admin lacks
		"global_admin": {"user.impersonate": true},
		"guest_issuer": {"server.config": true},
	}})
	exec(t, w, `UPDATE users SET disabled = 1 WHERE id = ?`, idViewer)
	for _, c := range []struct {
		name   string
		role   string
		target int64
		body   string
		status int
		code   string
	}{
		{"yourself", "super_admin", idSuper, startBody("check"), 403, "impersonation_self"},
		{"the same role level", "super_admin", idAdmin, startBody("check"), 403, "impersonation_role"},
		{"a role of the same level with other permissions", "global_admin", idServerAdmin, startBody("check"), 403, "impersonation_role"},
		{"a higher role", "global_admin", idSuper, startBody("check"), 403, "impersonation_role"},
		{"a disabled account", "super_admin", idViewer, startBody("check"), 403, "impersonation_disabled"},
		{"an account that can't use the console", "super_admin", idPro, startBody("check"), 403, "impersonation_no_console"},
		{"an account with a permission the admin lacks", "global_admin", routetest.RoleIDs["guest_issuer"], startBody("check"), 403, "impersonation_permissions"},
		{"no reason", "super_admin", idOperator, `{"password":"` + adminPassword + `"}`, 400, "impersonation_reason_required"},
		{"a reason of white space", "super_admin", idOperator, `{"password":"` + adminPassword + `","reason":"  \n\t "}`, 400, "impersonation_reason_required"},
		{"no password", "super_admin", idOperator, `{"reason":"check"}`, 400, "impersonation_password_required"},
		{"a wrong password", "super_admin", idOperator, `{"password":"wrong","reason":"check"}`, 400, "impersonation_wrong_password"},
		{"a password that is no text", "super_admin", idOperator, `{"password":["` + adminPassword + `"],"reason":"check"}`, 400, "impersonation_password_required"},
		{"an account that doesn't exist", "super_admin", 999, startBody("check"), 404, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := w.As(c.role)
			res := a.Do("POST", fmt.Sprintf("/api/users/%d/impersonate", c.target), c.body)
			if res.Status != c.status || code(t, res) != c.code {
				t.Fatalf("%d %s, want %d %s", res.Status, res.Body, c.status, c.code)
			}
			if a.Cookie(impCookie) != "" {
				t.Fatal("a refused start set the impersonation cookie")
			}
			// The check says the same before anyone types a password
			if c.status == 403 {
				res := a.Do("GET", fmt.Sprintf("/api/users/%d/impersonation", c.target), "")
				var out struct {
					Data struct {
						Allowed bool   `json:"allowed"`
						Code    string `json:"code"`
					} `json:"data"`
				}
				_ = json.Unmarshal([]byte(res.Body), &out)
				if res.Status != 200 || out.Data.Allowed || out.Data.Code != c.code {
					t.Fatalf("the check: %d %s", res.Status, res.Body)
				}
			}
		})
	}
	if res := w.As("operator").Do("POST", fmt.Sprintf("/api/users/%d/impersonate", idViewer), startBody("check")); res.Status != 403 {
		t.Errorf("an operator without user.impersonate: %d %s", res.Status, res.Body)
	}
	// What refused starts left: no session, and the wrong passwords count
	// as failed sign-ins
	var n int
	if err := w.DB.QueryRow(context.Background(), `SELECT COUNT(*) FROM panel_sessions WHERE impersonated_by IS NOT NULL`).Scan(&n); err != nil || n != 0 {
		t.Errorf("impersonation sessions after refused starts: %d %v", n, err)
	}
}

// A global admin may view an operator: every permission of the operator
// is one of the global admin's.
func TestAGlobalAdminWithThePermissionMayViewAnOperator(t *testing.T) {
	w, _ := newWorld(t, options{roles: routetest.Overrides{"global_admin": {"user.impersonate": true}}})
	start(t, w, "global_admin", idOperator)
}

// A274: a panel access row that leaves an admin's Users section at read
// takes Sign in as away, though the role has user.impersonate; at write
// it works. Stopping needs no section: an admin whose row went to read
// while viewing still gets back.
func TestSignInAsNeedsTheUsersSectionAtWrite(t *testing.T) {
	w, _ := newWorld(t, options{roles: routetest.Overrides{"global_admin": {"user.impersonate": true}}})
	users := func(level string) {
		t.Helper()
		body := `{"sections":[{"resource_key":"users","access_level":"` + level + `"}]}`
		if res := w.As("super_admin").Do("PUT", fmt.Sprintf("/api/panel/access/users/%d", idGlobal), body); res.Status != 200 {
			t.Fatalf("policy: %d %s", res.Status, res.Body)
		}
	}
	users("read")
	a := w.As("global_admin")
	res := a.Do("POST", fmt.Sprintf("/api/users/%d/impersonate", idOperator), startBody("check"))
	if res.Status != 403 || code(t, res) != "panel_access_denied" {
		t.Fatalf("users at read: %d %s", res.Status, res.Body)
	}
	if a.Cookie(impCookie) != "" {
		t.Fatal("a refused start set the impersonation cookie")
	}

	users("write")
	viewing := start(t, w, "global_admin", idOperator)
	users("read")
	if res := viewing.Do("POST", "/api/impersonation/stop", "{}"); res.Status != 200 || !strings.Contains(res.Body, `"stopped":true`) {
		t.Fatalf("stop with users at read: %d %s", res.Status, res.Body)
	}
}

// An admin whose panel access is narrower than the account's in some
// section may not view it.
func TestStartRefusesAWiderPanelAccess(t *testing.T) {
	w, _ := newWorld(t, options{roles: routetest.Overrides{"global_admin": {"user.impersonate": true}}})
	body := `{"sections":[{"resource_key":"devices","access_level":"none"}]}`
	if res := w.As("super_admin").Do("PUT", fmt.Sprintf("/api/panel/access/users/%d", idGlobal), body); res.Status != 200 {
		t.Fatalf("policy: %d %s", res.Status, res.Body)
	}
	res := w.As("global_admin").Do("POST", fmt.Sprintf("/api/users/%d/impersonate", idOperator), startBody("check"))
	if res.Status != 403 || code(t, res) != "impersonation_panel_access" {
		t.Fatalf("%d %s", res.Status, res.Body)
	}
}

// An admin limited to some devices may not view an account that sees a
// device outside them. (The operator gets user.edit too: Sign in as asks
// the users section at write, A274.)
func TestStartRefusesAWiderDeviceScope(t *testing.T) {
	w, _ := newWorld(t, options{
		roles: routetest.Overrides{"operator": {"user.impersonate": true, "user.edit": true}},
		env:   map[string]string{"DEVICE_SCOPE_DEFAULT": "restricted"},
	})
	exec(t, w, `INSERT INTO user_peer_grants (user_id, peer_id) VALUES (?, '100100100')`, idOperator)
	exec(t, w, `INSERT INTO user_peer_grants (user_id, peer_id) VALUES (?, '100100100')`, idViewer)
	// Inside the operator's devices: allowed
	start(t, w, "operator", idViewer)
	exec(t, w, `INSERT INTO user_peer_grants (user_id, peer_id) VALUES (?, '200200200')`, idViewer)
	res := w.As("operator").Do("POST", fmt.Sprintf("/api/users/%d/impersonate", idViewer), startBody("check"))
	if res.Status != 403 || code(t, res) != "impersonation_scope" {
		t.Fatalf("%d %s", res.Status, res.Body)
	}
}

// With two-factor sign-in on, the start asks for the code too, and a
// wrong one is refused.
func TestStartAsksForTheTwoFactorCode(t *testing.T) {
	w, _ := newWorld(t, options{})
	exec(t, w, `UPDATE users SET totp_enabled = 1, totp_secret = ? WHERE id = ?`, totpSecret, idSuper)
	a := w.As("super_admin")
	target := fmt.Sprintf("/api/users/%d/impersonate", idOperator)
	if res := a.Do("POST", target, startBody("check")); res.Status != 400 || code(t, res) != "impersonation_code_required" {
		t.Fatalf("no code: %d %s", res.Status, res.Body)
	}
	if res := a.Do("POST", target, `{"password":"`+adminPassword+`","code":"000000","reason":"check"}`); res.Status != 400 || code(t, res) != "impersonation_wrong_code" {
		t.Fatalf("a wrong code: %d %s", res.Status, res.Body)
	}
	if res := a.Do("POST", target, `{"password":"`+adminPassword+`","code":"`+totpNow(t)+`","reason":"check"}`); res.Status != 200 {
		t.Fatalf("the right code: %d %s", res.Status, res.Body)
	}
}

// An admin of a directory has no console password: without two-factor
// sign-in there is nothing to check, and the start is refused.
func TestADirectoryAdminNeedsTwoFactor(t *testing.T) {
	w, _ := newWorld(t, options{})
	exec(t, w, `UPDATE users SET auth_provider = 'ldap' WHERE id = ?`, idSuper)
	res := w.As("super_admin").Do("POST", fmt.Sprintf("/api/users/%d/impersonate", idOperator), startBody("check"))
	if res.Status != 403 || code(t, res) != "impersonation_needs_two_factor" {
		t.Fatalf("%d %s", res.Status, res.Body)
	}
}

// API tokens can't start an impersonation, not even a super admin's full
// one.
func TestATokenCannotStart(t *testing.T) {
	w, _ := newWorld(t, options{})
	all, _ := json.Marshal(auth.AllPermissions)
	res := w.As("super_admin").Do("POST", "/api/panel/api-tokens", `{"name":"cli","permissions":`+string(all)+`,"read_only":false,"expires_in_days":1}`)
	if res.Status != http.StatusCreated {
		t.Fatalf("token: %d %s", res.Status, res.Body)
	}
	var out struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	_ = json.Unmarshal([]byte(res.Body), &out)
	tok := w.SignedOut()
	got := tok.Do("POST", fmt.Sprintf("/api/users/%d/impersonate", idOperator), startBody("check"), "Authorization", "Bearer "+out.Data.Token)
	if got.Status != 403 || code(t, got) != "impersonation_token" {
		t.Fatalf("%d %s", got.Status, got.Body)
	}
	if strings.Contains(strings.Join(got.Header.Values("Set-Cookie"), ";"), impCookie) {
		t.Fatal("the token got an impersonation cookie")
	}
}

// The start needs the CSRF token like every write.
func TestStartNeedsTheCSRFToken(t *testing.T) {
	w, _ := newWorld(t, options{})
	a := w.As("super_admin")
	res := a.Do("POST", fmt.Sprintf("/api/users/%d/impersonate", idOperator), startBody("check"), "X-CSRF-Token", "forged")
	if res.Status != 403 || a.Cookie(impCookie) != "" {
		t.Fatalf("%d %s", res.Status, res.Body)
	}
}
