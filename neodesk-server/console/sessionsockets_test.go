package console

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/ws/wstest"
)

// TestEndingASessionClosesItsSockets: the sockets a browser opened with a
// session close the moment that session ends from another browser of the
// account, and the other browser's sockets stay open. Without this a live
// device list or a chat would go on in a browser that was signed out.
func TestEndingASessionClosesItsSockets(t *testing.T) {
	w := newWorld(t, setup{})
	addr := strings.TrimPrefix(w.front.URL, "http://")
	keep, gone := w.agent().signIn("admin"), w.agent().signIn("admin")

	open := func(a *agent, path string) *wstest.Client {
		t.Helper()
		c, res, err := wstest.Dial(addr, path, http.Header{"Cookie": {a.cookieHeader()}, "Origin": {"http://" + addr}})
		if err != nil || c == nil {
			status := 0
			if res != nil {
				status = res.StatusCode
			}
			t.Fatalf("dial %s: %v %d", path, err, status)
		}
		t.Cleanup(c.Drop)
		return c
	}
	paths := []string{"/ws/device-status", "/ws/chat-operator/panel", "/ws/chat-operator/panel?v=2"}
	var kept, ended []*wstest.Client
	for _, p := range paths {
		kept = append(kept, open(keep, p))
		ended = append(ended, open(gone, p))
	}

	// The entry of the browser to sign out, from its own list
	var list struct {
		Data struct {
			Sessions []struct {
				ID      string `json:"id"`
				Current bool   `json:"current"`
			} `json:"sessions"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(gone.get("/api/users/me/sessions").body), &list); err != nil {
		t.Fatal(err)
	}
	target := ""
	for _, s := range list.Data.Sessions {
		if s.Current {
			target = s.ID
		}
	}
	if target == "" {
		t.Fatal("no current entry")
	}
	if res := keep.do("DELETE", "/api/users/me/sessions/"+target, "", "X-CSRF-Token", keep.csrf()); res.status != http.StatusOK {
		t.Fatalf("end: %d %s", res.status, res.body)
	}
	for i, c := range ended {
		if !c.Wait(2*time.Second, func(_ []wstest.Frame, closed bool) bool { return closed }) {
			t.Errorf("%s of the signed-out browser is still open", paths[i])
		}
	}
	for i, c := range kept {
		if closed, code, reason := c.Closed(); closed {
			t.Errorf("%s of the other browser closed: %d %q", paths[i], code, reason)
		}
	}
}
