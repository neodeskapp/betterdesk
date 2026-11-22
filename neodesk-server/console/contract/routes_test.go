package contract

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func byName(values map[string]string) func(string) string {
	return func(name string) string {
		if v, ok := values[name]; ok {
			return v
		}
		return MissingID
	}
}

func TestFillPath(t *testing.T) {
	values := byName(map[string]string{"code": "zz", "*": "a b/c", "name": "x y"})
	cases := map[string]string{
		"/health":                            "/health",
		"/":                                  "/",
		"/api/devices/:id":                   "/api/devices/999999999",
		"/api/devices/:id/tags/:tagId":       "/api/devices/999999999/tags/999999999",
		`/api/tickets/:id(\d+)`:              "/api/tickets/999999999",
		`/api/tickets/:id(\d+)/comments`:     "/api/tickets/999999999/comments",
		`/api/x/:id([a-f0-9]{8}(?:-x)?)/y`:   "/api/x/999999999/y",
		"/api/panel/languages/:code/missing": "/api/panel/languages/zz/missing",
		"/api/optional/:id?":                 "/api/optional/999999999",
		"/files/*":                           "/files/a%20b%2Fc",
		"/downloads/:tag/:name":              "/downloads/999999999/x%20y",
		"/api/lang/:code.json":               "/api/lang/zz.json",
		"/api/dev-:id/x":                     "/api/dev-999999999/x",
		`/api/literal\.dot/:id`:              "/api/literal.dot/999999999",
		"/api/time/12:30":                    "/api/time/12999999999",
		"/api/colon/:/x":                     "/api/colon/:/x",
	}
	for pattern, want := range cases {
		if got := FillPath(pattern, values); got != want {
			t.Errorf("FillPath(%q) = %q, want %q", pattern, got, want)
		}
	}
}

func TestConstraintProblem(t *testing.T) {
	digits := parsePattern(`:id(\d+)`)[0]
	if digits.param != "id" || digits.constraint != `\d+` {
		t.Fatalf("parsed %+v", digits)
	}
	if problem := constraintProblem(digits, "999999999"); problem != "" {
		t.Errorf("digits accept 999999999, got problem %q", problem)
	}
	if problem := constraintProblem(digits, "abc"); !strings.Contains(problem, `parameter id must match \d+`) {
		t.Errorf("digits refuse abc, got problem %q", problem)
	}
	if problem := constraintProblem(parsePattern(":id")[0], "anything"); problem != "" {
		t.Errorf("no constraint: problem %q", problem)
	}
	if problem := constraintProblem(patternPart{param: "x", constraint: `(?<n>a)\k<n>`}, "b"); problem != "" {
		t.Errorf("a constraint Go cannot read must be let through, got %q", problem)
	}
}

func TestMatchRoute(t *testing.T) {
	cases := []struct {
		pattern, path string
		literals      int
		ok            bool
	}{
		{"/health", "/health", 1, true},
		{"/health", "/Health/", 1, true},
		{"/", "/", 0, true},
		{"/api/devices", "/api/devices", 2, true},
		{"/api/devices/:id", "/api/devices/900100001", 2, true},
		{"/api/devices/:id", "/api/devices/{device}", 2, true},
		{"/api/devices/stats", "/api/devices/stats", 3, true},
		{"/api/devices/stats", "/api/devices/{device}", 0, false},
		{"/api/devices/:id", "/api/devices", 0, false},
		{"/api/devices/:id", "/api/devices/1/tags", 0, false},
		{`/api/tickets/:id(\d+)`, "/api/tickets/12", 2, true},
		{`/api/tickets/:id(\d+)`, "/api/tickets/stats", 0, false},
		{`/api/tickets/:id(\d+)`, "/api/tickets/{ticket}", 2, true},
		{"/files/*", "/files/a/b/c.txt", 1, true},
		{"/api/lang/:code.json", "/api/lang/de.json", 2, true},
		{"/api/lang/:code.json", "/api/lang/de.txt", 0, false},
	}
	for _, c := range cases {
		literals, ok := matchRoute(c.pattern, c.path)
		if ok != c.ok || (ok && literals != c.literals) {
			t.Errorf("matchRoute(%q, %q) = %d, %v; want %d, %v", c.pattern, c.path, literals, ok, c.literals, c.ok)
		}
	}
}

func TestRouteSlug(t *testing.T) {
	cases := []struct{ method, pattern, want string }{
		{"GET", "/", "GET_root"},
		{"GET", "/health", "GET_health"},
		{"GET", "/api/devices/:id", "GET_api_devices__id"},
		{"DELETE", `/api/tickets/:id(\d+)/comments`, "DELETE_api_tickets__id_comments"},
		{"GET", "/files/*", "GET_files__any"},
		{"GET", "/api/lang/:code.json", "GET_api_lang__code.json"},
		{"POST", "/api/a b/ü", "POST_api_a-b_-"},
	}
	for _, c := range cases {
		if got := routeSlug(c.method, c.pattern); got != c.want {
			t.Errorf("routeSlug(%q, %q) = %q, want %q", c.method, c.pattern, got, c.want)
		}
	}
	long := routeSlug("GET", "/api/"+strings.Repeat("segment/", 30)+":id")
	other := routeSlug("GET", "/api/"+strings.Repeat("segment/", 30)+":name")
	if len(long) > maxSlug+16 || long == other {
		t.Errorf("long slugs must be cut and stay distinct: %q, %q", long, other)
	}
}

// withTables runs f with the matrix tables replaced, and puts them back.
func withTables(skips []Skip, routeParams map[string]map[string]string, f func()) {
	oldSkips, oldParams := matrixSkips, matrixRouteParams
	matrixSkips, matrixRouteParams = skips, routeParams
	defer func() { matrixSkips, matrixRouteParams = oldSkips, oldParams }()
	f()
}

func TestPlanMatrix(t *testing.T) {
	routes := []Route{
		{ID: "GET /api/a", Method: "GET", Path: "/api/a", App: ConsoleApp, Group: "a"},
		{ID: "POST /api/a", Method: "POST", Path: "/api/a", App: ConsoleApp, Group: "a"},
		{ID: "GET /api/a/:id", Method: "GET", Path: "/api/a/:id", App: ConsoleApp, Group: "a"},
		{ID: "DELETE /api/a/:id", Method: "DELETE", Path: "/api/a/:id", App: ConsoleApp, Group: "a"},
		{ID: "POST /api/restart", Method: "POST", Path: "/api/restart", App: ConsoleApp, Group: "b"},
		{ID: "GET /api/client", Method: "GET", Path: "/api/client", App: "rustdesk-api", Group: "c"},
		{ID: "GET /api/b/:id", Method: "GET", Path: "/api/b/:id", App: ConsoleApp, Group: "b"},
		{ID: "GET /api/a (2)", Method: "GET", Path: "/api/a", App: ConsoleApp, Group: "c"},
		{ID: "GET /api/a (3)", Method: "GET", Path: "/api/a", App: ConsoleApp, Group: "c", Unreachable: true},
		{ID: "GET /api/never", Method: "GET", Path: "/api/never", App: ConsoleApp, Group: "c", Unreachable: true},
		{ID: "GET /api/hidden", Method: "GET", Path: "/api/hidden", App: ConsoleApp, Group: "c", ShadowedBy: "GET /api/a"},
		{ID: "ALL /api/any", Method: "ALL", Path: "/api/any", App: ConsoleApp, Group: "c"},
	}
	skips := []Skip{{ID: "POST /api/restart", Reason: SkipServer, Why: "test"}}
	params := map[string]map[string]string{"GET /api/a/:id": {"id": "900100001"}}
	withTables(skips, params, func() {
		plan := PlanMatrix(routes)
		var reads, writes []string
		for _, e := range plan.Reads {
			reads = append(reads, e.ID+" -> "+e.Method+" "+e.Path+string(e.Body))
		}
		for _, e := range plan.Writes {
			writes = append(writes, e.ID+" -> "+e.Method+" "+e.Path+" "+string(e.Body))
		}
		wantReads := []string{
			"GET /api/a/:id -> GET /api/a/900100001",
			"HEAD /api/a/:id -> HEAD /api/a/900100001",
			"GET /api/b/:id -> GET /api/b/999999999",
			"GET /api/a (2) -> GET /api/a",
			"ALL /api/any as GET -> GET /api/any",
		}
		wantWrites := []string{
			"POST /api/a -> POST /api/a {}",
			"DELETE /api/a/:id -> DELETE /api/a/999999999 {}",
			"ALL /api/any as POST -> POST /api/any {}",
		}
		if !reflect.DeepEqual(reads, wantReads) {
			t.Errorf("reads:\n got %q\nwant %q", reads, wantReads)
		}
		if !reflect.DeepEqual(writes, wantWrites) {
			t.Errorf("writes:\n got %q\nwant %q", writes, wantWrites)
		}
		if len(plan.Skipped) != 1 || plan.Skipped[0].ID != "POST /api/restart" || plan.Other != 1 {
			t.Errorf("skipped %v, other %d; want the restart skipped and one route of another app", plan.Skipped, plan.Other)
		}
		if !reflect.DeepEqual(plan.Unreachable, []string{"GET /api/a (3)", "GET /api/never", "GET /api/hidden"}) {
			t.Errorf("unreachable = %q, want the three routes no request gets to", plan.Unreachable)
		}
		if !reflect.DeepEqual(plan.Same, map[string]string{"GET /api/a": "GET /api/a (2)"}) {
			t.Errorf("same = %v, want the first GET /api/a filed under the one registered last", plan.Same)
		}
		entries := plan.Entries()
		if len(entries) != 8 || entries[0].Method != "GET" || entries[5].Method != "POST" {
			t.Errorf("Entries() must list the reads before the writes, got %v", entries)
		}
		if read, write := entries[4].testName(), entries[7].testName(); read != "c.ALL-GET_api_any" || write != "c.ALL-POST_api_any" {
			t.Errorf("test names of the route for every method = %q and %q", read, write)
		}
		if got := entries[1].testName(); got != "a.HEAD_api_a__id" {
			t.Errorf("test name of the HEAD sample = %q", got)
		}
	})
}

func TestMatrixQuery(t *testing.T) {
	old := matrixQueries
	matrixQueries = map[string]string{"GET /api/list/:kind": "sortBy=id"}
	defer func() { matrixQueries = old }()
	routes := []Route{
		{ID: "GET /api/list/:kind", Method: "GET", Path: "/api/list/:kind", App: ConsoleApp, Group: "g"},
		{ID: "GET /api/plain", Method: "GET", Path: "/api/plain", App: ConsoleApp, Group: "g"},
	}
	withTables(nil, nil, func() {
		reads := PlanMatrix(routes).Reads
		if len(reads) != 3 || reads[0].Path != "/api/list/999999999?sortBy=id" || reads[1].Path != reads[0].Path || reads[2].Path != "/api/plain" {
			t.Errorf("reads = %+v, want the query on the GET and on its HEAD sample only", reads)
		}
	})
}

func TestHeadSampleTakesEveryTwelfthRead(t *testing.T) {
	var routes []Route
	for i := 0; i < 30; i++ {
		p := "/api/r" + strings.Repeat("x", i)
		routes = append(routes, Route{ID: "GET " + p, Method: "GET", Path: p, App: ConsoleApp, Group: "g"})
	}
	withTables(nil, nil, func() {
		heads := 0
		for i, e := range PlanMatrix(routes).Reads {
			if e.Method == "HEAD" {
				heads++
				if i == 0 || PlanMatrix(routes).Reads[i-1].Path != e.Path {
					t.Errorf("HEAD %s does not follow its GET", e.Path)
				}
			}
		}
		if heads != 3 {
			t.Errorf("%d HEAD requests for 30 GET routes, want 3 (the 1st, 13th and 25th)", heads)
		}
	})
}

func TestPlanProblems(t *testing.T) {
	routes := []Route{
		{ID: `GET /api/t/:id([a-f]+)`, Method: "GET", Path: `/api/t/:id([a-f]+)`, App: ConsoleApp, Group: "t"},
		{ID: "GET /api/t/:name", Method: "GET", Path: "/api/t/:name", App: ConsoleApp, Group: "t"},
		{ID: "GET /api/t__name", Method: "GET", Path: "/api/t__name", App: ConsoleApp, Group: "t"},
	}
	withTables(nil, nil, func() {
		problems := planProblems(PlanMatrix(routes))
		all := strings.Join(problems, "\n")
		if len(problems) != 2 || !strings.Contains(all, "would share the golden") || !strings.Contains(all, "parameter id must match [a-f]+") {
			t.Errorf("problems = %q, want a shared golden and a placeholder that does not fit", problems)
		}
	})
	withTables(nil, map[string]map[string]string{`GET /api/t/:id([a-f]+)`: {"id": "abc"}}, func() {
		if problems := planProblems(PlanMatrix(routes[:2])); len(problems) != 0 {
			t.Errorf("with a fitting placeholder: problems %q", problems)
		}
	})
}

func TestLoadRoutes(t *testing.T) {
	dir := t.TempDir()
	write := func(content string) string {
		file := filepath.Join(dir, "routes.json")
		if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return file
	}
	list, err := LoadRoutes(write(`{"source":"x","routes":[{"id":"GET /a","method":"GET","path":"/a","app":"console","group":"g","csrf":"gate"}]}`))
	if err != nil || len(list.Routes) != 1 || list.Routes[0].Group != "g" || list.Routes[0].CSRF != "gate" {
		t.Errorf("LoadRoutes = %+v, %v", list, err)
	}
	for _, bad := range []string{
		`{"routes":[{"id":"GET /a","method":"GET","path":"a"}]}`,
		`{"routes":[{"id":"","method":"GET","path":"/a"}]}`,
		`{"routes":[{"id":"GET /a","method":"GET","path":"/a"},{"id":"GET /a","method":"GET","path":"/a"}]}`,
		`not json`,
	} {
		if _, err := LoadRoutes(write(bad)); err == nil {
			t.Errorf("LoadRoutes accepted %s", bad)
		}
	}
	if _, err := LoadRoutes(filepath.Join(dir, "missing.json")); err == nil {
		t.Error("LoadRoutes accepted a missing file")
	}
}

// TestRouteListAndTablesFit checks the route list in the repository against
// the matrix tables: every placeholder fits, no two routes share a golden,
// the skip list and the per-route tables name routes that exist, and only
// reads use placeholders of things that exist.
func TestRouteListAndTablesFit(t *testing.T) {
	list, err := LoadRoutes("testdata/routes.json")
	if err != nil {
		t.Fatal(err)
	}
	plan := PlanMatrix(list.Routes)
	for _, problem := range planProblems(plan) {
		t.Error(problem)
	}
	for _, problem := range CoverageOf(list.Routes, nil).Problems {
		t.Error(problem)
	}
	byID := map[string]Route{}
	for _, route := range list.Routes {
		byID[route.ID] = route
	}
	for id := range matrixRouteParams {
		route, ok := byID[id]
		if !ok {
			t.Errorf("matrixRouteParams names %q, which is not in the route list", id)
		} else if route.Method != "GET" {
			t.Errorf("matrixRouteParams has %q: only GET routes may be sent with things that exist", id)
		}
	}
	sent := map[string]bool{}
	for _, entry := range plan.Entries() {
		sent[entry.ID] = true
	}
	for name, table := range map[string]map[string][]string{"matrixVolatile": matrixVolatile, "matrixVolatileHeaders": matrixVolatileHeaders} {
		for id := range table {
			if !sent[id] {
				t.Errorf("%s names %q, which is not a request of the matrix", name, id)
			}
		}
	}
	for id := range matrixHeaders {
		if !sent[id] {
			t.Errorf("matrixHeaders names %q, which is not a request of the matrix", id)
		}
	}
	for id := range matrixPaths {
		if _, ok := byID[id]; !ok {
			t.Errorf("matrixPaths names %q, which is not in the route list", id)
		}
	}
	for id := range matrixQueries {
		if route, ok := byID[id]; !ok || route.Method != "GET" || !sent[id] {
			t.Errorf("matrixQueries names %q, which is not a GET request of the matrix", id)
		}
	}
	for _, skip := range matrixSkips {
		if skip.Reason == "" || strings.TrimSpace(skip.Why) == "" {
			t.Errorf("skip %q needs a reason and a sentence on what the handler does", skip.ID)
		}
	}
	if len(plan.Reads)+len(plan.Writes) == 0 {
		t.Error("the matrix is empty")
	}
}
