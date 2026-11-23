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
// CDAP proxies do. The account is not named to the tunnel: the API server
// reads no X-Username or X-Role (BUGS G7). The account's client features
// narrow the session: without remote control it is read only, without
// the clipboard guacd copies nothing either way, without audio there is
// no sound (tunnelPath). The gateway never maps drives or printers, so
// there is no file transfer to switch off.
package remotetarget

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/session"
	"github.com/neodeskapp/neodesk/neodesk-server/console/ws"
	"github.com/neodeskapp/neodesk/neodesk-server/logging"
)

// MaxPayload is the largest message either side may send.
const MaxPayload = 16 << 20

// SessionCheck is how often an open socket's session, permission and
// target scope are looked at again.
const SessionCheck = 10 * time.Second

// Options configure the gateway.
type Options struct {
	// RemoteTarget looks a target up at the API server
	// (neodeskApi.getRemoteTarget; goapi.Client.GetRemoteTarget); nil
	// is no target.
	RemoteTarget func(ctx context.Context, id string) *goapi.Object
	// CanAccess is the account's device scope
	// (deviceGroupService.userCanAccessDevice;
	// devicegroups.Service.UserCanAccessDevice). An error answers 502.
	CanAccess func(ctx context.Context, user *session.User, device *goapi.Object) (bool, error)
	// FeaturesOff returns the client features the account may not use
	// (access.Service.ClientFeaturesOff); nil turns no feature off.
	FeaturesOff func(ctx context.Context, userID int64) ([]string, error)
	// API is the API server's handler the tunnel is opened on
	// (goapi.Client.Handler).
	API http.Handler
	// SessionCheck overrides SessionCheck (tests).
	SessionCheck time.Duration
}

// Gateway is the remote target socket.
type Gateway struct {
	opts Options
}

// New returns the gateway. Register adds its socket.
func New(opts Options) *Gateway {
	if opts.SessionCheck <= 0 {
		opts.SessionCheck = SessionCheck
	}
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
	browser, ok := u.Accept(ws.AcceptOptions{MaxPayload: MaxPayload})
	if !ok {
		return
	}
	defer browser.Wait()
	stopWatch := g.watch(u, browser, sn.UserID(), id)
	defer stopWatch()
	tunnel := g.tunnelPath(u.R.Context(), sn.UserID(), id)
	// Messages wait while the tunnel opens; a failed tunnel or one that
	// ends closes the browser's socket without a status code, and the
	// browser's end closes the tunnel the same way. Close codes are not
	// passed on.
	ws.Pass(browser, ws.PassOptions{
		Dial: func(ctx context.Context) (*ws.Upstream, error) {
			return ws.DialHandler(ctx, g.opts.API, tunnel, ws.DialOptions{MaxPayload: MaxPayload})
		},
		Queue: true,
		OnFail: func(err error) {
			logging.Logf("console", "WARN: [remote-target] upstream error: %v", err)
		},
	})
}

// watch looks at the socket's session every SessionCheck while it is open,
// and closes it with 1008 "Session ended" (which ends the tunnel) once the
// session has ended or belongs to another account, its role no longer has
// remote_target.connect, or the target is gone or outside the account's
// scope. A failed lookup counts as no, as at the upgrade. The returned
// function stops the watch and waits for it.
func (g *Gateway) watch(u *ws.Upgrade, browser *ws.Conn, userID int64, id string) (stop func()) {
	done, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		tick := time.NewTicker(g.opts.SessionCheck)
		defer tick.Stop()
		for {
			select {
			case <-done:
				return
			case <-browser.Context().Done():
				return
			case <-tick.C:
				if !g.stillAllowed(u, browser.Context(), userID, id) {
					browser.Close(1008, "Session ended")
					return
				}
			}
		}
	}()
	return func() {
		close(done)
		<-stopped
	}
}

// stillAllowed reads the socket's session again and runs the checks of the
// upgrade on it.
func (g *Gateway) stillAllowed(u *ws.Upgrade, parent context.Context, userID int64, id string) bool {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	check := *u
	check.R = u.R.WithContext(ctx)
	sn := check.Session()
	if !sn.SignedIn() || sn.UserID() != userID {
		return false
	}
	if allowed, err := check.RoleHasPermission(sn.Role(), "remote_target.connect"); err != nil || !allowed {
		return false
	}
	target := g.opts.RemoteTarget(ctx, id)
	if target == nil {
		return false
	}
	visible, err := g.opts.CanAccess(ctx, sn.User(), target)
	return err == nil && visible
}

// everyFeature is what an account may not use when its features cannot be
// looked up.
var everyFeature = []string{"remote_control", "file_transfer", "clipboard", "audio", "tunnel", "terminal", "reboot", "privacy_mode"}

// tunnelPath is the API server's tunnel of the target, with the limits the
// account's client features set: read_only without remote control,
// no_clipboard without the clipboard, no_audio without audio. A failed
// lookup sets them all.
func (g *Gateway) tunnelPath(ctx context.Context, userID int64, id string) string {
	path := "/api/remote-targets/" + goapi.EncodeURIComponent(id) + "/tunnel"
	if g.opts.FeaturesOff == nil {
		return path
	}
	off, err := g.opts.FeaturesOff(ctx, userID)
	if err != nil {
		logging.Logf("console", "WARN: [remote-target] client features of user %d: %v; the session is read only", userID, err)
		off = everyFeature
	}
	var limits []string
	for _, l := range []struct{ feature, param string }{
		{"remote_control", "read_only"}, {"clipboard", "no_clipboard"}, {"audio", "no_audio"},
	} {
		if slices.Contains(off, l.feature) {
			limits = append(limits, l.param+"=1")
		}
	}
	if len(limits) == 0 {
		return path
	}
	return path + "?" + strings.Join(limits, "&")
}
