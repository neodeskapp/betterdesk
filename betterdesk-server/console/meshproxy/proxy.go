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
	"net/http"

	"github.com/unitronix/betterdesk-server/console/ws"
	"github.com/unitronix/betterdesk-server/logging"
)

// Paths are the sockets, in the order the old console listed them.
var Paths = []string{"/agent.ashx", "/meshrelay.ashx", "/control.ashx"}

// Options configure the proxy.
type Options struct {
	// API is the API server's handler for sockets
	// (goapi.Client.Handler).
	API http.Handler
}

// Proxy passes the MeshCentral sockets on to the API server.
type Proxy struct {
	api http.Handler
}

// New returns the proxy.
func New(opts Options) *Proxy {
	return &Proxy{api: opts.API}
}

// Register adds the three sockets.
func (p *Proxy) Register(reg *ws.Registry) {
	for _, path := range Paths {
		reg.Handle(path, p.serve)
	}
}

// serve runs the checks, each a bare status line: a foreign origin 403,
// and for /control.ashx no signed-in session 401. Then the handshake, and
// the socket is passed on with the address it came from in
// X-Forwarded-For. Messages that arrive before the gateway has answered
// are dropped. The browser's subprotocols are not passed on.
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
	header := http.Header{}
	header.Set("X-Forwarded-For", u.SocketAddr())
	ws.Pass(conn, ws.PassOptions{
		Dial: func(ctx context.Context) (*ws.Upstream, error) {
			return ws.DialHandler(ctx, p.api, target, ws.DialOptions{Header: header})
		},
		OnFail: func(err error) {
			logging.Logf("console", "WARN: [mesh proxy] %s %v", u.Path, err)
		},
	})
}
