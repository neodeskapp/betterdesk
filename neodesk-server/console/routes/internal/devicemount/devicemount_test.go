package devicemount

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
)

// A group mounted at its own prefix and at /api/nd, with a later route
// under /api/nd, as the console has them. The table shows who answers.
func TestNot(t *testing.T) {
	router := kit.NewRouter()
	group := func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("group " + r.PathValue("id") + " at " + SpelledMount(r)))
	}
	// The order of the old console: the device mount first
	router.Mount("/api/nd").Get("/{id}", group, Not)
	router.Mount("/api/nd").Get("/", group, Not)
	router.Mount("/api/inventory").Get("/{id}", group, Not)
	router.Mount("/api/inventory").Get("/", group, Not)
	router.Get("/api/nd/operator-info", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("later")) })

	for target, want := range map[string]string{
		// its own mount, in any spelling
		"/api/inventory/D1":   "group D1 at /api/inventory",
		"/API/Inventory/D1":   "group D1 at /API/Inventory",
		"/api/inventory":      "group  at /api/inventory",
		"/api/inventory/":     "group  at /api/inventory",
		"/api/inventory/D1?x": "group D1 at /api/inventory",
		// the device mount, spelled exactly: the group steps aside
		"/api/nd/operator-info":       "later",
		"/api/nd/operator-info/":      "later",
		"/api/nd/Operator-Info":       "later",
		"/api/nd/operator-info?a=/b":  "later",
		"/api/nd/other":               "",
		"/api/nd":                     "",
		"/api/nd/":                    "",
		"/api/nd?x=/API/ND":           "",
		"/api/nd/operator-info/extra": "",
		// the device mount in any other spelling: the group steps aside
		// too (BUGS.md DV8), so the later route answers
		"/API/ND/Operator-Info/": "later",
		"/API/ND/operator-info":  "later",
		"/Api/nd/operator-info":  "later",
		"/API/ND/D1":             "",
		"/api/nD/x":              "",
		"/api/Nd":                "",
		"/API/ND/":               "",
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
	router.Get("/api/nd", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("mount " + SpelledMount(r)))
	}, Not)
	rec := httptest.NewRecorder()
	if !router.Serve(rec, httptest.NewRequest(http.MethodGet, "/api/nd", nil)) || rec.Body.String() != "mount " {
		t.Errorf("a route at the root answered %q", rec.Body.String())
	}
}
