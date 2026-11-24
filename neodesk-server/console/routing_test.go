package console

import (
	"net/url"
	"strings"
	"testing"
)

// TestRoutingReportNamesGo: the routing report says Go answers every
// request, routes, files, pages, sockets and the 404 alike.
func TestRoutingReportNamesGo(t *testing.T) {
	w := newWorld(t, setup{debug: true})
	w.s.Router().Get("/api/ported/{id}", probe)
	who := func(method, path string, upgrade bool) string {
		q := url.Values{"method": {method}, "path": {path}}
		if upgrade {
			q.Set("upgrade", "websocket")
		}
		res := w.agent().get("/__nd/routing?" + q.Encode())
		if res.status != 200 || res.header.Get("Cache-Control") != "no-store" || res.header.Get("Content-Type") != "application/json; charset=utf-8" {
			t.Fatalf("routing report: %d %v %s", res.status, res.header, res.body)
		}
		handler, _ := res.json(t)["handler"].(string)
		return handler
	}
	for _, path := range []string{"/api/client-devices", "/api/ported/7", "/api/ported/7?x=1", "/api/devices/a%2Fb", "/", "/login", "/favicon.svg", "/nothing/here", "/__nd/routing"} {
		for _, method := range []string{"GET", "get", "DELETE"} {
			if got := who(method, path, false); got != "go" {
				t.Errorf("%s %s: %q, want go", method, path, got)
			}
		}
	}
	for _, path := range []string{"/ws/device-status", "/ws/nothing"} {
		if got := who("GET", path, true); got != "go" {
			t.Errorf("an upgrade to %s: %q, want go", path, got)
		}
	}
}

// TestRoutingReportRejectsBadQuestions: a question without a method or a
// request path gets 400 with an error and no handler.
func TestRoutingReportRejectsBadQuestions(t *testing.T) {
	w := newWorld(t, setup{debug: true})
	for _, q := range []url.Values{
		{},
		{"method": {"GET"}},
		{"method": {" "}, "path": {"/api/x"}},
		{"path": {"/api/x"}},
		{"method": {"GET"}, "path": {"api/x"}},
		{"method": {"GET"}, "path": {"/api/%zz"}},
		{"method": {"GET"}, "path": {"http://other.example/api/x"}},
	} {
		res := w.agent().get("/__nd/routing?" + q.Encode())
		body := res.json(t)
		if res.status != 400 || body["error"] == nil || body["error"] == "" || body["handler"] != nil {
			t.Errorf("%v = %d %s, want 400 with an error", q, res.status, res.body)
		}
	}
}

func TestRoutingReportIsOffByDefault(t *testing.T) {
	w := newWorld(t, setup{})
	if res := w.agent().get("/__nd/routing?method=GET&path=/x"); res.status != 404 || strings.Contains(res.body, "handler") {
		t.Errorf("status %d %s, want the 404", res.status, res.body)
	}
}
