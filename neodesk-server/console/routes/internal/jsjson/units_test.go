package jsjson

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf16"
	"unicode/utf8"
)

// utf16Of is the UTF-16 units of a text of this package: a character as
// it is, half of a pair (three bytes UTF-8 does not allow) as that half.
func utf16Of(s string) []uint16 {
	var out []uint16
	for i := 0; i < len(s); {
		if half, ok := halfPair(s[i:]); ok {
			out = append(out, uint16(half))
			i += 3
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		out = append(out, utf16.Encode([]rune{r})...)
		i += size
	}
	return out
}

func TestUnits(t *testing.T) {
	high, low := halfText(0xD83D), halfText(0xDE00)
	for _, tt := range []struct {
		text string
		want []string
	}{
		{"", nil},
		{"ab", []string{"a", "b"}},
		{"café", []string{"c", "a", "f", "é"}},
		{"a😀b", []string{"a", high, low, "b"}},
		{"x" + high, []string{"x", high}},
		{low + high, []string{low, high}},
		{lineSeparator + "\x00", []string{lineSeparator, "\x00"}},
		// A byte that is no UTF-8 is the replacement character
		{"a\xffb", []string{"a", string(utf8.RuneError), "b"}},
	} {
		if got := Units(tt.text); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("Units(%q) = %q, want %q", tt.text, got, tt.want)
		}
	}
}

// lineSeparator is U+2028, which JSON.stringify leaves as it is.
var lineSeparator = string(rune(0x2028))

func TestWellFormed(t *testing.T) {
	bad := string(utf8.RuneError)
	for text, want := range map[string]string{
		"":                                  "",
		"plain café 😀":                      "plain café 😀",
		"a" + halfText(0xD83D) + "b":        "a" + bad + "b",
		halfText(0xD83D) + halfText(0xDE00): bad + bad,
		halfText(0xDBFF) + "x" + halfText(0xDC00): bad + "x" + bad,
	} {
		if got := WellFormed(text); got != want {
			t.Errorf("WellFormed(%q) = %q, want %q", text, got, want)
		}
	}
}

func TestKeys(t *testing.T) {
	v, err := Parse(`{"b":1,"2":2,"a":3,"1":4,"b":5}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := v.Keys(); !reflect.DeepEqual(got, []string{"1", "2", "b", "a"}) {
		t.Errorf("Keys = %q", got)
	}
	keys := v.Keys()
	keys[0] = "changed"
	if v.Keys()[0] != "1" {
		t.Errorf("the keys a caller changed changed the object")
	}
	for _, text := range []string{`[1,2]`, `"ab"`, `5`, `null`, `true`} {
		v, _ := Parse(text)
		if got := v.Keys(); got != nil {
			t.Errorf("Keys of %s = %q, want none", text, got)
		}
	}
}

// What Units makes of any text of a JSON document: as many texts as the
// text has UTF-16 units, each one of them, in order.
func FuzzUnits(f *testing.F) {
	for _, seed := range []string{`"a😀b"`, `"\ud83d"`, `"\ude00\ud83d"`, `"café"`, `""`, `"\u0000` + lineSeparator + `"`, "\"\xff\xfe\""} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, text string) {
		v, err := Parse(text)
		if err != nil || v.Kind() != String {
			return
		}
		s := v.Str()
		units := Units(s)
		if len(units) != Length(s) {
			t.Fatalf("Units(%q) has %d texts, the text %d units", s, len(units), Length(s))
		}
		want := utf16Of(s)
		for i, unit := range units {
			got := utf16Of(unit)
			if len(got) != 1 || got[0] != want[i] {
				t.Fatalf("unit %d of %q is %q (%x), want %x", i, s, unit, got, want[i])
			}
		}
		if well := WellFormed(strings.Join(units, "")); !utf8.ValidString(well) {
			t.Fatalf("WellFormed of the units of %q is no UTF-8: %q", s, well)
		}
	})
}
