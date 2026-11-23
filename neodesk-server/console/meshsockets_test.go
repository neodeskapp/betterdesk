package console

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/neodeskapp/neodesk/neodesk-server/console/ws/wstest"
)

// meshGateway is a made-up MeshCentral gateway: it notes the caller
// address and X-Forwarded-For of each socket it gets and accepts it.
type meshGateway struct {
	seen chan string
}

func (g *meshGateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/agent.ashx" {
		http.NotFound(w, r)
		return
	}
	g.seen <- r.RemoteAddr + " " + r.Header.Get("X-Forwarded-For")
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	_ = c.Close(websocket.StatusNormalClosure, "")
}

// TestMeshSocketsCarryTheClientsAddress: a MeshCentral socket reaches
// the gateway with the client's address, as TRUST_PROXY gives it, as the
// caller's, so the gateway's limit and blocklist per address count
// clients and not the console (BUGS.md SK6).
func TestMeshSocketsCarryTheClientsAddress(t *testing.T) {
	gw := &meshGateway{seen: make(chan string, 4)}
	w := newWorld(t, setup{api: gw, trustProxy: "loopback"})
	addr := strings.TrimPrefix(w.front.URL, "http://")
	for client, want := range map[string]string{
		"203.0.113.7": "203.0.113.7:0 203.0.113.7",
		"2001:db8::7": "[2001:db8::7]:0 2001:db8::7",
	} {
		c, res, err := wstest.Dial(addr, "/agent.ashx", http.Header{"X-Forwarded-For": {client}})
		if err != nil || c == nil {
			t.Fatalf("dial: %v %v", err, res)
		}
		select {
		case got := <-gw.seen:
			if got != want {
				t.Errorf("client %s: the gateway saw %q, want %q", client, got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("client %s: the gateway got no socket", client)
		}
		c.Drop()
	}
}
