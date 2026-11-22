package goapi

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestParseKeepsOrderAndTypes(t *testing.T) {
	v, err := Parse([]byte(` {"b":1,"a":{"z":null,"y":[1.5,"s",true,{}]},"b":2,"10":"x","2":"y","c":1e999} `))
	if err != nil {
		t.Fatal(err)
	}
	o, ok := v.(*Object)
	if !ok {
		t.Fatalf("got %T", v)
	}
	// Index keys first in numeric order, then the rest as first written; a
	// repeated key keeps its place and takes the last value; a number too
	// large for a float is Infinity, which encodes as null.
	if got, want := toJSON(t, o), `{"2":"y","10":"x","b":2,"a":{"z":null,"y":[1.5,"s",true,{}]},"c":null}`; got != want {
		t.Errorf("round trip = %s\n want       %s", got, want)
	}
	if n, ok := o.Number("b"); !ok || n != 2 {
		t.Errorf("b = %v", o.Get("b"))
	}
	if !o.Has("a") || !o.Object("a").Has("z") || o.Object("a").Get("z") != nil || o.Object("a").Has("missing") {
		t.Error("null and missing are not told apart")
	}
	if c, _ := o.Number("c"); !math.IsInf(c, 1) {
		t.Errorf("c = %v", c)
	}

	for _, bad := range []string{``, `{`, `{} x`, `[1,]`, `{'a':1}`, `1 2`, `undefined`} {
		if _, err := Parse([]byte(bad)); err == nil {
			t.Errorf("Parse(%q) gave no error", bad)
		}
	}
}

func TestObjectBehavesLikeAJavaScriptObject(t *testing.T) {
	o := NewObject("success", true, "data", nil)
	o.Set("error", "x").Set("success", false)
	if got := strings.Join(o.Keys(), ","); got != "success,data,error" {
		t.Errorf("keys = %s", got)
	}
	if o.Bool("success") || !o.Has("data") || o.Str("error") != "x" || o.Str("data") != "" || o.Len() != 3 {
		t.Errorf("object = %s", toJSON(t, o))
	}
	clone := o.Clone().Set("extra", 1.0)
	clone.Delete("data")
	clone.Delete("no such key")
	if toJSON(t, o) != `{"success":false,"data":null,"error":"x"}` || toJSON(t, clone) != `{"success":false,"error":"x","extra":1}` {
		t.Errorf("after Clone: %s and %s", toJSON(t, o), toJSON(t, clone))
	}

	var missing *Object
	if missing.Get("a") != nil || missing.Has("a") || missing.Len() != 0 || missing.Keys() != nil || missing.Bool("a") || missing.Str("a") != "" || missing.Object("a") != nil {
		t.Error("a nil object does not read as empty")
	}
	missing.Delete("a")
	if toJSON(t, missing.Clone()) != `{}` {
		t.Error("the clone of a nil object is not empty")
	}

	// Through encoding/json a nil object is null and a list of objects a list.
	out, err := json.Marshal(map[string]any{"none": missing, "list": []*Object{NewObject("a", 1.0), nil}, "empty": []*Object{}})
	if err != nil || string(out) != `{"empty":[],"list":[{"a":1},null],"none":null}` {
		t.Errorf("json.Marshal = %s, %v", out, err)
	}

	var decoded Object
	if err := json.Unmarshal([]byte(`{"z":1,"a":[{"k":"v"}]}`), &decoded); err != nil || toJSON(t, &decoded) != `{"z":1,"a":[{"k":"v"}]}` {
		t.Errorf("json.Unmarshal: %v, %s", err, toJSON(t, &decoded))
	}
	if err := json.Unmarshal([]byte(`[1]`), &decoded); err == nil {
		t.Error("a list was decoded into an object")
	}
}

func TestEncodeMatchesJSONStringify(t *testing.T) {
	cases := []struct {
		v    any
		want string
	}{
		{NewObject("html", "<a href='x'>&</a>"), `{"html":"<a href='x'>&</a>"}`},
		{[]any{1.0, 1.5, 1e21, 1e-7, 123456789012345680000.0, -0.5, 100.0}, `[1,1.5,1e+21,1e-7,123456789012345680000,-0.5,100]`},
		{[]any{math.NaN(), math.Inf(1), math.Inf(-1)}, `[null,null,null]`},
		{[]any{}, `[]`},
		{[]any(nil), `null`},
		{[]*Object(nil), `null`},
		{NewObject(), `{}`},
		{"a\"b\\c\n", `"a\"b\\c\n"`},
		{map[string]any{"b": 1, "a": 2}, `{"a":2,"b":1}`},
	}
	for _, c := range cases {
		if got := toJSON(t, c.v); got != c.want {
			t.Errorf("encode(%v) = %s, want %s", c.v, got, c.want)
		}
	}
}

func TestFromGo(t *testing.T) {
	v, err := FromGo(struct {
		B int      `json:"b"`
		A []string `json:"a"`
	}{1, []string{"x"}})
	if err != nil || toJSON(t, v) != `{"b":1,"a":["x"]}` {
		t.Errorf("FromGo = %s, %v", toJSON(t, v), err)
	}
	if _, err := FromGo(make(chan int)); err == nil {
		t.Error("FromGo took a channel")
	}
}

func TestTruthy(t *testing.T) {
	var nilObject *Object
	truthy := []any{true, 1.0, -1.0, "0", "false", " ", []any{}, NewObject(), 1, int64(2)}
	falsy := []any{nil, false, 0.0, math.NaN(), "", nilObject, 0, int64(0)}
	for _, v := range truthy {
		if !Truthy(v) {
			t.Errorf("Truthy(%#v) = false", v)
		}
	}
	for _, v := range falsy {
		if Truthy(v) {
			t.Errorf("Truthy(%#v) = true", v)
		}
	}
}

func TestDig(t *testing.T) {
	v, _ := Parse([]byte(`{"data":{"snapshots":{"cpu":{"data":[10,{"x":"y"}]}},"n":null}}`))
	if got := Dig(v, "data", "snapshots", "cpu", "data", 1, "x"); got != "y" {
		t.Errorf("Dig = %v", got)
	}
	for _, path := range [][]any{
		{"data", "missing", "x"}, {"data", "n", "x"}, {"data", "snapshots", "cpu", "data", 5}, {"data", "snapshots", "cpu", "data", -1},
		{"data", 0}, {"data", "snapshots", "cpu", "data", "x"}, {"data", 1.5},
	} {
		if got := Dig(v, path...); got != nil {
			t.Errorf("Dig(%v) = %v, want nil", path, got)
		}
	}
	if got := Dig([]*Object{NewObject("a", 1.0)}, 0, "a"); got != 1.0 {
		t.Errorf("Dig into a list of objects = %v", got)
	}
}

func TestJSString(t *testing.T) {
	cases := []struct {
		v    any
		want string
	}{
		{"x", "x"}, {nil, "null"}, {true, "true"}, {3389.0, "3389"}, {1.5, "1.5"}, {0.0, "0"}, {math.Copysign(0, -1), "0"},
		{1e21, "1e+21"}, {math.NaN(), "NaN"}, {math.Inf(1), "Infinity"}, {math.Inf(-1), "-Infinity"},
		{[]any{"a", nil, 2.0, []any{"b"}}, "a,,2,b"}, {NewObject("a", 1.0), "[object Object]"}, {7, "7"}, {int64(8), "8"},
	}
	for _, c := range cases {
		if got := jsString(c.v); got != c.want {
			t.Errorf("jsString(%#v) = %q, want %q", c.v, got, c.want)
		}
	}
}

func TestJSNumber(t *testing.T) {
	cases := []struct {
		v    any
		want float64
	}{
		{nil, 0}, {true, 1}, {false, 0}, {12.5, 12.5}, {"", 0}, {"  ", 0}, {" 12 ", 12}, {"1e3", 1000}, {"-.5", -0.5}, {"5.", 5},
		{"0x1F", 31}, {"0b101", 5}, {"0o17", 15}, {"Infinity", math.Inf(1)}, {"-Infinity", math.Inf(-1)}, {3, 3}, {int64(4), 4},
	}
	for _, c := range cases {
		if got := jsNumber(c.v); got != c.want {
			t.Errorf("jsNumber(%#v) = %v, want %v", c.v, got, c.want)
		}
	}
	for _, v := range []any{"abc", "12px", "1_000", "0x", "inf", "nan", "1e", "--1", ".", NewObject(), []any{}} {
		if got := jsNumber(v); !math.IsNaN(got) {
			t.Errorf("jsNumber(%#v) = %v, want NaN", v, got)
		}
	}
}

func TestJSTrim(t *testing.T) {
	if got := jsTrim("\t\n   x y　 \r"); got != "x y" {
		t.Errorf("jsTrim = %q", got)
	}
	if got := jsTrim("​x"); got != "​x" {
		t.Errorf("jsTrim removed a zero width space: %q", got)
	}
}

func TestEncodeURIComponent(t *testing.T) {
	cases := []struct{ in, want string }{
		{"abc-_.!~*'()XYZ019", "abc-_.!~*'()XYZ019"},
		{"a/b c", "a%2Fb%20c"},
		{"a+b&c=d?e#f", "a%2Bb%26c%3Dd%3Fe%23f"},
		{"ä€", "%C3%A4%E2%82%AC"},
		{"..", ".."},
		{"100%", "100%25"},
		{":@$,;", "%3A%40%24%2C%3B"},
	}
	for _, c := range cases {
		if got := EncodeURIComponent(c.in); got != c.want {
			t.Errorf("EncodeURIComponent(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSafeID(t *testing.T) {
	for _, ok := range []string{"PEERAAA1", " 12 ", "a.b_c:d@e%f+g-h", strings.Repeat("a", 256)} {
		got, e := safeID(ok, "peerId")
		if e != nil || got != strings.TrimSpace(ok) {
			t.Errorf("safeID(%q) = %q, %v", ok, got, e)
		}
	}
	for _, bad := range []string{"", "  ", ".", "..", "a b", "a/b", "a?b", "ä", "a\\b", strings.Repeat("a", 257)} {
		if _, e := safeID(bad, "peerId"); e == nil || e.Message != "Invalid peerId" || e.Response != nil {
			t.Errorf("safeID(%q) = %+v, want Invalid peerId", bad, e)
		}
	}
}

func TestSpread(t *testing.T) {
	src := NewObject("a", 1.0)
	cases := []struct {
		v    any
		want string
	}{
		{src, `{"a":1}`}, {nil, `{}`}, {5.0, `{}`}, {true, `{}`}, {"ab", `{"0":"a","1":"b"}`}, {[]any{"x", nil}, `{"0":"x","1":null}`},
	}
	for _, c := range cases {
		if got := toJSON(t, spread(c.v)); got != c.want {
			t.Errorf("spread(%#v) = %s, want %s", c.v, got, c.want)
		}
	}
	spread(src).Set("b", 2.0)
	if src.Has("b") {
		t.Error("spread shares the keys of its source")
	}
}

func TestWrapAndFail(t *testing.T) {
	parse := func(s string) any {
		v, err := Parse([]byte(s))
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	wraps := []struct{ data, want string }{
		{`{"a":1}`, `{"success":true,"data":{"a":1}}`},
		{`{"error":"x","a":1}`, `{"success":false,"error":"x"}`},
		{`{"error":null}`, `{"success":false,"error":null}`},
		{`["error"]`, `{"success":true,"data":["error"]}`},
		{`"error"`, `{"success":true,"data":"error"}`},
		{`null`, `{"success":true,"data":null}`},
	}
	for _, c := range wraps {
		if got := toJSON(t, wrap(parse(c.data))); got != c.want {
			t.Errorf("wrap(%s) = %s, want %s", c.data, got, c.want)
		}
	}

	answered := func(status int, body string) *Error {
		return &Error{Message: "Request failed with status code 400", Response: &Response{Status: status, Body: []byte(body)}}
	}
	fails := []struct {
		name string
		kind failKind
		e    *Error
		want string
	}{
		{"f1 ignores the body", f1, answered(400, `{"error":"x"}`), `{"success":false,"error":"Request failed with status code 400"}`},
		{"f2 takes the error of the body", f2, answered(400, `{"error":"x"}`), `{"success":false,"error":"x"}`},
		{"f2 with a body without error", f2, answered(404, `{"password_set":false}`), `{"success":true,"data":{"password_set":false}}`},
		{"f2 with plain text", f2, answered(400, "Invalid JSON\n"), `{"success":true,"data":"Invalid JSON\n"}`},
		{"f2 with an empty body", f2, answered(400, ""), `{"success":false,"error":"Request failed with status code 400"}`},
		{"f2 with a falsy body", f2, answered(400, "0"), `{"success":false,"error":"Request failed with status code 400"}`},
		{"f2 without an answer", f2, &Error{Message: "timeout of 5000ms exceeded"}, `{"success":false,"error":"timeout of 5000ms exceeded"}`},
		{"f3 adds the status", f3, answered(409, `{"error":"capability_required"}`), `{"success":false,"error":"capability_required","status":409}`},
		{"f3 with an empty body", f3, answered(500, ""), `{"success":false,"error":"Request failed with status code 400","status":500}`},
		{"f3 without an answer", f3, &Error{Message: "socket hang up"}, `{"success":false,"error":"socket hang up","status":502}`},
	}
	for _, c := range fails {
		if got := toJSON(t, fail(c.kind, c.e)); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}
