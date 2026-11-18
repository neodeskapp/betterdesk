// Package remotetarget is the browser's way to an RDP or VNC remote target
// (services/remoteTargetGateway.js of the old console): the socket
// /ws/remote-target/<id> checks the account and passes every message on
// to the API server's tunnel of the target, which looks the target up,
// decrypts its credentials and talks to guacd, and passes the tunnel's
// messages back.
//
// The old console dialed the tunnel over the network with the API key,
// which decides the role at the API server. This one opens it on the API
// server's handler in the same process (ws.DialHandler on
// goapi.Client.Handler), which carries the console's identity, as the
// CDAP proxies do. The API server reads neither X-Username nor X-Role
// (BUGS G7); they are sent as before.
package remotetarget

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/session"
	"github.com/unitronix/betterdesk-server/console/ws"
	"github.com/unitronix/betterdesk-server/logging"
)

// MaxPayload is the largest message either side may send.
const MaxPayload = 16 << 20

// Options configure the gateway.
type Options struct {
	// RemoteTarget looks a target up at the API server
	// (betterdeskApi.getRemoteTarget; goapi.Client.GetRemoteTarget); nil
	// is no target.
	RemoteTarget func(ctx context.Context, id string) *goapi.Object
	// CanAccess is the account's device scope
	// (deviceGroupService.userCanAccessDevice;
	// devicegroups.Service.UserCanAccessDevice). An error answers 502.
	CanAccess func(ctx context.Context, user *session.User, device *goapi.Object) (bool, error)
	// API is the API server's handler the tunnel is opened on
	// (goapi.Client.Handler).
	API http.Handler
}

// Gateway is the remote target socket.
type Gateway struct {
	opts Options
}

// New returns the gateway. Register adds its socket.
func New(opts Options) *Gateway {
	return &Gateway{opts: opts}
}

// Register adds /ws/remote-target/{id}.
func (g *Gateway) Register(reg *ws.Registry) {
	reg.Handle("/ws/remote-target/{id:rt_[A-Za-z0-9-]{8,80}}", g.serve)
}

// refusal is the gateway's own refusal: the status line and Connection:
// close.
func refusal(status int) string {
	return "HTTP/1.1 " + strconv.Itoa(status) + " " + http.StatusText(status) + "\r\nConnection: close\r\n\r\n"
}

// serve checks the upgrade in the old order: the origin (403 with a bare
// status line), a signed-in account whose role has
// remote_target.connect (403, also without a session and when the lookup
// fails), a target the API server knows and the account's scope reaches
// (404 for both), a failed scope check (502). These three answers carry
// Connection: close.
func (g *Gateway) serve(u *ws.Upgrade) {
	id := u.Param("id")
	if !u.CheckOrigin("remote-target " + id) {
		return
	}
	sn := u.Session()
	role := sn.Role()
	allowed := false
	if sn.SignedIn() {
		if ok, err := u.RoleHasPermission(role, "remote_target.connect"); err == nil {
			allowed = ok
		}
	}
	if !allowed {
		u.RefuseRaw(refusal(http.StatusForbidden))
		return
	}
	user := sn.User()
	target := g.opts.RemoteTarget(u.R.Context(), id)
	if target == nil {
		u.RefuseRaw(refusal(http.StatusNotFound))
		return
	}
	visible, err := g.opts.CanAccess(u.R.Context(), user, target)
	if err != nil {
		logging.Logf("console", "WARN: [remote-target] upgrade rejected: %v", err)
		u.RefuseRaw(refusal(http.StatusBadGateway))
		return
	}
	if !visible {
		u.RefuseRaw(refusal(http.StatusNotFound))
		return
	}
	username := ""
	if user != nil {
		username = user.Username
	}
	if username == "" {
		username = "user#" + strconv.FormatInt(sn.UserID(), 10)
	}
	if (!headerText(username) || !headerText(role)) && u.HandshakeValid() {
		// The old client threw on such a header while it was being made,
		// right after the handshake was answered; the upgrade's catch then
		// wrote its 502 on the same connection and ended it (BUGS RM11).
		logging.Logf("console", "WARN: [remote-target] upgrade rejected: the account's name cannot be sent in a header")
		u.RefuseRaw(switchingHead(u.R) + refusal(http.StatusBadGateway))
		return
	}
	browser, ok := u.Accept(ws.AcceptOptions{MaxPayload: MaxPayload})
	if !ok {
		return
	}
	defer browser.Wait()
	header := http.Header{}
	header.Set("X-Username", latin1(username))
	header.Set("X-Role", latin1(role))
	tunnel := "/api/remote-targets/" + goapi.EncodeURIComponent(id) + "/tunnel"
	// Messages wait while the tunnel opens; a failed tunnel or one that
	// ends closes the browser's socket without a status code, and the
	// browser's end closes the tunnel the same way. Close codes are not
	// passed on.
	ws.Pass(browser, ws.PassOptions{
		Dial: func(ctx context.Context) (*ws.Upstream, error) {
			return ws.DialHandler(ctx, g.opts.API, tunnel, ws.DialOptions{Header: header, MaxPayload: MaxPayload})
		},
		Queue: true,
		OnFail: func(err error) {
			logging.Logf("console", "WARN: [remote-target] upstream error: %v", err)
		},
	})
}

// acceptGUID is the websocket handshake's fixed key suffix (RFC 6455).
const acceptGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// switchingHead is the head of the 101 answer as the websocket library
// wrote it: its four lines, the subprotocol when the client offered one
// (the first), an empty line.
func switchingHead(r *http.Request) string {
	sum := sha1.Sum([]byte(strings.TrimSpace(strings.Join(r.Header.Values("Sec-WebSocket-Key"), ", ")) + acceptGUID))
	head := "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " +
		base64.StdEncoding.EncodeToString(sum[:]) + "\r\n"
	if offered := strings.Join(r.Header.Values("Sec-WebSocket-Protocol"), ", "); offered != "" {
		first, _, _ := strings.Cut(offered, ",")
		head += "Sec-WebSocket-Protocol: " + strings.TrimSpace(first) + "\r\n"
	}
	return head + "\r\n"
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
