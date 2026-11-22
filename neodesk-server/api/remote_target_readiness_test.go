package api

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

// readiness asks GET /api/remote-targets/readiness through the server's
// own routes.
func readiness(t *testing.T, srv *Server) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/remote-targets/readiness", nil)
	rec := httptest.NewRecorder()
	srv.handleRemoteTargetReadiness(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("readiness: %d %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("readiness answer %q: %v", rec.Body.String(), err)
	}
	return got
}

// L83: the readiness says whether a password can be saved (the vault has a
// key) and whether a session can start (guacd answers on its loopback
// address), so the target form can warn before either fails.
func TestRemoteTargetReadiness(t *testing.T) {
	closed, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closedAddr := closed.Addr().String()
	closed.Close()

	srv, _ := newInternalServer(t, nil)
	srv.cfg.GuacdAddress = closedAddr
	if got := readiness(t, srv); got["vault"] != false || got["guacd"] != false {
		t.Errorf("without a key and without guacd: %v, want vault false, guacd false", got)
	}

	gateway, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	go func() {
		for {
			conn, err := gateway.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()
	srv.cfg.GuacdAddress = gateway.Addr().String()
	if err := srv.InitRemoteTargetCredentialVault("a-key-of-sixteen-or-more"); err != nil {
		t.Fatal(err)
	}
	if got := readiness(t, srv); got["vault"] != true || got["guacd"] != true {
		t.Errorf("with a key and guacd: %v, want vault true, guacd true", got)
	}

	// The server talks to guacd on loopback only, so another address is
	// not ready, also when something answers there
	srv.cfg.GuacdAddress = "192.0.2.1:4822"
	if got := readiness(t, srv); got["guacd"] != false {
		t.Errorf("guacd on another address: %v, want guacd false", got)
	}
}

// The route answers the path itself; GET /api/remote-targets/{id} would
// take "readiness" for a target id and answer 404.
func TestRemoteTargetReadinessRoute(t *testing.T) {
	srv, _ := newInternalServer(t, nil)
	srv.cfg.GuacdAddress = ""
	status, body := listenerCall(t, srv, http.MethodGet, "/api/remote-targets/readiness", "", map[string]string{"X-API-Key": testViewerKey})
	if status != http.StatusOK {
		t.Fatalf("GET /api/remote-targets/readiness as a viewer: %d %s", status, body)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(body), &got); err != nil || got["guacd"] != false {
		t.Errorf("answer %q (%v), want guacd false", body, err)
	}
}
