package goapipath_test

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/goapipath"
	"github.com/unitronix/betterdesk-server/console/goapipath/proxytest"
	"github.com/unitronix/betterdesk-server/console/kit"
)

// register adds the routes of testdata/nodeoracle/proxy.js.
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

func TestProxyVectors(t *testing.T) {
	proxytest.Replay(t, filepath.Join("testdata", "proxy.json"), register)
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
