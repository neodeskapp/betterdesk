package jsreq

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/middleware"
)

// vectors is testdata/query.json: what Express and JavaScript answered
// (testdata/nodeoracle/query.js).
type vectors struct {
	Queries []struct {
		Query  string          `json:"query"`
		Value  json.RawMessage `json:"value"`
		String *string         `json:"string"`
		Truthy bool            `json:"truthy"`
		Int10  *string         `json:"int10"`
		Int    *string         `json:"int"`
	} `json:"queries"`
	IDs []struct {
		Input string  `json:"input"`
		Safe  *string `json:"safe"`
		Error *string `json:"error"`
	} `json:"ids"`
	Values []struct {
		JSON          string  `json:"json"`
		String        string  `json:"string"`
		StringOrEmpty string  `json:"stringOrEmpty"`
		Truthy        bool    `json:"truthy"`
		Int10         *string `json:"int10"`
		Int           *string `json:"int"`
	} `json:"values"`
}

func loadVectors(t *testing.T) vectors {
	t.Helper()
	raw, err := os.ReadFile("testdata/query.json")
	if err != nil {
		t.Fatal(err)
	}
	var v vectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	if len(v.Queries) < 1000 || len(v.IDs) < 20 || len(v.Values) < 20 {
		t.Fatalf("testdata/query.json holds %d queries, %d ids and %d values", len(v.Queries), len(v.IDs), len(v.Values))
	}
	return v
}

func encode(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func intText(v any, radix int) *string {
	n, ok := ParseInt(v, radix)
	if !ok {
		return nil
	}
	s := numberString(n)
	return &s
}

func same(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func show(s *string) string {
	if s == nil {
		return "NaN"
	}
	return *s
}

func TestQueryMatchesExpress(t *testing.T) {
	for _, vec := range loadVectors(t).Queries {
		name := vec.Query
		if len(name) > 80 {
			name = name[:80] + "..."
		}
		got := QueryValue(vec.Query, "a")
		if vec.Value == nil {
			if got != nil {
				t.Errorf("%q: got %s, Express left the parameter out", name, encode(t, got))
			}
		} else {
			want, err := goapi.Parse(vec.Value)
			if err != nil {
				t.Fatal(err)
			}
			if encode(t, got) != encode(t, want) {
				t.Errorf("%q: got %s, Express gave %s", name, encode(t, got), encode(t, want))
				continue
			}
			if s := String(got); s != *vec.String {
				t.Errorf("%q: String = %q, JavaScript gave %q", name, s, *vec.String)
			}
		}
		if goapi.Truthy(got) != vec.Truthy {
			t.Errorf("%q: truthy = %v, JavaScript gave %v", name, goapi.Truthy(got), vec.Truthy)
		}
		if n := intText(got, 10); !same(n, vec.Int10) {
			t.Errorf("%q: parseInt(v, 10) = %s, JavaScript gave %s", name, show(n), show(vec.Int10))
		}
		if n := intText(got, 0); !same(n, vec.Int) {
			t.Errorf("%q: parseInt(v) = %s, JavaScript gave %s", name, show(n), show(vec.Int))
		}
	}
}

func TestQueryReadsTheRequest(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/x?limit=7&action=a&action=b", nil)
	if got := Query(r, "limit"); got != "7" {
		t.Errorf("limit = %v", got)
	}
	if got := encode(t, Query(r, "action")); got != `["a","b"]` {
		t.Errorf("action = %s", got)
	}
	if got := Query(r, "offset"); got != nil {
		t.Errorf("offset = %v", got)
	}
}

// FuzzQueryValue checks that no query string makes the reader panic or
// return a value of a kind it does not promise, and for queries without
// brackets that it agrees with the standard library: one value is text,
// several are a list.
func FuzzQueryValue(f *testing.F) {
	for _, seed := range []string{
		"a=1", "a=1&a=2", "a[]=1", "a[x]=1&a=2", "a%5B0%5D=x", "b=1&a=%E2%9C%93", "a=+x+", "a", "=x&a=", "a[[]]=1", "a[1001]=z&a[]=y",
		"a[a][b][c][d][e][f][g]=deep", "a=%E0%A4%A", "%61=1", "a=1&&&a=2", "a[__proto__]=1", "a]=1", "a[=1",
	} {
		f.Add(seed)
	}
	var kinds func(v any) bool
	kinds = func(v any) bool {
		switch t := v.(type) {
		case nil, string:
			return true
		case []any:
			for _, el := range t {
				if el == nil || !kinds(el) {
					return false
				}
			}
			return true
		case *goapi.Object:
			for _, key := range t.Keys() {
				if !kinds(t.Get(key)) {
					return false
				}
			}
			return t != nil
		}
		return false
	}
	f.Fuzz(func(t *testing.T, raw string) {
		got := QueryValue(raw, "a")
		if !kinds(got) {
			t.Fatalf("QueryValue(%q) = %#v", raw, got)
		}
		_ = String(got)
		_, _ = ParseInt(got, 0)
		_ = Stringify(got)

		lower := strings.ToLower(raw)
		// The reference reads bytes; a query with bytes outside ASCII next
		// to escapes is not text to both in the same way
		ascii := strings.IndexFunc(raw, func(r rune) bool { return r >= 0x80 }) < 0
		if !ascii || strings.ContainsAny(raw, "[]") || strings.Contains(lower, "%5b") || strings.Contains(lower, "%5d") || strings.Count(raw, "&") >= qsParameterLimit {
			return
		}
		parsed, err := url.ParseQuery(raw)
		if err != nil {
			return
		}
		for key, values := range parsed {
			if !utf8.ValidString(key) {
				return
			}
			for _, value := range values {
				if !utf8.ValidString(value) {
					return
				}
			}
		}
		var want any
		switch values := parsed["a"]; len(values) {
		case 0:
		case 1:
			want = values[0]
		default:
			list := make([]any, len(values))
			for i, value := range values {
				list[i] = value
			}
			want = list
		}
		if encode(t, got) != encode(t, want) {
			t.Errorf("QueryValue(%q) = %s, the standard library reads %s", raw, encode(t, got), encode(t, want))
		}
	})
}

func TestValuesMatchJavaScript(t *testing.T) {
	for _, vec := range loadVectors(t).Values {
		v, err := goapi.Parse([]byte(vec.JSON))
		if err != nil {
			t.Fatal(err)
		}
		if s := String(v); s != vec.String {
			t.Errorf("String(%s) = %q, JavaScript gave %q", vec.JSON, s, vec.String)
		}
		if s := StringOr(v, ""); s != vec.StringOrEmpty {
			t.Errorf("String(%s || '') = %q, JavaScript gave %q", vec.JSON, s, vec.StringOrEmpty)
		}
		if goapi.Truthy(v) != vec.Truthy {
			t.Errorf("truthy(%s) = %v", vec.JSON, goapi.Truthy(v))
		}
		// parseInt(null) is NaN and so is the port's answer for nil
		if n := intText(v, 10); !same(n, vec.Int10) {
			t.Errorf("parseInt(%s, 10) = %s, JavaScript gave %s", vec.JSON, show(n), show(vec.Int10))
		}
		if n := intText(v, 0); !same(n, vec.Int) {
			t.Errorf("parseInt(%s) = %s, JavaScript gave %s", vec.JSON, show(n), show(vec.Int))
		}
	}
}

func TestIntOr(t *testing.T) {
	for _, tc := range []struct {
		v     any
		radix int
		want  string
	}{
		{nil, 0, "50"}, {"", 0, "50"}, {"0", 0, "50"}, {"-0", 0, "50"}, {"abc", 0, "50"},
		{"7", 0, "7"}, {"-3", 0, "-3"}, {"0x1F", 0, "31"}, {"0x1F", 10, "50"}, {"12.9", 0, "12"},
		{"99999999999999999999999", 0, "1e+23"}, {[]any{"8", "9"}, 0, "8"},
	} {
		if got := IntOr(tc.v, tc.radix, 50); got != tc.want {
			t.Errorf("IntOr(%v, %d) = %q, want %q", tc.v, tc.radix, got, tc.want)
		}
	}
}

func TestSafeSegmentMatchesNode(t *testing.T) {
	for _, vec := range loadVectors(t).IDs {
		got, err := SafeSegment(vec.Input, "deviceId")
		switch {
		case vec.Error != nil:
			if err == nil || err.Error() != *vec.Error {
				t.Errorf("SafeSegment(%q) = %q, %v; Node threw %q", vec.Input, got, err, *vec.Error)
			}
		case err != nil || got != *vec.Safe:
			t.Errorf("SafeSegment(%q) = %q, %v; Node gave %q", vec.Input, got, err, *vec.Safe)
		}
	}
}

func TestBody(t *testing.T) {
	read := func(contentType, body string) any {
		var got any
		h := middleware.Bodies(middleware.BodyOptions{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got = Body(r)
		}))
		r := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(body))
		if contentType != "" {
			r.Header.Set("Content-Type", contentType)
		}
		h.ServeHTTP(httptest.NewRecorder(), r)
		return got
	}
	for _, tc := range []struct{ contentType, body, want, peer string }{
		{"application/json", `{"peer_id":900100001,"b":1.0,"b":2e0}`, `{"peer_id":900100001,"b":2}`, "900100001"},
		{"application/json", `[1,{"peer_id":"x"}]`, `[1,{"peer_id":"x"}]`, ""},
		{"application/json", ``, `{}`, ""},
		{"", `{"peer_id":"x"}`, `{}`, ""},
		{"application/x-www-form-urlencoded", `peer_id=abc&n=1`, `{"peer_id":"abc","n":"1"}`, "abc"},
		{"application/json", `{"peer_id":["a","b"]}`, `{"peer_id":["a","b"]}`, "a,b"},
		{"application/json", `{"peer_id":null}`, `{"peer_id":null}`, ""},
	} {
		got := read(tc.contentType, tc.body)
		if encode(t, got) != tc.want {
			t.Errorf("Body of %q = %s, want %s", tc.body, encode(t, got), tc.want)
		}
		if peer := StringOr(Prop(got, "peer_id"), ""); peer != tc.peer {
			t.Errorf("peer_id of %q = %q, want %q", tc.body, peer, tc.peer)
		}
	}
}

// fakeAPI answers the paths the proxy tests ask for.
func fakeAPI(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/api/peers/900100001":
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"id":"900100001","z":1,"a":2}`))
	case "/api/peers/missing":
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"Peer not found"}`))
	case "/api/peers/text":
		http.Error(w, "plain refusal", http.StatusConflict)
	case "/api/peers/empty":
		w.WriteHeader(http.StatusServiceUnavailable)
	case "/api/peers/silent":
		w.WriteHeader(http.StatusNoContent)
	case "/api/peers/echo":
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"method":"` + r.Method + `"}`))
	default:
		panic(http.ErrAbortHandler)
	}
}

func TestProxyToGo(t *testing.T) {
	client := goapi.New(http.HandlerFunc(fakeAPI))
	call := func(method string, path func() (string, error)) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		ProxyToGo(rec, httptest.NewRequest(http.MethodGet, "/x", nil), client, method, path)
		return rec
	}
	device := func(id string) func() (string, error) {
		return func() (string, error) {
			seg, err := SafeSegment(id, "deviceId")
			if err != nil {
				return "", err
			}
			return "/peers/" + seg, nil
		}
	}
	for _, tc := range []struct {
		name   string
		method string
		path   func() (string, error)
		status int
		body   string
	}{
		{"an answer keeps its status and key order", "GET", device("900100001"), 202, `{"id":"900100001","z":1,"a":2}`},
		{"an error answer is passed on", "GET", device("missing"), 404, `{"error":"Peer not found"}`},
		{"an error in plain text becomes a JSON string", "GET", device("text"), 409, `"plain refusal\n"`},
		{"an error without a body", "GET", device("empty"), 503, `{"error":"Go server unreachable"}`},
		{"an empty answer is an empty string", "GET", device("silent"), 204, ``},
		{"no answer", "GET", device("gone"), 500, `{"error":"Go server unreachable"}`},
		{"a refused part of the path", "GET", device("a/b"), 400, `{"error":"Invalid deviceId"}`},
		{"a path the client refuses", "GET", func() (string, error) { return "/peers/a\\b", nil }, 400, `{"error":"Invalid API path"}`},
		{"a path the client refuses for another reason", "GET", func() (string, error) { return "/peers/../x", nil }, 500, `{"error":"Go server unreachable"}`},
		{"the method is passed on", "DELETE", device("echo"), 200, `{"method":"DELETE"}`},
	} {
		rec := call(tc.method, tc.path)
		if rec.Code != tc.status || rec.Body.String() != tc.body {
			t.Errorf("%s: %d %s, want %d %s", tc.name, rec.Code, rec.Body.String(), tc.status, tc.body)
		}
	}
}
