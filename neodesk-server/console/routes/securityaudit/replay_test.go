package securityaudit

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
)

// scenario is one entry of testdata/securityaudit.json: answers of the API
// server and what the Node route made of them (testdata/README.md).
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

// L104: the hardening list holds only what this server's settings decide:
// the session timeouts (the defaults in the scenarios) and the session
// limit as before; the fixed list of planned work is gone.
var _ = fixScenarios("L104", 200, func(node string) string {
	start := strings.Index(node, `{"id":"single_session"`)
	if start < 0 {
		return "a recorded answer without the session limit"
	}
	limit := node[start : start+strings.Index(node[start:], "}")+1]
	return `{"priorities":[{"id":"session_timeout","name":"Panel Idle and Absolute Session Timeout","category":"auth","severity":"medium","implemented":true,` +
		`"description":"A console session ends after 30 minutes without a request and 1440 minutes after sign-in (SESSION_IDLE_TIMEOUT, SESSION_MAX_AGE)."},` +
		limit + `]}`
}, "hardening, session limit undefined", `hardening, session limit "0"`, `hardening, session limit "1"`, `hardening, session limit "5"`,
	`hardening, session limit "abc"`, `hardening, session limit "2000"`, `hardening, session limit " 3 "`, `hardening, session limit "-1"`,
	`hardening, session limit "1.5"`, `hardening, session limit ""`, `hardening, session limit "007"`)

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

// TH15: a list of keys or events that is no list is 502, where Node
// answered an empty overview with 200, and so are entries of no kind Node
// could read; entries that are no objects are skipped instead: a null
// event or key leaves the overview of the others ("a key that is null" is
// then "a quiet server"), and the three keys that are text, a number and
// a list no longer count. An event whose time cannot be read is not
// recent: six of the failed sign-ins of "failed logins of every kind"
// (no time, "some day", "", a number, true, an object) leave the count of
// the last day, which then passes its check. A blocklist count that is
// no number leaves the count to the entries, three in these scenarios, as
// "blocklist count 2" counts with 3 bans.
var _ = fixScenarios("TH15", 502, func(string) string { return `{"success":false,"error":"The API server's audit data cannot be read"}` },
	"overview, audit events as text", "overview, audit events as an object", "overview, audit events as a number",
	"overview, keys as text", "overview, keys as an object")

var _ = fixScenarios("TH15", 200, func(string) string { return nodeAnswerOf("overview, a quiet server") },
	"overview, a key that is null")

var _ = fixScenarios("TH15", 200, func(string) string {
	return strings.Replace(nodeAnswerOf("overview, a quiet server"), `"config_changes":1`, `"config_changes":0`, 1)
}, "overview, an audit event that is null")

var _ = fixScenarios("TH15", 200, func(node string) string {
	return strings.NewReplacer(`"failed_logins":14`, `"failed_logins":8`,
		`{"id":"failed_logins","name":"Low Failed Login Rate","pass":false}`, `{"id":"failed_logins","name":"Low Failed Login Rate","pass":true}`).Replace(node)
}, "overview, failed logins of every kind")

var _ = fixScenarios("TH15", 200, func(node string) string { return strings.Replace(node, `"api_keys":20`, `"api_keys":17`, 1) },
	"overview, keys of every age")

var _ = fixScenarios("TH15", 200, func(string) string {
	return strings.Replace(nodeAnswerOf("overview, blocklist count 2"), `"bans":2`, `"bans":3`, 1)
}, `overview, blocklist count null`, `overview, blocklist count "5"`, `overview, blocklist count ""`, `overview, blocklist count " 7 "`,
	`overview, blocklist count true`, `overview, blocklist count false`, `overview, blocklist count []`, `overview, blocklist count [7]`,
	`overview, blocklist count "0x10"`, `overview, blocklist count "1e2"`)

// nodeAnswerOf is the text Node answered to a scenario, by "<route>,
// <name>".
func nodeAnswerOf(name string) string {
	raw, err := os.ReadFile("testdata/securityaudit.json")
	if err != nil {
		panic(err)
	}
	var all []scenario
	if err := json.Unmarshal(raw, &all); err != nil {
		panic(err)
	}
	for _, sc := range all {
		if sc.Route+", "+sc.Name == name {
			return sc.Answer.Text
		}
	}
	panic("no scenario " + name)
}

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

// TestOverviewSkipsEntriesOfTheWrongKind: an event or key that is no
// object is skipped, an event whose time cannot be read is not recent,
// a blocklist count that is no number leaves the count to the entries,
// and a list that is no list is 502 (BUGS.md TH15).
func TestOverviewSkipsEntriesOfTheWrongKind(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	overview := func(keys, events, blocklist string) (int, string) {
		t.Helper()
		var sc scenario
		raw := `{"route":"overview","now":` + jsonInt(now.UnixMilli()) + `,"api":{
			"/api/health":{"status":200,"body":"{\"tls\":true}"},
			"/api/keys":{"status":200,"body":` + strconv.Quote(keys) + `},
			"events":{"status":200,"body":` + strconv.Quote(events) + `},
			"/api/blocklist":{"status":200,"body":` + strconv.Quote(blocklist) + `}}}`
		if err := json.Unmarshal([]byte(raw), &sc); err != nil {
			t.Fatal(err)
		}
		rec, _ := replay(sc)
		return rec.Code, rec.Body.String()
	}
	recent := now.Add(-time.Hour).Format(time.RFC3339)
	old := now.Add(-100 * 24 * time.Hour).Format(time.RFC3339)
	status, body := overview(`[null,5,"k",{"created_at":"`+old+`"}]`,
		`{"events":[null,"x",[1],{"action":"auth_login_failed","timestamp":"`+recent+`"},{"action":"login_failed","timestamp":"garbage"},{"action":"login_failed"}]}`,
		`{"count":null,"entries":[1,2]}`)
	if want := `"stats":{"failed_logins":1,"bans":2,"config_changes":0,"api_keys":1,"old_keys":1}`; status != 200 || !strings.Contains(body, want) {
		t.Errorf("entries of the wrong kind: %d %s\nwant %s", status, body, want)
	}
	for _, c := range []struct{ blocklist, bans string }{
		{`{"count":"5","entries":[]}`, `"bans":0`}, {`{"count":true,"entries":[1]}`, `"bans":1`}, {`{"count":3,"entries":[]}`, `"bans":3`},
	} {
		if status, body := overview(`[]`, `[]`, c.blocklist); status != 200 || !strings.Contains(body, c.bans) {
			t.Errorf("blocklist %s: %d %s", c.blocklist, status, body)
		}
	}
	for _, c := range []struct{ keys, events string }{
		{`[]`, `{"events":"text"}`}, {`[]`, `{"events":{"a":1}}`}, {`{"keys":"text"}`, `[]`}, {`{"keys":{"a":1}}`, `[]`},
	} {
		if status, body := overview(c.keys, c.events, `{}`); status != 502 || body != `{"success":false,"error":"The API server's audit data cannot be read"}` {
			t.Errorf("keys %s, events %s: %d %s", c.keys, c.events, status, body)
		}
	}
}

// TestKeyAgeIsReadAsUTC: the API server writes a key's created_at in UTC
// without a zone, and the overview reads it so, also when the console's
// clock is east of UTC (BUGS.md TH16).
func TestKeyAgeIsReadAsUTC(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		age time.Duration
		old string
	}{{keyMaxAge - 30*time.Minute, `"old_keys":0`}, {keyMaxAge + 30*time.Minute, `"old_keys":1`}} {
		created := now.Add(-c.age).Format(time.DateTime)
		api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Path == "/api/keys" {
				_, _ = w.Write([]byte(`[{"id":1,"created_at":"` + created + `"}]`))
				return
			}
			_, _ = w.Write([]byte(`{}`))
		})
		h := &handlers{api: goapi.New(api), now: func() time.Time { return now }, local: time.FixedZone("east", 2*3600)}
		rec := httptest.NewRecorder()
		h.overview(rec, httptest.NewRequest(http.MethodGet, "/api/panel/security-audit/overview", nil))
		if !strings.Contains(rec.Body.String(), c.old) {
			t.Errorf("a key created %s, now %s: %s, want %s", created, now.Format(time.DateTime), rec.Body.String(), c.old)
		}
	}
}

func jsonInt(n int64) string {
	raw, _ := json.Marshal(n)
	return string(raw)
}
