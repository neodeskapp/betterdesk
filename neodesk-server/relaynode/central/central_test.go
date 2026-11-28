package central

import (
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/db"
)

func TestViaHasNode(t *testing.T) {
	for _, c := range []struct {
		via, node string
		want      bool
	}{
		{"node:n1", "n1", true},
		{"node:n1", "n2", false},
		{"node:n10", "n1", false},
		{"nodes:n1,n2", "n2", true},
		{"nodes:n1,n2", "n3", false},
		{"central", "n1", false},
		{"", "", false},
		{"node:", "", false},
	} {
		if got := viaHasNode(c.via, c.node); got != c.want {
			t.Errorf("viaHasNode(%q, %q) = %v, want %v", c.via, c.node, got, c.want)
		}
	}
}

// TestViaNodes: the nodes a session went through, for its live pair.
func TestViaNodes(t *testing.T) {
	for via, want := range map[string]string{
		"node:n1": "n1", "nodes:n1,n2": "n1,n2", "nodes:,n2,": "n2", "central": "", "": "", "node:": "",
	} {
		if got := strings.Join(viaNodes(via), ","); got != want {
			t.Errorf("viaNodes(%q) = %q, want %q", via, got, want)
		}
	}
}

// TestNodeSessionEndsReachTheHook: a node's end of a session reaches who
// listens, as the central relay's end does, and nobody once it is gone.
func TestNodeSessionEndsReachTheHook(t *testing.T) {
	g := &Glue{DB: noEnds{}}
	var got []string
	g.NodeSessionEnded("n1", "before", 0, 0)
	g.OnSessionEnded(func(uuid string) { got = append(got, uuid) })
	g.NodeSessionEnded("n1", "one", 0, 0)
	g.OnSessionEnded(nil)
	g.NodeSessionEnded("n1", "after", 0, 0)
	if strings.Join(got, ",") != "one" {
		t.Fatalf("the hook heard %v, want one", got)
	}
}

// noEnds is a database whose only call here, the end of a session record,
// does nothing.
type noEnds struct{ db.Database }

func (noEnds) EndPeerConnectionSessionByRelay(string) error { return nil }
