package meshproxy

import (
	"net/http"
	"path/filepath"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/ws"
	"github.com/neodeskapp/neodesk/neodesk-server/console/ws/wstest"
)

// describeHandshake is what the oracle's API server noted of a
// handshake: the path with the query, the subprotocol and
// X-Forwarded-For.
func describeHandshake(r *http.Request) wstest.Event {
	header := func(name string) string {
		values := r.Header.Values(name)
		if len(values) == 0 {
			return "<none>"
		}
		return values[0]
	}
	return wstest.Event{
		"open":      r.RequestURI,
		"protocol":  header("Sec-WebSocket-Protocol"),
		"forwarded": header("X-Forwarded-For"),
	}
}

// TestNodeScenarios plays every scenario the Node oracle recorded
// (testdata/node_proxy.json) on the Go proxy: the origin and session
// checks, the path and query that reach the gateway, the address in
// X-Forwarded-For (the connection's, not the client's own header), the
// messages both ways with their kind, the messages lost before the
// gateway answers, closes and failures on either side.
func TestNodeScenarios(t *testing.T) {
	wstest.PlayProxy(t, filepath.Join("testdata", "node_proxy.json"), wstest.ProxySetup{
		Register: func(reg *ws.Registry, api http.Handler) {
			New(Options{API: api}).Register(reg)
		},
		Describe:    describeHandshake,
		GreetBinary: true,
	})
}
