package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// ask calls the routing report and returns the status and the decoded answer.
func ask(t *testing.T, h http.Handler, query url.Values) (int, map[string]string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", RoutingPath+"?"+query.Encode(), nil))
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("answer is not JSON: %v (%q)", err, rec.Body.String())
	}
	return rec.Code, body
}

func TestRoutingNamesWhoAnswers(t *testing.T) {
	// The Go table of this test holds GET /api/ported and nothing else.
	var seen []string
	served := func(r *http.Request) bool {
		seen = append(seen, r.Method+" "+r.URL.EscapedPath())
		return r.Method == "GET" && r.URL.Path == "/api/ported"
	}
	cases := []struct {
		method, path string
		hasUpstream  bool
		want         string
	}{
		{"GET", "/api/ported", true, HandlerGo},
		{"GET", "/api/ported", false, HandlerGo},
		{"get", "/api/ported", true, HandlerGo},
		{"POST", "/api/ported", true, HandlerNode},
		{"GET", "/api/other", true, HandlerNode},
		{"GET", "/api/other", false, HandlerNone},
		{"GET", "/api/ported?x=1", true, HandlerGo},
	}
	for _, c := range cases {
		code, body := ask(t, Routing(served, c.hasUpstream), url.Values{"method": {c.method}, "path": {c.path}})
		if code != http.StatusOK || body["handler"] != c.want {
			t.Errorf("%s %s (upstream %v) = %d %v, want handler %q", c.method, c.path, c.hasUpstream, code, body, c.want)
		}
	}
	if seen[0] != "GET /api/ported" || seen[2] != "GET /api/ported" {
		t.Errorf("lookup saw %q, want the method in capitals and the path as sent", seen)
	}
}

func TestRoutingKeepsEscapedPath(t *testing.T) {
	var got string
	served := func(r *http.Request) bool {
		got = r.URL.EscapedPath()
		return false
	}
	ask(t, Routing(served, true), url.Values{"method": {"GET"}, "path": {"/api/devices/a%2Fb"}})
	if got != "/api/devices/a%2Fb" {
		t.Errorf("lookup saw path %q, want the escaped slash kept", got)
	}
}

func TestRoutingRejectsBadQuestions(t *testing.T) {
	served := func(*http.Request) bool {
		t.Error("lookup ran for a bad question")
		return true
	}
	for _, q := range []url.Values{
		{},
		{"method": {"GET"}},
		{"path": {"/api/x"}},
		{"method": {"GET"}, "path": {"api/x"}},
		{"method": {"GET"}, "path": {"/api/%zz"}},
		{"method": {"GET"}, "path": {"http://other.example/api/x"}},
	} {
		code, body := ask(t, Routing(served, true), q)
		if code != http.StatusBadRequest || body["error"] == "" || body["handler"] != "" {
			t.Errorf("%v = %d %v, want 400 with an error", q, code, body)
		}
	}
}
