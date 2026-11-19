package statuspush

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/access"
	"github.com/unitronix/betterdesk-server/console/devicegroups"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
	"github.com/unitronix/betterdesk-server/console/ws"
	"github.com/unitronix/betterdesk-server/console/ws/wstest"
)

// handshake is an upgrade request to path of the oracle's client, with a
// session and more header lines.
func handshake(path, session, more string) string {
	return "GET " + path + " HTTP/1.1\r\nHost: console.test\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n" +
		"X-Oracle-Session: " + session + "\r\n" + more + "\r\n"
}

// TestBothSocketsCheckTheOrigin: /ws/device-status and /ws/panel-events
// refuse an upgrade from a page of another site, and one without Origin
// that carries a session cookie, with 403 before anything else, as every
// other socket does; the console's own page gets the handshake.
func TestBothSocketsCheckTheOrigin(t *testing.T) {
	_, _, srv := newServer(t, Options{})
	addr := strings.TrimPrefix(srv.URL, "http://")
	admin := `{"userId":1,"user":{"role":"admin"}}`
	for _, path := range []string{"/ws/device-status", "/ws/panel-events"} {
		for _, c := range []struct {
			name, session, more, want string
		}{
			{"foreign origin", admin, "Origin: https://evil.example\r\n", "HTTP/1.1 403 Forbidden\r\n\r\n"},
			{"foreign origin, signed out", "", "Origin: https://evil.example\r\n", "HTTP/1.1 403 Forbidden\r\n\r\n"},
			{"no origin with a session cookie", admin, "Cookie: bd.sid=x\r\n", "HTTP/1.1 403 Forbidden\r\n\r\n"},
			{"own origin", admin, "Origin: http://console.test\r\n", "HTTP/1.1 101 Switching Protocols\r\n"},
			{"no origin, no cookie", admin, "", "HTTP/1.1 101 Switching Protocols\r\n"},
		} {
			got, err := wstest.Raw(addr, handshake(path, c.session, c.more))
			if err != nil {
				t.Fatalf("%s %s: %v", path, c.name, err)
			}
			if !strings.HasPrefix(got, c.want) {
				t.Errorf("%s %s: %q, want %q", path, c.name, got, c.want)
			}
		}
	}
}

// TestDeviceStatusNeedsDeviceView: /ws/device-status takes only an
// account with device.view (403; 503 when the lookup fails), as the
// device list it serves does.
func TestDeviceStatusNeedsDeviceView(t *testing.T) {
	_, _, srv := newServer(t, Options{})
	addr := strings.TrimPrefix(srv.URL, "http://")
	for role, want := range map[string]string{
		"viewer":  "HTTP/1.1 101 Switching Protocols\r\n",
		"nothing": "HTTP/1.1 403 Forbidden\r\n\r\n",
		"broken":  "HTTP/1.1 503 Service Unavailable\r\n\r\n",
	} {
		got, err := wstest.Raw(addr, handshake("/ws/device-status", `{"userId":1,"user":{"role":"`+role+`"}}`, ""))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(got, want) {
			t.Errorf("%s: %q, want %q", role, got, want)
		}
	}
}

// scopeDevices are the devices the API server knows in the scope test.
var scopeDevices = map[string]bool{"dev1": true, "dev2": true, "dev3": true}

// realScope is the device scope of package access on a test database
// under a restricted default: anna (operator, id 5) is granted dev1, the
// admin sees every device.
func realScope(t *testing.T, db *store.DB) Scope {
	t.Helper()
	ctx := context.Background()
	for _, row := range [][]any{{1, "admin", "admin"}, {5, "anna", "operator"}} {
		if _, err := db.Exec(ctx, "INSERT INTO users (id, username, password_hash, role) VALUES (?, ?, 'x', ?)", row...); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(ctx, "INSERT INTO user_peer_grants (user_id, peer_id) VALUES (?, ?)", 5, "dev1"); err != nil {
		t.Fatal(err)
	}
	scopes := access.New(db, access.NewDBRoleSource(nil), access.Options{DeviceScopeDefault: "restricted"})
	groups := devicegroups.New(devicegroups.Options{DB: db, Access: scopes})
	return Scope{
		Device: func(_ context.Context, id string) *goapi.Object {
			if !scopeDevices[id] {
				return nil
			}
			return goapi.NewObject("id", id)
		},
		Of: groups.DeviceScopeForUser,
	}
}

// TestStatusEventsFollowTheDeviceScope: every message goes only to the
// accounts whose device scope holds the device it is about (DECISIONS
// A54): the status of a device, a new device id, a help request, a
// registration that was decided. A pending registration, and a message
// that names no device the console knows, go only to accounts without a
// limit. The admin gets everything.
func TestStatusEventsFollowTheDeviceScope(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		check := wstest.Goroutines(t)
		reg := ws.New(ws.Options{Session: wstest.OracleSession, Permission: func(context.Context, string, string) (bool, error) {
			return true, nil
		}})
		svc := New(Options{Now: func() time.Time { return time.UnixMilli(7) }})
		svc.Register(reg, realScope(t, db))
		srv := httptest.NewServer(reg)
		addr := strings.TrimPrefix(srv.URL, "http://")
		dial := func(path, session string) *wstest.Client {
			c, _, err := wstest.Dial(addr, path, http.Header{"X-Oracle-Session": {session}})
			if err != nil || c == nil {
				t.Fatalf("%s: %v", path, err)
			}
			return c
		}
		admin := `{"userId":1,"user":{"id":1,"username":"admin","role":"admin"}}`
		anna := `{"userId":5,"user":{"id":5,"username":"anna","role":"operator"}}`
		adminStatus, annaStatus := dial("/ws/device-status", admin), dial("/ws/device-status", anna)
		adminPanel, annaPanel := dial("/ws/panel-events", admin), dial("/ws/panel-events", anna)
		waitClients(t, svc, 2, 2)

		ctx := context.Background()
		event := func(text string) { svc.Handle(ctx, parse(t, text)) }
		event(`{"type":"peer_online","timestamp":"t1","data":{"id":"dev2"}}`)
		event(`{"type":"peer_offline","timestamp":"t2","data":{"id":"dev9"}}`)
		event(`{"type":"peer_id_changed","timestamp":"t3","data":{"old_id":"dev1","new_id":"dev2"}}`)
		event(`{"type":"peer_id_changed","timestamp":"t4","data":{"old_id":"dev2","new_id":"dev1"}}`)
		event(`{"type":"peer_online","timestamp":"t5","data":{"id":"dev1"}}`)
		event(`{"type":"enrollment_pending","timestamp":"p1","data":{"device_id":"dev1"}}`)
		event(`{"type":"help_request","timestamp":"p2","data":{"device_id":"dev2","message":"help"}}`)
		event(`{"type":"device_rejected","timestamp":"p3","data":{"device_id":"dev9"}}`)
		event(`{"type":"help_request","timestamp":"p4","data":{"device_id":"dev1","message":"help"}}`)
		event(`{"type":"device_approved","timestamp":"p5","data":{"device_id":"dev1"}}`)
		if err := svc.PublishPanelEvent(goapi.NewObject("type", "registration_pending",
			"registration", goapi.NewObject("source", "lan", "device_id", "dev1"), "timestamp", float64(1))); err != nil {
			t.Fatal(err)
		}
		svc.PublishPanelText(`{"type":"registration_changed","registration":{"source":"lan","device_id":"dev1","status":"approved"},"timestamp":2}`)
		svc.PublishPanelText(`{"n":3}`)

		texts := func(c *wstest.Client, n int) []string {
			t.Helper()
			if !c.Wait(3*time.Second, func(f []wstest.Frame, _ bool) bool { return len(f) >= n }) {
				t.Errorf("only %d of %d messages", len(c.Frames()), n)
			}
			time.Sleep(100 * time.Millisecond)
			var out []string
			for _, f := range c.Frames() {
				out = append(out, f.Text())
			}
			return out
		}
		stamps := func(list []string) string {
			var out []string
			for _, text := range list {
				msg := parse(t, text)
				out = append(out, fmt.Sprint(msg.Get("timestamp")))
			}
			return strings.Join(out, " ")
		}
		if got := stamps(texts(adminStatus, 5)); got != "t1 t2 t3 t4 t5" {
			t.Errorf("admin status messages %s", got)
		}
		if got := stamps(texts(annaStatus, 2)); got != "t4 t5" {
			t.Errorf("anna status messages %s, want t4 t5", got)
		}
		if got := texts(adminPanel, 8); len(got) != 8 || got[7] != `{"n":3}` || stamps(got[:7]) != "p1 p2 p3 p4 p5 1 2" {
			t.Errorf("admin panel messages %q", got)
		}
		if got := stamps(texts(annaPanel, 3)); got != "p4 p5 2" {
			t.Errorf("anna panel messages %s, want p4 p5 2", got)
		}
		srv.Close()
		reg.Close()
		for _, c := range []*wstest.Client{adminStatus, annaStatus, adminPanel, annaPanel} {
			c.Drop()
		}
		check()
	})
}
