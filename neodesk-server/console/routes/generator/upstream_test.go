package generator_test

import (
	"regexp"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/generator"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/internal/upstreamname"
)

// brandingHashes turns the hashes of a branding in the recorded answers
// into the ones the console makes. Node hashed each branding with
// upstream's names (the default product type and app name of a support
// agent), which the recording can't map, while the answers show the
// brandings with NeoDesk's names. learn finds each branding of an answer
// and keeps Node's hash of it with upstream's names against its hash
// with NeoDesk's; translate puts the one in place of the other.
type brandingHashes map[string]string

func (h brandingHashes) learn(t *testing.T, answer string) {
	t.Helper()
	v, err := generator.Parse(answer)
	if err != nil {
		return
	}
	u, err := generator.Parse(upstreamname.Names(answer))
	if err != nil {
		t.Fatal(err)
	}
	// the same answer twice, with NeoDesk's names and with upstream's,
	// read as Node read it (lone surrogates stay)
	var walk func(v, u any)
	walk = func(v, u any) {
		switch x := v.(type) {
		case *goapi.Object:
			y, _ := u.(*goapi.Object)
			if y == nil {
				t.Fatalf("the answer has another shape with upstream's names: %s", answer)
			}
			if b, ub := x.Object("branding"), y.Object("branding"); b != nil && ub != nil {
				if up, nd := generator.HashBranding(ub), generator.HashBranding(b); up != nd {
					h[up] = nd
				}
			}
			for _, key := range x.Keys() {
				walk(x.Get(key), y.Get(key))
			}
		case []any:
			y, _ := u.([]any)
			if len(y) != len(x) {
				t.Fatalf("the answer has another shape with upstream's names: %s", answer)
			}
			for i := range x {
				walk(x[i], y[i])
			}
		}
	}
	walk(v, u)
}

var hashText = regexp.MustCompile(`"[0-9a-f]{64}"`)

func (h brandingHashes) translate(answer string) string {
	return hashText.ReplaceAllStringFunc(answer, func(m string) string {
		if nd, ok := h[m[1:len(m)-1]]; ok {
			return `"` + nd + `"`
		}
		return m
	})
}
