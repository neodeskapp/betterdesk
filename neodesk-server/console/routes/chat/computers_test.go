package chat

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// dialRaw opens a socket and returns the refusal's status and headers when
// the server refuses it.
func dialRaw(t *testing.T, addr, path string) (*websocket.Conn, *http.Response) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(ctx, "ws://"+addr+path, nil)
	if err == nil {
		t.Cleanup(func() { _ = conn.CloseNow() })
		return conn, resp
	}
	return nil, resp
}

// TestChatWithComputersOff: with "Chat with computers" off (A259) a
// computer's socket of version 2 is refused with 503, the code chat_off
// and a time to ask again, and an open one closes; team chat between
// accounts goes on. On again, the computer gets in.
func TestChatWithComputersOff(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		relay, st := hubRelay(t, db)
		ctx := context.Background()
		open := relay.v2(t, "dev1")
		open.take()

		if err := st.SetSetting(ctx, settingComputers, "0"); err != nil {
			t.Fatal(err)
		}
		relay.relay.opts.Hub.ComputersChanged(ctx)
		if !open.closedWithin(3 * time.Second) {
			t.Error("the computer's open socket stayed open")
		}
		conn, resp := dialRaw(t, relay.addr, "/ws/chat/dev2?v=2&token=tok-dev2")
		if conn != nil || resp == nil || resp.StatusCode != http.StatusServiceUnavailable ||
			resp.Header.Get("X-NeoDesk-Chat") != "chat_off" || resp.Header.Get("Retry-After") != "600" {
			status, header := 0, http.Header{}
			if resp != nil {
				status, header = resp.StatusCode, resp.Header
			}
			t.Fatalf("a computer with chat off: %d %v", status, header)
		}

		// Colleagues still write to each other
		if _, err := st.Direct(ctx, 5, 6); err != nil {
			t.Fatal(err)
		}
		anna, ben := relay.v2(t, "anna"), relay.v2(t, "ben")
		anna.take()
		ben.take()
		anna.send(t, `{"type":"send","conversation_id":"dm:5:6","text":"lunch?"}`)
		if got := frames(ben.takeAtLeast(1), "message.new"); len(got) != 1 || textOf(got[0]) != "lunch?" {
			t.Errorf("team chat with computers off: %v", got)
		}

		if err := st.SetSetting(ctx, settingComputers, "1"); err != nil {
			t.Fatal(err)
		}
		if conn, resp := dialRaw(t, relay.addr, "/ws/chat/dev2?v=2&token=tok-dev2"); conn == nil {
			status := 0
			if resp != nil {
				status = resp.StatusCode
			}
			t.Errorf("a computer with chat on again: %d", status)
		}
	})
}

// TestChatWithComputersSetting: the setting is on by default, an admin
// turns it off and on with the other chat settings, a save without it
// keeps it, and each change is audited like its neighbours.
func TestChatWithComputersSetting(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := newInboxWorld(t, db)
		if _, got := w.get("anna", "/api/chat/settings"); settingsOf(got)["computers"] != true {
			t.Errorf("the default: %v", got)
		}
		if status, _ := w.send("admin", "PUT", "/api/chat/settings", `{"mail_after_minutes":15,"mail_preview":true,"computers":false}`); status != 200 {
			t.Fatalf("turning it off: %d", status)
		}
		if _, got := w.get("anna", "/api/chat/settings"); settingsOf(got)["computers"] != false {
			t.Errorf("after turning it off: %v", got)
		}
		if len(w.audits) != 1 || !strings.Contains(w.audits[0], `"computers":false`) || !strings.HasPrefix(w.audits[0], "chat_settings_changed ") {
			t.Errorf("audit: %q", w.audits)
		}
		// An older page that doesn't know the setting leaves it as it is
		if status, _ := w.send("admin", "PUT", "/api/chat/settings", `{"mail_after_minutes":20,"mail_preview":true}`); status != 200 {
			t.Fatalf("a save without it: %d", status)
		}
		if _, got := w.get("anna", "/api/chat/settings"); settingsOf(got)["computers"] != false || settingsOf(got)["mail_after_minutes"] != float64(20) {
			t.Errorf("after a save without it: %v", got)
		}
		if status, _ := w.send("anna", "PUT", "/api/chat/settings", `{"mail_after_minutes":15,"mail_preview":true,"computers":true}`); status != 403 {
			t.Errorf("an operator turning it on: %d", status)
		}
	})
}

func settingsOf(answer map[string]any) map[string]any {
	s, _ := answer["settings"].(map[string]any)
	return s
}

// closedWithin reports whether the server closed the socket within d.
func (s *testSocket) closedWithin(d time.Duration) bool {
	select {
	case <-s.done:
		return true
	case <-time.After(d):
		return false
	}
}
