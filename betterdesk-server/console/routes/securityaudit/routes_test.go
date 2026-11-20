package securityaudit_test

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/config"
	"github.com/unitronix/betterdesk-server/console/routes/internal/routetest"
)

func newAPI() *routetest.API {
	api := routetest.NewAPI()
	api.On("GET /api/health", 200, `{"status":"ok","tls":true}`)
	api.On("GET /api/keys", 200, `[]`)
	api.On("GET /api/audit/events?limit=500", 200, `{"enabled":true,"total":0,"count":0,"events":[]}`)
	api.On("GET /api/audit/events?limit=50&offset=0", 200, `{"enabled":true,"total":0,"count":0,"events":[]}`)
	api.On("GET /api/blocklist", 200, `{"enabled":true,"count":0,"entries":[]}`)
	return api
}

func TestGuards(t *testing.T) {
	api := newAPI()
	w := routetest.New(t, routetest.Options{API: api})
	for _, route := range []string{"overview", "events", "hardening", "vulnerabilities"} {
		target := "/api/panel/security-audit/" + route
		if got := w.SignedOut().Do("GET", target, ""); got.Status != 401 || got.Body != `{"success":false,"error":"Unauthorized. Please log in.","code":"session_required"}` {
			t.Errorf("%s signed out: %d %s", route, got.Status, got.Body)
		}
		if got := w.As("guest_issuer").Do("GET", target, ""); got.Status != 403 || got.Body != `{"success":false,"error":"Permission denied: audit.view"}` {
			t.Errorf("%s as guest_issuer: %d %s", route, got.Status, got.Body)
		}
		if calls := api.Calls(); calls != nil {
			t.Errorf("%s reached the API server for accounts that may not: %v", route, calls)
		}
		for _, role := range []string{"viewer", "operator", "admin"} {
			if got := w.As(role).Do("GET", target, ""); got.Status != 200 || got.Header.Get("Content-Type") != "application/json; charset=utf-8" {
				t.Errorf("%s as %s: %d %s", route, role, got.Status, got.Body)
			}
		}
		api.Calls()
	}
}

func TestOverviewThroughTheConsole(t *testing.T) {
	api := newAPI()
	admin := routetest.New(t, routetest.Options{API: api}).As("admin")
	want := `{"score":100,"checks":[{"id":"tls_api","name":"API TLS","pass":true},{"id":"api_key_age","name":"API Key Rotation (< 90 days)","pass":true},` +
		`{"id":"failed_logins","name":"Low Failed Login Rate","pass":true},{"id":"no_bans","name":"No Active Bans","pass":true},` +
		`{"id":"audit_log","name":"Audit Logging","pass":true},{"id":"blocklist","name":"Blocklist Available","pass":true}],` +
		`"stats":{"failed_logins":0,"bans":0,"config_changes":0,"api_keys":0,"old_keys":0},"tls":{"signal":null,"relay":null,"api":true,"wss":false}}`
	if got := admin.Do("GET", "/api/panel/security-audit/overview", ""); got.Status != 200 || got.Body != want {
		t.Errorf("overview: %d %s", got.Status, got.Body)
	}
	calls := api.Calls()
	sort.Strings(calls)
	if !reflect.DeepEqual(calls, []string{"GET /api/audit/events?limit=500", "GET /api/blocklist", "GET /api/health", "GET /api/keys"}) {
		t.Errorf("the API server was asked %q", calls)
	}
	// The browser's own word for the scheme counts, whoever set the header
	if got := admin.Do("GET", "/api/panel/security-audit/overview", "", "X-Forwarded-Proto", "HTTPS, http"); !strings.HasSuffix(got.Body, `"wss":true}}`) {
		t.Errorf("with X-Forwarded-Proto https: %s", got.Body)
	}
	if got := admin.Do("GET", "/api/panel/security-audit/overview", "", "X-Forwarded-Proto", "http, https"); !strings.HasSuffix(got.Body, `"wss":false}}`) {
		t.Errorf("with X-Forwarded-Proto http first: %s", got.Body)
	}
}

// TestEventsWithoutABodyAreNoEvents: an answer of the API server without a
// body (or with null, 0, false or "") is no events, which the web app
// reads, where the route failed with 502 and an error about circular
// JSON (BUGS.md TH13).
func TestEventsWithoutABodyAreNoEvents(t *testing.T) {
	for _, body := range []string{``, `null`, `0`, `false`, `""`} {
		api := newAPI()
		api.On("GET /api/audit/events?limit=50&offset=0", 200, body)
		admin := routetest.New(t, routetest.Options{API: api}).As("admin")
		if got := admin.Do("GET", "/api/panel/security-audit/events", ""); got.Status != 200 || got.Body != `{"events":[],"total":0}` {
			t.Errorf("an answer %q: %d %s", body, got.Status, got.Body)
		}
	}
}

func TestHardeningTellsTheSessionLimit(t *testing.T) {
	for limit, want := range map[string]string{
		"":  `{"id":"single_session","name":"Concurrent Panel Session Limit per Account","category":"auth","severity":"high","implemented":true,"description":"A new console login ends the least recently active sessions beyond SESSION_MAX_PER_USER (currently 1) and records IP/User-Agent for audit."}`,
		"0": `{"id":"single_session","name":"Concurrent Panel Session Limit per Account","category":"auth","severity":"high","implemented":false,"description":"A new console login ends the least recently active sessions beyond SESSION_MAX_PER_USER (currently unlimited) and records IP/User-Agent for audit."}`,
		"4": `"implemented":true,"description":"A new console login ends the least recently active sessions beyond SESSION_MAX_PER_USER (currently 4) and records IP/User-Agent for audit."}`,
	} {
		w := routetest.New(t, routetest.Options{Env: map[string]string{"SESSION_MAX_PER_USER": limit}})
		got := w.As("viewer").Do("GET", "/api/panel/security-audit/hardening", "")
		if got.Status != 200 || !strings.Contains(got.Body, want) || !strings.HasPrefix(got.Body, `{"priorities":[{"id":"mutual_tls","name":"Mutual TLS Console ↔ Go Server","category":"network","severity":"high","implemented":false,"description":"Replace plain HTTP`) {
			t.Errorf("SESSION_MAX_PER_USER=%q: %d %s", limit, got.Status, got.Body)
		}
	}
}

func TestVulnerabilitiesCountsThePackageFile(t *testing.T) {
	report := func(total string) string {
		return `{"scan_available":false,"counts":{"critical":0,"high":0,"medium":0,"low":0,"total":0},"vulnerabilities":[],` +
			`"dependencies":{"go":{"total":0,"vulnerable":0,"items":[]},"nodejs":{"total":` + total + `,"vulnerable":0,"items":[]}},"last_scan":null}`
	}
	dir := t.TempDir()
	w := routetest.New(t, routetest.Options{Config: func(cfg *config.Config) { cfg.ConsoleDir = dir }})
	viewer := w.As("viewer")
	get := func() string { return viewer.Do("GET", "/api/panel/security-audit/vulnerabilities", "").Body }

	if got := get(); got != report("0") {
		t.Errorf("without a package file: %s", got)
	}
	for content, total := range map[string]string{
		`{"dependencies":{"a":"1","b":"2"},"devDependencies":{"c":"3"}}`: "3",
		// A name in both lists counts once
		`{"dependencies":{"a":"1","b":"2"},"devDependencies":{"b":"2","c":"3"}}`: "3",
		`{"dependencies":{"a":"1"}}`:                            "1",
		`{"devDependencies":{"a":"1","b":"2"}}`:                 "2",
		`{"name":"console"}`:                                    "0",
		`{"dependencies":null,"devDependencies":5}`:             "0",
		`{"dependencies":["x","y","z"],"devDependencies":"ab"}`: "3",
		`not json`:                              "0",
		`[]`:                                    "0",
		"\uFEFF" + `{"dependencies":{"a":"1"}}`: "1",
	} {
		if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := get(); got != report(total) {
			t.Errorf("package file %s:\n%s\nwant\n%s", content, got, report(total))
		}
	}
}
