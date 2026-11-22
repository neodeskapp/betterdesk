package cdapproxy

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/unitronix/betterdesk-server/console/ws/wstest"
)

// featuresOf answers FeaturesOff from a fixed list per account; account 99
// fails the lookup.
func featuresOf(off map[int64][]string) func(context.Context, int64) ([]string, error) {
	return func(_ context.Context, userID int64) ([]string, error) {
		if userID == 99 {
			return nil, errors.New("store down")
		}
		return off[userID], nil
	}
}

// onChannel is the session header of an account for a channel's socket.
func onChannel(id int64, channel string) http.Header {
	h := as(id, "anna")
	h.Set("Sec-WebSocket-Protocol", "cdap-"+channel)
	return h
}

// A channel that needs a feature the account may not use is refused with
// 403: the terminal, the file browser and audio. The desktop and video
// open. A failed lookup refuses them all.
func TestChannelsOfAFeatureTheAccountMayNotUseAreRefused(t *testing.T) {
	addr := proxyAt(t, Options{Devices: knownDevices{}, CanAccess: everyDevice, FeaturesOff: featuresOf(map[int64][]string{
		30: {"terminal", "file_transfer", "audio"},
		31: {"clipboard"},
	})})
	for _, c := range []struct {
		account int64
		channel string
		open    bool
	}{
		{30, "terminal", false}, {30, "files", false}, {30, "audio", false},
		{30, "desktop", true}, {30, "video", true},
		{31, "terminal", true}, {31, "files", true}, {31, "audio", true},
		{99, "terminal", false}, {99, "files", false}, {99, "audio", false}, {99, "desktop", true},
	} {
		conn, status := open(t, addr, "/api/cdap/devices/dev1/"+c.channel, onChannel(c.account, c.channel))
		if got := conn != nil; got != c.open {
			t.Errorf("account %d, %s: open %v (status %d), want %v", c.account, c.channel, got, status, c.open)
		}
		if conn == nil && status != http.StatusForbidden {
			t.Errorf("account %d, %s: refused with %d, want 403", c.account, c.channel, status)
		}
		if conn != nil {
			conn.Close(1000)
		}
	}
}

// deviceDesktop is an API server whose desktop socket first sends the
// device's clipboard, a cursor and a screen frame, then sends back what
// it gets.
func deviceDesktop() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{r.Header.Get("Sec-WebSocket-Protocol")}})
		if err != nil {
			return
		}
		defer c.CloseNow()
		for _, m := range []struct {
			typ  websocket.MessageType
			data string
		}{
			{websocket.MessageText, `{"type":"clipboard_update","format":"text","data":"secret"}`},
			{websocket.MessageText, `{"type":"cursor_update"}`},
			{websocket.MessageBinary, `{"type":"clipboard_update"}`},
		} {
			if err := c.Write(r.Context(), m.typ, []byte(m.data)); err != nil {
				return
			}
		}
		for {
			typ, data, err := c.Read(r.Context())
			if err != nil {
				return
			}
			if err := c.Write(r.Context(), typ, []byte("echo:"+string(data))); err != nil {
				return
			}
		}
	})
}

// The desktop drops what the account may not do: with remote control and
// the clipboard off the browser's input, input controls and clipboard
// never reach the device, text or binary, and the device's clipboard
// never reaches the browser; the screen and everything else pass. With
// every feature on, all of it passes.
func TestTheDesktopDropsWhatTheAccountMayNotDo(t *testing.T) {
	addr := proxyAt(t, Options{API: deviceDesktop(), Devices: knownDevices{}, CanAccess: everyDevice,
		FeaturesOff: featuresOf(map[int64][]string{40: {"remote_control", "clipboard", "reboot"}})})
	sent := []string{
		`{"type":"input","event":"mouse_move","x":1}`,
		`{"type":"block_input","enabled":true}`,
		`{"type":"lock_screen"}`,
		`{"type":"clipboard_set","format":"text","data":"x"}`,
		`{"TYPE":"input"}`,
		`{"type":"restart_device"}`,
		`{"type":"privacy_mode","enabled":true}`,
		`{"type":"resize","width":800,"height":600}`,
		`not json`,
	}
	for _, c := range []struct {
		account int64
		want    []string
	}{
		{40, []string{
			`{"type":"cursor_update"}`, `{"type":"clipboard_update"}`,
			`echo:{"type":"privacy_mode","enabled":true}`, `echo:{"type":"resize","width":800,"height":600}`, `echo:not json`,
			`echo:{"type":"resize","binary":true}`,
		}},
		{41, nil},
	} {
		conn, status := open(t, addr, "/api/cdap/devices/dev1/desktop", onChannel(c.account, "desktop"))
		if conn == nil {
			t.Fatalf("account %d: refused with %d", c.account, status)
		}
		for _, m := range sent {
			if err := conn.Text(m); err != nil {
				t.Fatal(err)
			}
		}
		if err := conn.Binary([]byte(`{"type":"input"}`)); err != nil {
			t.Fatal(err)
		}
		if err := conn.Binary([]byte(`{"type":"resize","binary":true}`)); err != nil {
			t.Fatal(err)
		}
		want := c.want
		if want == nil {
			want = []string{`{"type":"clipboard_update","format":"text","data":"secret"}`, `{"type":"cursor_update"}`, `{"type":"clipboard_update"}`}
			for _, m := range sent {
				want = append(want, "echo:"+m)
			}
			want = append(want, `echo:{"type":"input"}`, `echo:{"type":"resize","binary":true}`)
		}
		if !conn.Wait(5*time.Second, func(frames []wstest.Frame, _ bool) bool { return len(frames) >= len(want) }) {
			t.Fatalf("account %d: got %d messages, want %d", c.account, len(conn.Frames()), len(want))
		}
		time.Sleep(100 * time.Millisecond)
		var got []string
		for _, f := range conn.Frames() {
			got = append(got, f.Text())
		}
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Errorf("account %d: the browser got\n%s\nwant\n%s", c.account, strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
		conn.Close(1000)
	}
}
