package cdapstudio

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestParseAndStringify(t *testing.T) {
	cases := []struct{ in, out string }{
		{`{}`, `{}`},
		{` [ ] `, `[]`},
		{`{"b":1,"a":2,"b":3}`, `{"b":3,"a":2}`},
		{`{"x":1,"10":2,"2":3,"01":4,"-1":5,"4294967294":6,"4294967295":7}`, `{"2":3,"10":2,"4294967294":6,"x":1,"01":4,"-1":5,"4294967295":7}`},
		{`[1.50, 1e3, 1E-7, -0, 0.000001, 1e21, 123456789012345678901234567890, 1e999, -1e999, 5e-324, 0.1, 100, 1e20]`,
			`[1.5,1000,1e-7,0,0.000001,1e+21,1.2345678901234568e+29,null,null,5e-324,0.1,100,100000000000000000000]`},
		{`"a\u00e9\ud83d\ude00\ud83d \ude00x\u2028\u0007\/<>&\"\\\b\f\n\r\t\u007f"`, "\"a\u00e9\U0001F600\\ud83d \\ude00x\u2028\\u0007/<>&\\\"\\\\\\b\\f\\n\\r\\t\u007f\""},
		{`"\uD83D\uDE00"`, "\"\U0001F600\""},
		{`"\ud83d\u0041"`, `"\ud83dA"`},
		{`{"__proto__":{"a":1},"constructor":null,"toString":true}`, `{"__proto__":{"a":1},"constructor":null,"toString":true}`},
		{`true`, `true`}, {`false`, `false`}, {`null`, `null`}, {`-0.0`, `0`}, {` "x" `, `"x"`},
	}
	for _, c := range cases {
		v, err := parseJSON(c.in)
		if err != nil {
			t.Errorf("parseJSON(%s): %v", c.in, err)
			continue
		}
		if got, _ := stringify(v, ""); got != c.out {
			t.Errorf("%s was written as %s, want %s", c.in, got, c.out)
		}
	}
	for _, bad := range []string{
		``, ` `, `{`, `[1,]`, `{"a":1,}`, `{a:1}`, `{'a':1}`, `[01]`, `[.5]`, `[1.]`, `[+1]`, `[1e]`, `[-]`, `NaN`, `Infinity`, `undefined`,
		`{} {}`, `[1 2]`, `{"a" 1}`, `{"a":}`, "[\"a\tb\"]", `["\x41"]`, `["\u12"]`, `["\u+123"]`, `["\u12g4"]`, `"abc`, "\ufeff{}", `tru`, `nul`, `// x`, `[1] x`, "[\u00a0]", "{}\u2028",
	} {
		if v, err := parseJSON(bad); err == nil {
			t.Errorf("parseJSON(%q) = %v, want an error", bad, v)
		}
	}
}

func TestParseDepth(t *testing.T) {
	deep := func(n int) string { return strings.Repeat("[", n) + strings.Repeat("]", n) }
	if _, err := parseJSON(deep(maxDepth)); err != nil {
		t.Errorf("%d levels: %v", maxDepth, err)
	}
	if _, err := parseJSON(deep(maxDepth + 1)); err == nil {
		t.Errorf("%d levels were read", maxDepth+1)
	}
	objects := strings.Repeat(`{"a":`, maxDepth+1) + "1" + strings.Repeat("}", maxDepth+1)
	if _, err := parseJSON(objects); err == nil {
		t.Errorf("%d levels of objects were read", maxDepth+1)
	}
}

func TestStringifyIndent(t *testing.T) {
	v, err := parseJSON(`{"a":[],"b":{},"c":[1,[2,{"d":null}]],"e":"x"}`)
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"a\": [],\n  \"b\": {},\n  \"c\": [\n    1,\n    [\n      2,\n      {\n        \"d\": null\n      }\n    ]\n  ],\n  \"e\": \"x\"\n}"
	if got, _ := stringify(v, "  "); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	// A member that is not there is left out, also when it is the only one
	o := newObject()
	o.set("gone", missing)
	if got, _ := stringify(o, "  "); got != "{}" {
		t.Errorf("an object of missing members: %s", got)
	}
	o.set("here", []any{missing, 1.0})
	if got, _ := stringify(o, ""); got != `{"here":[null,1]}` {
		t.Errorf("a missing list element: %s", got)
	}
	if _, ok := stringify(missing, ""); ok {
		t.Error("a missing value has a text")
	}
}

func TestToTextAndTruthy(t *testing.T) {
	cases := []struct {
		in     string
		text   string
		ok     bool
		truthy bool
	}{
		{`null`, "null", true, false},
		{`true`, "true", true, true},
		{`false`, "false", true, false},
		{`0`, "0", true, false},
		{`-0`, "0", true, false},
		{`1.50`, "1.5", true, true},
		{`1e21`, "1e+21", true, true},
		{`1e999`, "Infinity", true, true},
		{`-1e999`, "-Infinity", true, true},
		{`""`, "", true, false},
		{`"0"`, "0", true, true},
		{`[]`, "", true, true},
		{`[1,null,"a",[2,[3]],{}]`, "1,,a,2,3,[object Object]", true, true},
		{`{}`, "[object Object]", true, true},
		{`{"toString":1}`, "", false, true},
		{`[1,{"toString":null}]`, "", false, true},
		{`{"valueOf":1}`, "[object Object]", true, true},
	}
	for _, c := range cases {
		v, err := parseJSON(c.in)
		if err != nil {
			t.Fatal(err)
		}
		text, ok := toText(v)
		if text != c.text || ok != c.ok {
			t.Errorf("toText(%s) = %q, %v, want %q, %v", c.in, text, ok, c.text, c.ok)
		}
		if truthy(v) != c.truthy {
			t.Errorf("truthy(%s) = %v", c.in, truthy(v))
		}
	}
	if text, ok := toText(missing); text != "undefined" || !ok || truthy(missing) {
		t.Errorf("a missing value: %q %v", text, ok)
	}
}

func TestUnitsAndProperty(t *testing.T) {
	half, _ := parseJSON(`"a\ud83d\ude00\ud83db"`)
	if n := units(half.(string)); n != 5 {
		t.Errorf("units = %d, want 5", n)
	}
	list, _ := parseJSON(`[1,2,3]`)
	obj, _ := parseJSON(`{"length":"x","id":7}`)
	if property(list, "length") != 3.0 || property(half, "length") != 5.0 || property(obj, "length") != "x" || property(obj, "id") != 7.0 {
		t.Error("property: length or member is wrong")
	}
	for _, v := range []any{list, half, obj, 5.0, true} {
		if property(v, "nodes") != missing {
			t.Errorf("%v has a member nodes", v)
		}
	}
	if property(5.0, "length") != missing || property(true, "length") != missing {
		t.Error("a number or a flag has a length")
	}
}

// FuzzParseJSON compares the reader with the standard library's: both
// take the same texts, as long as they are not nested deeper than either
// allows. What was read is written out as JSON that reads back the same.
func FuzzParseJSON(f *testing.F) {
	for _, seed := range []string{
		`{"nodes":[{"id":"n1","type":"log"}],"wires":[]}`, `[1e3,-0,1.5,"a\ud83dx",null,true]`, `{"a":{"a":{"a":[[[]]]}}}`,
		`"\u00e9\ud83d\ude00"`, `[01]`, `{"a":1,}`, "\ufeff{}", `{"1":1,"0":0,"x":2,"1":3}`, ` 5 `, `"`,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, text string) {
		if !utf8.ValidString(text) || strings.Count(text, "[")+strings.Count(text, "{") > 5000 {
			return
		}
		v, err := parseJSON(text)
		if valid := json.Valid([]byte(text)); valid != (err == nil) {
			t.Fatalf("%q: read: %v, the standard library says valid: %v", text, err, valid)
		}
		if err != nil {
			return
		}
		for _, indent := range []string{"", "  "} {
			out, ok := stringify(v, indent)
			if !ok {
				t.Fatalf("%q has no text", text)
			}
			again, err := parseJSON(out)
			if err != nil {
				t.Fatalf("%q was written as %q, which is not JSON: %v", text, out, err)
			}
			if twice, _ := stringify(again, indent); twice != out {
				t.Fatalf("%q was written as %q and then as %q", text, out, twice)
			}
		}
		toText(v)
	})
}

// TestIndentedLen: the counted length is the length stringify writes, at
// any depth.
func TestIndentedLen(t *testing.T) {
	for _, text := range []string{`0`, `"a\"b\u00e9\n"`, `[]`, `{}`, `[1,[2,[3,{}]],{"a":[],"b":{"c":null}}]`, `{"x":{"y":[true,false,1e21,-0.5]}}`, `[[[[["€"]]]]]`} {
		v, err := parseJSON(text)
		if err != nil {
			t.Fatal(err)
		}
		for _, indent := range []int{0, 2} {
			want, _ := stringify(v, strings.Repeat(" ", indent))
			if got := indentedLen(v, indent, 0, 1<<30); got != len(want) {
				t.Errorf("%s with %d spaces: counted %d, written %d", text, indent, got, len(want))
			}
		}
		file := newObject()
		file.set("flow", v)
		whole, _ := stringify(file, "  ")
		if got := indentedLen(v, 2, 1, 1<<30); got != len(whole)-len("{\n  \"flow\": \n}") {
			t.Errorf("%s one level in: counted %d, written %d", text, got, len(whole)-len("{\n  \"flow\": \n}"))
		}
	}
}
