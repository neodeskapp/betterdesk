package i18n

import (
	"math"
	"strings"
	"testing"
)

// JSON.parse and JSON.stringify of Node 22, input by input: the value
// written back compact and with two spaces, or the error message.
func TestParseAndStringifyLikeNode(t *testing.T) {
	cases := list(vectors(t, "jsonparse.json"))
	if len(cases) < 150 {
		t.Fatalf("only %d vectors", len(cases))
	}
	for _, c := range cases {
		input := str(c, "input")
		v, err := parseJSON(input)
		if want := str(c, "error"); want != "" {
			if err == nil {
				t.Errorf("parse %q: no error, Node says %q", input, want)
			} else if err.Error() != want {
				t.Errorf("parse %q:\n got  %q\n want %q", input, err.Error(), want)
			}
			continue
		}
		if err != nil {
			t.Errorf("parse %q: %v, Node parses it", input, err)
			continue
		}
		if got, want := stringify(v, ""), str(c, "compact"); got != want {
			t.Errorf("stringify(%q) = %q, want %q", input, got, want)
		}
		if got, want := stringify(v, "  "), str(c, "pretty"); got != want {
			t.Errorf("stringify(%q, 2) = %q, want %q", input, got, want)
		}
	}
}

func TestObjectKeyOrder(t *testing.T) {
	o := NewObject()
	for _, k := range []string{"b", "10", "a", "2", "01", "4294967294", "4294967295", "-1", "0", "b"} {
		o.Set(k, k)
	}
	got := strings.Join(o.Keys(), " ")
	want := "0 2 10 4294967294 b a 01 4294967295 -1"
	if got != want {
		t.Errorf("Keys() = %q, want %q", got, want)
	}
	if o.Len() != 9 {
		t.Errorf("Len() = %d, want 9", o.Len())
	}
}

func TestNumberString(t *testing.T) {
	cases := map[float64]string{
		0: "0", 1: "1", -1.5: "-1.5", 1e21: "1e+21", 1e20: "100000000000000000000", 1e-7: "1e-7", 1e-6: "0.000001",
		123456789012345680000: "123456789012345680000", 0.1: "0.1", 12.5: "12.5", 5e-324: "5e-324",
		1.7976931348623157e308: "1.7976931348623157e+308", -1e21: "-1e+21", 1.5e300: "1.5e+300", 100: "100",
	}
	for f, want := range cases {
		if got := numberString(f); got != want {
			t.Errorf("numberString(%v) = %q, want %q", f, got, want)
		}
	}
	if got := numberString(math.NaN()); got != "NaN" {
		t.Errorf("NaN prints as %q", got)
	}
	if got := numberString(math.Inf(-1)); got != "-Infinity" {
		t.Errorf("-Infinity prints as %q", got)
	}
	if got := numberString(negZero()); got != "0" {
		t.Errorf("-0 prints as %q", got)
	}
}

func TestToFixed1(t *testing.T) {
	cases := map[float64]string{
		0: "0.0", 100: "100.0", 99.95: "100.0", 99.94: "99.9", 6.25: "6.3", 6.35: "6.3", 0.05: "0.1", 0.04: "0.0",
		33.33333333333333: "33.3", 66.66666666666666: "66.7", 98.75: "98.8", 2.5: "2.5", 0.25: "0.3", 0.75: "0.8",
	}
	for f, want := range cases {
		if got := toFixed1(f); got != want {
			t.Errorf("toFixed1(%v) = %q, want %q", f, got, want)
		}
	}
	if got := toFixed1(math.NaN()); got != "NaN" {
		t.Errorf("toFixed1(NaN) = %q", got)
	}
}

func TestDecodeUTF8ReplacesBrokenSequences(t *testing.T) {
	cases := []struct {
		in   []byte
		want string
	}{
		{[]byte("plain"), "plain"},
		{[]byte{0xFF}, "\uFFFD"},
		{[]byte{'a', 0xC3, 0x28}, "a\uFFFD("},
		{[]byte{0xE2, 0x82, 0x20}, "\uFFFD "},
		{[]byte{0xF0, 0x9F, 0x98, 0x41}, "\uFFFDA"},
		{[]byte{0xED, 0xA0, 0x80}, "\uFFFD\uFFFD\uFFFD"},
		{[]byte{0xC0, 0xAF}, "\uFFFD\uFFFD"},
		{[]byte{0xE2, 0x82}, "\uFFFD"},
		{[]byte{0xF0, 0x9F, 0x98, 0x80}, "\U0001F600"},
	}
	for _, c := range cases {
		if got := decodeUTF8(c.in); got != c.want {
			t.Errorf("decodeUTF8(% x) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestUTF16Helpers(t *testing.T) {
	s := "a\U0001F600é"
	if got := utf16Len(s); got != 4 {
		t.Errorf("utf16Len = %d, want 4", got)
	}
	lone := fromUnits([]uint16{0xD83D, 'x'})
	if got := utf16Len(lone); got != 2 {
		t.Errorf("a lone surrogate and a letter have length %d, want 2", got)
	}
	if got := fromUnits(units(lone)); got != lone {
		t.Errorf("a lone surrogate does not survive the round trip: %q", got)
	}
	if got := stringify(lone, ""); got != `"\ud83dx"` {
		t.Errorf("stringify of a lone surrogate = %s", got)
	}
	if got := sliceUnits(s, 2); got != fromUnits([]uint16{'a', 0xD83D}) {
		t.Errorf("sliceUnits cuts inside the pair as JavaScript does, got %q", got)
	}
}

func TestJSStringAndTruthy(t *testing.T) {
	obj := NewObject()
	obj.Set("a", 1.0)
	hidden := NewObject()
	hidden.Set("toString", "x")
	cases := []struct {
		v      any
		text   string
		ok     bool
		truthy bool
	}{
		{nil, "null", true, false},
		{undefined, "undefined", true, false},
		{true, "true", true, true},
		{false, "false", true, false},
		{0.0, "0", true, false},
		{math.NaN(), "NaN", true, false},
		{"", "", true, false},
		{"x", "x", true, true},
		{[]any{}, "", true, true},
		{[]any{"a", nil, []any{"b", 1.5}}, "a,,b,1.5", true, true},
		{obj, "[object Object]", true, true},
		{hidden, "", false, true},
		{[]any{hidden}, "", false, true},
	}
	for _, c := range cases {
		text, ok := jsString(c.v)
		if text != c.text || ok != c.ok {
			t.Errorf("jsString(%v) = %q, %v; want %q, %v", c.v, text, ok, c.text, c.ok)
		}
		if got := truthy(c.v); got != c.truthy {
			t.Errorf("truthy(%v) = %v", c.v, got)
		}
	}
}
