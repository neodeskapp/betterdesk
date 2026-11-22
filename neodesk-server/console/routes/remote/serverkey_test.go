package remote

import (
	"encoding/json"
	"strings"
	"testing"
)

// RV210: the RustDesk client checks the device's identity against the
// server's public key and stops at the handshake without one. When the
// key is missing or cannot be read, the viewer's data for a RustDesk
// session is refused with a reason instead of carrying an empty key;
// the transports that do not use the key still get their data.
func TestViewerRefusesARustDeskSessionWithoutTheServerKey(t *testing.T) {
	w, sc := scopedWorld(t)
	operator := as(5, "e2e_operator", "operator")
	const refused = `{"success":false,"code":"server_key_unavailable","error":"The server's public key could not be read, so the device's identity cannot be checked."}`
	for _, key := range []string{`""`, `{"throws":"no key"}`} {
		world := sc
		world.ServerKey = json.RawMessage(key)
		for _, c := range []struct {
			path   string
			status int
			body   string
		}{
			{"/api/remote/viewer/dev-1", 503, refused},
			{"/api/remote/viewer/dev-1?transport=rd", 503, refused},
			{"/api/remote/viewer/dev-1?transport=mesh", 200, `"serverPubKey":""`},
			{"/api/remote/viewer/rt_inside01", 200, `"serverPubKey":""`},
		} {
			rec := w.do(&vector{Request: request{Method: "GET", Path: c.path, Session: operator}, World: world})
			body := strings.TrimSpace(rec.Body.String())
			if rec.Code != c.status || (c.status == 503 && body != c.body) || (c.status == 200 && !strings.Contains(body, c.body)) {
				t.Errorf("key %s, %s: %d %s, want %d %s", key, c.path, rec.Code, body, c.status, c.body)
			}
		}
	}
	// With the key the RustDesk session gets it
	rec := w.do(&vector{Request: request{Method: "GET", Path: "/api/remote/viewer/dev-1", Session: operator}, World: sc})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"serverPubKey":"server-key-base64="`) {
		t.Errorf("with the key: %d %s", rec.Code, rec.Body.String())
	}
}
