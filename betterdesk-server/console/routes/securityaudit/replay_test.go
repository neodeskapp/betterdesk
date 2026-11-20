package securityaudit

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/goapi"
)

// scenario is one entry of testdata/securityaudit.json: answers of the API
// server and what the Node route made of them
// (testdata/nodeoracle/securityaudit.js).
type scenario struct {
	Name  string `json:"name"`
	Route string `json:"route"`
	Query string `json:"query"`
	API   map[string]struct {
		Status int    `json:"status"`
		Body   string `json:"body"`
		Hang   bool   `json:"hang"`
	} `json:"api"`
	ForwardedProto *string `json:"forwardedProto"`
	Secure         bool    `json:"secure"`
	MaxPerUser     *string `json:"maxPerUser"`
	Now            int64   `json:"now"`
	Answer         struct {
		Status int    `json:"status"`
		Text   string `json:"text"`
	} `json:"answer"`
	Calls []string `json:"calls"`
}

func loadScenarios(t *testing.T) []scenario {
	t.Helper()
	raw, err := os.ReadFile("testdata/securityaudit.json")
	if err != nil {
		t.Fatal(err)
	}
	var out []scenario
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if len(out) < 150 {
		t.Fatalf("testdata/securityaudit.json holds %d scenarios", len(out))
	}
	return out
}

// replay sends a scenario's request to the handlers and returns the
// answer and the calls the made-up API server received, sorted.
func replay(sc scenario) (*httptest.ResponseRecorder, []string) {
	var mu sync.Mutex
	var calls []string
	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls = append(calls, r.Method+" "+r.RequestURI)
		mu.Unlock()
		answer, ok := sc.API[r.RequestURI]
		if !ok && strings.HasPrefix(r.RequestURI, "/api/audit/events") {
			answer, ok = sc.API["events"]
		}
		switch {
		case !ok:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
		case answer.Hang:
			panic(http.ErrAbortHandler)
		default:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(answer.Status)
			_, _ = w.Write([]byte(answer.Body))
		}
	})
	h := &handlers{
		api: goapi.New(api),
		getenv: func(key string) string {
			if key == "SESSION_MAX_PER_USER" && sc.MaxPerUser != nil {
				return *sc.MaxPerUser
			}
			return ""
		},
		now:   func() time.Time { return time.UnixMilli(sc.Now) },
		local: time.UTC,
	}
	target := "/api/panel/security-audit/" + sc.Route
	if sc.Query != "" {
		target += "?" + sc.Query
	}
	r := httptest.NewRequest(http.MethodGet, "http://console.test"+target, nil)
	// The query as the scenario wrote it, also where it is no valid URL
	r.URL.RawQuery = sc.Query
	if sc.ForwardedProto != nil {
		r.Header.Set("X-Forwarded-Proto", *sc.ForwardedProto)
	}
	if sc.Secure {
		r.TLS = &tls.ConnectionState{}
	}
	rec := httptest.NewRecorder()
	switch sc.Route {
	case "overview":
		h.overview(rec, r)
	case "events":
		h.events(rec, r)
	case "hardening":
		h.hardening(rec, r)
	}
	sort.Strings(calls)
	return rec, calls
}

// scenarioFix is a scenario whose answer a bug fix changes on purpose:
// the Go route answers status and text, which answer makes from Node's.
type scenarioFix struct {
	bug    string
	status int
	answer func(node string) string
}

// scenarioFixes are the scenarios of testdata/securityaudit.json that the
// bug fixes change, by "<route>, <name>": the package's list of intended
// differences.
var scenarioFixes = map[string]scenarioFix{}

func fixScenarios(bug string, status int, answer func(node string) string, names ...string) bool {
	for _, name := range names {
		if _, twice := scenarioFixes[name]; twice {
			panic("two fixes for " + name)
		}
		scenarioFixes[name] = scenarioFix{bug: bug, status: status, answer: answer}
	}
	return true
}

// TH13: an API answer without a body, or with null, 0, false or "", is
// no events, where Node failed with 502 trying to send the client's
// answer object.
var _ = fixScenarios("TH13", 200, func(string) string { return `{"events":[],"total":0}` },
	"events, an empty answer", "events, a null answer", "events, a zero answer", "events, a false answer", "events, an empty text answer")

// TH14: tls.wss follows the scheme the proxy settings give; the scenarios
// trust no proxy, so an X-Forwarded-Proto the client sent itself no
// longer makes it true.
var _ = fixScenarios("TH14", 200, func(node string) string { return strings.Replace(node, `"wss":true`, `"wss":false`, 1) },
	`overview, forwarded proto "https"`, `overview, forwarded proto "HTTPS"`, `overview, forwarded proto " https "`,
	`overview, forwarded proto "https, http"`, `overview, forwarded proto "https,http"`)

func TestRoutesMatchNode(t *testing.T) {
	used := map[string]bool{}
	for _, sc := range loadScenarios(t) {
		name := sc.Route + ", " + sc.Name
		rec, calls := replay(sc)
		if fix, listed := scenarioFixes[name]; listed {
			used[name] = true
			if want := fix.answer(sc.Answer.Text); rec.Code != fix.status || rec.Body.String() != want {
				t.Errorf("%s (%s):\n got %d %s\n fix %d %s", name, fix.bug, rec.Code, rec.Body.String(), fix.status, want)
			}
		} else if rec.Code != sc.Answer.Status || rec.Body.String() != sc.Answer.Text {
			t.Errorf("%s:\n got %d %s\nNode %d %s", name, rec.Code, rec.Body.String(), sc.Answer.Status, sc.Answer.Text)
		}
		if !reflect.DeepEqual(calls, sc.Calls) && (len(calls) != 0 || len(sc.Calls) != 0) {
			t.Errorf("%s: the API server was asked %q, Node asked %q", name, calls, sc.Calls)
		}
	}
	for name, fix := range scenarioFixes {
		if !used[name] {
			t.Errorf("the fix of %s names no scenario: %s", fix.bug, name)
		}
	}
}

// TestOverviewCountsWhatTheAPIServerReports: an old key, a failed sign-in
// of this hour and two bans show in the figures and fail their checks,
// and TLS on the API passes its check.
func TestOverviewCountsWhatTheAPIServerReports(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	var sc scenario
	raw := `{"route":"overview","now":` + jsonInt(now.UnixMilli()) + `,"api":{
		"/api/health":{"status":200,"body":"{\"tls\":true}"},
		"/api/keys":{"status":200,"body":"{\"keys\":[{\"created_at\":\"` + now.Add(-100*24*time.Hour).Format(time.RFC3339) + `\"}]}"},
		"events":{"status":200,"body":"{\"events\":[{\"action\":\"auth_login_failed\",\"timestamp\":\"` + now.Format(time.RFC3339) + `\"}]}"},
		"/api/blocklist":{"status":200,"body":"{\"count\":2,\"entries\":[]}"}}}`
	if err := json.Unmarshal([]byte(raw), &sc); err != nil {
		t.Fatal(err)
	}
	rec, calls := replay(sc)
	var got struct {
		Score  int `json:"score"`
		Checks []struct {
			ID   string `json:"id"`
			Pass bool   `json:"pass"`
		} `json:"checks"`
		Stats map[string]float64 `json:"stats"`
		TLS   map[string]any     `json:"tls"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != 200 {
		t.Fatalf("%d %s: %v", rec.Code, rec.Body.String(), err)
	}
	if got.Stats["failed_logins"] != 1 || got.Stats["bans"] != 2 || got.Stats["api_keys"] != 1 || got.Stats["old_keys"] != 1 || got.Score != 80 {
		t.Errorf("figures: %s", rec.Body.String())
	}
	if got.TLS["api"] != true || got.TLS["signal"] != nil || got.TLS["relay"] != nil {
		t.Errorf("tls: %s", rec.Body.String())
	}
	passed := map[string]bool{}
	for _, c := range got.Checks {
		passed[c.ID] = c.Pass
	}
	if !passed["tls_api"] || passed["no_bans"] || passed["api_key_age"] || !passed["failed_logins"] {
		t.Errorf("checks: %s", rec.Body.String())
	}
	want := []string{"GET /api/audit/events?limit=500", "GET /api/blocklist", "GET /api/health", "GET /api/keys"}
	if !reflect.DeepEqual(calls, want) {
		t.Errorf("the API server was asked %q, want %q", calls, want)
	}
}

func jsonInt(n int64) string {
	raw, _ := json.Marshal(n)
	return string(raw)
}
