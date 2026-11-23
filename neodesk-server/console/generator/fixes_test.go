package generator

import (
	"context"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
)

// The recorded Node answers that the Go console gives otherwise on
// purpose: Node's answer was a bug that is fixed here. Each list names
// its row of BUGS.md.

// colourListVectors are the validate_branding vectors where Node threw a
// TypeError for a colour given as a list holding one valid colour, by
// the vector's index and the colour's key (GN2). Go counts such a list as
// an invalid colour, as any other value that is not a colour.
var colourListVectors = map[int]string{
	5:    "primary_color",
	6:    "accent_color",
	314:  "accent_color",
	321:  "primary_color",
	1276: "status_ready_color",
}

// checkColourListVector checks what Go makes of such a vector: no error,
// the colour's error, its default colour, and otherwise the same as for
// the profile with the list replaced by a text that is no colour.
func checkColourListVector(t *testing.T, i int, key string, input any) bool {
	t.Helper()
	got, err := ValidateBranding(input, nil)
	if err != nil {
		t.Errorf("case %d: colour list in %s: %v", i, key, err)
		return false
	}
	asText := input.(*goapi.Object).Clone()
	for _, name := range asText.Keys() {
		if _, isList := asText.Get(name).([]any); isList && (name == key || name == camelKey(key)) {
			asText.Set(name, "not a colour")
		}
	}
	want, err := ValidateBranding(asText, nil)
	if err != nil {
		t.Fatalf("case %d: %v", i, err)
	}
	if got.Valid || !contains(got.Errors, key+"_invalid") || got.Normalized.Get(key) != DefaultBranding().Get(key) ||
		!sameStrings(got.Errors, want.Errors) || stringify(got.Normalized) != stringify(want.Normalized) {
		t.Errorf("case %d: colour list in %s: %v %v %s, want %v %s", i, key, got.Valid, got.Errors, stringify(got.Normalized), want.Errors, stringify(want.Normalized))
		return false
	}
	return true
}

// camelKey is primary_color as primaryColor.
func camelKey(key string) string {
	return underscoreWord.ReplaceAllStringFunc(key, func(m string) string { return string(m[1] - 'a' + 'A') })
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// TestColourListIsInvalid answers a colour given as a list with the
// colour's error and its default colour (BUGS GN2: the old service threw,
// and create, update and preview answered 500).
func TestColourListIsInvalid(t *testing.T) {
	for _, c := range []struct {
		key   string
		value any
	}{
		{"primary_color", []any{"#aabbcc"}},
		{"primaryColor", []any{"#AABBCC"}},
		{"accent_color", []any{[]any{"#aabbcc"}}},
		{"header_text_color", []any{"#abc"}},
	} {
		got, err := ValidateBranding(goapi.NewObject("server_host", "h.example", c.key, c.value), nil)
		if err != nil {
			t.Errorf("%s %v: %v", c.key, c.value, err)
			continue
		}
		snake := underscoreKey(c.key)
		if got.Valid || !sameStrings(got.Errors, []string{snake + "_invalid"}) || got.Normalized.Get(snake) != DefaultBranding().Get(snake) {
			t.Errorf("%s %v: %v %v %v", c.key, c.value, got.Valid, got.Errors, got.Normalized.Get(snake))
		}
	}
}

// underscoreKey is primaryColor as primary_color.
func underscoreKey(key string) string {
	out := make([]byte, 0, len(key)+2)
	for i := 0; i < len(key); i++ {
		if c := key[i]; c >= 'A' && c <= 'Z' {
			out = append(out, '_', c-'A'+'a')
		} else {
			out = append(out, c)
		}
	}
	return string(out)
}

// relayFixes are the relays of the recorded vectors that the relay rule
// gives otherwise (UM4): Node took a URL with a port for a bare IPv6
// address and put it in brackets with the relay port after it.
var relayFixes = map[string]string{
	"[https://Desk.Example.com:8443/path]:21117": "Desk.Example.com:8443",
	"[http://[::1]:80]:21117":                    "[::1]:80",
}

// withFixedRelay is a recorded JSON text with the relay under key as the
// fixed rule gives it, and whether that changed it.
func withFixedRelay(text, key string) (string, bool) {
	for node, fixed := range relayFixes {
		if old := `"` + key + `":"` + node + `"`; strings.Contains(text, old) {
			return strings.Replace(text, old, `"`+key+`":"`+fixed+`"`, 1), true
		}
	}
	return text, false
}

// ipv6HostVectors are the validate_branding vectors whose server host is
// an IPv6 address, by index (SI3): Node cut "http://[::1]:80" at its
// first colon and refused it; Go reads the address "::1".
var ipv6HostVectors = map[int]string{304: "::1", 950: "::1", 1240: "::1"}

// withIPv6Host is a recorded result of such a vector as Go gives it: the
// host error gone and the address as the server host.
func withIPv6Host(t *testing.T, normalized string, errs []string, host string) (string, []string, string) {
	t.Helper()
	v, err := parseJSON(normalized)
	if err != nil {
		t.Fatal(err)
	}
	o := v.(*goapi.Object)
	o.Set("server_host", host)
	kept := []string{}
	for _, e := range errs {
		if e != "server_host_invalid" {
			kept = append(kept, e)
		}
	}
	return stringify(o), kept, HashBranding(o)
}

// TestIPv6ServerHostInAPIAddress puts an IPv6 server host in brackets in
// the API address a profile and a template build name (BUGS SI3).
func TestIPv6ServerHostInAPIAddress(t *testing.T) {
	s := New(Options{ServerInfo: fakeServerInfo{}, DataDir: t.TempDir(), Env: func() []string { return nil }})
	v, err := ValidateBranding(goapi.NewObject("server_host", "[2001:db8::1]:21116", "use_https", false), nil)
	if err != nil || !v.Valid {
		t.Fatalf("%v %v", err, v.Errors)
	}
	finalized := s.FinalizeSupportBranding(context.Background(), v.Normalized)
	if got := finalized.Get("api_server"); got != "http://[2001:db8::1]:21114" {
		t.Errorf("profile's API address %v", got)
	}
	if got := s.buildAPIServer(goapi.NewObject("server_host", "::1", "use_https", true, "api_port", "443")); got != "https://[::1]" {
		t.Errorf("template build's API address %q", got)
	}
}
