package goapipath

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/unitronix/betterdesk-server/console/httpx"
)

// FuzzQueryOf feeds raw query strings to the parser. Whatever comes in,
// it must not panic, and what it returns must hold together: a missing
// name is not truthy, text that exists is what Text returns, and the only
// error is the one for an object that cannot become text.
func FuzzQueryOf(f *testing.F) {
	for _, seed := range []string{
		"", "a=1", "a=1&a=2", "a[]=1", "a[0]=1&a[x]=2", "a[toString]=1", "a%5B%5D=1", "a=%zz&a=%C3%A9", "[a][b][c][d][e][f][g]=1",
		"a[1000]=1&a=2&a[x]=3", "a=1&a[1000]=2", "a[[]]=1", "a[b=1", "a]=1", "=&=&", "a[__proto__]=1", strings.Repeat("a=1&", 1002),
		strings.Repeat("a[]=1&", 600) + "a[5]=x&a[y]=z&a=q", "a[999]=1&a[998][x]=2&a=3",
	} {
		f.Add(seed, "a")
	}
	f.Fuzz(func(t *testing.T, raw, name string) {
		if len(raw) > 1<<16 {
			return
		}
		value := QueryOf(raw, name)
		text, err := value.Text()
		if err != nil && err != ErrNotPrimitive {
			t.Fatalf("unexpected error %v", err)
		}
		if !value.Exists() {
			if value.Truthy() || text != "undefined" || err != nil {
				t.Fatalf("a missing name: truthy=%v text=%q err=%v", value.Truthy(), text, err)
			}
			return
		}
		if err == nil && !utf8.ValidString(text) {
			t.Fatalf("text %q is not UTF-8", text)
		}
		if value.node.kind == queryString && value.Truthy() != (text != "") {
			t.Fatalf("text %q, truthy %v", text, value.Truthy())
		}
		if value.node.kind != queryString && !value.Truthy() {
			t.Fatal("a list or an object is not truthy")
		}
		// The same query read twice gives the same value
		again, againErr := QueryOf(raw, name).Text()
		if again != text || againErr != err {
			t.Fatalf("second read differs: %q, %v", again, againErr)
		}
	})
}

// FuzzSafeSegment checks what the path check promises about every value
// it lets through: one segment, nothing that could leave it.
func FuzzSafeSegment(f *testing.F) {
	for _, seed := range []string{"", ".", "..", "a", "a/b", "a..b", " a ", "a%2Fb", "a:b@c+d%", "\ufeffa", strings.Repeat("a", 257), "a\x00", "a\\b", "a?b", "ä"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		id, err := AssertSafeAPIID(value, "id")
		segment, segmentErr := SafeSegment(value, "id")
		if (err == nil) != (segmentErr == nil) {
			t.Fatalf("AssertSafeAPIID says %v, SafeSegment says %v", err, segmentErr)
		}
		if err != nil {
			if err.Error() != "Invalid id" || !IsInvalid(err) {
				t.Fatalf("error %q", err)
			}
			return
		}
		if id == "" || id == "." || id == ".." || len(id) > 256 || strings.ContainsAny(id, "/\\?#&= \x00\r\n") {
			t.Fatalf("%q passed as %q", value, id)
		}
		if strings.ContainsAny(segment, "/\\?#&=:@+ ") {
			t.Fatalf("segment %q of %q holds a character with a meaning in a URL", segment, value)
		}
		// A segment keeps the whole path inside the API: the client's
		// check takes it unless it holds two dots in a row
		if _, err := AssertSafeGoAPIRelativePath("/x/" + segment + "/y"); err != nil && !strings.Contains(id, "..") {
			t.Fatalf("path with segment %q refused: %v", segment, err)
		}
	})
}

// FuzzStringify checks the one thing stringify changes in JSON text: the
// two separators go out as characters, and the text still reads back as
// the value that went in.
func FuzzStringify(f *testing.F) {
	for _, seed := range []string{"", "plain", "a\u2028b\u2029c", `back\slash`, `\u2028`, `\\u2029`, `"quoted"`, "\\", "tab\tnew\nline", "\u00e9\u4e16", `{"k":"\u2028"}`} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, text string) {
		if !utf8.ValidString(text) {
			return
		}
		for _, value := range []any{text, map[string]any{text: []any{text, 1.5, nil}}} {
			plain, err := httpx.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			got, err := stringify(value)
			if err != nil {
				t.Fatal(err)
			}
			var before, after any
			if err := json.Unmarshal(plain, &before); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(got, &after); err != nil {
				t.Fatalf("stringify wrote %q, which is no JSON: %v", got, err)
			}
			first, _ := httpx.Marshal(before)
			again, _ := httpx.Marshal(after)
			if !bytes.Equal(first, again) {
				t.Fatalf("%q reads back as %q", plain, got)
			}
			for _, separator := range []string{"\u2028", "\u2029"} {
				if bytes.Count(got, []byte(separator)) < strings.Count(text, separator) {
					t.Fatalf("%q lost a separator: %q", text, got)
				}
			}
		}
	})
}
