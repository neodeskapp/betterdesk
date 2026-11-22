package meshcentral

import (
	"context"
	"testing"

	"github.com/unitronix/betterdesk-server/console/access"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// featuresFor is the console's access service with the test account (7)
// held to the client features in off.
func featuresFor(t *testing.T, off ...string) *access.Service {
	t.Helper()
	svc := access.New(storetest.Migrated(t), nil, access.Options{})
	rows := []any{}
	for _, key := range off {
		rows = append(rows, map[string]any{"feature_key": key, "enabled": false})
	}
	if _, err := svc.SavePolicy(context.Background(), "user", "7", []any{}, rows); err != nil {
		t.Fatal(err)
	}
	return svc
}

func tunnelAnswer(string, string) answer { return jsonAnswer(200, `{"relay_id":"r1"}`) }

// The terminal, commands, the file browser, TCP and UDP tunnels and the
// power actions other than wake are refused, before the API server is
// asked, when the account may not use their feature.
func TestMeshSessionsTheAccountMayNotOpenAreRefused(t *testing.T) {
	w := newWorldWith(t, tunnelAnswer, "", featuresFor(t, "terminal", "file_transfer", "tunnel", "reboot"))
	for _, c := range []struct{ path, body, refusal string }{
		{"terminal", "", "The terminal is turned off for your account"},
		{"exec", `{"command":"id"}`, "The terminal is turned off for your account"},
		{"files", "", "File transfer is turned off for your account"},
		{"tcp", `{"host":"127.0.0.1","port":22}`, "TCP tunnels are turned off for your account"},
		{"udp", `{"host":"127.0.0.1","port":53}`, "TCP tunnels are turned off for your account"},
		{"power", `{"action":"reboot"}`, "Remote reboot is turned off for your account"},
		{"power", `{"action":" Shutdown "}`, "Remote reboot is turned off for your account"},
		{"power", `{"action":"sleep"}`, "Remote reboot is turned off for your account"},
	} {
		rec := w.do("admin", "POST", "/api/mesh/devices/d1/"+c.path, c.body)
		expect(t, c.path+" "+c.body, rec, 403, `{"error":"`+c.refusal+`"}`)
		if got := w.sent(); got != "" {
			t.Errorf("%s: the API server got %q", c.path, got)
		}
	}
	// Waking a device needs no feature.
	rec := w.do("admin", "POST", "/api/mesh/devices/d1/power", `{"action":"Wake"}`)
	expect(t, "wake", rec, 200, `{"relay_id":"r1"}`)
	if got := w.sent(); got != `POST /mesh/devices/d1/power {"action":"Wake"}` {
		t.Errorf("wake: the API server got %q", got)
	}

	// With every feature on they all go on.
	w = newWorldWith(t, tunnelAnswer, "", featuresFor(t, "clipboard"))
	for _, path := range []string{"terminal", "files", "tcp", "power"} {
		rec := w.do("admin", "POST", "/api/mesh/devices/d1/"+path, `{"action":"reboot"}`)
		expect(t, path+" with the feature", rec, 200, `{"relay_id":"r1"}`)
	}
}

// Without remote control the account's desktop tunnel and the share links
// it makes are view only, whatever it asked for; with remote control they
// are as asked.
func TestMeshDesktopWithoutRemoteControlIsViewOnly(t *testing.T) {
	w := newWorldWith(t, tunnelAnswer, "", featuresFor(t, "remote_control"))
	w.do("operator", "POST", "/api/mesh/devices/d1/desktop?record=1", "{}")
	if got := w.sent(); got != "POST /mesh/devices/d1/desktop?record=1&view_only=1" {
		t.Errorf("desktop: the API server got %q", got)
	}
	w.do("operator", "POST", "/api/mesh/devices/d1/share", `{"ttl_minutes":30,"view_only":false}`)
	if got := w.sent(); got != `POST /mesh/devices/d1/share {"ttl_minutes":30,"view_only":true}` {
		t.Errorf("share: the API server got %q", got)
	}

	w = newWorldWith(t, tunnelAnswer, "", featuresFor(t))
	w.do("operator", "POST", "/api/mesh/devices/d1/desktop?record=1", "{}")
	if got := w.sent(); got != "POST /mesh/devices/d1/desktop?record=1" {
		t.Errorf("desktop with remote control: the API server got %q", got)
	}
	w.do("operator", "POST", "/api/mesh/devices/d1/share", `{"ttl_minutes":30,"view_only":false}`)
	if got := w.sent(); got != `POST /mesh/devices/d1/share {"ttl_minutes":30,"view_only":false}` {
		t.Errorf("share with remote control: the API server got %q", got)
	}
}
