package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/db"
	"github.com/unitronix/betterdesk-server/peer"
)

// scopedFixture is the package's server with a device group that keeps
// PEERAAA1 for rv_boss: the operator key sees PEERBBB2 only.
func scopedFixture(t *testing.T) *Server {
	t.Helper()
	srv, database := newInternalServer(t, nil)
	srv.SetPanelStore(&mockPanelACLStore{
		userIDs: map[string]int64{"rv_boss": 7},
		groups: []db.PanelDeviceGroup{{
			ID: 1, GUID: "dg-boss", Name: "Boss", AllowedUsers: []string{"rv_boss"},
		}},
		members: map[int64][]string{1: {"PEERAAA1"}},
	})
	for _, id := range []string{"PEERAAA1", "PEERBBB2"} {
		if err := database.UpdatePeerTags(id, "rv"); err != nil {
			t.Fatal(err)
		}
		srv.peers.Put(&peer.Entry{ID: id})
	}
	return srv
}

// TestDeviceRoutesKeepToTheScope: every route that names one device applies
// the device scope that GET /api/peers filters by, and the routes that list
// devices leave out the ones outside it; the console's in-process identity
// sees every device (RV173).
func TestDeviceRoutesKeepToTheScope(t *testing.T) {
	srv := scopedFixture(t)
	key := map[string]string{"X-API-Key": testOperatorKey}

	if status, body := listenerCall(t, srv, http.MethodGet, "/api/peers", "", key); status != http.StatusOK || strings.Contains(body, "PEERAAA1") {
		t.Fatalf("GET /api/peers: %d %s, want 200 without PEERAAA1", status, body)
	}

	routes := []struct{ method, path, body string }{
		{http.MethodGet, "/api/peers/%s", ""},
		{http.MethodPatch, "/api/peers/%s", `{"note":"x"}`},
		{http.MethodPut, "/api/peers/%s/tags", `{"tags":"rv"}`},
		{http.MethodGet, "/api/peers/%s/status", ""},
		{http.MethodGet, "/api/peers/%s/linked", ""},
		{http.MethodGet, "/api/peers/%s/telemetry", ""},
		{http.MethodPost, "/api/peers/%s/telemetry/command", `{"command":"files.browse","args":{"path":"C:/"}}`},
		{http.MethodPost, "/api/peers/%s/wol", `{"mac":"00:11:22:33:44:55"}`},
		{http.MethodGet, "/api/peers/%s/access-policy", ""},
		{http.MethodPost, "/api/peers/%s/session-grant", `{}`},
		{http.MethodGet, "/api/peers/%s/connect-password", ""},
		{http.MethodPost, "/api/mesh/devices/%s/share", `{}`},
		{http.MethodPost, "/api/cdap/devices/%s/command", `{"widget_id":"w","action":"a"}`},
		{http.MethodPost, "/api/mesh/devices/%s/desktop", `{}`},
		{http.MethodPost, "/api/guest/access-links", `{"peer_ids":["%s"],"ttl_minutes":30}`},
	}
	for _, rt := range routes {
		out := strings.Replace(rt.path, "%s", "PEERAAA1", 1)
		outBody := strings.Replace(rt.body, "%s", "PEERAAA1", 1)
		status, body := listenerCall(t, srv, rt.method, out, outBody, key)
		if status != http.StatusForbidden || !strings.Contains(body, "Device not in your scope") {
			t.Errorf("%s %s %s outside the scope: %d %s, want 403", rt.method, out, outBody, status, body)
		}
		in := strings.Replace(rt.path, "%s", "PEERBBB2", 1)
		inBody := strings.Replace(rt.body, "%s", "PEERBBB2", 1)
		if status, body := listenerCall(t, srv, rt.method, in, inBody, key); strings.Contains(body, "not in your scope") {
			t.Errorf("%s %s %s inside the scope: %d %s, want past the scope", rt.method, in, inBody, status, body)
		}
	}

	for _, path := range []string{"/api/tags/rv/peers", "/api/peers/online"} {
		status, body := listenerCall(t, srv, http.MethodGet, path, "", key)
		if status != http.StatusOK || strings.Contains(body, "PEERAAA1") || !strings.Contains(body, "PEERBBB2") {
			t.Errorf("GET %s: %d %s, want 200 with PEERBBB2 only", path, status, body)
		}
		status, body = asConsole(t, srv, http.MethodGet, path, "")
		if status != http.StatusOK || !strings.Contains(body, "PEERAAA1") {
			t.Errorf("GET %s in process: %d %s, want PEERAAA1 listed", path, status, body)
		}
	}

	for _, path := range []string{"/api/peers/PEERAAA1", "/api/peers/PEERAAA1/status", "/api/peers/PEERAAA1/access-policy"} {
		if status, body := asConsole(t, srv, http.MethodGet, path, ""); status != http.StatusOK {
			t.Errorf("GET %s in process: %d %s, want 200", path, status, body)
		}
	}
}
