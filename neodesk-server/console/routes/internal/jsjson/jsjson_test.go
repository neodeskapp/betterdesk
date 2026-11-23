package jsjson

import (
	"encoding/json"
	"math"
	"os"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// vector is what JavaScript made of one JSON text (testdata/vectors.json,
// recorded from the Node console; see testdata/README.md).
type vector struct {
	Text        string             `json:"text"`
	Error       bool               `json:"error"`
	JSON        string             `json:"json"`
	Truthy      bool               `json:"truthy"`
	String      []uint16           `json:"string"`
	StringError bool               `json:"stringError"`
	Number      string             `json:"number"`
	NumberError bool               `json:"numberError"`
	Members     map[string]*string `json:"members"`
	Length      *int               `json:"length"`
	Slices      [][]json.RawMessage
}

func loadVectors(t testing.TB) []vector {
	t.Helper()
	raw, err := os.ReadFile("testdata/vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors []vector
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatal(err)
	}
	if len(vectors) < 8000 {
		t.Fatalf("%d vectors, want the whole file", len(vectors))
	}
	return vectors
}

// units is the UTF-16 units of a text of this package.
func units(s string) []uint16 {
	out := []uint16{}
	for i := 0; i < len(s); {
		r, size, width := decode(s[i:])
		if width == 2 {
			r -= 0x10000
			out = append(out, uint16(0xD800+r>>10), uint16(0xDC00+r&0x3FF))
		} else {
			out = append(out, uint16(r))
		}
		i += size
	}
	return out
}

// numberText prints a number as the vectors do: String(n), with the
// sign of a negative zero.
func numberText(f float64) string {
	if f == 0 && math.Signbit(f) {
		return "-0"
	}
	return jsbind.NumberString(f)
}

// TestNodeVectors holds every operation against what JavaScript did with
// the same JSON text: whether JSON.parse took it, JSON.stringify of the
// result, its truth, String() and Number() of it (or that they threw),
// the members the routes read, and for a text its length and its first
// characters.
func TestNodeVectors(t *testing.T) {
	failed := 0
	fail := func(format string, args ...any) {
		if failed++; failed <= 30 {
			t.Errorf(format, args...)
		}
	}
	parsed, refused := 0, 0
	for _, vec := range loadVectors(t) {
		v, err := Parse(vec.Text)
		if vec.Error {
			refused++
			if err == nil {
				fail("Parse(%q) took what JSON.parse refused", vec.Text)
			}
			continue
		}
		parsed++
		if err != nil {
			fail("Parse(%q): %v", vec.Text, err)
			continue
		}
		if out, _ := v.MarshalJSON(); string(out) != vec.JSON {
			fail("stringify of %q: %s, want %s", vec.Text, out, vec.JSON)
		}
		if v.Truthy() != vec.Truthy {
			fail("truth of %q: %v", vec.Text, v.Truthy())
		}
		text, err := v.Text()
		if (err != nil) != vec.StringError || err == nil && !reflect.DeepEqual(units(text), vec.String) {
			fail("String of %q: %q %v, want units %v, error %v", vec.Text, text, err, vec.String, vec.StringError)
		}
		num, err := v.Num()
		if (err != nil) != vec.NumberError || err == nil && numberText(num) != vec.Number {
			fail("Number of %q: %v %v, want %s, error %v", vec.Text, num, err, vec.Number, vec.NumberError)
		}
		for name, want := range vec.Members {
			member := v.Get(name)
			switch {
			case want == nil && member.Kind() != Undefined:
				fail("%q has a member %s", vec.Text, name)
			case want != nil:
				if out, _ := member.MarshalJSON(); member.Kind() == Undefined || string(out) != *want {
					fail("member %s of %q: %s, want %s", name, vec.Text, out, *want)
				}
			}
		}
		if vec.Length != nil {
			if got := Length(v.Str()); got != *vec.Length {
				fail("length of %q: %d, want %d", vec.Text, got, *vec.Length)
			}
			for _, slice := range vec.Slices {
				var n int
				var want []uint16
				var quoted string
				_ = json.Unmarshal(slice[0], &n)
				_ = json.Unmarshal(slice[1], &want)
				_ = json.Unmarshal(slice[2], &quoted)
				got := Slice(v.Str(), n)
				if !reflect.DeepEqual(units(got), want) || string(AppendQuoted(nil, got)) != quoted {
					fail("first %d of %q: %q, want units %v, written %s", n, vec.Text, got, want, quoted)
				}
			}
		}
	}
	if failed > 30 {
		t.Errorf("%d more differences", failed-30)
	}
	if parsed < 5000 || refused < 1500 {
		t.Errorf("%d texts parsed and %d refused: the vectors have lost their balance", parsed, refused)
	}
}

func TestOperationsOnWhatIsNotThere(t *testing.T) {
	var undefined Value
	if undefined.Kind() != Undefined || undefined.Truthy() || !undefined.IsNullish() || !NullValue.IsNullish() || OfText("").IsNullish() || OfNumber(math.NaN()).Truthy() {
		t.Error("undefined and null are the two values that are nullish, and they and NaN do not count as true")
	}
	if text, _ := undefined.Text(); text != "undefined" {
		t.Errorf("String(undefined) = %q", text)
	}
	if num, _ := undefined.Num(); !math.IsNaN(num) {
		t.Errorf("Number(undefined) = %v", num)
	}
	if got := undefined.Get("a").Get("b").Get("length"); got.Kind() != Undefined {
		t.Errorf("a chain through undefined gives %v", got.Kind())
	}
	if got := NullValue.Get("length"); got.Kind() != Undefined {
		t.Errorf("null?.length gives %v", got.Kind())
	}
	if out, _ := undefined.MarshalJSON(); string(out) != "null" {
		t.Errorf("undefined in JSON is %s", out)
	}
	if got := undefined.Or(OfNumber(0)).Or(OfText("x")); got.Str() != "x" {
		t.Errorf("undefined || 0 || 'x' is %v", got)
	}
	if got := OfNumber(2).Or(OfText("x")); got.Float() != 2 || got.Kind() != Number {
		t.Errorf("2 || 'x' is %v", got)
	}
	list, _ := Parse(`[1,"a"]`)
	object, _ := Parse(`{"a":1}`)
	if !list.IsObjectLike() || !object.IsObjectLike() || NullValue.IsObjectLike() || OfText("a").IsObjectLike() || len(list.Items()) != 2 || object.Items() != nil {
		t.Error("an object and a list are the values typeof calls object, null left out")
	}
}

func TestParseLimits(t *testing.T) {
	deep := strings.Repeat("[", MaxDepth) + strings.Repeat("]", MaxDepth)
	if _, err := Parse(deep); err != nil {
		t.Errorf("%d levels: %v", MaxDepth, err)
	}
	if _, err := Parse("[" + deep + "]"); err == nil {
		t.Errorf("%d levels parsed", MaxDepth+1)
	}
	// What is no UTF-8 reached JavaScript as the replacement character
	v, err := Parse("\"a\xffb\xed\xa0\xbdc\"")
	if out, _ := v.MarshalJSON(); err != nil || string(out) != "\"a\xef\xbf\xbdb\xef\xbf\xbd\xef\xbf\xbd\xef\xbf\xbdc\"" {
		t.Errorf("bytes that are no UTF-8: %q %v", out, err)
	}
	if got := Length(v.Str()); got != 7 {
		t.Errorf("their length: %d", got)
	}
}

// FuzzParse holds Parse against the standard library on any text: the
// two agree on what is JSON, a parsed value is written back as JSON that
// parses to the same value, and where no half of a surrogate pair is
// involved the output is what package store makes of the same text, which
// was written and tested on its own.
func FuzzParse(f *testing.F) {
	for i, vec := range loadVectors(f) {
		if i%40 == 0 {
			f.Add(vec.Text)
		}
	}
	f.Fuzz(func(t *testing.T, text string) {
		if len(text) > 1<<16 {
			return
		}
		v, err := Parse(text)
		if utf8.ValidString(text) && (err == nil) != json.Valid([]byte(text)) {
			t.Fatalf("Parse(%q): %v, but the standard library says valid: %v", text, err, json.Valid([]byte(text)))
		}
		if err != nil {
			return
		}
		out, _ := v.MarshalJSON()
		if !json.Valid(out) {
			t.Fatalf("Parse(%q) writes %q, which is no JSON", text, out)
		}
		again, err := Parse(string(out))
		if err != nil {
			t.Fatalf("Parse(%q) writes %q, which does not parse: %v", text, out, err)
		}
		if twice, _ := again.MarshalJSON(); string(twice) != string(out) {
			t.Fatalf("Parse(%q) writes %q, which parses to %q", text, out, twice)
		}
		if _, err := v.Text(); err == nil {
			if _, err := v.Num(); err != nil {
				t.Fatalf("Number(%q) fails where String does not", text)
			}
		}
		if !utf8.ValidString(text) || strings.Contains(string(out), `\ud`) {
			return
		}
		var other store.JSONParsed
		if err := other.Scan(text); err != nil {
			t.Fatal(err)
		}
		if want, _ := other.MarshalJSON(); string(want) != string(out) {
			t.Fatalf("Parse(%q) writes %q, package store writes %q", text, out, want)
		}
	})
}

// Length, Slice and AppendQuoted take any Go string. A byte that is no
// UTF-8 counts as one replacement character, also where it only looks
// like the start of a character.
func TestTextsThatAreNoUTF8(t *testing.T) {
	for text, want := range map[string]int{
		"\xc0\x80": 2, "\xc1\xbf": 2, "\xc2\x80": 1, "\xe0\x80\x80": 3, "\xe0\xa0\x80": 1, "\xf0\x80\x80\x80": 4, "\xf0\x90\x80\x80": 2,
		"\xf4\x90\x80\x80": 4, "\xff": 1, "a\xe2\x82": 3, "\xed\xa0\xbd": 1, "\xed\xa0": 2,
	} {
		if got := Length(text); got != want {
			t.Errorf("the length of %q: %d, want %d", text, got, want)
		}
	}
	if got := string(AppendQuoted(nil, "a\xc0\x80b")); got != "\"a\xef\xbf\xbd\xef\xbf\xbdb\"" {
		t.Errorf("bytes that are no UTF-8, written: %q", got)
	}
	if got := Slice("\xc0\x80b", 1); got != "\xc0" {
		t.Errorf("the first unit of bytes that are no UTF-8: %q", got)
	}
}
