package api

import (
	"net/http"
	"strings"
	"testing"

	eventsModule "github.com/neodeskapp/neodesk/neodesk-server/events"
	"github.com/neodeskapp/neodesk/neodesk-server/meshcentral"
	"github.com/neodeskapp/neodesk/neodesk-server/peer"
)

// The default group can't be removed: the server made it again with a new
// MeshID, so every .msh file already handed out named a group that was
// gone. A group ID that is empty or has spaces, which the hint rules out,
// is refused too (BUGS L384).
func TestMeshGroupsKeepTheDefaultAndRefuseBadIDs(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	// The gateway writes its agent certificate into the working directory
	t.Chdir(t.TempDir())
	gw, err := meshcentral.NewGateway(srv.cfg, database, peer.NewMap(), eventsModule.NewBus(), "test-jwt-secret-0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gw.Stop)
	srv.SetMeshGateway(gw)
	status, body := asConsole(t, srv, http.MethodGet, "/api/mesh/groups", "")
	if status != http.StatusOK || !strings.Contains(body, `"id":"default"`) {
		t.Fatalf("groups: %d %s", status, body)
	}
	for _, c := range []struct{ body, want string }{
		{`{"groups":[{"id":"office","name":"Office"}]}`, "The default group can't be removed"},
		{`{"groups":[{"id":"default","name":"Default"},{"id":"","name":"Empty"}]}`, "Invalid group ID"},
		{`{"groups":[{"id":"default","name":"Default"},{"id":"lab group/ä?","name":"Lab"}]}`, "Invalid group ID"},
	} {
		status, body := asConsole(t, srv, http.MethodPost, "/api/mesh/groups", c.body)
		if status != http.StatusBadRequest || !strings.Contains(body, c.want) {
			t.Errorf("%s: %d %s", c.body, status, body)
		}
	}
	before := srv.meshGw.DefaultMeshIDHex()
	if status, body := asConsole(t, srv, http.MethodPost, "/api/mesh/groups", `{"groups":[{"id":"default","name":"Default"},{"id":"lab-2.x_y","name":"Lab"}]}`); status != http.StatusOK {
		t.Errorf("good groups: %d %s", status, body)
	}
	// saved again without its MeshID, the default keeps the one it has
	if after := srv.meshGw.DefaultMeshIDHex(); after != before {
		t.Errorf("the default group's MeshID changed from %s to %s", before, after)
	}
}
