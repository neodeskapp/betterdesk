package securityaudit_test

import (
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/config"
	"github.com/unitronix/betterdesk-server/console/httpx"
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
	for _, route := range []string{"overview", "events", "hardening"} {
		target := "/api/panel/security-audit/" + route
		if got := w.SignedOut().Do("GET", target, ""); got.Status != 401 || got.Body != `{"success":false,"error":"Unauthorized. Please log in.","code":"session_required"}` {
			t.Errorf("%s signed out: %d %s", route, got.Status, got.Body)
		}
		// The panel access guard refuses first: the security audit section
		// asks audit.view to read (BUGS PA4, F2)
		if got := w.As("guest_issuer").Do("GET", target, ""); got.Status != 403 || got.Body != `{"success":false,"error":"Panel access denied: security-audit","code":"panel_access_denied"}` {
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
}

// TestOverviewIgnoresAForwardedSchemeItDoesNotTrust: tls.wss follows the
// scheme the proxy settings give, so a client that sends X-Forwarded-Proto
// itself cannot make it true (BUGS.md TH14).
func TestOverviewIgnoresAForwardedSchemeItDoesNotTrust(t *testing.T) {
	for _, c := range []struct {
		trust, proto, wss string
	}{
		{"", "https", "false"}, {"", "HTTPS, http", "false"},
		{"true", "https", "true"}, {"true", "http, https", "false"},
	} {
		trust, err := httpx.ParseTrustProxy(c.trust)
		if err != nil {
			t.Fatal(err)
		}
		admin := routetest.New(t, routetest.Options{API: newAPI(), Config: func(cfg *config.Config) { cfg.TrustProxy = trust }}).As("admin")
		if got := admin.Do("GET", "/api/panel/security-audit/overview", "", "X-Forwarded-Proto", c.proto); !strings.HasSuffix(got.Body, `"wss":`+c.wss+`}}`) {
			t.Errorf("TRUST_PROXY=%q, X-Forwarded-Proto %q: %s", c.trust, c.proto, got.Body)
		}
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

// TestHardeningReadsThisServer: the Hardening tab lists only what this
// server's settings decide, the session timeouts and the session limit per
// account, with their values; the fixed list of planned work and the
// dependency scan, which no scan stood behind, are gone (BUGS L104).
func TestHardeningReadsThisServer(t *testing.T) {
	timeouts := func(idle, absolute string) string {
		return `{"id":"session_timeout","name":"Panel Idle and Absolute Session Timeout","category":"auth","severity":"medium","implemented":true,` +
			`"description":"A console session ends after ` + idle + ` minutes without a request and ` + absolute + ` minutes after sign-in (SESSION_IDLE_TIMEOUT, SESSION_MAX_AGE)."}`
	}
	limit := func(on bool, count string) string {
		return `{"id":"single_session","name":"Concurrent Panel Session Limit per Account","category":"auth","severity":"high","implemented":` + strconv.FormatBool(on) +
			`,"description":"A new console login ends the least recently active sessions beyond SESSION_MAX_PER_USER (currently ` + count + `) and records IP/User-Agent for audit."}`
	}
	for _, c := range []struct {
		env  map[string]string
		want string
	}{
		{map[string]string{}, timeouts("30", "1440") + "," + limit(true, "1")},
		{map[string]string{"SESSION_MAX_PER_USER": "0"}, timeouts("30", "1440") + "," + limit(false, "unlimited")},
		{map[string]string{"SESSION_MAX_PER_USER": "4", "SESSION_IDLE_TIMEOUT": "600000", "SESSION_MAX_AGE": "7200000"}, timeouts("10", "120") + "," + limit(true, "4")},
	} {
		w := routetest.New(t, routetest.Options{Env: c.env})
		viewer := w.As("viewer")
		if got := viewer.Do("GET", "/api/panel/security-audit/hardening", ""); got.Status != 200 || got.Body != `{"priorities":[`+c.want+`]}` {
			t.Errorf("%v: %d %s\nwant %s", c.env, got.Status, got.Body, `{"priorities":[`+c.want+`]}`)
		}
		if got := viewer.Do("GET", "/api/panel/security-audit/vulnerabilities", ""); got.Status != 404 {
			t.Errorf("the dependency scan: %d %s", got.Status, got.Body)
		}
	}
}
