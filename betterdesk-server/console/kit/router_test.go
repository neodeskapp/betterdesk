package kit

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func echo(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Route", name)
		w.Header().Set("X-Id", r.PathValue("id"))
		w.Header().Set("X-Rest", r.PathValue("rest"))
		w.Header().Set("X-Mount", MountPath(r))
		w.Header().Set("X-Route-Path", RoutePath(r))
	}
}

func newTestRouter() *Router {
	rt := NewRouter()
	rt.Get("/", echo("root"))
	rt.Get("/api/devices", echo("list"))
	rt.Get("/api/devices/stats", echo("stats"))
	rt.Get("/api/devices/{id}", echo("one"))
	rt.Get("/api/devices/late", echo("late"))
	rt.Post("/api/devices/{id}/ban", echo("ban"))
	rt.Get("/files/{rest...}", echo("files"))
	rt.All("/any", echo("any"))
	return rt
}

func serve(rt *Router, method, target string) (*httptest.ResponseRecorder, bool) {
	rec := httptest.NewRecorder()
	ok := rt.Serve(rec, httptest.NewRequest(method, target, nil))
	return rec, ok
}

func TestRouterServe(t *testing.T) {
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
		// The first route that fits answers, not the most specific one
		{"GET", "/api/devices/late", "one", "late", ""},
		{"POST", "/api/devices/7/ban", "ban", "7", ""},
		{"POST", "/api/devices/7/BAN", "ban", "7", ""},
		{"GET", "/files/a/b.txt", "files", "", "a/b.txt"},
		{"GET", "/files/a%20b/c", "files", "", "a b/c"},
		{"GET", "/files/", "files", "", ""},
		{"DELETE", "/any", "any", "", ""},
		{"OPTIONS", "/any", "any", "", ""},

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
			rec := httptest.NewRecorder()
			ok := rt.Serve(rec, req)
			if rt.Match(req) != ok {
				t.Errorf("Match = %v, Serve = %v: they must agree when no route passes on", rt.Match(req), ok)
			}
			if c.route == "" {
				if ok {
					t.Fatalf("answered by %q, want no route", rec.Header().Get("X-Route"))
				}
				if len(rec.Header()) != 0 || rec.Body.Len() != 0 {
					t.Errorf("an unanswered request left headers %v", rec.Header())
				}
				return
			}
			if !ok {
				t.Fatalf("no route, want %q", c.route)
			}
			if got := rec.Header().Get("X-Route"); got != c.route {
				t.Errorf("route = %q, want %q", got, c.route)
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
	if _, ok := serve(rt, "GET", "/x"); !ok {
		t.Fatal("no route")
	}
	if got := strings.Join(order, ","); got != "first,second,handler" {
		t.Fatalf("order = %s, want first,second,handler", got)
	}
}

func TestRouterRoutes(t *testing.T) {
	rt := newTestRouter()
	rt.Mount("/api/bd").Get("/x", echo("mounted"))
	routes := rt.Routes()
	if len(routes) != 9 {
		t.Fatalf("got %d routes, want 9", len(routes))
	}
	if routes[3] != (Route{Method: "GET", Path: "/api/devices/{id}"}) {
		t.Errorf("fourth route = %+v", routes[3])
	}
	if routes[8] != (Route{Method: "GET", Path: "/api/bd/x", Mount: "/api/bd"}) {
		t.Errorf("mounted route = %+v", routes[8])
	}
}

func TestPassOnGoesToTheNextRouteThatFits(t *testing.T) {
	rt := NewRouter()
	var seen []string
	decline := func(name string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			seen = append(seen, name+":"+r.PathValue("id")+r.PathValue("name"))
			PassOn(w, r)
		}
	}
	rt.Get("/api/x/{id}", decline("first"))
	rt.Post("/api/x/{id}", echo("post"))
	rt.Get("/api/x/{name}", decline("second"))
	rt.Get("/api/x/{id}", echo("third"))
	rt.Get("/api/y", decline("alone"))

	rec, ok := serve(rt, "GET", "/api/x/7")
	if !ok || rec.Header().Get("X-Route") != "third" || rec.Header().Get("X-Id") != "7" {
		t.Errorf("answered=%v by %q, want the third route", ok, rec.Header().Get("X-Route"))
	}
	if got := strings.Join(seen, " "); got != "first:7 second:7" {
		t.Errorf("declining routes saw %q, want first:7 second:7 in that order", got)
	}

	// Every route that fits declines: the request is not answered
	rec, ok = serve(rt, "GET", "/api/y")
	if ok || len(rec.Header()) != 0 {
		t.Errorf("answered=%v headers=%v, want an unanswered request", ok, rec.Header())
	}
	if !rt.Match(httptest.NewRequest("GET", "/api/y", nil)) {
		t.Error("Match must say yes for a path with a route, also when the route would pass on")
	}
}

func TestPassOnFromMiddlewareSkipsTheHandler(t *testing.T) {
	rt := NewRouter()
	called := false
	rt.Get("/api/rules/{id}", func(http.ResponseWriter, *http.Request) { called = true }, Digits("id"))
	rt.Get("/api/rules/{name}", echo("by-name"))

	rec, _ := serve(rt, "GET", "/api/rules/abc")
	if called || rec.Header().Get("X-Route") != "by-name" {
		t.Errorf("handler called=%v, answered by %q; want the digits route to step aside", called, rec.Header().Get("X-Route"))
	}
	if _, ok := serve(rt, "GET", "/api/rules/12"); !ok || !called {
		t.Errorf("a numeric id must reach the digits route")
	}
}

func TestMountedRoutes(t *testing.T) {
	rt := NewRouter()
	register := func(r *Router, name string) {
		r.Get("/", echo(name+"-root"))
		r.Get("/{id}", echo(name+"-one"), OnMount("/api/inventory"))
		r.Get("/commands", echo(name+"-commands"), OnMount("/api/bd"))
	}
	register(rt.Mount("/api/bd"), "bd")
	register(rt.Mount("/api/inventory"), "inv")

	cases := []struct{ target, route, mount, path string }{
		{"/api/bd", "bd-root", "/api/bd", "/"},
		{"/api/bd/", "bd-root", "/api/bd", "/"},
		{"/API/BD/commands", "bd-commands", "/api/bd", "/commands"},
		{"/api/inventory", "inv-root", "/api/inventory", "/"},
		{"/api/inventory/7", "inv-one", "/api/inventory", "/7"},
		// On the device mount the {id} route steps aside, and nothing else fits
		{"/api/bd/7", "", "", ""},
		// On the admin mount "commands" is an id: the first route that fits answers
		{"/api/inventory/commands", "inv-one", "/api/inventory", "/commands"},
	}
	for _, c := range cases {
		rec, ok := serve(rt, "GET", c.target)
		if c.route == "" {
			if ok {
				t.Errorf("%s answered by %q, want nobody", c.target, rec.Header().Get("X-Route"))
			}
			continue
		}
		h := rec.Header()
		if !ok || h.Get("X-Route") != c.route || h.Get("X-Mount") != c.mount || h.Get("X-Route-Path") != c.path {
			t.Errorf("%s: answered=%v route=%q mount=%q path=%q, want %q %q %q", c.target, ok,
				h.Get("X-Route"), h.Get("X-Mount"), h.Get("X-Route-Path"), c.route, c.mount, c.path)
		}
	}
}

func TestOptionsListsTheMethodsOfAPath(t *testing.T) {
	rt := NewRouter()
	rt.Get("/health", echo("get"))
	rt.Post("/thing/{id}", echo("post"))
	rt.Get("/thing/{id}", echo("get"))
	rt.Delete("/thing/{id}", echo("delete"))

	rec, ok := serve(rt, "OPTIONS", "/health")
	if !ok || rec.Code != 200 || rec.Header().Get("Allow") != "GET,HEAD" || rec.Body.String() != "GET,HEAD" {
		t.Errorf("OPTIONS /health: answered=%v status=%d Allow=%q body=%q", ok, rec.Code, rec.Header().Get("Allow"), rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	rec, _ = serve(rt, "OPTIONS", "/thing/7")
	if got := rec.Header().Get("Allow"); got != "POST,GET,HEAD,DELETE" {
		t.Errorf("Allow = %q, want the methods in the order of the routes", got)
	}
	if _, ok := serve(rt, "OPTIONS", "/nothing"); ok {
		t.Error("OPTIONS on a path without a route was answered")
	}
}

func TestParamThatDoesNotDecode(t *testing.T) {
	rt := NewRouter()
	called := false
	rt.Get("/api/devices/{id}", func(http.ResponseWriter, *http.Request) { called = true })

	rec, ok := serve(rt, "GET", "/api/devices/%ff")
	if !ok || called || rec.Code != http.StatusBadRequest {
		t.Errorf("answered=%v handler=%v status=%d, want a 400 without the handler", ok, called, rec.Code)
	}

	var got error
	rt.OnBadParam(func(w http.ResponseWriter, _ *http.Request, err error) {
		got = err
		w.WriteHeader(http.StatusTeapot)
	})
	rec, _ = serve(rt, "GET", "/api/devices/%c3%28")
	if rec.Code != http.StatusTeapot || got == nil || got.Error() != "Failed to decode param '%c3%28'" {
		t.Errorf("status=%d err=%v, want the hook with the old console's message", rec.Code, got)
	}
}

func TestMountRefusesBadPrefixes(t *testing.T) {
	for _, prefix := range []string{"api", "/api/", ""} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("Mount(%q) did not panic", prefix)
				}
			}()
			NewRouter().Mount(prefix)
		}()
	}
}
