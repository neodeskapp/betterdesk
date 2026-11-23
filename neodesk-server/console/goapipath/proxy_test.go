package goapipath_test

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapipath"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapipath/proxytest"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
)

// register adds the routes the recording of testdata/proxy.json ran.
func register(d *kit.Deps) {
	sendsBody := func(r *http.Request) []goapi.CallOption {
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch:
			return []goapi.CallOption{goapipath.Body(goapipath.RequestBody(r))}
		}
		return nil
	}
	d.Router.All("/json/{a}", func(w http.ResponseWriter, r *http.Request) {
		goapipath.ProxyToGo(d.GoAPI, w, r, r.Method, goapipath.PathOf(r, "/x/{a}"), sendsBody(r)...)
	})
	d.Router.All("/json/{a}/{b}", func(w http.ResponseWriter, r *http.Request) {
		goapipath.ProxyToGo(d.GoAPI, w, r, r.Method, goapipath.PathOf(r, "/x/{a}/{b}"), sendsBody(r)...)
	})
	d.Router.Get("/fixed", func(w http.ResponseWriter, r *http.Request) {
		goapipath.ProxyToGo(d.GoAPI, w, r, "GET", goapipath.Fixed(r.URL.Query().Get("path")))
	})
	d.Router.Get("/binary/{a}", func(w http.ResponseWriter, r *http.Request) {
		goapipath.ProxyBinaryToGo(d.GoAPI, w, r, "GET", goapipath.PathOf(r, "/files/{a}"))
	})
}

// proxyFixes are the vectors of proxy.json whose answer a bug fix of
// BUGS.md changed on purpose (proxytest.Replay).
var proxyFixes = slices.Concat(proxytest.Fixes("FL4",
	// An id with two dots in it is refused before a call is built.
	60, 61, 62, 63, 64, 65,
), proxytest.Fixes("FL6",
	// The body goes to the API server with every member, also __proto__,
	// constructor and prototype.
	135, 136, 148, 149, 161, 162,
))

func TestProxyVectors(t *testing.T) {
	proxytest.Replay(t, filepath.Join("testdata", "proxy.json"), register, proxyFixes...)
}

func TestPathOfStopsAtTheFirstRefusedParameter(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.SetPathValue("orgId", "bad id")
	r.SetPathValue("resourceId", "also bad")
	if _, err := goapipath.PathOf(r, "/fleet/resources/{orgId}/{resourceId}")(); err == nil || err.Error() != "Invalid orgId" {
		t.Errorf("error = %v, want Invalid orgId", err)
	}
	r.SetPathValue("orgId", "acme")
	if _, err := goapipath.PathOf(r, "/fleet/resources/{orgId}/{resourceId}")(); err == nil || err.Error() != "Invalid resourceId" {
		t.Errorf("error = %v, want Invalid resourceId", err)
	}
	r.SetPathValue("resourceId", "r:1@x")
	path, err := goapipath.PathOf(r, "/fleet/resources/{orgId}/{resourceId}")()
	if err != nil || path != "/fleet/resources/acme/r%3A1%40x" {
		t.Errorf("path = %q, %v", path, err)
	}
}

func TestProxyAnswersWhenTheCallNeverLeaves(t *testing.T) {
	w := proxytest.NewWorld(t, register)
	// A path the client's own check refuses with a text that is no
	// "Invalid ...": no call, 500
	rec, calls := w.Do(proxytest.Request{Path: "/fixed?path=/a/../b"}, nil)
	if rec.Code != 500 || rec.Body.String() != `{"error":"Go server unreachable"}` || len(calls) != 0 {
		t.Errorf("got %d %s after %d calls", rec.Code, rec.Body.String(), len(calls))
	}
	rec, calls = w.Do(proxytest.Request{Path: "/fixed?path=/a%5Cb"}, nil)
	if rec.Code != 400 || rec.Body.String() != `{"error":"Invalid API path"}` || len(calls) != 0 {
		t.Errorf("got %d %s after %d calls", rec.Code, rec.Body.String(), len(calls))
	}
}

// TestBodyKeepsEveryMember: a body goes to the API server with every
// member the client sent, also __proto__, constructor and prototype, at
// any depth (BUGS.md FL6).
func TestBodyKeepsEveryMember(t *testing.T) {
	w := proxytest.NewWorld(t, register)
	ok := &proxytest.Answer{Status: 200, Type: "application/json", Body: `{"ok":true}`}
	for _, body := range []string{
		`{"constructor":"x","keep":1}`,
		`{"__proto__":{"x":1},"prototype":2}`,
		`{"keep":{"prototype":1,"inner":{"constructor":2}},"list":[{"__proto__":3}]}`,
	} {
		sent := body
		rec, calls := w.Do(proxytest.Request{Method: "PUT", Path: "/json/one", Type: "application/json", Body: &sent}, ok)
		if rec.Code != 200 || len(calls) != 1 || calls[0].Body != body {
			t.Errorf("PUT %s: %d, the API server got %+v", body, rec.Code, calls)
		}
	}
}
