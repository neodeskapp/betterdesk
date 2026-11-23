package generator

import (
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/internal/upstreamname"
)

// The recordings hold the default names of a support agent with
// NeoDesk's names, but Node made its hashes with upstream's, which can't
// be mapped. These helpers check a recorded hash under upstream's names
// and give what it is with NeoDesk's.

// neoDeskBrandingHash returns the hash the recorded branding has with
// NeoDesk's names. The recorded hash must be Node's hash of that
// branding, as recorded or with upstream's names.
func neoDeskBrandingHash(t *testing.T, i int, normalized, recorded string) string {
	t.Helper()
	v, err := parseJSON(normalized)
	if err != nil {
		t.Fatalf("case %d: %v", i, err)
	}
	o, _ := v.(*goapi.Object)
	if o == nil || HashBranding(o) == recorded {
		return recorded
	}
	u, err := parseJSON(upstreamname.Names(normalized))
	if err != nil {
		t.Fatalf("case %d: %v", i, err)
	}
	if HashBranding(u.(*goapi.Object)) != recorded {
		t.Errorf("case %d: the recorded hash %s is no hash of the branding, with NeoDesk's names or upstream's", i, recorded)
		return recorded
	}
	return HashBranding(o)
}
