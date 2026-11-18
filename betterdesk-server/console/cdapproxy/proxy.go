// Package cdapproxy holds the sockets of an IoT device (CDAP) under
// /api/cdap/devices/<id>/: terminal, desktop, video, files and audio
// (services/cdapTerminalProxy.js and services/cdapMediaProxy.js of the
// old console). Each is passed on to the API server's socket of the same
// path, in this process, after the console's own checks: the origin, a
// signed-in session, and the channel's permission with the role
// overrides applied.
//
// The console does not check the device's scope and sets no limits of
// its own (BUGS SK1): the API server decides what the device does.
package cdapproxy

import (
	"context"
	"net/http"
	"strconv"

	"github.com/unitronix/betterdesk-server/console/ws"
	"github.com/unitronix/betterdesk-server/logging"
)

// failText is what the browser gets when the API server's socket fails.
const failText = `{"type":"error","error":"Server connection failed"}`

// Channel is one of the device's sockets.
type Channel struct {
	// Name is the last segment of the path.
	Name string
	// Subprotocol is offered to the API server.
	Subprotocol string
	// Permission is what the role needs.
	Permission string
	// Label names the socket in the log.
	Label string
	// Binary passes every message on as binary, both ways.
	Binary bool
}

// Channels are the device's sockets in the order the old console set
// them up: the terminal, then the media proxies. The desktop is remote
// control of the device, like the terminal: cdap.terminal; video and
// audio need cdap.command.
var Channels = []Channel{
	{Name: "terminal", Subprotocol: "cdap-terminal", Permission: "cdap.terminal", Label: "Terminal", Binary: true},
	{Name: "desktop", Subprotocol: "cdap-desktop", Permission: "cdap.terminal", Label: "Desktop"},
	{Name: "video", Subprotocol: "cdap-video", Permission: "cdap.command", Label: "Video"},
	{Name: "files", Subprotocol: "cdap-filebrowser", Permission: "cdap.files", Label: "FileBrowser"},
	{Name: "audio", Subprotocol: "cdap-audio", Permission: "cdap.command", Label: "Audio"},
}

// Options configure the proxy.
type Options struct {
	// API is the API server's handler for sockets
	// (goapi.Client.Handler): calls through it act as the console's API
	// key.
	API http.Handler
}

// Proxy passes the device sockets on to the API server.
type Proxy struct {
	api http.Handler
}

// New returns the proxy.
func New(opts Options) *Proxy {
	return &Proxy{api: opts.API}
}

// Register adds the five sockets. A device id is 1 to 64 letters, digits,
// _ and -; another path is not one of these sockets.
func (p *Proxy) Register(reg *ws.Registry) {
	for _, ch := range Channels {
		reg.Handle("/api/cdap/devices/{id:[A-Za-z0-9_-]{1,64}}/"+ch.Name, func(u *ws.Upgrade) { p.serve(u, ch) })
	}
}

// serve runs the checks in the old order, each a bare status line: a
// foreign origin 403, no signed-in session 401, the channel's permission
// missing or its lookup failing 403. Then the handshake, and the socket
// is passed on.
func (p *Proxy) serve(u *ws.Upgrade, ch Channel) {
	deviceID := u.Param("id")
	if !u.CheckOrigin("cdap-" + ch.Name + " " + u.Path) {
		return
	}
	session := u.Session()
	if !session.SignedIn() {
		logf("WARN: [CDAP %s] 401 upgrade rejected for %s (no session; ip=%s)", ch.Label, u.Path, u.SocketAddr())
		u.Refuse(http.StatusUnauthorized)
		return
	}
	role := session.Role()
	name := session.Username()
	if name == "" {
		name = "user#" + strconv.FormatInt(session.UserID(), 10)
	}
	allowed, err := u.RoleHasPermission(role, ch.Permission)
	if err != nil || !allowed {
		logf("WARN: [CDAP %s] 403 upgrade rejected for %s (role=%s needs %s)", ch.Label, u.Path, role, ch.Permission)
		u.Refuse(http.StatusForbidden)
		return
	}
	if (!headerText(name) || !headerText(role)) && u.HandshakeValid() {
		// The old proxy's HTTP client threw on such a header right after the
		// handshake, and the process ended before the answer went out (BUGS
		// SK2); this console drops the connection and goes on.
		logf("WARN: [CDAP %s] the account's name cannot be sent in a header, connection dropped", ch.Label)
		u.Drop()
		return
	}
	conn, ok := u.Accept(ws.AcceptOptions{})
	if !ok {
		return
	}
	defer conn.Wait()
	logf("[CDAP %s] Proxy started for device %s (role=%s)", ch.Label, deviceID, role)
	header := http.Header{}
	header.Set("X-Username", latin1(name))
	header.Set("X-Role", latin1(role))
	target := "/api/cdap/devices/" + deviceID + "/" + ch.Name
	ws.Pass(conn, ws.PassOptions{
		Dial: func(ctx context.Context) (*ws.Upstream, error) {
			return ws.DialHandler(ctx, p.api, target, ws.DialOptions{Header: header, Subprotocols: []string{ch.Subprotocol}})
		},
		Queue:    true,
		Binary:   ch.Binary,
		FailText: failText,
		OnFail: func(err error) {
			logf("ERROR: [CDAP %s] Go WS error for %s: %v", ch.Label, deviceID, err)
		},
	})
}

// headerText reports whether Node's HTTP client took s as a header value:
// tabs, printable ASCII and the characters U+0080 to U+00FF.
func headerText(s string) bool {
	for _, r := range s {
		if r != '\t' && (r < 0x20 || r == 0x7f || r > 0xff) {
			return false
		}
	}
	return true
}

// latin1 is s as Node's HTTP client wrote a header value: one byte per
// character, for a text headerText takes.
func latin1(s string) string {
	b := make([]byte, 0, len(s))
	for _, r := range s {
		b = append(b, byte(r))
	}
	return string(b)
}

func logf(format string, args ...any) { logging.Logf("console", format, args...) }
