package generator

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"unicode/utf8"

	"github.com/unitronix/betterdesk-server/console/goapi"
)

// FuzzParseJSON reads request bodies and stored profiles: it accepts what
// encoding/json accepts, and what it reads writes back to text that reads
// the same.
func FuzzParseJSON(f *testing.F) {
	for _, s := range []string{`{}`, `[]`, `{"a":[1,2.5,-0,1e21,"\ud83d",{"b":null}]}`, `"\u2028"`, `{"a":1,"a":2}`, `1E400`, `[[[`, `{"a":}`} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, text string) {
		v, err := parseJSON(text)
		if utf8.ValidString(text) && (err == nil) != json.Valid([]byte(text)) {
			t.Fatalf("%q: parse error %v, encoding/json valid %v", text, err, json.Valid([]byte(text)))
		}
		if err != nil {
			return
		}
		out := stringify(v)
		again, err := parseJSON(out)
		if err != nil || stringify(again) != out {
			t.Fatalf("%q wrote %q, which reads back as %q (%v)", text, out, stringify(again), err)
		}
	})
}

var knownErrors = regexp.MustCompile(`^(contact_url_invalid|logo_invalid|(primary|accent|background|surface|text|text_muted|status_ready|header_text)_color_invalid|server_host_required|server_host_invalid)$`)

// FuzzValidateBranding takes any profile a request can send: the result
// has the fixed keys, known error codes only, and a stable hash.
func FuzzValidateBranding(f *testing.F) {
	for _, s := range []string{`{}`, `{"server_host":"desk.example.com","contact_url":"x.example"}`, `{"primary_color":["#aabbcc"]}`, `{"capabilities":[],"server":"x"}`} {
		f.Add(s)
	}
	keys := stringify(func() any { v, _ := ValidateBranding(goapi.NewObject(), nil); return v.Normalized.Keys() }())
	f.Fuzz(func(t *testing.T, text string) {
		input, err := parseJSON(text)
		if err != nil {
			return
		}
		v, err := ValidateBranding(input, nil)
		if err != nil {
			return
		}
		if got := stringify(v.Normalized.Keys()); got != keys {
			t.Fatalf("keys %s, want %s", got, keys)
		}
		for _, e := range v.Errors {
			if !knownErrors.MatchString(e) {
				t.Fatalf("unknown error %q", e)
			}
		}
		if v.Valid != (len(v.Errors) == 0) || HashBranding(v.Normalized) != HashBranding(v.Normalized.Clone()) {
			t.Fatal("validity or hash not stable")
		}
	})
}

// FuzzTemplateListing checks archive listings: a name with a ".." part,
// an absolute path or a drive letter refuses the archive, others pass.
func FuzzTemplateListing(f *testing.F) {
	for _, s := range []string{"generator-templates/\ngenerator-templates/a", "../x", "/etc/passwd", "c:\\x", "a/./b", "a\\..\\b", "..."} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, listing string) {
		unpacked := false
		runner := &fakeRunner{handle: func(c Command) (Result, error) {
			if c.Args[0] == "-tzf" {
				return Result{Stdout: listing}, nil
			}
			unpacked = true
			return Result{}, nil
		}}
		s := New(Options{DataDir: t.TempDir(), Run: runner, Env: func() []string { return nil }})
		err := s.extractTarGz(context.Background(), filepath.Join(t.TempDir(), "a.tar.gz"), t.TempDir())
		bad := false
		for _, line := range regexp.MustCompile(`\r?\n`).Split(listing, -1) {
			entry := strings.ReplaceAll(line, `\`, "/")
			if line != "" && (strings.HasPrefix(entry, "/") || regexp.MustCompile(`^[a-zA-Z]:`).MatchString(entry) || strings.Contains("/"+entry+"/", "/../")) {
				bad = true
			}
		}
		if bad != (err != nil) || bad == unpacked {
			t.Fatalf("%q: error %v, unpacked %v", listing, err, unpacked)
		}
	})
}

// FuzzSeed reads a seed as Node's base64 decoder does; strict base64 reads
// as the standard library reads it.
func FuzzSeed(f *testing.F) {
	for _, s := range []string{testSeed, "AA==", "A", "-_+/", " a b=c "} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		got := nodeBase64Decode(s)
		if len(got) > len(s) {
			t.Fatalf("%q: %d bytes", s, len(got))
		}
		if want, err := base64.StdEncoding.DecodeString(s); err == nil && string(want) != string(got) {
			t.Fatalf("%q: %x, stdlib %x", s, got, want)
		}
	})
}
