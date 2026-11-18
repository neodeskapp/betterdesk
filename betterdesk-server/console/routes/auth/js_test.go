package auth

import (
	"encoding/json"
	"math"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/middleware"
)

// value reads one JSON value as a request body would hold it.
func value(t testing.TB, text string) middleware.Value {
	t.Helper()
	r, err := http.NewRequest("POST", "/", strings.NewReader(`{"v":`+text+`}`))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Content-Type", "application/json")
	var got middleware.Value
	handler := middleware.Bodies(middleware.BodyOptions{})(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = middleware.Body(r).Get("v")
	}))
	handler.ServeHTTP(discard{}, r)
	return got
}

type discard struct{}

func (discard) Header() http.Header         { return http.Header{} }
func (discard) Write(p []byte) (int, error) { return len(p), nil }
func (discard) WriteHeader(int)             {}

func TestJSString(t *testing.T) {
	for text, want := range map[string]string{
		`"abc"`: "abc", `""`: "", `12345`: "12345", `1.50`: "1.5", `-0`: "0", `1e21`: "1e+21", `1e999`: "Infinity", `-1e999`: "-Infinity",
		`true`: "true", `false`: "false", `null`: "null",
		`[]`: "", `["a",null,["b",1],{}]`: "a,,b,1,[object Object]", `[null]`: "", `{}`: "[object Object]", `{"a":1}`: "[object Object]",
	} {
		if got := jsString(value(t, text)); got != want {
			t.Errorf("String(%s) = %q, want %q", text, got, want)
		}
	}
	if got := jsString(middleware.Value{}); got != "undefined" {
		t.Errorf("String(undefined) = %q", got)
	}
}

func TestJSNumberAndLength(t *testing.T) {
	nan := math.NaN()
	for text, want := range map[string]float64{
		`null`: 0, `true`: 1, `false`: 0, `7`: 7, `"7"`: 7, `" 7.5 "`: 7.5, `""`: 0, `"  "`: 0, `"0x10"`: 16, `"0b11"`: 3, `"0o17"`: 15,
		`"1e3"`: 1000, `".5"`: 0.5, `"+5"`: 5, `"Infinity"`: math.Inf(1), `"-Infinity"`: math.Inf(-1),
		`"abc"`: nan, `"7px"`: nan, `"1_000"`: nan, `"inf"`: nan, `"nan"`: nan, `"0x"`: nan, `"0x1p3"`: nan, `"0x-1"`: nan,
		`[]`: 0, `[5]`: 5, `["6"]`: 6, `[1,2]`: nan, `{}`: nan,
	} {
		if got := jsNumber(value(t, text)); got != want && !(math.IsNaN(got) && math.IsNaN(want)) {
			t.Errorf("Number(%s) = %v, want %v", text, got, want)
		}
	}
	for text, want := range map[string]float64{
		`"abc"`: 3, `""`: 0, `"😀"`: 2, `"é"`: 1, `[]`: 0, `[1,2,3]`: 3,
		`{"length":9}`: 9, `{"length":"12"}`: 12, `{"length":null}`: 0, `{"length":[4]}`: 4, `{"length":"x"}`: nan, `{}`: nan,
		`5`: nan, `true`: nan, `null`: nan,
	} {
		if got := jsLength(value(t, text)); got != want && !(math.IsNaN(got) && math.IsNaN(want)) {
			t.Errorf("(%s).length = %v, want %v", text, got, want)
		}
	}
	long := `"` + strings.Repeat("a", 129) + `"`
	for text, want := range map[string]bool{
		long: true, `"` + strings.Repeat("a", 128) + `"`: false, `{"length":128.5}`: true, `{"length":128}`: false, `7`: false, `{"length":"x"}`: false,
	} {
		if got := tooLong(value(t, text)); got != want {
			t.Errorf("tooLong(%.20s) = %v, want %v", text, got, want)
		}
	}
	for text, want := range map[string]bool{
		`"123456"`: true, `"12345"`: false, `"😀😀😀"`: true, `[1,2,3,4,5,6]`: true, `[1]`: false,
		`{"length":6}`: true, `{"length":6.0}`: true, `{"length":"6"}`: false, `{"length":[6]}`: false, `123456`: false, `true`: false,
	} {
		if got := hasLength(value(t, text), 6); got != want {
			t.Errorf("hasLength(%s, 6) = %v, want %v", text, got, want)
		}
	}
}

func TestStrictEqual(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{`"a"`, `"a"`, true}, {`"a"`, `"b"`, false}, {`1`, `1.0`, true}, {`1`, `"1"`, false}, {`true`, `true`, true}, {`true`, `false`, false},
		{`null`, `null`, true}, {`null`, `0`, false}, {`[]`, `[]`, false}, {`{}`, `{}`, false}, {`["a"]`, `["a"]`, false},
	} {
		if got := strictEqual(value(t, c.a), value(t, c.b)); got != c.want {
			t.Errorf("%s === %s is %v, want %v", c.a, c.b, got, c.want)
		}
	}
	if !strictEqual(middleware.Value{}, middleware.Value{}) || strictEqual(middleware.Value{}, value(t, `null`)) {
		t.Error("undefined is equal to undefined only")
	}
}

// The answers of "...".toLowerCase() in Node 22. The lookup vectors
// (testdata/lookup_*.json) hold many more.
func TestJSLower(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"Alice", "alice"}, {"ÄÖÜ", "äöü"}, {"İstanbul", "i̇stanbul"},
		{"ΟΔΟΣ", "οδος"}, {"ΣΑ", "σα"}, {"Σ", "σ"}, {"ΑΣ ΒΣ", "ας βς"}, {"ΑΣΑ", "ασα"},
		{"ΑΣ.", "ας."}, {"ΑΣ.Α", "ασ.α"}, {"Α.Σ", "α.ς"}, {"ΆΣ", "άς"}, {"ΣΣ", "σς"},
		{"1Σ", "1σ"}, {"K", "k"}, {"ẞ", "ß"},
	} {
		if got := jsLower(c.in); got != c.want {
			t.Errorf("jsLower(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCutUTF16(t *testing.T) {
	for _, c := range []struct {
		s    string
		n    int
		want string
	}{
		{"abcdef", 3, "abc"}, {"abc", 3, "abc"}, {"ab", 3, "ab"}, {"ééé", 2, "éé"}, {"a\U0001F600b", 3, "a\U0001F600"}, {"", 5, ""},
	} {
		if got := cutUTF16(c.s, c.n); got != c.want {
			t.Errorf("cutUTF16(%q, %d) = %q, want %q", c.s, c.n, got, c.want)
		}
	}
	if utf16Length("a\U0001F600é") != 4 {
		t.Error("utf16Length counts a character outside the BMP as two")
	}
	// A cut in the middle of a pair leaves the half as a replacement
	if got := cutUTF16("\U0001F600", 1); got != "�" {
		t.Errorf("half a pair: %q", got)
	}
}

func TestQueryText(t *testing.T) {
	for query, want := range map[string]string{
		"code=abc":             "abc",
		"a=1&code=a+b%20c&b=2": "a b c",
		"code=%E2%9C%93":       "✓",
		"code=":                "",
		"code":                 "",
		"c%6Fde=x":             "x",
		"code=%ZZ":             "%ZZ",
		"code=%FF":             "%FF",
		"&&code=x&":            "x",
		"code=a=b":             "a=b",
	} {
		r := &http.Request{URL: &url.URL{RawQuery: query}}
		if got, ok := queryText(r, "code"); !ok || got != want {
			t.Errorf("%q: %q %v, want %q", query, got, ok, want)
		}
	}
	for _, query := range []string{"", "other=1", "code=a&code=b", "code[]=a", "code[x]=a", "code=a&code[]=b", "code%5B%5D=a", "codex=1"} {
		r := &http.Request{URL: &url.URL{RawQuery: query}}
		if got, ok := queryText(r, "code"); ok {
			t.Errorf("%q: text %q, want none", query, got)
		}
	}
}

func TestCallbackQuery(t *testing.T) {
	r := &http.Request{URL: &url.URL{RawQuery: "code=a+b&state=s&code=c&x%5B%5D=1&&flag"}}
	got := callbackQuery(r)
	want := url.Values{"code": {"a b", "c"}, "state": {"s"}, "x[]": {"1"}, "flag": {""}}
	if got.Encode() != want.Encode() {
		t.Errorf("callbackQuery = %v, want %v", got, want)
	}
}

func TestSpread(t *testing.T) {
	object := goapi.NewObject("enabled", true, "name", "x")
	for name, c := range map[string]struct {
		in   any
		want string
	}{
		"an object":  {object, `{"enabled":true,"name":"x"}`},
		"a list":     {[]any{"a", 2.0}, `{"0":"a","1":2}`},
		"a text":     {"hé", `{"0":"h","1":"é"}`},
		"nothing":    {nil, `{}`},
		"a number":   {5.0, `{}`},
		"no object":  {(*goapi.Object)(nil), `{}`},
		"true":       {true, `{}`},
		"empty list": {[]any{}, `{}`},
	} {
		raw, err := json.Marshal(spread(c.in))
		if err != nil || string(raw) != c.want {
			t.Errorf("%s: %s %v, want %s", name, raw, err, c.want)
		}
	}
	// The copy is the caller's: a change does not reach the original
	spread(object).Set("local_bypass", true)
	if object.Has("local_bypass") {
		t.Error("spread hands out the object itself")
	}
}

func TestStoredJSON(t *testing.T) {
	for text, want := range map[string]bool{
		``: false, `null`: false, `false`: false, `""`: false, `0`: false, `-0`: false, `0.0`: false, ` null `: false,
		`true`: true, `"0"`: true, `" "`: true, `7`: true, `-1`: true, `0.5`: true, `{}`: true, `[]`: true, `"false"`: true,
	} {
		if got := jsonTruthy(json.RawMessage(text)); got != want {
			t.Errorf("truth of %q = %v, want %v", text, got, want)
		}
	}
	for text, want := range map[string]int64{
		`7`: 7, `"7"`: 7, `9007199254740993`: 9007199254740993, `"9007199254740993"`: 9007199254740993,
		`7.5`: 0, `"x"`: 0, `null`: 0, `true`: 0, `{}`: 0, ``: 0, `-3`: -3,
	} {
		if got := jsonInt(json.RawMessage(text)); got != want {
			t.Errorf("id of %q = %d, want %d", text, got, want)
		}
	}
}

func TestPendingAccount(t *testing.T) {
	who := pendingAccount(json.RawMessage(`{"id":"12","username":"alice","role":"admin","preferred_language":"de","totpRequired":true,"goPartialToken":null}`))
	if who.id != 12 || string(who.ID) != `"12"` || who.username != "alice" || who.role != "admin" || who.language != "de" ||
		string(who.Username) != `"alice"` || string(who.Role) != `"admin"` || string(who.PreferredLanguage) != `"de"` {
		t.Errorf("account: %+v", who)
	}
	// A language that counts as none is null, and missing fields stay out
	for _, text := range []string{`{"id":3,"preferred_language":""}`, `{"id":3,"preferred_language":null}`, `{"id":3}`, `{"id":3,"preferred_language":0}`} {
		who = pendingAccount(json.RawMessage(text))
		if who.id != 3 || string(who.PreferredLanguage) != "null" || who.Username != nil || who.Role != nil || who.language != "" {
			t.Errorf("%s: %+v", text, who)
		}
	}
	// What is no object has no fields
	for _, text := range []string{`"alice"`, `[1]`, `7`, `true`} {
		if who = pendingAccount(json.RawMessage(text)); who.id != 0 || who.ID != nil {
			t.Errorf("%s: %+v", text, who)
		}
	}
}

func FuzzQueryText(f *testing.F) {
	for _, seed := range []string{"code=abc", "code=a&code=b", "code[]=1", "code=%E2%9C%93", "code=%ZZ&x", "%63ode=1", "a=b&c", "code=+", "=&=&"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, query string) {
		r := &http.Request{URL: &url.URL{RawQuery: query}}
		text, ok := queryText(r, "code")
		if !ok && text != "" {
			t.Errorf("%q: text %q without being one", query, text)
		}
		// A query whose escapes are all good decodes to valid text
		if ok && utf8.ValidString(query) && !utf8.ValidString(text) {
			t.Errorf("%q: decoded to invalid text %q", query, text)
		}
		// One plain pair is always its value
		if !strings.ContainsAny(query, "&=[%+") && query != "" {
			single := &http.Request{URL: &url.URL{RawQuery: "code=" + query}}
			if got, ok := queryText(single, "code"); !ok || got != query {
				t.Errorf("code=%q: %q %v", query, got, ok)
			}
		}
		for key, values := range callbackQuery(r) {
			if len(values) == 0 {
				t.Errorf("%q: key %q without a value", query, key)
			}
		}
	})
}

func FuzzTextNumber(f *testing.F) {
	for _, seed := range []string{"", " 7 ", "0x10", "1e3", "Infinity", "-Infinity", "nan", "1_0", "0b102", ".", "+", "0x", "١٢٣", "1e", "0o8"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, text string) {
		got := textNumber(text)
		trimmed := jsTrim(text)
		switch {
		case trimmed == "":
			if got != 0 {
				t.Errorf("Number(%q) = %v, want 0", text, got)
			}
		case strings.Trim(trimmed, "0123456789") == "" && len(trimmed) < 15:
			var want float64
			for _, c := range trimmed {
				want = want*10 + float64(c-'0')
			}
			if got != want {
				t.Errorf("Number(%q) = %v, want %v", text, got, want)
			}
		case strings.ContainsAny(trimmed, "_ghijklmnpqrstuvwyzGHIJKLMNPQRSTUVWYZ ") && trimmed != "Infinity" && trimmed != "+Infinity" && trimmed != "-Infinity":
			if !math.IsNaN(got) {
				t.Errorf("Number(%q) = %v, want NaN", text, got)
			}
		}
	})
}

func FuzzPendingAccount(f *testing.F) {
	for _, seed := range []string{`{"id":7,"username":"a","role":"admin","preferred_language":null}`, `{"id":"7"}`, `[]`, `null`, `{"id":{}}`, `{"username":5}`, ``} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, text string) {
		who := pendingAccount(json.RawMessage(text))
		if who.PreferredLanguage == nil {
			t.Errorf("%q: no preferred language at all", text)
		}
		if who.ID == nil && who.id != 0 {
			t.Errorf("%q: an id %d out of nothing", text, who.id)
		}
		_ = jsonTruthy(json.RawMessage(text))
		_ = jsonInt(json.RawMessage(text))
	})
}
