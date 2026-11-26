package console

import (
	"context"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
)

// closeSection writes a panel access row for a test account.
func (w *world) setSection(role, section, level string) {
	w.t.Helper()
	_, err := w.db.Exec(context.Background(),
		"INSERT INTO panel_access_policies (subject_type, subject_id, resource_key, access_level) VALUES ('user', ?, ?, ?)",
		roleIDs[role], section, level)
	if err != nil {
		w.t.Fatalf("policy row: %v", err)
	}
}

// PA3 (analysis C4): the routes the guard's path rules cannot name say
// their section themselves, and the guard asks them first. This is the
// whole list; a route added to it changes who reaches it.
func TestRoutesThatDeclareTheirSection(t *testing.T) {
	w := newWorld(t, setup{})
	got := []string{}
	for _, table := range []*kit.Router{w.s.Early(), w.s.Router()} {
		for _, route := range table.Routes() {
			if route.Section == nil {
				continue
			}
			line := route.Method + " " + route.Path + " -> "
			if route.Section.Section == "" {
				line += "no section"
			} else {
				line += route.Section.Section
			}
			if route.Section.Level != "" {
				line += " at " + string(route.Section.Level)
			}
			got = append(got, line)
		}
	}
	sort.Strings(got)
	want := []string{
		// A242: what Support chat and Team chat both use, and Team chat's own
		"DELETE /api/chat/canned/{id} -> no section",
		"DELETE /api/chat/team/teams/{id} -> team-chat",
		"GET /api/chat/canned -> no section",
		"GET /api/chat/conversations/{id} -> no section",
		"GET /api/chat/files/{fileId} -> no section",
		"GET /api/chat/me -> no section",
		"GET /api/chat/messages/{id}/trail -> no section",
		"GET /api/chat/search -> no section",
		"GET /api/chat/settings -> no section",
		"GET /api/chat/summary -> no section",
		"GET /api/chat/team -> team-chat",
		"GET /api/chat/team/teams -> team-chat",
		"GET /api/users/{id}/avatar -> no section",
		"PATCH /api/chat/canned/{id} -> no section",
		"PATCH /api/chat/team/groups/{id} -> team-chat",
		"PATCH /api/chat/team/teams/{id} -> team-chat",
		"POST /api/chat/canned -> no section",
		"POST /api/chat/team/direct -> team-chat",
		"POST /api/chat/team/groups -> team-chat",
		"POST /api/chat/team/teams -> team-chat",
		"POST /api/chat/upload -> no section",
		"POST /api/network/targets/{id}/check -> toolkit",
		// Sign in as: the users section at write (A274), so a row that
		// leaves an admin's Users at read takes it away; the route itself
		// also needs user.impersonate (only super admins by default) and
		// refuses the caller's own account and any with as many rights or more
		"POST /api/users/{id}/impersonate -> users at write",
		"PUT /api/chat/me -> no section",
		"PUT /api/chat/settings -> no section",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("routes with their own section:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// A profile picture needs a session only, like the list of pictures
// beside it: viewer and guest_issuer, who have no user.view, get the
// route's answer and not the users section's refusal.
func TestProfilePictureIsForEveryAccount(t *testing.T) {
	w := newWorld(t, setup{})
	const missing = `{"success":false,"error":"not_found"}`
	for _, role := range []string{"viewer", "guest_issuer", "operator", "global_admin", "server_admin", "admin", "super_admin"} {
		a := w.agent().signIn(role)
		for _, path := range []string{"/api/users/999999/avatar", "/API/Users/999999/Avatar", "/api/users/999999/avatar/"} {
			if res := a.get(path); res.status != 404 || strings.TrimSpace(res.body) != missing {
				t.Errorf("%s GET %s: %d %s", role, path, res.status, res.body)
			}
		}
	}
	// The rest of the users section stays closed to them
	for _, role := range []string{"viewer", "guest_issuer"} {
		if res := w.agent().signIn(role).get("/api/users"); res.status != 403 || !strings.Contains(res.body, "Panel access denied: users") {
			t.Errorf("%s GET /api/users: %d %s", role, res.status, res.body)
		}
	}
	// A row that closes the users section does not take the pictures away
	w.setSection("global_admin", "users", "none")
	a := w.agent().signIn("global_admin")
	if res := a.get("/api/users/999999/avatar"); res.status != 404 || strings.TrimSpace(res.body) != missing {
		t.Errorf("users at none, the picture: %d %s", res.status, res.body)
	}
	if res := a.get("/api/users"); res.status != 403 || !strings.Contains(res.body, "Panel access denied: users") {
		t.Errorf("users at none, the list: %d %s", res.status, res.body)
	}
	// Signed out it is the route's own 401
	if res := w.agent().get("/api/users/999999/avatar"); res.status != 401 || !strings.Contains(res.body, "session_required") {
		t.Errorf("signed out: %d %s", res.status, res.body)
	}
}

// Checking a stored network target asks device.connect, like the four
// tools of the network page: it belongs to the toolkit section, not to
// the network section, whose writes ask server.config.
func TestNetworkTargetCheckIsAToolkitRoute(t *testing.T) {
	w := newWorld(t, setup{})
	const (
		path     = "/api/network/targets/999999/check"
		missing  = `{"error":"Target not found"}`
		toolkit  = `{"success":false,"error":"Panel access denied: toolkit","code":"panel_access_denied"}`
		network  = `{"success":false,"error":"Panel access denied: network","code":"panel_access_denied"}`
		notFound = 404
	)
	for role, want := range map[string]struct {
		status int
		body   string
	}{
		"operator": {notFound, missing}, "global_admin": {notFound, missing},
		"admin": {notFound, missing}, "super_admin": {notFound, missing},
		"viewer": {403, toolkit}, "guest_issuer": {403, toolkit}, "server_admin": {403, toolkit},
	} {
		a := w.agent().signIn(role)
		for _, target := range []string{path, "/API/Network/Targets/999999/Check"} {
			if res := a.post(target, "{}"); res.status != want.status || strings.TrimSpace(res.body) != want.body {
				t.Errorf("%s POST %s: %d %s, want %d %s", role, target, res.status, res.body, want.status, want.body)
			}
		}
	}
	// A real target: an admin stores it, an operator runs its check (which
	// reports the target down: the console does not connect to loopback)
	front, _ := url.Parse(w.front.URL)
	created := w.agent().signIn("admin").post("/api/network/targets",
		`{"name":"front","host":"`+front.Hostname()+`","port":`+front.Port()+`,"check_type":"tcp","timeout_ms":3000,"interval_ms":30000}`)
	id, _ := created.json(t)["id"].(float64)
	if created.status >= 300 || id == 0 {
		t.Fatalf("create a target: %d %s", created.status, created.body)
	}
	if res := w.agent().signIn("operator").post("/api/network/targets/"+strconv.Itoa(int(id))+"/check", "{}"); res.status != 200 || res.json(t)["target_id"] != id {
		t.Errorf("operator checks a stored target: %d %s", res.status, res.body)
	}
	// The other writes of the network section still ask its write level
	if res := w.agent().signIn("operator").post("/api/network/targets", "{}"); res.status != 403 || strings.TrimSpace(res.body) != network {
		t.Errorf("operator POST /api/network/targets: %d %s", res.status, res.body)
	}
	// Reading the check's address is no route: the path rule answers
	if res := w.agent().signIn("viewer").get(path); res.status != 404 {
		t.Errorf("viewer GET %s: %d %s", path, res.status, res.body)
	}

	// Rows: the toolkit section decides, the network section does not
	w.setSection("operator", "toolkit", "none")
	if res := w.agent().signIn("operator").post(path, "{}"); res.status != 403 || strings.TrimSpace(res.body) != toolkit {
		t.Errorf("toolkit at none: %d %s", res.status, res.body)
	}
	w.setSection("global_admin", "network", "none")
	a := w.agent().signIn("global_admin")
	if res := a.post(path, "{}"); res.status != notFound || strings.TrimSpace(res.body) != missing {
		t.Errorf("network at none, the check: %d %s", res.status, res.body)
	}
	if res := a.get("/api/network/targets"); res.status != 403 || strings.TrimSpace(res.body) != network {
		t.Errorf("network at none, the list: %d %s", res.status, res.body)
	}
	// A row cannot raise an account above its role (A14): a viewer with
	// toolkit at write still has no device.connect
	w.setSection("viewer", "toolkit", "write")
	if res := w.agent().signIn("viewer").post(path, "{}"); res.status != 403 {
		t.Errorf("viewer with toolkit at write: %d %s", res.status, res.body)
	}
}

// The early stage answers before the route table, so its route is the
// one the guard asks, also when it then leaves the request to the table.
func TestGuardAsksTheEarlyStageFirst(t *testing.T) {
	w := newWorld(t, setup{})
	w.s.Early().NoSection().Get("/api/users/early/probe", kit.PassOn)
	w.s.Router().Get("/api/users/early/probe", probe)
	w.s.Early().Get("/api/users/late/probe", kit.PassOn)
	w.s.Router().NoSection().Get("/api/users/late/probe", probe)
	a := w.agent().signIn("viewer")
	if res := a.get("/api/users/early/probe"); res.status != 200 {
		t.Errorf("declared in the early stage: %d %s", res.status, res.body)
	}
	if res := a.get("/api/users/late/probe"); res.status != 403 || !strings.Contains(res.body, "Panel access denied: users") {
		t.Errorf("declared only behind an early route: %d %s", res.status, res.body)
	}
}
