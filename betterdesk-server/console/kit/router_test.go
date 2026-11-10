package kit

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTestRouter() *Router {
	rt := NewRouter()
	echo := func(name string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Route", name)
			w.Header().Set("X-Id", r.PathValue("id"))
			w.Header().Set("X-Rest", r.PathValue("rest"))
		}
	}
	rt.Get("/", echo("root"))
	rt.Get("/api/devices", echo("list"))
	rt.Get("/api/devices/{id}", echo("one"))
	rt.Get("/api/devices/stats", echo("stats"))
	rt.Post("/api/devices/{id}/ban", echo("ban"))
	rt.Get("/files/{rest...}", echo("files"))
	rt.Handle("", "/any", echo("any"))
	return rt
}

func TestRouterLookup(t *testing.T) {
	rt := newTestRouter()
	cases := []struct {
		method, target string
		route          string // "" means no route
		id, rest       string
	}{
		{"GET", "/", "root", "", ""},
		{"GET", "/api/devices", "list", "", ""},
		{"HEAD", "/api/devices", "list", "", ""},
		{"GET", "/api/devices/", "list", "", ""},
		{"GET", "/API/Devices", "list", "", ""},
		{"GET", "/api/devices/AbC123", "one", "AbC123", ""},
		{"GET", "/Api/Devices/AbC123/", "one", "AbC123", ""},
		{"GET", "/api/devices/a%2Fb", "one", "a/b", ""},
		{"GET", "/api/devices/stats", "stats", "", ""},
		{"GET", "/api/devices/STATS", "stats", "", ""},
		{"POST", "/api/devices/7/ban", "ban", "7", ""},
		{"POST", "/api/devices/7/BAN", "ban", "7", ""},
		{"GET", "/files/a/b.txt", "files", "", "a/b.txt"},
		{"GET", "/files/", "files", "", ""},
		{"DELETE", "/any", "any", "", ""},

		{"POST", "/api/devices", "", "", ""},
		{"GET", "/api/devices/7/ban", "", "", ""},
		{"GET", "/api/devices//", "", "", ""},
		{"GET", "/api//devices", "", "", ""},
		{"GET", "/api/devices/../devices", "", "", ""},
		{"GET", "/files", "", "", ""},
		{"GET", "/nope", "", "", ""},
	}
	for _, c := range cases {
		t.Run(c.method+" "+c.target, func(t *testing.T) {
			req := httptest.NewRequest(c.method, c.target, nil)
			h, ok := rt.Lookup(req)
			if c.route == "" {
				if ok {
					t.Fatalf("matched a route, want none")
				}
				return
			}
			if !ok {
				t.Fatalf("no route, want %q", c.route)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if got := rec.Header().Get("X-Route"); got != c.route {
				t.Errorf("route = %q (status %d), want %q", got, rec.Code, c.route)
			}
			if got := rec.Header().Get("X-Id"); got != c.id {
				t.Errorf("id = %q, want %q", got, c.id)
			}
			if got := rec.Header().Get("X-Rest"); got != c.rest {
				t.Errorf("rest = %q, want %q", got, c.rest)
			}
		})
	}
}

func TestRouterMiddlewareOrder(t *testing.T) {
	rt := NewRouter()
	var order []string
	mark := func(name string) Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name)
				next.ServeHTTP(w, r)
			})
		}
	}
	rt.Get("/x", func(http.ResponseWriter, *http.Request) { order = append(order, "handler") }, mark("first"), mark("second"))
	req := httptest.NewRequest("GET", "/x", nil)
	h, ok := rt.Lookup(req)
	if !ok {
		t.Fatal("no route")
	}
	h.ServeHTTP(httptest.NewRecorder(), req)
	want := []string{"first", "second", "handler"}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
}

func TestRouterRoutes(t *testing.T) {
	routes := newTestRouter().Routes()
	if len(routes) != 7 {
		t.Fatalf("got %d routes, want 7", len(routes))
	}
	if routes[2].Method != "GET" || routes[2].Path != "/api/devices/{id}" {
		t.Errorf("third route = %+v", routes[2])
	}
}
