package ws

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

type targetVectors struct {
	Targets []struct {
		Target string            `json:"target"`
		Throws bool              `json:"throws"`
		Path   string            `json:"path"`
		Params map[string]string `json:"params"`
	} `json:"targets"`
	Origins []struct {
		Env    map[string]string `json:"env"`
		Origin string            `json:"origin"`
		Cookie string            `json:"cookie"`
		Host   string            `json:"host"`
		OK     bool              `json:"ok"`
		Reason string            `json:"reason"`
	} `json:"origins"`
}

func loadVectors(t *testing.T) targetVectors {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "node_targets.json"))
	if err != nil {
		t.Fatal(err)
	}
	var v targetVectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// searchNames are the names the oracle asked searchParams for.
var searchNames = []string{"device_id", "token", "session", "role", "a", "q", "", "x"}

// TestNodeTargets compares what the old console made of 6,000 request
// targets (new URL(req.url, base): the pathname, whether it threw, and
// searchParams.get) with parseTarget and searchParam.
func TestNodeTargets(t *testing.T) {
	v := loadVectors(t)
	if len(v.Targets) < 5000 {
		t.Fatalf("only %d targets", len(v.Targets))
	}
	failed := 0
	for _, c := range v.Targets {
		got, ok := parseTarget(c.Target)
		var problems []string
		switch {
		case c.Throws && ok:
			problems = append(problems, "Node threw, Go read path "+got.Path)
		case !c.Throws && !ok:
			problems = append(problems, "Go refused, Node read path "+c.Path)
		case !c.Throws && got.Path != c.Path:
			problems = append(problems, "path "+c.Path+" != "+got.Path)
		}
		if ok && !c.Throws {
			for _, name := range searchNames {
				want, has := c.Params[name]
				value, found := searchParam(got.Query, name)
				if has != found || want != value {
					problems = append(problems, "param "+name+": "+want+" != "+value)
				}
			}
		}
		if len(problems) > 0 {
			if failed++; failed <= 20 {
				t.Errorf("%q: %s", c.Target, strings.Join(problems, "; "))
			}
		}
	}
	if failed > 0 {
		t.Errorf("%d of %d targets differ", failed, len(v.Targets))
	}
}

// TestSearchParamDecodesNames: URLSearchParams decodes the names as it
// does the values ("+" a space, escapes), so an escaped name finds the
// parameter; the first pair wins.
func TestSearchParamDecodesNames(t *testing.T) {
	for _, c := range []struct {
		query, name, want string
		found             bool
	}{
		{"device%5Fid=7", "device_id", "7", true},
		{"device%5fid=7&device_id=8", "device_id", "7", true},
		{"a+b=1", "a b", "1", true},
		{"%74oken=t", "token", "t", true},
		{"device%5Fid=7", "device%5Fid", "", false},
	} {
		got, found := searchParam(c.query, c.name)
		if got != c.want || found != c.found {
			t.Errorf("searchParam(%q, %q) = %q %v, want %q %v", c.query, c.name, got, found, c.want, c.found)
		}
	}
}

// TestNodeOrigins compares validateOrigin under five settings with Allow.
func TestNodeOrigins(t *testing.T) {
	v := loadVectors(t)
	for _, c := range v.Origins {
		policy := OriginPolicy{
			Allowed:      ParseAllowedOrigins(c.Env["WS_ALLOWED_ORIGINS"]),
			AllowMissing: c.Env["BD_ALLOW_MISSING_ORIGIN"] == "true",
		}
		r := &http.Request{Header: http.Header{}, Host: c.Host}
		if c.Origin != "" {
			r.Header.Set("Origin", c.Origin)
		}
		if c.Cookie != "" {
			r.Header.Set("Cookie", c.Cookie)
		}
		ok, reason := policy.Allow(r)
		if ok != c.OK || reason != c.Reason {
			t.Errorf("env %v origin %q cookie %q host %q: node %v %q, go %v %q", c.Env, c.Origin, c.Cookie, c.Host, c.OK, c.Reason, ok, reason)
		}
	}
}

// FuzzParseTarget feeds request targets to the parser: it must not panic,
// every query value decodes to UTF-8, and a target that starts with a
// slash (the form a server gets) has a path that starts with one and
// holds none of the bytes the standard escapes. (A URL of another scheme
// without "//" has an opaque path, "a:0" has the path "0".)
func FuzzParseTarget(f *testing.F) {
	for _, s := range []string{"/ws/device-status", "//h/ws/x", "http://h:1/a?b=c", "/a/../b/%2e%2E/c?x=%FF+%C3", "foo://x/y", "/\\a", "*"} {
		f.Add(s, "x")
	}
	f.Fuzz(func(t *testing.T, target, name string) {
		got, ok := parseTarget(target)
		if !ok {
			return
		}
		if value, _ := searchParam(got.Query, name); !utf8.ValidString(value) {
			t.Fatalf("%q: value %q is not UTF-8", target, value)
		}
		if !strings.HasPrefix(target, "/") {
			return
		}
		if !strings.HasPrefix(got.Path, "/") {
			t.Fatalf("%q: path %q", target, got.Path)
		}
		for i := 0; i < len(got.Path); i++ {
			if c := got.Path[i]; c <= 0x20 || c >= 0x7f || strings.IndexByte("\"<>`{}#?", c) >= 0 {
				t.Fatalf("%q: path %q holds %q", target, got.Path, c)
			}
		}
	})
}

// FuzzDecodeUTF8 checks the decoder against the standard library where
// they must agree: valid input comes back as it is, and the output is
// always UTF-8 with at most one replacement per input byte.
func FuzzDecodeUTF8(f *testing.F) {
	for _, s := range []string{"abc", "\xc3\xa9", "\xc3(", "\xe2\x82", "\xf0\x9f\x98\x80", "\xed\xa0\x80", "\xff\xfe", "\xf4\x90\x80\x80"} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		out := decodeUTF8(b)
		if !utf8.ValidString(out) {
			t.Fatalf("%q: not UTF-8: %q", b, out)
		}
		if utf8.Valid(b) && out != string(b) {
			t.Fatalf("%q: valid input changed to %q", b, out)
		}
		if n := utf8.RuneCountInString(out); n > len(b) {
			t.Fatalf("%q: %d characters out of %d bytes", b, n, len(b))
		}
	})
}

// FuzzSubprotocols checks the parser of Sec-WebSocket-Protocol: what it
// takes is a list of distinct tokens that, joined with ", ", parses to
// itself.
func FuzzSubprotocols(f *testing.F) {
	for _, s := range []string{"chat", "a, b", "a,b", "a,,b", "a b", "\ta", "a\t,b", "x/y"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, header string) {
		got, err := parseSubprotocols(header)
		if err != nil {
			return
		}
		seen := map[string]bool{}
		for _, p := range got {
			if p == "" || seen[p] {
				t.Fatalf("%q: %q", header, got)
			}
			seen[p] = true
			for i := 0; i < len(p); i++ {
				if !tokenChar(p[i]) {
					t.Fatalf("%q: %q holds %q", header, p, p[i])
				}
			}
		}
		again, err := parseSubprotocols(strings.Join(got, ", "))
		if err != nil || strings.Join(again, ",") != strings.Join(got, ",") {
			t.Fatalf("%q: %q does not parse to itself: %q %v", header, got, again, err)
		}
	})
}
