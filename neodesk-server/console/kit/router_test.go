package kit

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/access"
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

// The table reads the path as the client sent it, the text the steps in
// front of it read: Go writes the path anew as soon as it holds a byte Go
// would have escaped, and must not turn an escaped fixed segment into one
// that matches.
func TestRouterMatchesThePathAsSent(t *testing.T) {
	rt := newTestRouter()
	cases := []struct {
		target   string
		route    string // "" means no route
		id, rest string
	}{
		{`/api/%64evices/7"`, "", "", ""},
		{`/api/devices%2F7"`, "", "", ""},
		{"/api/devic%65s/st%61ts|", "", "", ""},
		{"/%61pi/devices/x|y", "", "", ""},
		{`/api/devices/a"b%20c`, "one", `a"b c`, ""},
		{"/api/devices/\xc3\xa4|", "one", "ä|", ""},
		{"/api/devices/7#x", "one", "7", ""},
		{"/files/a%2Fb|c", "files", "", "a/b|c"},
		{"/files/a{b}/c^", "files", "", "a{b}/c^"},
	}
	for _, c := range cases {
		t.Run(c.target, func(t *testing.T) {
			rec, ok := serve(rt, "GET", c.target)
			found, inTable := find(rt, "GET", c.target)
			if c.route == "" {
				if ok || inTable {
					t.Fatalf("answered by %q, Find %v %q; want no route", rec.Header().Get("X-Route"), inTable, found.Path)
				}
				return
			}
			if !ok || !inTable {
				t.Fatalf("Serve %v, Find %v; want %q", ok, inTable, c.route)
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
	rt.Mount("/api/nd").Get("/x", echo("mounted"))
	routes := rt.Routes()
	if len(routes) != 9 {
		t.Fatalf("got %d routes, want 9", len(routes))
	}
	if routes[3] != (Route{Method: "GET", Path: "/api/devices/{id}"}) {
		t.Errorf("fourth route = %+v", routes[3])
	}
	if routes[8] != (Route{Method: "GET", Path: "/api/nd/x", Mount: "/api/nd"}) {
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
		r.Get("/commands", echo(name+"-commands"), OnMount("/api/nd"))
	}
	register(rt.Mount("/api/nd"), "nd")
	register(rt.Mount("/api/inventory"), "inv")

	cases := []struct{ target, route, mount, path string }{
		{"/api/nd", "nd-root", "/api/nd", "/"},
		{"/api/nd/", "nd-root", "/api/nd", "/"},
		{"/API/ND/commands", "nd-commands", "/api/nd", "/commands"},
		{"/api/inventory", "inv-root", "/api/inventory", "/"},
		{"/api/inventory/7", "inv-one", "/api/inventory", "/7"},
		// On the device mount the {id} route steps aside, and nothing else fits
		{"/api/nd/7", "", "", ""},
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

// guardFor is a Use middleware that refuses the id "no" and lets every
// other request go on, noting what it saw.
func guardFor(seen *[]string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			*seen = append(*seen, r.Method+" "+r.URL.Path+" id="+r.PathValue("id"))
			if r.PathValue("id") == "no" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// TestUseRunsInFrontOfTheRoutesAfterIt checks Use the way router.use(path,
// fn) worked: every method and every path below the pattern, at its place
// in the order, also for a path no route answers.
func TestUseRunsInFrontOfTheRoutesAfterIt(t *testing.T) {
	var seen []string
	rt := NewRouter()
	rt.Get("/api/devices/{id}/before", echo("before"))
	rt.Use("/api/devices/{id}", guardFor(&seen))
	rt.Get("/api/devices/{id}", echo("one"))
	rt.Post("/api/devices/{id}/ban", echo("ban"))
	later := rt.Mount("/later")
	later.Patch("/api/devices/{id}/folder", echo("folder"))
	rt.Patch("/api/devices/{id}/folder", echo("folder"))

	for _, c := range []struct {
		method, target string
		answered       bool
		status         int
		route          string
		seen           string
	}{
		// A route in front of it answers without it
		{"GET", "/api/devices/no/before", true, 200, "before", ""},
		{"GET", "/api/devices/A1", true, 200, "one", "GET /api/devices/A1 id=A1"},
		{"GET", "/API/Devices/A1/", true, 200, "one", "GET /API/Devices/A1/ id=A1"},
		{"HEAD", "/api/devices/A1", true, 200, "one", "HEAD /api/devices/A1 id=A1"},
		{"POST", "/api/devices/a%2Fb/ban", true, 200, "ban", "POST /api/devices/a/b/ban id=a/b"},
		{"PATCH", "/api/devices/A1/folder", true, 200, "folder", "PATCH /api/devices/A1/folder id=A1"},
		// It refuses for every method and every path below the pattern
		{"GET", "/api/devices/no", true, 403, "", "GET /api/devices/no id=no"},
		{"PATCH", "/api/devices/no/folder", true, 403, "", "PATCH /api/devices/no/folder id=no"},
		{"PUT", "/api/devices/no/nothing/here", true, 403, "", "PUT /api/devices/no/nothing/here id=no"},
		{"OPTIONS", "/api/devices/no/ban", true, 403, "", "OPTIONS /api/devices/no/ban id=no"},
		// A path it lets through that no route answers stays unanswered
		{"PUT", "/api/devices/A1/nothing", false, 200, "", "PUT /api/devices/A1/nothing id=A1"},
		// A path shorter than the pattern is none of its
		{"GET", "/api/devices", false, 200, "", ""},
		{"GET", "/api/devices/", false, 200, "", ""},
	} {
		seen = nil
		rec, ok := serve(rt, c.method, c.target)
		if ok != c.answered || rec.Code != c.status || rec.Header().Get("X-Route") != c.route || strings.Join(seen, ",") != c.seen {
			t.Errorf("%s %s: answered=%v status=%d route=%q guard saw %q; want %v %d %q %q",
				c.method, c.target, ok, rec.Code, rec.Header().Get("X-Route"), seen, c.answered, c.status, c.route, c.seen)
		}
	}

	// OPTIONS it lets through gets the methods of the routes
	seen = nil
	rec, ok := serve(rt, "OPTIONS", "/api/devices/A1/ban")
	if !ok || rec.Header().Get("Allow") != "POST" || len(seen) != 1 {
		t.Errorf("OPTIONS: answered=%v Allow=%q guard saw %q", ok, rec.Header().Get("Allow"), seen)
	}
	// A parameter that does not decode is the 400 also where no route follows
	rec, ok = serve(rt, "GET", "/api/devices/%ff/nothing")
	if !ok || rec.Code != http.StatusBadRequest {
		t.Errorf("a bad parameter: answered=%v status=%d", ok, rec.Code)
	}
}

// TestUseIsNoRoute checks that what Use adds does not count as a route:
// the table matches only the paths its routes have, and lists only them.
func TestUseIsNoRoute(t *testing.T) {
	var seen []string
	rt := NewRouter()
	rt.Use("/api/devices/{id}", guardFor(&seen))
	rt.Get("/api/devices/{id}", echo("one"))
	for _, c := range []struct {
		method, target string
		match          bool
	}{
		{"GET", "/api/devices/A1", true},
		{"OPTIONS", "/api/devices/A1", true},
		{"POST", "/api/devices/A1", false},
		{"GET", "/api/devices/A1/nothing", false},
		{"OPTIONS", "/api/devices/A1/nothing", false},
	} {
		if got := rt.Match(httptest.NewRequest(c.method, c.target, nil)); got != c.match {
			t.Errorf("Match(%s %s) = %v, want %v", c.method, c.target, got, c.match)
		}
	}
	if routes := rt.Routes(); len(routes) != 1 || routes[0].Path != "/api/devices/{id}" {
		t.Errorf("Routes() = %+v, want the one route", routes)
	}
	defer func() {
		if recover() == nil {
			t.Error("Use with {rest...} did not panic")
		}
	}()
	rt.Use("/files/{rest...}", guardFor(&seen))
}

// Digits looks at a parameter as the client wrote it, as the pattern
// :id(\d+) did before anything was decoded: an escaped digit is no digit,
// and a parameter that does not decode to text passes the request on like
// any other that is no digits (BUGS AU11). Without Digits first, such a
// parameter is refused with 400 before any middleware of the route runs.
func TestDigitsLooksAtTheWrittenPath(t *testing.T) {
	alone := NewRouter()
	alone.Get("/api/tickets/{id}", echo("ticket"), Digits("id"))
	for path, want := range map[string]string{
		"/api/tickets/12": "12", "/api/tickets/007": "007", "/api/tickets/%31": "", "/api/tickets/1%32": "",
		"/api/tickets/%C3%28": "", "/api/tickets/%ff": "", "/api/tickets/%EF%BC%91": "", "/api/tickets/1a": "",
	} {
		rec, ok := serve(alone, "GET", path)
		if ok != (want != "") || rec.Header().Get("X-Id") != want {
			t.Errorf("GET %s: answered %v with %d and id %q, want id %q", path, ok, rec.Code, rec.Header().Get("X-Id"), want)
		}
	}

	next := NewRouter()
	next.Get("/api/tickets/{id}", echo("ticket"), Digits("id"))
	next.Get("/api/tickets/{id}", echo("other"))
	for path, want := range map[string]string{"/api/tickets/5": "ticket 5", "/api/tickets/%35": "other 5", "/api/tickets/%C3%28": "400"} {
		rec, ok := serve(next, "GET", path)
		got := rec.Header().Get("X-Route") + " " + rec.Header().Get("X-Id")
		if rec.Code == http.StatusBadRequest {
			got = "400"
		}
		if !ok || got != want {
			t.Errorf("GET %s with a route after it: answered %v with %q, want %q", path, ok, got, want)
		}
	}

	ran := false
	late := NewRouter()
	late.Get("/api/tickets/{id}", echo("ticket"), func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { ran = true; next.ServeHTTP(w, r) })
	}, Digits("id"))
	if rec, ok := serve(late, "GET", "/api/tickets/%C3%28"); !ok || ran || rec.Code != http.StatusBadRequest {
		t.Errorf("Digits behind other middleware: answered %v with %d, middleware ran %v; want the 400 first", ok, rec.Code, ran)
	}
	if _, ok := serve(late, "GET", "/api/tickets/%31"); ok || !ran {
		t.Errorf("Digits behind other middleware: %%31 answered %v, middleware ran %v; want it passed on after the middleware", ok, ran)
	}
}

// find is Find for a method and target.
func find(rt *Router, method, target string) (Route, bool) {
	return rt.Find(httptest.NewRequest(method, target, nil))
}

// PA3: a route says itself which panel section it belongs to, and the
// table hands that to whoever asks before the route runs.
func TestRoutesDeclareTheirSection(t *testing.T) {
	rt := NewRouter()
	rt.Get("/api/users", echo("list"))
	rt.NoSection().Get("/api/users/{id}/avatar", echo("avatar"))
	rt.Post("/api/users/{id}/avatar", echo("upload"))
	net := rt.Mount("/api/network")
	net.Get("/targets/{id}", echo("target"))
	net.Section("toolkit").Post("/targets/{id}/check", echo("check"))
	rt.SectionLevel("devices", access.LevelRead).Post("/api/devices/{id}/screenshot", echo("shot"))

	type want struct {
		found   bool
		path    string
		section *access.RouteSection
	}
	none, toolkit := &access.RouteSection{}, &access.RouteSection{Section: "toolkit"}
	for _, c := range []struct {
		method, target string
		want           want
	}{
		{"GET", "/api/users", want{true, "/api/users", nil}},
		{"GET", "/api/users/3/avatar", want{true, "/api/users/{id}/avatar", none}},
		{"HEAD", "/api/users/3/avatar", want{true, "/api/users/{id}/avatar", none}},
		{"GET", "/api/users/3/avatar?v=2", want{true, "/api/users/{id}/avatar", none}},
		// in any letter case and with a slash at the end, as the routes match
		{"GET", "/API/Users/3/Avatar/", want{true, "/api/users/{id}/avatar", none}},
		// the declaration belongs to one route, not to its address
		{"POST", "/api/users/3/avatar", want{true, "/api/users/{id}/avatar", nil}},
		{"DELETE", "/api/users/3/avatar", want{}},
		{"OPTIONS", "/api/users/3/avatar", want{}},
		{"GET", "/api/users/3/avatar/x", want{}},
		{"GET", "/api/users/3", want{}},
		{"POST", "/api/network/targets/7/check", want{true, "/api/network/targets/{id}/check", toolkit}},
		{"GET", "/api/network/targets/7", want{true, "/api/network/targets/{id}", nil}},
		{"GET", "/api/network/targets/7/check", want{}},
		{"POST", "/api/devices/A1/screenshot", want{true, "/api/devices/{id}/screenshot", &access.RouteSection{Section: "devices", Level: access.LevelRead}}},
		// no route can have this path
		{"GET", "/api/users/../users/3/avatar", want{}},
	} {
		got, ok := find(rt, c.method, c.target)
		if ok != c.want.found || got.Path != c.want.path {
			t.Errorf("%s %s: route %q found %v, want %q found %v", c.method, c.target, got.Path, ok, c.want.path, c.want.found)
			continue
		}
		switch {
		case (got.Section == nil) != (c.want.section == nil):
			t.Errorf("%s %s: section %+v, want %+v", c.method, c.target, got.Section, c.want.section)
		case got.Section != nil && *got.Section != *c.want.section:
			t.Errorf("%s %s: section %+v, want %+v", c.method, c.target, *got.Section, *c.want.section)
		}
	}

	// Routes lists the declaration too
	declared := map[string]access.RouteSection{}
	for _, route := range rt.Routes() {
		if route.Section != nil {
			declared[route.Method+" "+route.Path] = *route.Section
		}
	}
	if len(declared) != 3 || declared["GET /api/users/{id}/avatar"] != *none || declared["POST /api/network/targets/{id}/check"] != *toolkit {
		t.Errorf("declared routes: %+v", declared)
	}

	// Declaring changes nothing about who answers
	if rec, ok := serve(rt, "POST", "/api/network/targets/7/check"); !ok || rec.Header().Get("X-Route") != "check" || rec.Header().Get("X-Mount") != "/api/network" {
		t.Errorf("the declared route does not answer: %v %v", ok, rec.Header())
	}
}

// Find gives the route that answers first. A route behind it does not
// count, with or without a section of its own; nor does a middleware
// added with Use.
func TestFindIsTheFirstRouteThatFits(t *testing.T) {
	rt := NewRouter()
	rt.Use("/api/things/{id}", func(next http.Handler) http.Handler { return next })
	rt.Get("/api/things/{id}", echo("first"))
	rt.NoSection().Get("/api/things/special", echo("late"))
	if got, ok := find(rt, "GET", "/api/things/special"); !ok || got.Section != nil {
		t.Errorf("a declared route behind another: %+v %v", got, ok)
	}
	if _, ok := find(rt, "GET", "/api/things/special/more"); ok {
		t.Error("a Use entry was found as a route")
	}

	// A route for digits only steps aside for anything else, so the
	// route behind it is the first that fits
	rt = NewRouter()
	rt.Section("tickets").Get("/api/tickets/{id}", echo("number"), Digits("id"))
	rt.NoSection().Get("/api/tickets/{name}", echo("name"))
	if got, ok := find(rt, "GET", "/api/tickets/12"); !ok || got.Section == nil || got.Section.Section != "tickets" {
		t.Errorf("digits: %+v %v", got, ok)
	}
	if got, ok := find(rt, "GET", "/api/tickets/stats"); !ok || got.Section == nil || got.Section.Section != "" {
		t.Errorf("no digits: %+v %v", got, ok)
	}
	if got, ok := find(rt, "GET", "/api/tickets/%31"); !ok || got.Section == nil || got.Section.Section != "" {
		t.Errorf("an escaped digit: %+v %v", got, ok)
	}
}

func TestSectionRefusesWhatTheCatalogDoesNotHave(t *testing.T) {
	for name, declare := range map[string]func(rt *Router){
		"unknown section":      func(rt *Router) { rt.Section("tools") },
		"empty section":        func(rt *Router) { rt.Section("") },
		"level none":           func(rt *Router) { rt.SectionLevel("devices", access.LevelNone) },
		"a second section":     func(rt *Router) { rt.Section("toolkit").Section("devices") },
		"a section after none": func(rt *Router) { rt.NoSection().Section("devices") },
		"use with a section": func(rt *Router) {
			rt.Section("toolkit").Use("/api/x", func(next http.Handler) http.Handler { return next })
		},
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: no panic", name)
				}
			}()
			declare(NewRouter())
		}()
	}
	// A mount below a declaration keeps it
	rt := NewRouter()
	rt.Section("toolkit").Mount("/api/tools").Post("/run", echo("run"))
	if got, ok := find(rt, "POST", "/api/tools/run"); !ok || got.Section == nil || got.Section.Section != "toolkit" {
		t.Errorf("mount below a section: %+v %v", got, ok)
	}
}
