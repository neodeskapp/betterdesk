package cdapproxy

import (
	"net/http"
	"path/filepath"
	"testing"

	"github.com/unitronix/betterdesk-server/console/ws"
	"github.com/unitronix/betterdesk-server/console/ws/wstest"
)

// oraclePermissions are the roles of the Node oracle (proxy.js).
var oraclePermissions = map[string][]string{
	"admin":        {"cdap.terminal", "cdap.files", "cdap.command"},
	"global_admin": {"cdap.terminal", "cdap.files", "cdap.command"},
	"operator":     {"cdap.command"},
}

// describeHandshake is what the oracle's API server noted of a
// handshake: the path, the subprotocol and the user headers, read as
// Latin-1 as Node read them.
func describeHandshake(r *http.Request) wstest.Event {
	header := func(name string) string {
		values := r.Header.Values(name)
		if len(values) == 0 {
			return "<none>"
		}
		return wstest.FromLatin1([]byte(values[0]))
	}
	return wstest.Event{
		"open":     r.URL.RequestURI(),
		"protocol": r.Header.Get("Sec-WebSocket-Protocol"),
		"user":     header("X-Username"),
		"role":     header("X-Role"),
	}
}

// TestNodeScenarios plays every scenario the Node oracle recorded
// (testdata/node_proxy.json) on the Go proxy: the checks and their order,
// who may open which socket, what reaches the API server's socket (path,
// subprotocol, user headers, each message with its kind), what comes
// back, the closes on either side, the messages that wait for the
// handshake, the API server refusing or failing.
func TestNodeScenarios(t *testing.T) {
	wstest.PlayProxy(t, filepath.Join("testdata", "node_proxy.json"), wstest.ProxySetup{
		Register: func(reg *ws.Registry, api http.Handler) {
			New(Options{API: api}).Register(reg)
		},
		Permissions: oraclePermissions,
		Describe:    describeHandshake,
	})
}
