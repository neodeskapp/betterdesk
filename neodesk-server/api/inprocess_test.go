package api

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
)

// consoleBridge is the identity the console's in-process calls carry.
var consoleBridge = Identity{Username: "apikey:console-bridge", Role: auth.RoleAdmin}

// callAs sends a request through InternalHandler as id, from loopback,
// and returns the status and the body without its trailing newline.
func callAs(t *testing.T, srv *Server, id Identity, method, path, body string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "127.0.0.1:50000"
	req = req.WithContext(WithIdentity(req.Context(), id))
	rec := httptest.NewRecorder()
	srv.InternalHandler().ServeHTTP(rec, req)
	data, err := io.ReadAll(rec.Result().Body)
	if err != nil {
		t.Fatal(err)
	}
	return rec.Code, strings.TrimSpace(string(data))
}

// asConsole is callAs with the console's identity.
func asConsole(t *testing.T, srv *Server, method, path, body string) (int, string) {
	t.Helper()
	return callAs(t, srv, consoleBridge, method, path, body)
}
