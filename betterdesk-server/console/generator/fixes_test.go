package generator

import (
	"testing"

	"github.com/unitronix/betterdesk-server/console/goapi"
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
