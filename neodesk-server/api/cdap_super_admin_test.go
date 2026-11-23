package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
	"github.com/neodeskapp/neodesk/neodesk-server/cdap"
	"github.com/neodeskapp/neodesk/neodesk-server/config"
	eventsModule "github.com/neodeskapp/neodesk/neodesk-server/events"
	"github.com/neodeskapp/neodesk/neodesk-server/peer"
)

// TestCDAPAdminRoutesTakeSuperAdmin: the delegation routes and the CDAP
// terminal, desktop and file sockets let a super_admin in like the legacy
// admin (RV188).
func TestCDAPAdminRoutesTakeSuperAdmin(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	srv.SetCDAPGateway(cdap.New(config.DefaultConfig(), database, peer.NewMap(), eventsModule.NewBus()))
	super := Identity{Username: "rv_super", Role: auth.RoleSuperAdmin}

	if status, body := callAs(t, srv, super, http.MethodGet, "/api/cdap/delegations", ""); status != http.StatusOK {
		t.Errorf("GET /api/cdap/delegations as super_admin: %d %s, want 200", status, body)
	}
	for _, path := range []string{"/api/cdap/devices/CDAPSA01/terminal", "/api/cdap/devices/CDAPSA01/desktop", "/api/cdap/devices/CDAPSA01/files"} {
		if status, body := callAs(t, srv, super, http.MethodGet, path, ""); status == http.StatusForbidden || strings.Contains(body, "requires admin role") {
			t.Errorf("GET %s as super_admin: %d %s, want past the role check", path, status, body)
		}
	}
}
