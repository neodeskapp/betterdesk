package cookie

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestLookup(t *testing.T) {
	cases := []struct {
		name   string
		header string
		want   string
		found  bool
	}{
		{"plain", "a=1; sid=abc; b=2", "abc", true},
		{"missing", "a=1; b=2", "", false},
		{"empty header", "", "", false},
		{"one character", "s", "", false},
		{"first of two wins", "sid=first; sid=second", "first", true},
		{"first wins also when empty", "sid=; sid=second", "", true},
		{"spaces and tabs are trimmed", " \tsid \t= \tabc\t ; b=2", "abc", true},
		{"quotes are removed once", `sid="abc"`, "abc", true},
		{"inner quotes stay", `sid=""abc""`, `"abc"`, true},
		{"a lone quote is an empty value", `sid="`, "", true},
		{"one-sided quote stays", `sid="abc`, `"abc`, true},
		{"percent-decoded", "sid=s%3Aabc.x%2By%2Fz", "s:abc.x+y/z", true},
		{"plus stays a plus", "sid=a+b", "a+b", true},
		{"UTF-8 is decoded", "sid=%C3%A4%E2%82%AC%F0%9F%98%80", "ä€😀", true},
		{"bad escape keeps the raw value", "sid=abc%zz%3A", "abc%zz%3A", true},
		{"cut-off escape keeps the raw value", "sid=abc%3", "abc%3", true},
		{"a lone continuation byte keeps the raw value", "sid=%A4", "%A4", true},
		{"an overlong form keeps the raw value", "sid=%C0%AF", "%C0%AF", true},
		{"a surrogate keeps the raw value", "sid=%ED%A0%80", "%ED%A0%80", true},
		{"a cut-off character keeps the raw value", "sid=%E2%82", "%E2%82", true},
		{"a part without = is skipped", "junk; sid=abc", "abc", true},
		{"a part without = at the end", "sid=abc; junk", "abc", true},
		{"value with =", "sid=a=b=c", "a=b=c", true},
		{"name is case sensitive", "SID=abc", "", false},
		{"a longer name does not match", "xsid=abc; sid2=def", "", false},
		{"no value after the last =", "a=1; sid=", "", true},
		{"a high byte is one Latin-1 character", "sid=\xe4", "ä", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, found := Lookup(tc.header, "sid")
			if got != tc.want || found != tc.found {
				t.Fatalf("Lookup(%q) = %q, %v; want %q, %v", tc.header, got, found, tc.want, tc.found)
			}
		})
	}
}

// TestLookupMatchesNode checks Lookup against what the Node console's
// parser read from the same headers (testdata/parse_vectors.json, written
// by ../testdata/interop/cookie-vectors.js).
func TestLookupMatchesNode(t *testing.T) {
	raw, err := os.ReadFile("testdata/parse_vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors []struct {
		HeaderHex string  `json:"header_hex"`
		Value     *string `json:"value"`
	}
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatal(err)
	}
	if len(vectors) < 1000 {
		t.Fatalf("only %d vectors", len(vectors))
	}
	for _, v := range vectors {
		header, err := hex.DecodeString(v.HeaderHex)
		if err != nil {
			t.Fatal(err)
		}
		got, found := Lookup(string(header), "sid")
		switch {
		case v.Value == nil && found:
			t.Errorf("Lookup(%q) = %q, Node finds no cookie", header, got)
		case v.Value != nil && (!found || got != *v.Value):
			t.Errorf("Lookup(%q) = %q, %v; Node reads %q", header, got, found, *v.Value)
		}
	}
}

func TestGetJoinsCookieHeaders(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Add("Cookie", "a=1")
	r.Header.Add("Cookie", "sid=two")
	if got, ok := Get(r, "sid"); !ok || got != "two" {
		t.Fatalf("Get = %q, %v", got, ok)
	}
}

func TestFormat(t *testing.T) {
	expires := time.Date(2026, 11, 10, 1, 33, 6, 999e6, time.FixedZone("x", 3600))
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"bare", Format("n", "v", Attributes{}), "n=v"},
		{"value is encoded", Format("n", "s:a.b+c/d|e é", Attributes{}), "n=s%3Aa.b%2Bc%2Fd%7Ce%20%C3%A9"},
		{"unreserved characters stay", Format("n", "A-z_0.9!~*'()", Attributes{}), "n=A-z_0.9!~*'()"},
		{
			"all attributes in order",
			Format("n", "v", Attributes{Path: "/", Expires: expires, HTTPOnly: true, Secure: true, SameSite: "Lax"}),
			"n=v; Path=/; Expires=Tue, 10 Nov 2026 00:33:06 GMT; HttpOnly; Secure; SameSite=Lax",
		},
		{"strict", Format("n", "v", Attributes{SameSite: "strict"}), "n=v; SameSite=Strict"},
		{"none", Format("n", "v", Attributes{SameSite: "NONE"}), "n=v; SameSite=None"},
		{"unknown SameSite is left out", Format("n", "v", Attributes{SameSite: "sometimes"}), "n=v"},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, tc.got, tc.want)
		}
	}
}

func TestSetAndClear(t *testing.T) {
	w := httptest.NewRecorder()
	Set(w, "a", "1", Attributes{Path: "/"})
	Clear(w, "bd.sid", Attributes{})
	Clear(w, "__csrf", Attributes{Path: "/", HTTPOnly: true, Secure: true, SameSite: "lax"})
	got := w.Header().Values("Set-Cookie")
	want := []string{
		"a=1; Path=/",
		"bd.sid=; Path=/; Expires=Thu, 01 Jan 1970 00:00:00 GMT",
		"__csrf=; Path=/; Expires=Thu, 01 Jan 1970 00:00:00 GMT; HttpOnly; Secure; SameSite=Lax",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d headers: %q", len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("header %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

func FuzzLookup(f *testing.F) {
	for _, seed := range []string{"sid=abc", "sid=%C3%A4; sid=x", `a; sid="b"`, "sid=%", "=;=;sid=1", "sid=\xff%ff"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, header string) {
		value, found := Lookup(header, "sid")
		if !found && value != "" {
			t.Fatalf("a value %q without a cookie", value)
		}
	})
}
