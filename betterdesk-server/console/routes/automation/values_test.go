package automation

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/middleware"
)

// bodyOf is req.body for a JSON object.
func bodyOf(t testing.TB, object string) middleware.JSONObject {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(object))
	r.Header.Set("Content-Type", "application/json")
	var body middleware.JSONObject
	parse := middleware.Bodies(middleware.BodyOptions{OnError: func(http.ResponseWriter, *http.Request, int, error) {}})
	parse(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		body = middleware.Body(r)
	})).ServeHTTP(httptest.NewRecorder(), r)
	return body
}

func TestTurnedOff(t *testing.T) {
	for value, want := range map[string]bool{
		`false`: true, `0`: true, `-0`: true, `0.0`: true, `0e3`: true, `"0"`: true, `"false"`: true,
		`true`: false, `1`: false, `0.5`: false, `"1"`: false, `"true"`: false, `""`: false, `" 0"`: false, `"0.0"`: false, `"False"`: false, `"no"`: false,
		`null`: false, `[]`: false, `[0]`: false, `[false]`: false, `{}`: false,
	} {
		body := bodyOf(t, `{"enabled":`+value+`}`)
		if got := turnedOff(jsbind.Of(body.Get("enabled"))); got != want {
			t.Errorf("turnedOff(%s) = %v, want %v", value, got, want)
		}
	}
	if turnedOff(jsbind.Undefined) {
		t.Error("turnedOff(undefined) = true, want false")
	}
}

func TestAsText(t *testing.T) {
	for value, want := range map[string]string{
		`"D1"`: "D1", `5`: "5", `1.50`: "1.5", `1e21`: "1e+21", `1e400`: "Infinity", `true`: "true", `null`: "null",
		`[]`: "", `["a","b"]`: "a,b", `[1,[2,["x"]],null,"q"]`: "1,2,x,,q", `[null]`: "", `{}`: "[object Object]", `{"a":1}`: "[object Object]",
		`{"valueOf":1}`: "[object Object]", `[{}]`: "[object Object]", `"half \ud83d pair"`: "half \ufffd pair",
	} {
		body := bodyOf(t, `{"device_id":`+value+`}`)
		got, err := asText(body.Get("device_id"))
		if err != nil || got != want {
			t.Errorf("asText(%s) = %q, %v; want %q", value, got, err, want)
		}
	}
	for _, value := range []string{`{"toString":1}`, `{"toString":null}`, `{"toString":"x","valueOf":"y"}`, `[{"toString":1}]`, `[1,[2,{"toString":{}}]]`} {
		body := bodyOf(t, `{"device_id":`+value+`}`)
		if got, err := asText(body.Get("device_id")); err == nil || err.Error() != "Cannot convert object to primitive value" {
			t.Errorf("asText(%s) = %q, %v; want the TypeError", value, got, err)
		}
	}
}

// The group is mounted at /api/bd too, for the routes of the device
// agents. There the thirteen panel routes step aside, so that GET
// /api/bd/commands reaches the device route after them (the console
// tests show that one answering).
func TestTheRoutesStepAsideAtTheDeviceMount(t *testing.T) {
	router := kit.NewRouter()
	Register((&kit.Deps{Router: router, Log: kit.NewLogger("silent", true)}).At("/api/bd"))
	if len(router.Routes()) != 15 {
		t.Fatalf("%d routes registered, want the thirteen panel routes and the two device routes", len(router.Routes()))
	}
	for _, target := range []string{
		"GET /api/bd/rules", "POST /api/bd/rules", "GET /api/bd/rules/1", "PATCH /api/bd/rules/1", "DELETE /api/bd/rules/1",
		"GET /api/bd/alerts", "POST /api/bd/alerts/1/ack", "POST /api/bd/commands", "GET /api/bd/commands/1",
		"GET /api/bd/smtp", "PUT /api/bd/smtp", "POST /api/bd/smtp/test",
	} {
		method, path, _ := strings.Cut(target, " ")
		rec := httptest.NewRecorder()
		if router.Serve(rec, httptest.NewRequest(method, path, nil)) {
			t.Errorf("%s was answered with %d: the route has to pass the request on", target, rec.Code)
		}
	}
}

// writtenInDigits lets an id through that is written with digits and
// nothing else.
func TestWrittenInDigits(t *testing.T) {
	router := kit.NewRouter()
	mounted := router.Mount("/api/automation")
	mounted.Get("/rules/{id}", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("id " + r.PathValue("id"))) }, writtenInDigits)
	mounted.Post("/alerts/{id}/ack", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ack " + r.PathValue("id"))) }, writtenInDigits)
	for target, want := range map[string]string{
		"GET /api/automation/rules/1":                           "id 1",
		"GET /api/automation/rules/007":                         "id 007",
		"GET /api/automation/rules/1/":                          "id 1",
		"GET /API/Automation/RULES/12":                          "id 12",
		"GET /api/automation/rules/99999999999999999999":        "id 99999999999999999999",
		"GET /api/automation/rules/1?x=/2a":                     "id 1",
		"POST /api/automation/alerts/45/ack":                    "ack 45",
		"POST /api/automation/alerts/45/ack/":                   "ack 45",
		"GET /api/automation/rules/%31":                         "",
		"GET /api/automation/rules/1%20":                        "",
		"GET /api/automation/rules/%201":                        "",
		"GET /api/automation/rules/1.0":                         "",
		"GET /api/automation/rules/+1":                          "",
		"GET /api/automation/rules/-1":                          "",
		"GET /api/automation/rules/0x1":                         "",
		"GET /api/automation/rules/abc":                         "",
		"GET /api/automation/rules/1a":                          "",
		"GET /api/automation/rules/%EF%BC%91":                   "",
		"GET /api/automation/rules/%D9%A1":                      "",
		"POST /api/automation/alerts/4%35/ack":                  "",
		"POST /api/automation/alerts/x/ack":                     "",
		"POST /api/automation/alerts/ack/ack":                   "",
		"GET /api/automation/rules/1;x":                         "",
		"GET /api/automation/rules/1%2C2":                       "",
		"GET /api/automation/rules/" + strings.Repeat("9", 400): "id " + strings.Repeat("9", 400),
	} {
		method, path, _ := strings.Cut(target, " ")
		r := httptest.NewRequest(method, path, nil)
		rec := httptest.NewRecorder()
		answered := router.Serve(rec, r)
		if got := rec.Body.String(); answered != (want != "") || got != want {
			t.Errorf("%s: answered %v with %q, want %q", target, answered, got, want)
		}
	}
}

// Whatever a query string holds, a filter is one text or refused, the
// limit is a whole number from 1 to 500 or refused, and the id rule lets
// through digits only.
func FuzzQueryAndPath(f *testing.F) {
	for _, seed := range []string{"device_id=D1&limit=5", "limit[toString]=1", "device_id[]=a&device_id[]=b", "acknowledged=true", "status[x][y]=1#limit=2", "limit=%zz", "1", "%31", "12/ack", ""} {
		f.Add(seed)
	}
	router := kit.NewRouter()
	router.Mount("/api/automation").Get("/rules/{id}", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(r.PathValue("id"))) }, writtenInDigits)
	f.Fuzz(func(t *testing.T, text string) {
		if strings.ContainsAny(text, " \x7f") || strings.IndexFunc(text, func(r rune) bool { return r < 0x21 || r > 0x7e }) >= 0 {
			return
		}
		r, err := http.NewRequest(http.MethodGet, "http://console.test/api/automation/alerts?"+text, nil)
		if err != nil {
			return
		}
		for _, name := range []string{"device_id", "severity", "status"} {
			value, ok := filterOf(r, name)
			if _, given, err := jsbind.QueryText(r.URL.RawQuery, name); ok != (err == nil) || !given && value != "" {
				t.Fatalf("%s of %q = %q, %v", name, text, value, ok)
			}
		}
		if n, ok := limitOf(r); ok && (n < 0 || n > 500) || !ok && n != 0 {
			t.Fatalf("limit of %q = %d, %v", text, n, ok)
		}

		if strings.ContainsAny(text, "?#") {
			return
		}
		p, err := http.NewRequest(http.MethodGet, "http://console.test/api/automation/rules/"+text, nil)
		if err != nil {
			return
		}
		rec := httptest.NewRecorder()
		if router.Serve(rec, p) && rec.Code == http.StatusOK {
			id := strings.TrimSuffix(text, "/")
			if id == "" || strings.Trim(id, "0123456789") != "" || rec.Body.String() != id {
				t.Fatalf("the id rule let %q through as %q", text, rec.Body.String())
			}
		}
	})
}

// Whatever a body holds, making text of a member of it either works or is
// the TypeError of an object with a member named toString.
func FuzzAsText(f *testing.F) {
	for _, seed := range []string{`"text"`, `5`, `1e400`, `true`, `null`, `[1,[2],null,{"a":1}]`, `{"toString":1}`, `"\ud83d"`, `[{"toString":[]}]`} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		if !json.Valid([]byte(value)) || len(value) > 4096 {
			return
		}
		body := bodyOf(t, `{"v":`+value+`}`)
		if !body.Has("v") {
			return
		}
		text, err := asText(body.Get("v"))
		if err != nil && (err.Error() != "Cannot convert object to primitive value" || !strings.Contains(value, "toString")) {
			t.Fatalf("asText(%s) = %q, %v", value, text, err)
		}
		_ = turnedOff(jsbind.Of(body.Get("v")))
	})
}
