package console

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/api"
	"github.com/neodeskapp/neodesk/neodesk-server/config"
	"github.com/neodeskapp/neodesk/neodesk-server/console/authn"
	consoleconfig "github.com/neodeskapp/neodesk/neodesk-server/console/config"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/chat"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
	"github.com/neodeskapp/neodesk/neodesk-server/console/ws/wstest"
	"github.com/neodeskapp/neodesk/neodesk-server/events"
	"github.com/neodeskapp/neodesk/neodesk-server/peer"
)

// The computer's enrollment token of these tests: a device token from
// Add computers, Device tokens, bound to the computer.
const enrolledToken = "device-token-from-the-tokens-page-1"

// revokeWorld is a console with the server's API on one database and one
// event bus, the computer's chat socket open at the API port with its
// device token, and the chat's watch of the bus running as the console
// runs it. It returns the token's id.
func revokeWorld(t *testing.T) (*world, *wstest.Client, int64) {
	t.Helper()
	db := storetest.Migrated(t)
	gdb := storetest.GoServer(t, db)
	bus := events.NewBus()
	apiSrv := api.New(config.DefaultConfig(), gdb, peer.NewMap(), nil, "test")
	apiSrv.SetEventBus(bus)
	files := t.TempDir()
	w := newWorld(t, setup{db: db, api: apiSrv.InternalHandler(), listener: apiSrv.ListenerHandler(),
		configure: func(c *consoleconfig.Config) { c.ConsoleDir = files },
		options:   func(o *Options) { o.Events = bus }})
	ctx := context.Background()
	if _, err := db.Exec(ctx, `INSERT INTO peers (id) VALUES ('`+chatDevice+`')`); err != nil {
		t.Fatal(err)
	}
	id, err := db.Insert(ctx, `INSERT INTO device_tokens (token_hash, peer_id, status, max_uses, use_count) VALUES (?, ?, 'active', 0, 0)`,
		authn.HashDeviceToken(enrolledToken), chatDevice)
	if err != nil {
		t.Fatal(err)
	}
	apiSrv.SetDeviceChat(w.s)
	front := httptest.NewServer(apiSrv.ListenerHandler())
	t.Cleanup(front.Close)
	watch, stop := context.WithCancel(ctx)
	t.Cleanup(stop)
	d := w.s.Deps()
	go chat.HubOf(d).WatchDeviceCredentials(watch, d.GoAPI.Events)
	time.Sleep(100 * time.Millisecond)

	computer, status := dialChat(t, strings.TrimPrefix(front.URL, "http://"), "/ws/chat/"+chatDevice+"?v=2&token="+enrolledToken, nil)
	if computer == nil || !waitText(computer, `"type":"welcome"`) {
		t.Fatalf("the computer's socket: %d", status)
	}
	return w, computer, id
}

// closedAsRefused reports whether the socket closed with the code a
// client reads as "token refused".
func closedAsRefused(c *wstest.Client) (bool, int) {
	c.Wait(5*time.Second, func(_ []wstest.Frame, closed bool) bool { return closed })
	closed, code, _ := c.Closed()
	return closed && code == 4403, code
}

// TestRevokingTheTokenClosesTheComputersChat: revoking the computer's
// device token on the Device tokens page (DELETE /api/panel/tokens/{id})
// closes its open chat socket at once with 4403 (L539). Before, the
// computer went on reading and writing until its socket dropped.
func TestRevokingTheTokenClosesTheComputersChat(t *testing.T) {
	w, computer, id := revokeWorld(t)
	admin := w.agent().signIn("admin")
	res := admin.do("DELETE", "/api/panel/tokens/"+strconv.FormatInt(id, 10), "", "X-CSRF-Token", admin.csrf())
	if res.status != http.StatusOK {
		t.Fatalf("revoke: %d %s", res.status, res.body)
	}
	if ok, code := closedAsRefused(computer); !ok {
		t.Errorf("the computer's socket after the revoke: closed with %d, want 4403", code)
	}
}

// TestBanningTheComputerClosesItsChat: a ban ends what the computer may
// do, its chat too.
func TestBanningTheComputerClosesItsChat(t *testing.T) {
	w, computer, _ := revokeWorld(t)
	admin := w.agent().signIn("admin")
	res := admin.do("POST", "/api/devices/"+chatDevice+"/ban", `{"reason":"lost laptop"}`, "Content-Type", "application/json", "X-CSRF-Token", admin.csrf())
	if res.status != http.StatusOK {
		t.Fatalf("ban: %d %s", res.status, res.body)
	}
	if ok, code := closedAsRefused(computer); !ok {
		t.Errorf("the computer's socket after the ban: closed with %d, want 4403", code)
	}
}

// TestAWorkingTokenKeepsItsChat: what ends another computer's credentials
// leaves this one's socket open.
func TestAWorkingTokenKeepsItsChat(t *testing.T) {
	w, computer, _ := revokeWorld(t)
	chat.HubOf(w.s.Deps()).RecheckDevices(context.Background(), "")
	time.Sleep(300 * time.Millisecond)
	if closed, code, _ := computer.Closed(); closed {
		t.Errorf("a working token's socket closed with %d", code)
	}
}
