package auditlog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The vectors in testdata are a fixed recording of the Node console's own
// lib/logRedact.js on Node 22, made before the Node console was removed.
// Each holds an input and what the Node function answered for it.

type vector struct {
	In  string `json:"in"`
	Out string `json:"out"`
}

type redactVectors struct {
	Username     []vector `json:"username"`
	Sanitize     []vector `json:"sanitize"`
	Details      []vector `json:"details"`
	URL          []vector `json:"url"`
	SensitiveKey []struct {
		In  string `json:"in"`
		Out bool   `json:"out"`
	} `json:"sensitive_key"`
}

func loadJSON(t testing.TB, name string, v any) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

func loadRedactVectors(t testing.TB) redactVectors {
	t.Helper()
	var file redactVectors
	loadJSON(t, "vectors.json", &file)
	return file
}

func replay(t *testing.T, name string, vectors []vector, atLeast int, fn func(string) string) {
	t.Helper()
	if len(vectors) < atLeast {
		t.Fatalf("%s: %d vectors, want at least %d", name, len(vectors), atLeast)
	}
	for _, v := range vectors {
		if got, want := fn(v.In), nodeOrFixed(name, v); got != want {
			t.Errorf("%s(%q) = %q, want %q (Node answers %q)", name, v.In, got, want, v.Out)
		}
	}
}

func TestRedactUsernameAgainstNode(t *testing.T) {
	replay(t, "RedactUsername", loadRedactVectors(t).Username, 300, RedactUsername)
}

func TestSanitizeLogValueAgainstNode(t *testing.T) {
	replay(t, "SanitizeLogValue", loadRedactVectors(t).Sanitize, 300, SanitizeLogValue)
}

func TestRedactDetailsAgainstNode(t *testing.T) {
	replay(t, "RedactDetails", loadRedactVectors(t).Details, 1000, RedactDetails)
}

func TestSensitiveDetailKeyAgainstNode(t *testing.T) {
	keys := loadRedactVectors(t).SensitiveKey
	if len(keys) < 30 {
		t.Fatalf("%d vectors, want at least 30", len(keys))
	}
	for _, v := range keys {
		if got := SensitiveDetailKey(v.In); got != v.Out {
			t.Errorf("SensitiveDetailKey(%q) = %v, Node answers %v", v.In, got, v.Out)
		}
	}
}

// TestRedactURLAgainstNode replays every URL. A host with letters outside
// ASCII or an "xn--" label is the one kind RedactURL does not rewrite the
// way Node does; for those the test checks that the user name and password
// are gone, and TestRedactURLInternationalHost pins what comes out.
func TestRedactURLAgainstNode(t *testing.T) {
	vectors := loadRedactVectors(t).URL
	if len(vectors) < 1000 {
		t.Fatalf("%d vectors, want at least 1000", len(vectors))
	}
	international, same := 0, 0
	for _, v := range vectors {
		got, internationalHost := redactURL(v.In)
		if !internationalHost {
			if want := nodeOrFixed("RedactURL", v); got != want {
				t.Errorf("RedactURL(%q) = %q, want %q (Node answers %q)", v.In, got, want, v.Out)
			}
			continue
		}
		international++
		if got == v.Out {
			same++
		}
		if !highByte.MatchString(v.In) && !strings.Contains(strings.ToLower(v.In), "xn--") {
			t.Errorf("RedactURL(%q) is counted as an international host, but nothing in it is one", v.In)
		}
		if strings.Contains(got, "u:p@") {
			t.Errorf("RedactURL(%q) = %q still holds the user name and password", v.In, got)
		}
	}
	if international == 0 || same == 0 || international > len(vectors)/20 {
		t.Errorf("%d of %d URLs count as international hosts, %d of them come out as in Node; want a few of each", international, len(vectors), same)
	}
}

// highByte finds a character outside ASCII, typed or percent-encoded.
var highByte = regexp.MustCompile(`[^\x00-\x7f]|%[89a-fA-F][0-9a-fA-F]`)
