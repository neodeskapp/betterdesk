package contract

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

// withGroups runs f with an empty case registry, and puts the registry back.
func withGroups(f func()) {
	old := caseGroups
	caseGroups = map[string]*CaseGroup{}
	defer func() { caseGroups = old }()
	f()
}

func panicText(f func()) (text string) {
	defer func() {
		if r := recover(); r != nil {
			text, _ = r.(string)
		}
	}()
	f()
	return ""
}

func TestGroupRegistersCasesInNameOrder(t *testing.T) {
	withGroups(func() {
		Group("tickets", Case{Name: "list", Path: "/api/tickets"})
		Group("devices", Case{Name: "list", Path: "/api/devices"}, Case{Name: "rename", Method: "PATCH", Path: "/api/devices/{device}"})
		groups := Groups()
		if len(groups) != 2 || groups[0].Name != "devices" || groups[1].Name != "tickets" {
			t.Fatalf("groups = %+v, want devices then tickets", groups)
		}
		if groups[0].Cases[0].Method != "GET" || groups[0].Cases[1].Method != "PATCH" {
			t.Errorf("methods = %q, %q; an empty method is GET", groups[0].Cases[0].Method, groups[0].Cases[1].Method)
		}
	})
}

func TestGroupRefusesCasesItCannotRun(t *testing.T) {
	bad := map[string]func(){
		"group name":      func() { Group("Devices", Case{Name: "list", Path: "/x"}) },
		"twice":           func() { Group("a", Case{Name: "x", Path: "/x"}); Group("a", Case{Name: "y", Path: "/y"}) },
		"two cases named": func() { Group("a", Case{Name: "x", Path: "/x"}, Case{Name: "x", Path: "/y"}) },
		"name":            func() { Group("a", Case{Name: "List Devices", Path: "/x"}) },
		"unknown method":  func() { Group("a", Case{Name: "x", Method: "FETCH", Path: "/x"}) },
		"path":            func() { Group("a", Case{Name: "x", Path: "api/x"}) },
		"query goes in":   func() { Group("a", Case{Name: "x", Path: "/api/x?limit=1"}) },
		"one of Body":     func() { Group("a", Case{Name: "x", Method: "POST", Path: "/x", Body: map[string]any{}, RawBody: "x"}) },
		"unknown role":    func() { Group("a", Case{Name: "x", Path: "/x", As: "root"}) },
		"capture":         func() { Group("a", Case{Name: "x", Path: "/x", Capture: map[string]string{"id": "data.items[*].id"}}) },
		"volatile":        func() { Group("a", Case{Name: "x", Path: "/x", Volatile: []string{"~data..id"}}) },
		"covers":          func() { Group("a", Case{Name: "x", Path: "/x", Covers: []string{"/api/auth/logout"}}) },
	}
	for want, register := range bad {
		withGroups(func() {
			if text := panicText(register); !strings.Contains(text, want) {
				t.Errorf("%s: panic %q, want one that names the mistake", want, text)
			}
		})
	}
	if text := panicText(func() { Roles("admin", "wizard") }); !strings.Contains(text, `unknown role "wizard"`) {
		t.Errorf("Roles with an unknown name: panic %q", text)
	}
}

func TestCaseRoles(t *testing.T) {
	all := Case{Name: "x", Path: "/x"}
	if !reflect.DeepEqual(all.roles(), AllRoles) {
		t.Errorf("default roles = %v", all.roles())
	}
	some := Case{Name: "x", Path: "/x", Roles: Roles("admin", "viewer", "signed-out", "viewer")}
	if want := []Role{SignedOut, "viewer", "admin"}; !reflect.DeepEqual(some.roles(), want) {
		t.Errorf("roles = %v, want %v (least privilege first, each once)", some.roles(), want)
	}
	if some.runsFor("operator") || !some.runsFor("viewer") {
		t.Error("runsFor does not follow Roles")
	}
}

func TestVariablesFill(t *testing.T) {
	vars := variables{"ticket": json.Number("42"), "name": "a b", "flag": true, "list": []any{"x"}}
	cases := map[string]string{
		"/api/tickets/{ticket}":            "/api/tickets/42",
		"/api/tickets/{ticket}/x/{name}":   "/api/tickets/42/x/a b",
		"/api/tickets/{unset}":             "/api/tickets/999999999",
		"{flag} {list}":                    `true ["x"]`,
		`{"json": "body", "id": {ticket}}`: `{"json": "body", "id": 42}`,
		"no variables {} { x } {a-b}":      "no variables {} { x } {a-b}",
	}
	for in, want := range cases {
		if got := vars.fill(in); got != want {
			t.Errorf("fill(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCaseRequest(t *testing.T) {
	vars := variables{"ticket": json.Number("42"), "name": "printer"}
	c := Case{
		Name: "x", Method: "PATCH", Path: "/api/tickets/{ticket}",
		Query:   url.Values{"b": {"{name}"}, "a": {"1", "2"}},
		Headers: map[string]string{"X-Ticket": "{ticket}"},
		Body: map[string]any{
			"id": "{ticket}", "title": "about {name} #{ticket}", "missing": "{nothing}",
			"nested": []any{map[string]any{"ref": "{ticket}"}, 7, nil},
		},
		NoCSRF: true, NoSession: true, FollowRedirects: true,
	}
	r, err := c.request(turnWith(vars))
	if err != nil {
		t.Fatal(err)
	}
	if r.Method != "PATCH" || r.Path != "/api/tickets/42?a=1&a=2&b=printer" || r.Header.Get("X-Ticket") != "42" {
		t.Errorf("request = %s %s, header %q", r.Method, r.Path, r.Header.Get("X-Ticket"))
	}
	if !r.NoCSRF || !r.NoSession || r.NoCookies || !r.FollowRedirects {
		t.Errorf("flags = %+v", r)
	}
	want := `{"id":42,"missing":"999999999","nested":[{"ref":42},7,null],"title":"about printer #42"}`
	if string(r.Body) != want || r.ContentType != "application/json" {
		t.Errorf("body = %s (%s)\nwant %s", r.Body, r.ContentType, want)
	}

	raw := Case{Name: "x", Method: "POST", Path: "/x", RawBody: `{"cut": "{name}"`, ContentType: "application/json"}
	r, _ = raw.request(turnWith(vars))
	if string(r.Body) != `{"cut": "printer"` || r.ContentType != "application/json" {
		t.Errorf("raw body = %s (%s)", r.Body, r.ContentType)
	}

	none := Case{Name: "x", Path: "/x"}
	if r, _ = none.request(turnWith(vars)); r.Body != nil || r.ContentType != "" || r.Path != "/x" {
		t.Errorf("a case without a body sends %q (%s) to %s", r.Body, r.ContentType, r.Path)
	}
}

func TestCaseRequestMultipart(t *testing.T) {
	c := Case{Name: "x", Method: "POST", Path: "/api/settings/restore", Multipart: []Part{
		{Field: "note", Content: []byte("hello")},
		{Field: "backup", FileName: "backup.zip", ContentType: "application/zip", Content: []byte{0x50, 0x4b, 0x03, 0x04}},
	}}
	first, err := c.request(turnWith(nil))
	if err != nil {
		t.Fatal(err)
	}
	second, _ := c.request(turnWith(nil))
	if !bytes.Equal(first.Body, second.Body) || first.ContentType != second.ContentType {
		t.Error("the same multipart case gives different bytes each time")
	}
	mediaType, params, err := mime.ParseMediaType(first.ContentType)
	if err != nil || mediaType != "multipart/form-data" {
		t.Fatalf("content type %q: %v", first.ContentType, err)
	}
	reader := multipart.NewReader(bytes.NewReader(first.Body), params["boundary"])
	var got []string
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		content, _ := io.ReadAll(part)
		got = append(got, part.FormName()+"|"+part.FileName()+"|"+part.Header.Get("Content-Type")+"|"+string(content))
	}
	want := []string{"note|||hello", "backup|backup.zip|application/zip|PK\x03\x04"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parts = %q, want %q", got, want)
	}
}

func TestCaseCapture(t *testing.T) {
	c := Case{Name: "x", Path: "/x", Capture: map[string]string{"ticket": "data.id", "first": "data.tags[0]", "gone": "data.nothing"}}
	vars := variables{"gone": "kept"}
	c.capture(jsonAnswer(200, `{"data": {"id": 42, "tags": ["a"]}}`), turnWith(vars))
	want := variables{"ticket": json.Number("42"), "first": "a", "gone": "kept"}
	if !reflect.DeepEqual(vars, want) {
		t.Errorf("variables = %v, want %v", vars, want)
	}
	c.capture(Answer{Status: 200, Body: Body{Kind: BodyBytes, Type: "text/html"}}, turnWith(vars))
	c.capture(jsonAnswer(403, `{"error": "no"}`), turnWith(vars))
	if !reflect.DeepEqual(vars, want) {
		t.Errorf("an answer without the paths changed the variables: %v", vars)
	}
}

func TestCaseInfo(t *testing.T) {
	c := Case{
		Name: "x", Method: "POST", Path: "/api/tickets/{ticket}", Query: url.Values{"limit": {"10"}},
		Headers: map[string]string{"X-B": "2", "X-A": "1"}, RawBody: "a=b", ContentType: "application/x-www-form-urlencoded",
		NoCSRF: true, NoSession: true, FollowRedirects: true, As: "admin",
	}
	info := c.info()
	wantNotes := []string{
		`raw body, content type "application/x-www-form-urlencoded"`,
		"header X-A: 1", "header X-B: 2",
		"no CSRF token", "no session cookie", "redirects followed", "sent as admin in every role's turn",
	}
	if info.Method != "POST" || info.Path != "/api/tickets/{ticket}?limit=10" || info.Body != "a=b" || !reflect.DeepEqual(info.Notes, wantNotes) {
		t.Errorf("info = %+v", info)
	}
}

func TestCoverageOf(t *testing.T) {
	routes := []Route{
		{ID: "GET /api/devices", Method: "GET", Path: "/api/devices", App: ConsoleApp},
		{ID: "GET /api/devices/:id", Method: "GET", Path: "/api/devices/:id", App: ConsoleApp},
		{ID: "GET /api/devices/stats", Method: "GET", Path: "/api/devices/stats", App: ConsoleApp},
		{ID: "POST /api/auth/logout", Method: "POST", Path: "/api/auth/logout", App: ConsoleApp},
		{ID: "GET /logout", Method: "GET", Path: "/logout", App: ConsoleApp},
		{ID: "POST /api/settings/restart", Method: "POST", Path: "/api/settings/restart", App: ConsoleApp},
		{ID: "POST /api/terminal/:id", Method: "POST", Path: "/api/terminal/:id", App: ConsoleApp},
		{ID: "GET /api/login", Method: "GET", Path: "/api/login", App: "rustdesk-api"},
		{ID: "GET /api/never", Method: "GET", Path: "/api/never", App: ConsoleApp, Unreachable: true},
		{ID: "ALL /api/any", Method: "ALL", Path: "/api/any", App: ConsoleApp},
	}
	skips := []Skip{
		{ID: "POST /api/auth/logout", Reason: SkipSession, Why: "ends the session"},
		{ID: "GET /logout", Reason: SkipSession, Why: "ends the session"},
		{ID: "POST /api/settings/restart", Reason: SkipServer, Why: "restarts"},
		{ID: "POST /api/terminal/:id", Reason: SkipShell, Why: "opens a shell"},
		{ID: "GET /api/devices/stats", Reason: SkipInternet, Why: "only for this test"},
		{ID: "DELETE /api/gone", Reason: SkipServer, Why: "stale"},
		{ID: "ALL /api/any", Reason: SkipSession, Why: "only for this test"},
	}
	groups := []*CaseGroup{{Name: "auth", Cases: []Case{
		{Name: "logout", Method: "POST", Path: "/api/auth/logout"},
		{Name: "logout-page", Method: "HEAD", Path: "/logout"},
		{Name: "terminal", Method: "POST", Path: "/api/other", Covers: []string{"POST /api/terminal/:id", "POST /api/typo"}},
		{Name: "one-device", Method: "GET", Path: "/api/devices/{device}"},
		{Name: "any-method", Method: "DELETE", Path: "/api/any"},
	}}}
	withTables(skips, nil, func() {
		cov := CoverageOf(routes, groups)
		if cov.Matrix != 2 || cov.Cases != 4 || cov.Other != 1 || cov.Unreachable != 1 {
			t.Errorf("matrix %d, cases %d, other %d, unreachable %d; want 2, 4, 1, 1", cov.Matrix, cov.Cases, cov.Other, cov.Unreachable)
		}
		var uncovered []string
		for _, skip := range cov.Uncovered {
			uncovered = append(uncovered, skip.ID)
		}
		// The case for /api/devices/{device} stands for the route with the
		// parameter, not for /api/devices/stats.
		if want := []string{"GET /api/devices/stats", "POST /api/settings/restart"}; !reflect.DeepEqual(uncovered, want) {
			t.Errorf("uncovered = %q, want %q", uncovered, want)
		}
		wantProblems := []string{
			`case auth/terminal covers "POST /api/typo", which is not in the route list`,
			`the skip list names "DELETE /api/gone", which is not in the route list`,
		}
		if !reflect.DeepEqual(cov.Problems, wantProblems) {
			t.Errorf("problems = %q, want %q", cov.Problems, wantProblems)
		}
	})

	withTables(skips, nil, func() {
		literal := []*CaseGroup{{Name: "d", Cases: []Case{{Name: "stats", Method: "GET", Path: "/api/devices/stats"}}}}
		for _, skip := range CoverageOf(routes, literal).Uncovered {
			if skip.ID == "GET /api/devices/stats" {
				t.Error("a case for /api/devices/stats does not cover that route: the most literal route must win")
			}
		}
	})
}

func TestSettings(t *testing.T) {
	env := func(values map[string]string) func(string) string {
		return func(key string) string { return values[key] }
	}
	s, err := LoadSettings(env(map[string]string{"BD_URL": "http://bdgo-c1:5000/"}))
	if err != nil {
		t.Fatal(err)
	}
	if s.BaseURL.String() != "http://bdgo-c1:5000" || s.PasswordFile != "/stack/secrets/e2e_password" || s.GoldenDir != "testdata/golden" ||
		s.RoutesFile != "testdata/routes.json" || s.Record != Compare || s.RequireGo || s.Timeout.Seconds() != 20 ||
		s.SecretsDir != "/stack/secrets" || s.RecordNew {
		t.Errorf("defaults = %+v", s)
	}
	s, err = LoadSettings(env(map[string]string{
		"BD_URL": "https://console.example", "BD_PASSWORD_FILE": "/p", "CONTRACT_GOLDEN": "/g", "CONTRACT_ROUTES": "/r",
		"CONTRACT_RECORD": "merge", "CONTRACT_REQUIRE_GO": "1", "CONTRACT_TIMEOUT": "5s", "BD_SECRETS_DIR": "/s", "CONTRACT_RECORD_NEW": "1",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if s.PasswordFile != "/p" || s.GoldenDir != "/g" || s.RoutesFile != "/r" || s.Record != RecordMerge || !s.RequireGo || s.Timeout.Seconds() != 5 ||
		s.SecretsDir != "/s" || !s.RecordNew {
		t.Errorf("settings = %+v", s)
	}
	if s, _ = LoadSettings(env(map[string]string{"BD_URL": "http://x", "CONTRACT_RECORD": "1"})); s == nil || s.Record != RecordFirst {
		t.Errorf("CONTRACT_RECORD=1 gives %+v", s)
	}
	for _, bad := range []map[string]string{
		{},
		{"BD_URL": "bdgo-c1:5000"},
		{"BD_URL": "ftp://x"},
		{"BD_URL": "http://x", "CONTRACT_RECORD": "yes"},
		{"BD_URL": "http://x", "CONTRACT_TIMEOUT": "soon"},
		{"BD_URL": "http://x", "CONTRACT_RECORD_NEW": "1"},
		{"BD_URL": "http://x", "CONTRACT_RECORD": "1", "CONTRACT_RECORD_NEW": "yes"},
	} {
		if _, err := LoadSettings(env(bad)); err == nil {
			t.Errorf("LoadSettings(%v) accepted a bad value", bad)
		}
	}
}
