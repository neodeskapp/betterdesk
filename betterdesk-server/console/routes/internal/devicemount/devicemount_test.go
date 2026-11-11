package devicemount

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/unitronix/betterdesk-server/console/kit"
)

// A group mounted at its own prefix and at /api/bd, with a later route
// under /api/bd, as the console has them. The table shows who answers.
func TestNot(t *testing.T) {
	router := kit.NewRouter()
	group := func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("group " + r.PathValue("id") + " at " + SpelledMount(r)))
	}
	// The order of the old console: the device mount first
	router.Mount("/api/bd").Get("/{id}", group, Not)
	router.Mount("/api/bd").Get("/", group, Not)
	router.Mount("/api/inventory").Get("/{id}", group, Not)
	router.Mount("/api/inventory").Get("/", group, Not)
	router.Get("/api/bd/operator-info", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("later")) })

	for target, want := range map[string]string{
		// its own mount, in any spelling
		"/api/inventory/D1":   "group D1 at /api/inventory",
		"/API/Inventory/D1":   "group D1 at /API/Inventory",
		"/api/inventory":      "group  at /api/inventory",
		"/api/inventory/":     "group  at /api/inventory",
		"/api/inventory/D1?x": "group D1 at /api/inventory",
		// the device mount, spelled exactly: the group steps aside
		"/api/bd/operator-info":       "later",
		"/api/bd/operator-info/":      "later",
		"/api/bd/Operator-Info":       "later",
		"/api/bd/operator-info?a=/b":  "later",
		"/api/bd/other":               "",
		"/api/bd":                     "",
		"/api/bd/":                    "",
		"/api/bd?x=/API/BD":           "",
		"/api/bd/operator-info/extra": "",
		// the device mount in another spelling: the group answers, and
		// the later route never sees the request
		"/API/BD/Operator-Info/": "group Operator-Info at /API/BD",
		"/API/BD/operator-info":  "group operator-info at /API/BD",
		"/Api/bd/operator-info":  "group operator-info at /Api/bd",
		"/api/bD/x":              "group x at /api/bD",
		"/api/Bd":                "group  at /api/Bd",
		"/API/BD/":               "group  at /API/BD",
	} {
		req := httptest.NewRequest(http.MethodGet, "http://console.test/", nil)
		req.RequestURI = target
		parsed, err := req.URL.Parse(target)
		if err != nil {
			t.Fatalf("%s: %v", target, err)
		}
		req.URL = parsed
		rec := httptest.NewRecorder()
		answered := router.Serve(rec, req)
		if got := rec.Body.String(); got != want || answered != (want != "") {
			t.Errorf("GET %s: answered %v with %q, want %q", target, answered, got, want)
		}
	}
}

// A route at the root has no mount to compare.
func TestSpelledMountAtTheRoot(t *testing.T) {
	router := kit.NewRouter()
	router.Get("/api/bd", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("mount " + SpelledMount(r)))
	}, Not)
	rec := httptest.NewRecorder()
	if !router.Serve(rec, httptest.NewRequest(http.MethodGet, "/api/bd", nil)) || rec.Body.String() != "mount " {
		t.Errorf("a route at the root answered %q", rec.Body.String())
	}
}
