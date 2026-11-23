package remotetarget

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/ws"
	"github.com/neodeskapp/neodesk/neodesk-server/console/ws/wstest"
)

// A permission lookup that fails counts as no, whatever it answered.
func TestFailedPermissionLookupIsNo(t *testing.T) {
	check := wstest.Goroutines(t)
	w := &world{}
	g := New(w.options())
	reg := ws.New(ws.Options{Session: wstest.OracleSession, Permission: func(context.Context, string, string) (bool, error) {
		return true, errors.New("roles down")
	}})
	g.Register(reg)
	srv := httptest.NewServer(reg)
	c, res, err := wstest.Dial(strings.TrimPrefix(srv.URL, "http://"), "/ws/remote-target/rt_echo-0001", operator)
	if err != nil {
		t.Fatal(err)
	}
	if c != nil || res.StatusCode != http.StatusForbidden {
		t.Errorf("answered %v, want 403", res.Status)
	}
	srv.Close()
	reg.Close()
	check()
}
