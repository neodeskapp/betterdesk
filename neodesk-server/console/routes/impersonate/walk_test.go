package impersonate_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
)

// allowedWrites are the only writes an impersonation may make: its own
// end and its one extension, and the admin's own accessibility settings
// (TestViewingAsKeepsTheAdminsOwnAccessibilitySettings). Signing out is tested on its own (it ends the
// impersonation and signs the admin out).
var allowedWrites = map[string]bool{
	"POST /api/impersonation/stop":   true,
	"POST /api/impersonation/extend": true,
	"PUT /api/accessibility":         true,
	"POST /api/auth/logout":          true,
	"POST /logout":                   true,
}

// Every route of both tables that changes something, as the tables are
// now (a route added later is walked too), is refused to an
// impersonation with 403 impersonation_read_only before its handler
// runs, and the impersonation goes on.
func TestEveryWriteIsRefused(t *testing.T) {
	w, _ := newWorld(t, options{})
	a := start(t, w, "super_admin", idOperator)
	walked := 0
	for _, table := range []*kit.Router{w.Server.Early(), w.Server.Router()} {
		for _, route := range table.Routes() {
			if strings.HasPrefix(route.Path, "/__test/") {
				continue
			}
			methods := []string{route.Method}
			switch route.Method {
			case "":
				methods = []string{"POST", "PUT", "PATCH", "DELETE"}
			case "GET", "HEAD", "OPTIONS":
				continue
			}
			for _, method := range methods {
				if allowedWrites[method+" "+route.Path] {
					continue
				}
				target := fill(route.Path)
				res := a.Do(method, target, `{}`)
				walked++
				if res.Status != http.StatusForbidden || code(t, res) != "impersonation_read_only" {
					t.Errorf("%s %s (%s): %d %s", method, target, route.Path, res.Status, cut(res.Body))
				}
			}
		}
	}
	if walked < 400 {
		t.Fatalf("only %d writes walked", walked)
	}
	t.Logf("%d writes refused", walked)
	if name, _ := whoAmI(t, a); name != "e2e_operator" {
		t.Fatalf("the impersonation ended on the way: %q", name)
	}
}

// The reads that show a secret, start remote work or touch the person's
// own credentials and devices are refused too: every GET route that
// needs a permission of that kind, and the routes on the deny list.
func TestSecretReadsAreRefused(t *testing.T) {
	w, _ := newWorld(t, options{})
	a := start(t, w, "super_admin", idOperator)
	denied := map[string]bool{
		auth.PermDeviceConnect: true, auth.PermDeviceSigninUse: true, auth.PermDeviceSigninManage: true,
		auth.PermRemoteTargetConnect: true, auth.PermServerKeys: true, auth.PermAgentTerminal: true, auth.PermAgentFiles: true,
		auth.PermCDAPCommand: true, auth.PermCDAPTerminal: true, auth.PermCDAPFiles: true,
		auth.PermMeshTerminal: true, auth.PermMeshFiles: true, auth.PermMeshPower: true,
	}
	var targets []string
	for _, table := range []*kit.Router{w.Server.Early(), w.Server.Router()} {
		for _, route := range table.Routes() {
			if route.Method != "GET" {
				continue
			}
			for _, need := range route.Permissions {
				for _, p := range strings.Split(need, " or ") {
					if denied[p] {
						targets = append(targets, fill(route.Path))
					}
				}
			}
		}
	}
	if len(targets) < 15 {
		t.Fatalf("only %d guarded reads found", len(targets))
	}
	targets = append(targets,
		"/api/users/me/sessions", "/api/auth/sessions", "/api/users/me/security-events",
		"/api/panel/api-tokens", "/api/panel/api-tokens/current",
		"/api/devices/1/signin", "/api/devices/1/clipboard", "/api/devices/1/processes",
		"/api/remote-targets/1/credentials", "/api/panel/org/1/peer-credentials",
		"/api/keys/api", "/api/keys/api?show=true", "/api/telemetry/key", "/api/panel/tokens", "/api/panel/pairing/codes",
		"/api/guest/access-links", "/api/mesh/download.msh", "/api/settings/backup",
		"/api/settings/advanced/files", "/api/server-management/files/read?path=/etc/passwd",
		"/api/remote/login-info", "/remote-desktop/1", "/api/auth/oidc/authorize",
		"/API/Keys/Api", "/api/keys/api/",
	)
	for _, target := range targets {
		res := a.Do("GET", target, "", "Accept", "application/json")
		if res.Status != http.StatusForbidden || code(t, res) != "impersonation_read_only" {
			t.Errorf("GET %s: %d %s", target, res.Status, cut(res.Body))
		}
	}
	// Plain reads pass
	for _, target := range []string{"/api/devices", "/api/users", "/api/console/bootstrap", "/api/impersonation"} {
		if res := a.Do("GET", target, ""); res.Status == http.StatusForbidden && code(t, res) == "impersonation_read_only" {
			t.Errorf("GET %s refused", target)
		}
	}
}

// Websockets: an impersonation opens only the two that push what the
// account may see; every other registered socket is refused before its
// handler runs.
func TestSocketsAreRefused(t *testing.T) {
	w, _ := newWorld(t, options{})
	a := start(t, w, "super_admin", idOperator)
	patterns := w.Server.Deps().Sockets.Patterns()
	if len(patterns) < 15 {
		t.Fatalf("only %d sockets", len(patterns))
	}
	upgrade := []string{"Connection", "Upgrade", "Upgrade", "websocket", "Sec-WebSocket-Version", "13",
		"Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==", "Origin", "http://" + w.Addr()}
	for _, pattern := range patterns {
		target := fill(strings.ReplaceAll(strings.ReplaceAll(pattern, "{id:rt_[A-Za-z0-9-]{8,80}}", "rt_abcdefgh"), "{id:[A-Za-z0-9_-]{1,64}}", "1"))
		pushOnly := pattern == "/ws/device-status" || pattern == "/ws/panel-events"
		if pushOnly {
			// Without a key the handshake fails in the socket's own check:
			// it got past the impersonation
			res := a.Do("GET", target, "", "Connection", "Upgrade", "Upgrade", "websocket")
			if code(t, res) == "impersonation_read_only" {
				t.Errorf("%s was refused", pattern)
			}
			continue
		}
		res := a.Do("GET", target, "", upgrade...)
		if res.Status != http.StatusForbidden || code(t, res) != "impersonation_read_only" {
			t.Errorf("%s (%s): %d %s", target, pattern, res.Status, cut(res.Body))
		}
	}
	if got := strings.Join(auditAbout(t, w, idOperator), "\n"); !strings.Contains(got, "impersonation_request|2|socket /ws/chat-operator/1 (refused") {
		t.Errorf("no audit entry for a refused socket:\n%s", got)
	}
}

func cut(s string) string {
	if len(s) > 160 {
		return s[:160] + "…"
	}
	return s
}

var _ = fmt.Sprintf
