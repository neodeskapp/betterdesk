// Package meshproxy holds the MeshCentral sockets of the console:
// /agent.ashx, /meshrelay.ashx and /control.ashx
// (services/meshAshxProxy.js of the old console). Each is passed on to
// the API server's MeshCentral gateway at the same path and query, in
// this process. The console checks the origin, and for /control.ashx a
// signed-in session of any role; the agent and relay sockets need no
// session, the gateway checks the agent and the relay cookie itself.
package meshproxy

import (
	"context"
	"net"
	"net/http"

	"github.com/neodeskapp/neodesk/neodesk-server/console/ws"
	"github.com/neodeskapp/neodesk/neodesk-server/logging"
)

// Paths are the sockets, in the order the old console listed them.
var Paths = []string{"/agent.ashx", "/meshrelay.ashx", "/control.ashx"}

// Options configure the proxy.
type Options struct {
	// API is the API server's handler for sockets that keeps the caller
	// address a socket brings (goapi.Client.ForwardHandler).
	API http.Handler
	// ClientIP is the client's address under TRUST_PROXY
	// (httpx.TrustProxy.ClientIP). nil takes the connection's address.
	ClientIP func(r *http.Request) string
}

// Proxy passes the MeshCentral sockets on to the API server.
type Proxy struct {
	api      http.Handler
	clientIP func(r *http.Request) string
}

// New returns the proxy.
func New(opts Options) *Proxy {
	return &Proxy{api: opts.API, clientIP: opts.ClientIP}
}

// Register adds the three sockets.
func (p *Proxy) Register(reg *ws.Registry) {
	for _, path := range Paths {
		reg.Handle(path, p.serve)
	}
}

// serve runs the checks, each a bare status line: a foreign origin 403,
// and for /control.ashx no signed-in session 401. Then the handshake, and
// the socket is passed on with the client's address as its caller and in
// X-Forwarded-For, so the gateway's limit and blocklist per address count
// clients (DECISIONS A60). Messages that arrive before the gateway has
// answered are dropped. The browser's subprotocols are not passed on.
func (p *Proxy) serve(u *ws.Upgrade) {
	if !u.CheckOrigin("mesh-" + u.Path) {
		return
	}
	if u.Path == "/control.ashx" && !u.Session().SignedIn() {
		u.Refuse(http.StatusUnauthorized)
		return
	}
	conn, ok := u.Accept(ws.AcceptOptions{})
	if !ok {
		return
	}
	defer conn.Wait()
	target := u.Path
	if q := u.Query(); q != "" {
		target += "?" + q
	}
	client := u.SocketAddr()
	if p.clientIP != nil {
		client = p.clientIP(u.R)
	}
	header := http.Header{}
	header.Set("X-Forwarded-For", client)
	dial := ws.DialOptions{Header: header, RemoteAddr: net.JoinHostPort(client, "0")}
	ws.Pass(conn, ws.PassOptions{
		Dial: func(ctx context.Context) (*ws.Upstream, error) {
			return ws.DialHandler(ctx, p.api, target, dial)
		},
		OnFail: func(err error) {
			logging.Logf("console", "WARN: [mesh proxy] %s %v", u.Path, err)
		},
	})
}
