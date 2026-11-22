package remote

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// RV30: an id that starts with rt_ is a remote target only when the API
// server has a target of that id; else it is a device like any other. A
// device named rt_device1 gets the viewer's device data, asks
// device.connect and takes a guest link, while a real target keeps the
// target's checks and still takes no guest link.
func TestAnIDWithTheTargetPrefixIsADeviceWhenNoTargetHasIt(t *testing.T) {
	w, sc := scopedWorld(t)
	sc.Devices["rt_device1"] = json.RawMessage(`{"id":"rt_device1","hostname":"Lab PC","platform":"Linux","tags":[]}`)
	sc.API["GET /api/peers/rt_device1"] = apiAnswer{Status: http.StatusOK, Type: "application/json", Body: `{"id":"rt_device1","device_type":"rustdesk"}`}
	link := func(peer string) scene {
		s := sc
		s.API = map[string]apiAnswer{}
		for k, v := range sc.API {
			s.API[k] = v
		}
		s.API["GET /api/guest/access-links/validate"] = apiAnswer{Status: http.StatusOK, Type: "application/json",
			Body: `{"valid":true,"id":"g1","peer_ids":["` + peer + `"],"view_only":false,"expires_at":"2099-01-01T00:00:00Z"}`}
		return s
	}
	operator := as(5, "e2e_operator", "operator")
	for _, c := range []struct {
		name     string
		who      *account
		path     string
		world    scene
		status   int
		contains []string
	}{
		{"a device", operator, "/api/remote/viewer/rt_device1", sc, 200,
			[]string{`"deviceId":"rt_device1"`, `"name":"Lab PC"`, `"transport":"rd"`, `"remote_target":false`}},
		{"a device without device.connect", as(3, "e2e_viewer", "viewer"), "/api/remote/viewer/rt_device1", sc, 403,
			[]string{"Permission denied: device.connect"}},
		{"a device by a guest link", nil, "/api/remote/viewer/rt_device1?guest=tok", link("rt_device1"), 200,
			[]string{`"deviceId":"rt_device1"`, `"guest_access":true`, `"remote_target":false`}},
		{"a target", operator, "/api/remote/viewer/rt_inside01", sc, 200,
			[]string{`"remote_target":true`, `"name":"Inside"`}},
		{"a target by a guest link", nil, "/api/remote/viewer/rt_inside01?guest=tok", link("rt_inside01"), 401,
			[]string{"Unauthorized"}},
	} {
		rec := w.do(&vector{Request: request{Method: "GET", Path: c.path, Session: c.who}, World: c.world})
		body := rec.Body.String()
		if rec.Code != c.status {
			t.Errorf("%s: %d %s, want %d", c.name, rec.Code, body, c.status)
			continue
		}
		for _, want := range c.contains {
			if !strings.Contains(body, want) {
				t.Errorf("%s: %s, want %s in it", c.name, body, want)
			}
		}
	}
}
