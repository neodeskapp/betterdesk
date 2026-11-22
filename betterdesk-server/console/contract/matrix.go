package contract

import (
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// The matrix sends one placeholder request per console route for every
// role. It shows who may call what, what a refusal looks like, what the
// CSRF check and the validation of an empty body answer, and which headers
// go out. What a route does with real input is for the cases.

// MatrixEntry is one request of the matrix.
type MatrixEntry struct {
	// ID names the entry and its golden. It is the route's ID; the route's
	// ID with HEAD in place of GET for the HEAD sample; and for a route that
	// takes every method the route's ID with " as GET" or " as POST".
	ID    string
	Route Route
	// Method and Path are what is sent. Path has the placeholders filled in.
	Method string
	Path   string
	// Body is "{}" for writes and empty for reads.
	Body []byte
}

// MatrixPlan is the matrix for a route list: what is sent, in order, and
// what is left out.
type MatrixPlan struct {
	// Reads are the GET and HEAD requests, Writes all others. Per role the
	// reads go first.
	Reads, Writes []MatrixEntry
	// Skipped are the console routes on the skip list.
	Skipped []Skip
	// Unreachable are the IDs of console routes no request gets to.
	Unreachable []string
	// Same maps the ID of a route to the ID of another route with the same
	// method and path that the request is filed under. One request stands
	// for both: which of them answers depends on more than the path. The
	// Node console registers such a pair where the first route steps aside
	// for requests without a bearer token, so the request is filed under
	// the route registered last ("GET /api/devices (2)"), the one that
	// answers what the matrix sends.
	Same map[string]string
	// Other counts the routes of other apps than the console.
	Other int
}

// headEvery picks the GET routes that are also sent as HEAD: every n-th
// one, in the order of the route list.
const headEvery = 12

// PlanMatrix builds the matrix for a route list. The order is the order of
// the list, so the same list always gives the same requests.
func PlanMatrix(routes []Route) MatrixPlan {
	plan := MatrixPlan{Same: map[string]string{}}
	skips := skipIndex()
	sendable := func(route Route) bool {
		_, skipped := skips[route.ID]
		return route.App == ConsoleApp && !route.Unreachable && route.ShadowedBy == "" && !skipped
	}
	// Of the routes with the same method and path, the one registered last
	// gets the request: its ID is the longest ("... (2)") or, at the same
	// length, the greatest.
	last := map[string]string{}
	for _, route := range routes {
		key := route.Method + " " + route.Path
		if other := last[key]; sendable(route) && (len(route.ID) > len(other) || (len(route.ID) == len(other) && route.ID > other)) {
			last[key] = route.ID
		}
	}
	gets := 0
	for _, route := range routes {
		if route.App != ConsoleApp {
			plan.Other++
			continue
		}
		if route.Unreachable || route.ShadowedBy != "" {
			plan.Unreachable = append(plan.Unreachable, route.ID)
			continue
		}
		if skip, ok := skips[route.ID]; ok {
			plan.Skipped = append(plan.Skipped, skip)
			continue
		}
		if filed := last[route.Method+" "+route.Path]; filed != route.ID {
			plan.Same[route.ID] = filed
			continue
		}
		entry := MatrixEntry{ID: route.ID, Route: route, Method: route.Method, Path: matrixPath(route)}
		switch route.Method {
		case AllMethods:
			// A route for every method is sent as a read and as a write.
			read, write := entry, entry
			read.Method, read.ID = http.MethodGet, route.ID+" as GET"
			write.Method, write.ID, write.Body = http.MethodPost, route.ID+" as POST", []byte("{}")
			plan.Reads = append(plan.Reads, read)
			plan.Writes = append(plan.Writes, write)
		case http.MethodGet:
			plan.Reads = append(plan.Reads, entry)
			if gets%headEvery == 0 {
				head := entry
				head.Method = http.MethodHead
				head.ID = http.MethodHead + strings.TrimPrefix(route.ID, http.MethodGet)
				plan.Reads = append(plan.Reads, head)
			}
			gets++
		case http.MethodHead, http.MethodOptions:
			plan.Reads = append(plan.Reads, entry)
		default:
			entry.Body = []byte("{}")
			plan.Writes = append(plan.Writes, entry)
		}
	}
	return plan
}

// Entries lists every request of the plan, reads first.
func (p MatrixPlan) Entries() []MatrixEntry {
	return append(append([]MatrixEntry{}, p.Reads...), p.Writes...)
}

// matrixPath is the path the matrix sends for a route: the route's path
// with a placeholder for each parameter, and the route's query when
// matrixQueries has one.
func matrixPath(route Route) string {
	target, ok := matrixPaths[route.ID]
	if !ok {
		target = FillPath(route.Path, func(name string) string { return matrixParam(route, name) })
	}
	if query := matrixQueries[route.ID]; query != "" {
		target += "?" + query
	}
	return target
}

// matrixParam is the placeholder for one parameter of a route: the route's
// own value when it has one, else the value for the parameter's name, else
// an ID that does not exist.
func matrixParam(route Route, name string) string {
	if value, ok := matrixRouteParams[route.ID][name]; ok {
		return value
	}
	if value, ok := matrixParams[name]; ok {
		return value
	}
	return MissingID
}

// slug is the name of the entry's golden file: the method that is sent
// (ALL-GET and ALL-POST for a route that takes every method) and the
// route's path.
func (e MatrixEntry) slug() string {
	method := e.Method
	if e.Route.Method == AllMethods {
		method = AllMethods + "-" + e.Method
	}
	return routeSlug(method, e.Route.Path)
}

func (e MatrixEntry) testName() string {
	return e.Route.Group + "." + e.slug()
}

func (e MatrixEntry) request() Request {
	r := Request{Method: e.Method, Path: e.Path}
	if len(e.Body) > 0 {
		r.Body, r.ContentType = e.Body, "application/json"
	}
	if headers := matrixHeaders[e.ID]; len(headers) > 0 {
		r.Header = http.Header{}
		for name, value := range headers {
			r.Header.Set(name, value)
		}
	}
	return r
}

func (e MatrixEntry) slot() *slot {
	info := RequestInfo{Method: e.Method, Path: e.Path}
	if len(e.Body) > 0 {
		info.Body = map[string]any{}
	}
	headers := make([]string, 0, len(matrixHeaders[e.ID]))
	for name, value := range matrixHeaders[e.ID] {
		headers = append(headers, "header "+name+": "+value)
	}
	sort.Strings(headers)
	info.Notes = append(info.Notes, headers...)
	sl := newSlot("matrix", e.Route.Group, e.slug(), e.ID, info)
	sl.method, sl.target = e.Method, e.Path
	sl.rec.Volatile = matrixVolatile[e.ID]
	sl.rec.VolatileHeaders = matrixVolatileHeaders[e.ID]
	return sl
}

// RunMatrix is the matrix test. For each role in AllRoles it signs in and
// sends every read, then every write, one after the other. The sign-in
// answers are part of the matrix (golden matrix/_session/sign-in.json):
// that is where a role that may not use the console shows.
func RunMatrix(t *testing.T) {
	s := suiteFor(t)
	s.requireRouting(t)
	list, err := LoadRoutes(s.Settings.RoutesFile)
	if err != nil {
		t.Fatalf("%v", err)
	}
	plan := PlanMatrix(list.Routes)
	if problems := planProblems(plan); len(problems) > 0 {
		t.Fatalf("the matrix cannot be built:\n    %s", strings.Join(problems, "\n    "))
	}

	signIn := newSlot("matrix", "_session", "sign-in", "sign-in",
		RequestInfo{Method: http.MethodPost, Path: "/api/auth/login", Notes: []string{"the account e2e_<role> with its password"}})
	signIn.method, signIn.target = http.MethodPost, "/api/auth/login"
	slots := map[string]*slot{}
	ordered := []*slot{signIn}
	for _, entry := range plan.Entries() {
		sl := entry.slot()
		slots[entry.ID] = sl
		ordered = append(ordered, sl)
	}

	for _, role := range AllRoles {
		t.Run(string(role), func(t *testing.T) {
			sess, answer := s.freshSession(t, role)
			if answer != nil {
				t.Run("_session.sign-in", func(t *testing.T) { s.judge(t, signIn, role, *answer) })
			}
			for _, entry := range plan.Entries() {
				t.Run(entry.testName(), func(t *testing.T) {
					s.note(role, entry.ID)
					start := time.Now()
					got := sess.Do(entry.request())
					s.Report.took(fmt.Sprintf("%s as %s", entry.ID, role), time.Since(start))
					s.judge(t, slots[entry.ID], role, got)
					s.watch(t, sess, entry.ID, got, !isSafeMethod(entry.Method))
				})
			}
		})
	}
	s.flush(t, ordered)
	keep := map[string]bool{}
	for _, sl := range ordered {
		keep[filepath.Clean(sl.file(s.Settings.GoldenDir))] = true
	}
	s.prune(t, filepath.Join(s.Settings.GoldenDir, "matrix"), keep)
}

// planProblems checks a plan before anything is sent: two entries must not
// share a golden file, and a placeholder must fit its parameter.
func planProblems(plan MatrixPlan) []string {
	var problems []string
	files := map[string]string{}
	for _, entry := range plan.Entries() {
		name := entry.testName()
		if other, taken := files[name]; taken {
			problems = append(problems, fmt.Sprintf("%s and %s would share the golden %s", other, entry.ID, name))
		}
		files[name] = entry.ID
		if _, whole := matrixPaths[entry.Route.ID]; whole || entry.ID != entry.Route.ID {
			continue
		}
		for _, part := range parsePattern(entry.Route.Path) {
			if part.param == "" {
				continue
			}
			if problem := constraintProblem(part, matrixParam(entry.Route, part.param)); problem != "" {
				problems = append(problems, fmt.Sprintf("%s: %s (add it to matrixRouteParams)", entry.ID, problem))
			}
		}
	}
	sort.Strings(problems)
	return problems
}
