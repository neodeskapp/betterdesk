package pairing

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/neodeskapp/neodesk/neodesk-server/console/middleware"
)

// labelValue reads raw as the "label" member of a request body, the way a
// request brings it in. ok is false when raw is no JSON value.
func labelValue(raw string) (v middleware.Value, ok bool) {
	req := httptest.NewRequest(http.MethodPost, "/api/panel/pairing/codes", strings.NewReader(`{"label":`+raw+`}`))
	req.Header.Set("Content-Type", "application/json")
	parse := middleware.Bodies(middleware.BodyOptions{OnError: func(http.ResponseWriter, *http.Request, int, error) {}})
	parse(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		v, ok = middleware.Body(r).Get("label"), true
	})).ServeHTTP(httptest.NewRecorder(), req)
	return v, ok
}

// FuzzLabel reads any JSON value as a label: the label is at most 64
// UTF-16 units of valid text, and its JSON form is a JSON string that
// reads back as the same text.
func FuzzLabel(f *testing.F) {
	for _, seed := range []string{
		`"Front desk"`, `"  x  "`, `""`, `null`, `0`, `-0`, `12.5`, `1e999`, `true`, `false`,
		`["a",1,null,["b"],{"x":1}]`, `{"toString":1}`, `"\ud83d"`, `"a\ud83d\udda5"`,
		`"` + strings.Repeat("\U0001F5A5", 40) + `"`, `"a` + strings.Repeat("\U0001F5A5", 40) + `"`,
		`"\u2028\u00a0x\ufeff"`, `"a\"b\\c\u0001"`, `[[[[[[]]]]]]`, `1e21`, `"` + strings.Repeat("x", 100) + `"`,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		v, ok := labelValue(raw)
		if !ok {
			return
		}
		l := labelOf(v)
		if len(l) > maxLabel {
			t.Fatalf("%q gives a label of %d units", raw, len(l))
		}
		text := l.String()
		if !utf8.ValidString(text) {
			t.Fatalf("%q gives a label that is not valid text: %q", raw, text)
		}
		encoded, err := l.MarshalJSON()
		if err != nil || !json.Valid(encoded) {
			t.Fatalf("%q gives the JSON %s (%v)", raw, encoded, err)
		}
		var back string
		if err := json.Unmarshal(encoded, &back); err != nil || back != text {
			t.Fatalf("%q: the JSON %s reads back as %q, the label is %q (%v)", raw, encoded, back, text, err)
		}
	})
}

// FuzzCodeID checks the form of an id against a second reading of the
// rule: 8 to 32 hex digits after trimming.
func FuzzCodeID(f *testing.F) {
	for _, seed := range []string{"abcdef01", " ABCDEF0123456789\t", "abcdef0", strings.Repeat("a", 33), "abcdef01\nabcdef01", "ghijklmn", "", "\u00a0abcdef01\u2028", "abcdef0\u212a"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, id string) {
		clean := trim(id)
		want := len(clean) >= 8 && len(clean) <= 32 && strings.Trim(clean, "0123456789abcdefABCDEF") == ""
		if got := codeID.MatchString(clean); got != want {
			t.Fatalf("id %q: accepted %v, want %v", id, got, want)
		}
	})
}
