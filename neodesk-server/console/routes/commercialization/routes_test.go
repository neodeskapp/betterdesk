package commercialization_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/goapipath/proxytest"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/routes/commercialization"
	"github.com/unitronix/betterdesk-server/console/routes/internal/reqquery"
)

// The vectors are what routes/commercialization.routes.js of the Node
// console answered and asked the API server for the routes that pass a
// call on (testdata/vectors.json): every role, every kind of answer
// of the API server (money and times included), the files of the
// exports, path parameters, queries and bodies. They are replayed in two
// parts: the calls the API server answers, and the ones it never answers,
// which each wait for the call to give up.

func TestVectorsAnswered(t *testing.T) {
	replay(t, func(v proxytest.Vector) bool { return v.API == nil || !v.API.Hang })
}

func TestVectorsUnanswered(t *testing.T) {
	replay(t, func(v proxytest.Vector) bool { return v.API != nil && v.API.Hang })
}

// TestVectorsQuick is a part of the answered vectors that takes seconds:
// every route for every role (the first 147), every query that cannot be
// written, and every fifteenth of the others. Mutation runs use it.
func TestVectorsQuick(t *testing.T) {
	i := -1
	replay(t, func(v proxytest.Vector) bool {
		i++
		if v.API != nil && v.API.Hang {
			return false
		}
		return i < 147 || strings.Contains(v.Request.Path, "toString") || i%15 == 0
	})
}

// twoDots are the requests of the vectors whose answer a bug fix of
// BUGS.md changed on purpose, with the parameter the route names: an id
// with two dots in it is refused with 400 before any call (FL4), where
// Node built the path and its API client refused it, which the route
// answered as a call without an answer.
var twoDots = map[string]string{
	"GET /api/panel/billing/packages/a..b/preview": "packageId",
	"PUT /api/panel/billing/packages/a..b":         "packageId",
	"DELETE /api/panel/billing/packages/a..b":      "packageId",
	"PUT /api/panel/billing/contracts/a..b":        "contractId",
	"DELETE /api/panel/billing/contracts/a..b":     "contractId",
	"POST /api/panel/billing/sessions/a..b/report": "sessionId",
	"PUT /api/panel/billing/currencies/a..b":       "currencyCode",
}

// fixed changes the vectors of twoDots into the answer the console gives
// now and reports whether v is one of them.
func fixed(v *proxytest.Vector) bool {
	label, ok := twoDots[v.Request.Method+" "+v.Request.Path]
	if !ok || v.Status != 500 || v.Body != `{"error":"Go server unreachable"}` {
		return false
	}
	v.Status, v.Type, v.Body, v.Calls = 400, "application/json; charset=utf-8", `{"error":"Invalid `+label+`"}`, nil
	return true
}

// othersReport changes the vector of a work report filed by an account
// without user.edit into the answer the console gives now: the route
// looks the session up first (GET /api/billing/sessions/{id}) to see
// whether its operator is another account (L49), and the vectors' API
// answer holds no session, so the lookup fails before the report goes
// on. It reports whether v is one of them.
func othersReport(v *proxytest.Vector) bool {
	session := v.Request.Session
	if v.Request.Method != http.MethodPost || !strings.HasSuffix(v.Request.Path, "/report") || session == nil ||
		session.User.Role != "operator" || len(v.Calls) != 1 {
		return false
	}
	target := strings.TrimSuffix(v.Calls[0].URL, "/report")
	v.Status, v.Type, v.Body = http.StatusInternalServerError, "application/json; charset=utf-8", `{"error":"Go server unreachable"}`
	v.Calls = []proxytest.Call{{Method: http.MethodGet, URL: target, Type: "application/json"}}
	return true
}

func TestOthersReportVectorsAreCounted(t *testing.T) {
	changed := 0
	for _, v := range proxytest.Load(t, filepath.Join("testdata", "vectors.json")) {
		if othersReport(&v) {
			changed++
		}
	}
	if changed != 1 {
		t.Errorf("%d vectors file a work report without user.edit, want 1", changed)
	}
}

func TestTwoDotsVectorsAreListed(t *testing.T) {
	found := map[string]int{}
	for _, v := range proxytest.Load(t, filepath.Join("testdata", "vectors.json")) {
		if fixed(&v) {
			found[v.Request.Method+" "+v.Request.Path]++
		}
	}
	for request := range twoDots {
		if found[request] != 1 {
			t.Errorf("%s is in %d vectors, want 1", request, found[request])
		}
	}
}

// namedMember finds a member named __proto__, constructor or prototype in
// a JSON body.
var namedMember = regexp.MustCompile(`"(__proto__|constructor|prototype)":`)

// everyMember changes the vectors whose body has a member named
// __proto__, constructor or prototype into the call the console makes
// now, with the body as the client sent it (BUGS FL6), where Node's API
// client left those members out. It reports whether v is one of them.
func everyMember(v *proxytest.Vector) bool {
	if v.Request.Body == nil || !namedMember.MatchString(*v.Request.Body) || len(v.Calls) != 1 ||
		v.Calls[0].Body == "" || v.Calls[0].Body == *v.Request.Body {
		return false
	}
	v.Calls[0].Body = *v.Request.Body
	return true
}

func TestEveryMemberVectorsAreCounted(t *testing.T) {
	changed := 0
	for _, v := range proxytest.Load(t, filepath.Join("testdata", "vectors.json")) {
		if everyMember(&v) {
			changed++
		}
	}
	if changed != 6 {
		t.Errorf("%d vectors send a body with __proto__, constructor or prototype, want 6", changed)
	}
}

// plainQueries changes the vectors whose query has a parameter written
// with brackets into the call the console makes now, which leaves such
// parameters out (BUGS CM7): where Node called the API server, the call's
// query loses them; where Node could not write the query and answered
// 500, the console calls the API server and answers as the same route
// does for the same caller and API answer without such a query
// (template). It reports whether v is one of them.
func plainQueries(v *proxytest.Vector, templates map[string]proxytest.Vector) bool {
	route, raw, ok := bracketedQuery(v)
	if !ok {
		return false
	}
	plain := withoutBrackets(raw)
	switch {
	case len(v.Calls) == 1:
		api, _, _ := strings.Cut(v.Calls[0].URL, "?")
		v.Calls[0].URL = api + plain
	case v.Status == http.StatusInternalServerError && strings.Contains(v.Body, "Cannot convert object to primitive value"):
		tpl, found := templates[templateKey(*v, route)]
		if !found {
			return false
		}
		v.Status, v.Type, v.Body, v.Base64, v.Disposition, v.Calls = tpl.Status, tpl.Type, tpl.Body, tpl.Base64, tpl.Disposition, nil
		if len(tpl.Calls) == 1 {
			call := tpl.Calls[0]
			api, _, _ := strings.Cut(call.URL, "?")
			call.URL = api + plain
			v.Calls = []proxytest.Call{call}
		}
	default:
		return false
	}
	return true
}

// bracketedQuery returns the path and the query of a vector whose query
// has a part with a bracket in its name.
func bracketedQuery(v *proxytest.Vector) (route, raw string, ok bool) {
	r := httptest.NewRequest(http.MethodGet, "http://console"+v.Request.Path, nil)
	raw, _, _ = strings.Cut(r.URL.RawQuery, "#")
	for _, part := range strings.Split(raw, "&") {
		name, _, _ := strings.Cut(strings.NewReplacer("%5B", "[", "%5b", "[").Replace(part), "=")
		if before, _, found := strings.Cut(part, "]="); found {
			name = strings.NewReplacer("%5B", "[", "%5b", "[").Replace(before)
		}
		if strings.Contains(name, "[") {
			route, _, _ = strings.Cut(v.Request.Path, "?")
			return route, raw, true
		}
	}
	return "", "", false
}

// withoutBrackets is the query the console sends for a request's query:
// the parts without a bracket in their name, as req.query writes them.
func withoutBrackets(raw string) string {
	var plain []string
	for _, part := range strings.Split(raw, "&") {
		name, _, _ := strings.Cut(strings.NewReplacer("%5B", "[", "%5b", "[").Replace(part), "=")
		if before, _, found := strings.Cut(part, "]="); found {
			name = strings.NewReplacer("%5B", "[", "%5b", "[").Replace(before)
		}
		if !strings.Contains(name, "[") {
			plain = append(plain, part)
		}
	}
	text, _ := reqquery.Of(&http.Request{URL: &url.URL{RawQuery: strings.Join(plain, "&")}}).Search()
	if text == "" {
		return ""
	}
	return "?" + text
}

// templateKey names a request by method, path, caller and API answer.
func templateKey(v proxytest.Vector, route string) string {
	who := "signed out"
	if v.Request.Session != nil {
		who = v.Request.Session.User.Role
	}
	api, _ := json.Marshal(v.API)
	return v.Request.Method + " " + route + " " + who + " " + string(api)
}

// templates are the answers of the vectors without a query, by
// templateKey, as the console gives them.
func templates(vectors []proxytest.Vector) map[string]proxytest.Vector {
	out := map[string]proxytest.Vector{}
	for _, v := range vectors {
		if strings.Contains(v.Request.Path, "?") {
			continue
		}
		fixed(&v)
		everyMember(&v)
		othersReport(&v)
		if key := templateKey(v, v.Request.Path); out[key].Status == 0 {
			out[key] = v
		}
	}
	return out
}

func TestPlainQueryVectorsAreCounted(t *testing.T) {
	vectors := proxytest.Load(t, filepath.Join("testdata", "vectors.json"))
	from := templates(vectors)
	changed, unwritable := 0, 0
	for _, v := range vectors {
		was := v.Status
		if plainQueries(&v, from) {
			changed++
			if was == http.StatusInternalServerError {
				unwritable++
			}
		} else if _, _, bracketed := bracketedQuery(&v); bracketed && v.Status == http.StatusInternalServerError {
			t.Errorf("no template for %s as %v", v.Request.Path, v.Request.Session)
		}
	}
	if changed != 1376 || unwritable != 194 {
		t.Errorf("%d vectors with brackets in the query changed, %d of them unwritable before; want 1376 and 194", changed, unwritable)
	}
}

func replay(t *testing.T, keep func(proxytest.Vector) bool) {
	t.Helper()
	all := proxytest.Load(t, filepath.Join("testdata", "vectors.json"))
	from := templates(all)
	var kept []proxytest.Vector
	for _, v := range all {
		if keep(v) {
			fixed(&v)
			everyMember(&v)
			othersReport(&v)
			plainQueries(&v, from)
			kept = append(kept, v)
		}
	}
	data, err := json.Marshal(kept)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "vectors.json")
	if err := os.WriteFile(file, data, 0o600); err != nil {
		t.Fatal(err)
	}
	proxytest.Replay(t, file, commercialization.Register)
}

func TestRegisteredRoutes(t *testing.T) {
	d := &kit.Deps{Router: kit.NewRouter(), Log: kit.NewLogger("error", true)}
	commercialization.Register(d)
	var got []string
	for _, route := range d.Router.Routes() {
		got = append(got, route.Method+" "+route.Path)
	}
	sort.Strings(got)
	routes := []string{
		"GET /api/panel/billing/timesync/status",
		"POST /api/panel/billing/timesync/check",
		"GET /api/panel/billing/clock/settings",
		"PUT /api/panel/billing/clock/settings",
		"GET /api/panel/billing/clock/detect-timezone",
		"GET /api/panel/billing/packages",
		"GET /api/panel/billing/packages/{packageId}/preview",
		"POST /api/panel/billing/packages",
		"PUT /api/panel/billing/packages/{packageId}",
		"DELETE /api/panel/billing/packages/{packageId}",
		"GET /api/panel/billing/contracts",
		"POST /api/panel/billing/contracts",
		"PUT /api/panel/billing/contracts/{contractId}",
		"DELETE /api/panel/billing/contracts/{contractId}",
		"GET /api/panel/billing/stats",
		"GET /api/panel/billing/sessions",
		"GET /api/panel/billing/check",
		"GET /api/panel/billing/sessions/pending",
		"GET /api/panel/billing/sessions/export",
		"GET /api/panel/billing/reports",
		"GET /api/panel/billing/reports/export",
		"POST /api/panel/billing/sessions/{sessionId}/report",
		"GET /api/panel/billing/currencies",
		"PUT /api/panel/billing/currencies/{currencyCode}",
		"GET /api/panel/commercialization/email-config",
		"PUT /api/panel/commercialization/email-config",
		"GET /api/panel/commercialization/smtp-status",
	}
	sort.Strings(routes)
	if strings.Join(got, "\n") != strings.Join(routes, "\n") {
		t.Errorf("routes:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(routes, "\n"))
	}
}
