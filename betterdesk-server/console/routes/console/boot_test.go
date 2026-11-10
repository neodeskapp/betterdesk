package console

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/unitronix/betterdesk-server/console/branding"
)

// The boot script is, byte for byte, the one the Node route sent for the
// same boot values (testdata/bootscript.json, recorded by
// testdata/nodeoracle/bootscript.js): the script itself, the values as
// JSON.stringify wrote them, and every "<" escaped.
func TestBootScriptAsNodeSentIt(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "bootscript.json"))
	if err != nil {
		t.Fatal(err)
	}
	var recorded struct {
		Vectors []struct {
			Boot json.RawMessage `json:"boot"`
			Body string          `json:"body"`
		} `json:"vectors"`
	}
	if err := json.Unmarshal(raw, &recorded); err != nil {
		t.Fatal(err)
	}
	if len(recorded.Vectors) < 50 {
		t.Fatalf("only %d vectors", len(recorded.Vectors))
	}
	for i, v := range recorded.Vectors {
		parsed, err := branding.ParseJSON(v.Boot)
		if err != nil {
			t.Fatalf("vector %d: %v", i, err)
		}
		boot := parsed.(*branding.Object)
		boot.Set("publicPaths", publicPaths)
		if got := string(bootScriptBody(boot)); got != v.Body {
			t.Errorf("vector %d (%s): the script differs\n  node: ...%s\n    go: ...%s", i, v.Boot, tail(v.Body), tail(got))
		}
	}
}

func tail(s string) string {
	if len(s) > 400 {
		return s[len(s)-400:]
	}
	return s
}
