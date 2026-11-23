package api

import (
	"net/http"
	"testing"

	eventsModule "github.com/neodeskapp/neodesk/neodesk-server/events"
	"github.com/neodeskapp/neodesk/neodesk-server/meshcentral"
	"github.com/neodeskapp/neodesk/neodesk-server/peer"
)

// TestShareValidateRefusalIsBoolean: a share link that is not valid is
// refused with "valid": false as a boolean, as a valid one says true
// (BUGS G14).
func TestShareValidateRefusalIsBoolean(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	// The gateway writes its agent certificate into the working directory
	t.Chdir(t.TempDir())
	gw, err := meshcentral.NewGateway(srv.cfg, database, peer.NewMap(), eventsModule.NewBus(), "test-jwt-secret-0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gw.Stop)
	srv.SetMeshGateway(gw)
	status, body := asConsole(t, srv, http.MethodGet, "/api/mesh/share/validate?peer_id=PEERAAA1&token=abc", "")
	if want := `{"error":"invalid or expired share link","valid":false}`; status != http.StatusForbidden || body != want {
		t.Errorf("unknown share link: %d %s, want 403 %s", status, body, want)
	}
}

// TestMSHRefusesAnInvalidMeshID: a mesh_id that is no mesh id is refused
// instead of the file naming the default group without a word (L383).
func TestMSHRefusesAnInvalidMeshID(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	t.Chdir(t.TempDir())
	gw, err := meshcentral.NewGateway(srv.cfg, database, peer.NewMap(), eventsModule.NewBus(), "test-jwt-secret-0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gw.Stop)
	srv.SetMeshGateway(gw)
	if status, body := asConsole(t, srv, http.MethodGet, "/api/mesh/download.msh?mesh_id=not-a-mesh-id", ""); status != http.StatusBadRequest {
		t.Errorf("an invalid mesh_id: %d %s, want 400", status, body)
	}
	if status, body := asConsole(t, srv, http.MethodGet, "/api/mesh/download.msh", ""); status != http.StatusOK {
		t.Errorf("no mesh_id: %d %s, want 200 with the default group", status, body)
	}
}
