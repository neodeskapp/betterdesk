package automation

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/middleware"
	"github.com/unitronix/betterdesk-server/console/store"
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

func TestParseInt10(t *testing.T) {
	nan := math.NaN()
	for text, want := range map[string]float64{
		"5": 5, "05": 5, " 7": 7, "\t\n7": 7, "\xc2\xa07": 7, "\xef\xbb\xbf7": 7, "\xe2\x80\x837": 7, "7 ": 7, "7abc": 7, "1.9": 1, "1e3": 1, "1,2": 1,
		"+3": 3, "-3": -3, "--3": nan, "+-3": nan, "-": nan, "": nan, "abc": nan, ".5": nan, "Infinity": nan, "NaN": nan,
		"0": 0, "-0": 0, "0x10": 0, "0X10": 0, "-0x10": 0, " +0xff": 0, "0x": 0, "0xzz": 0, "00x10": 0, "x10": nan, "1x10": 1,
		"99999999999999999999": 1e20, "9007199254740993": 9007199254740992, "1" + strings.Repeat("0", 400): math.Inf(1),
		"-1" + strings.Repeat("0", 400): math.Inf(-1),
	} {
		got := parseInt10(text)
		if math.IsNaN(want) != math.IsNaN(got) || !math.IsNaN(want) && got != want {
			t.Errorf("parseInt(%q, 10) = %v, want %v", text, got, want)
		}
	}
}

// What parseInt10 makes of any text: a whole number or NaN, the number
// of the digits it starts with, and never the hexadecimal reading.
func FuzzParseInt10(f *testing.F) {
	for _, seed := range []string{"5", " -12abc", "0x1f", "+0X", "1e5", "  9", "", "-", "9999999999999999999999"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, text string) {
		got := parseInt10(text)
		if !math.IsNaN(got) && !math.IsInf(got, 0) && got != math.Trunc(got) {
			t.Fatalf("parseInt(%q, 10) = %v is no whole number", text, got)
		}
		digits := strings.TrimLeft(text, "0123456789")
		if lead := text[:len(text)-len(digits)]; lead != "" {
			want, _ := strconv.ParseFloat(lead, 64)
			if got != want {
				t.Fatalf("parseInt(%q, 10) = %v, want the number of its digits %v", text, got, want)
			}
		} else if text == "" || text[0] >= 'a' && text[0] <= 'z' {
			if !math.IsNaN(got) {
				t.Fatalf("parseInt(%q, 10) = %v, want NaN", text, got)
			}
		}
	})
}

func TestLimit(t *testing.T) {
	nan := math.NaN()
	for query, want := range map[string]struct {
		n     float64
		given bool
		fails bool
	}{
		"":                       {0, false, false},
		"limit=":                 {0, false, false},
		"limit":                  {0, false, false},
		"other=5":                {0, false, false},
		"limit=5":                {5, true, false},
		"limit=0":                {100, true, false},
		"limit=-3":               {1, true, false},
		"limit=abc":              {100, true, false},
		"limit=0x10":             {100, true, false},
		"limit=%202rows":         {2, true, false},
		"limit=3&limit=4":        {3, true, false},
		"limit[]=7":              {7, true, false},
		"limit[rows]=7":          {100, true, false},
		"limit[toString]=7":      {0, false, true},
		"limit[0][toString]=7":   {0, false, true},
		"limit=5#limit=6":        {5, true, false},
		"LIMIT=5":                {0, false, false},
		"limit=9007199254740993": {500, true, false},
		"limit=500":              {500, true, false},
		"limit=501":              {500, true, false},
		"limit=1":                {1, true, false},
	} {
		r := httptest.NewRequest(http.MethodGet, "/api/automation/alerts?"+query, nil)
		n, given, err := limit(r)
		if (err != nil) != want.fails || given != want.given || math.IsNaN(n) != math.IsNaN(want.n) || !math.IsNaN(n) && n != want.n {
			t.Errorf("limit of %q = %v, %v, %v; want %v, %v, fails %v", query, n, given, err, want.n, want.given, want.fails)
		}
	}
	for limit, want := range map[float64]bool{0: false, nan: false, 1: true, -1: true, math.Inf(1): true, 0.5: true} {
		if got := counts(limit); got != want {
			t.Errorf("counts(%v) = %v, want %v", limit, got, want)
		}
	}
}

func TestQueryValue(t *testing.T) {
	for query, want := range map[string]string{
		"device_id=D1":                 `"D1"`,
		"device_id=":                   `""`,
		"other=1":                      ``,
		"device_id=D1&device_id=D2":    `["D1","D2"]`,
		"device_id[]=D1":               `["D1"]`,
		"device_id=D1#device_id=D2":    `"D1"`,
		"device_id=a%20b+c":            `"a b c"`,
		"x=1&device_id=D1#&device_id=": `"D1"`,
	} {
		r := httptest.NewRequest(http.MethodGet, "/api/automation/alerts?"+query, nil)
		if got := queryValue(r, "device_id").JSON(); got != want {
			t.Errorf("device_id of %q = %s, want %s", query, got, want)
		}
	}
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

func TestGiven(t *testing.T) {
	body := bodyOf(t, `{"n":1.0,"big":1e21,"inf":1e400,"s":"a\u2028<b>é","half":" x \ud83d ","o":{"b":1,"a":2,"10":"x","2":"y","a":3},"l":[1.0,"x",null,{"z":[]}],"zero":0,"empty":"","no":null,"f":false}`)
	for key, want := range map[string]string{
		"n": `1`, "big": `1e+21`, "inf": `null`, "s": "\"a\u2028<b>é\"", "half": `" x \ud83d "`, "o": `{"2":"y","10":"x","b":1,"a":3}`, "l": `[1,"x",null,{"z":[]}]`,
		"zero": `0`, "empty": `""`, "no": `null`, "f": `false`, "missing": `null`,
	} {
		got, _ := member(body, key).MarshalJSON()
		if string(got) != want {
			t.Errorf("JSON of %s = %s, want %s", key, got, want)
		}
	}
	// g || fallback
	for key, want := range map[string]string{"n": `1`, "zero": `"d"`, "empty": `"d"`, "no": `"d"`, "f": `"d"`, "missing": `"d"`, "o": `{"2":"y","10":"x","b":1,"a":3}`, "l": `[1,"x",null,{"z":[]}]`, "inf": `null`} {
		got, _ := member(body, key).or(text("d")).MarshalJSON()
		if string(got) != want {
			t.Errorf("JSON of %s || \"d\" = %s, want %s", key, got, want)
		}
	}
	// name.trim(): half a pair stays in the answer and is the replacement
	// character for the database
	trimmed := member(body, "half").trimmed()
	if got, _ := trimmed.MarshalJSON(); string(got) != `"x \ud83d"` || trimmed.value.Str() != "x \ufffd" {
		t.Errorf("trimmed half pair: JSON %s, value %q", got, trimmed.value.Str())
	}
	if got, _ := constant(jsbind.Null).MarshalJSON(); string(got) != `null` {
		t.Errorf("JSON of null = %s", got)
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

// Whatever a body holds, a member of it is repeated as JSON that reads
// back as the same value, and making text of it either works or is the
// TypeError.
func FuzzGiven(f *testing.F) {
	for _, seed := range []string{`"text"`, `5`, `1.0`, `1e400`, `-0`, `true`, `null`, `[]`, `[1,[2],null,{"a":1}]`, `{"b":1,"a":2,"a":3,"10":0}`, `{"toString":1}`,
		`"\ud83d"`, `"\u0000"`, `" x "`, `12345678901234567890`, `[{"toString":[]}]`, `{"":""}`} {
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
		g := member(body, "v")
		out, err := g.MarshalJSON()
		if err != nil || !json.Valid(out) {
			t.Fatalf("member %s: JSON %q, %v", value, out, err)
		}
		// The same value again, as the body parser handed it over: bytes
		// that are no UTF-8 are replacement characters by then
		var sent, repeated any
		if json.Unmarshal(body.Get("v").Raw(), &sent) == nil && json.Unmarshal(out, &repeated) == nil {
			if !reflect.DeepEqual(sent, repeated) {
				t.Fatalf("member %s is repeated as %s", value, out)
			}
		}
		if either := g.or(text("fallback")); !either.value.Truthy() || !g.value.Truthy() && either.value.Str() != "fallback" {
			t.Fatalf("member %s: || gives a value that is not truthy", value)
		}
		text, err := asText(body.Get("v"))
		if err != nil && (err.Error() != "Cannot convert object to primitive value" || !strings.Contains(value, "toString")) {
			t.Fatalf("asText(%s) = %q, %v", value, text, err)
		}
		_ = turnedOff(g.value)
		if g.value.IsString() {
			trimmed := g.trimmed()
			if out, err := trimmed.MarshalJSON(); err != nil || !json.Valid(out) {
				t.Fatalf("trimmed %s: JSON %q, %v", value, out, err)
			}
			if trimmed.value.Str() != jsbind.Trim(g.value.Str()) {
				t.Fatalf("trimmed %s = %q", value, trimmed.value.Str())
			}
		}
	})
}

// Whatever a query string holds, reading the filters and the limit from
// it does not fail in another way than JavaScript did, and the id rule
// lets through digits only.
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
		for _, name := range []string{"device_id", "severity", "acknowledged", "status"} {
			v := queryValue(r, name)
			_ = v.JSON()
			if _, err := jsbind.Args(store.SQLite, 1, v); err != nil && v.IsString() {
				t.Fatalf("%s of %q is text and does not bind: %v", name, text, err)
			}
		}
		n, given, err := limit(r)
		if err != nil && err.Error() != "Cannot convert object to primitive value" {
			t.Fatalf("limit of %q: %v", text, err)
		}
		if given && !math.IsNaN(n) && !math.IsInf(n, 0) && n != math.Trunc(n) {
			t.Fatalf("limit of %q = %v is no whole number", text, n)
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
