// Package cdapproxy holds the sockets of an IoT device (CDAP) under
// /api/cdap/devices/<id>/: terminal, desktop, video, files and audio
// (services/cdapTerminalProxy.js and services/cdapMediaProxy.js of the
// old console). Each is passed on to the API server's socket of the same
// path, in this process, after the console's own checks: the origin, a
// signed-in session, the channel's permission with the role overrides
// applied, the account's device scope, the account's client features
// (features.go), and a limit of sockets per account. An open socket is
// pinged, and closed when its client stops answering (limits.go).
package cdapproxy

import (
	"context"
	"net/http"
	"slices"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/session"
	"github.com/neodeskapp/neodesk/neodesk-server/console/ws"
	"github.com/neodeskapp/neodesk/neodesk-server/logging"
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
	// Feature is the client feature the channel needs; "" for none.
	Feature string
}

// Channels are the device's sockets in the order the old console set
// them up: the terminal, then the media proxies. The desktop is remote
// control of the device, like the terminal: cdap.terminal; video and
// audio need cdap.command.
var Channels = []Channel{
	{Name: "terminal", Subprotocol: "cdap-terminal", Permission: "cdap.terminal", Label: "Terminal", Binary: true, Feature: "terminal"},
	{Name: "desktop", Subprotocol: "cdap-desktop", Permission: "cdap.terminal", Label: "Desktop"},
	{Name: "video", Subprotocol: "cdap-video", Permission: "cdap.command", Label: "Video"},
	{Name: "files", Subprotocol: "cdap-filebrowser", Permission: "cdap.files", Label: "FileBrowser", Feature: "file_transfer"},
	{Name: "audio", Subprotocol: "cdap-audio", Permission: "cdap.command", Label: "Audio", Feature: "audio"},
}

// Options configure the proxy.
type Options struct {
	// API is the API server's handler for sockets
	// (goapi.Client.Handler): calls through it act as the console's API
	// key.
	API http.Handler
	// Devices looks the device of a socket up (goapi.Backend).
	Devices Devices
	// CanAccess is the account's device scope
	// (devicegroups.Service.UserCanAccessDevice).
	CanAccess func(ctx context.Context, user *session.User, device *goapi.Object) (bool, error)
	// FeaturesOff returns the client features the account may not use
	// (access.Service.ClientFeaturesOff); nil turns no feature off.
	FeaturesOff func(ctx context.Context, userID int64) ([]string, error)
	// PingInterval overrides PingInterval (tests).
	PingInterval time.Duration
}

// Devices finds a device by id; nil when there is none.
type Devices interface {
	GetDeviceByID(ctx context.Context, id string, includeDeleted bool) *goapi.Object
}

// Proxy passes the device sockets on to the API server.
type Proxy struct {
	opts Options
	open sockets
}

// New returns the proxy.
func New(opts Options) *Proxy {
	if opts.PingInterval <= 0 {
		opts.PingInterval = PingInterval
	}
	return &Proxy{opts: opts}
}

// Register adds the five sockets. A device id is 1 to 64 letters, digits,
// _ and -; another path is not one of these sockets.
func (p *Proxy) Register(reg *ws.Registry) {
	for _, ch := range Channels {
		reg.Handle("/api/cdap/devices/{id:[A-Za-z0-9_-]{1,64}}/"+ch.Name, func(u *ws.Upgrade) { p.serve(u, ch) })
	}
}

// serve runs the checks, each a bare status line: a foreign origin 403,
// no signed-in session 401, the channel's permission missing or its
// lookup failing 403 (the old proxy's checks, in its order); then a
// device that is unknown or outside the account's scope 404, the scope
// check failing 403, a channel whose client feature the account may not
// use 403, the account's MaxSocketsPerAccount sockets of the channel open
// 429. Then the handshake, and the socket is passed on; the desktop's
// messages are filtered by the account's features (desktopFilters).
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
	allowed, err := u.RoleHasPermission(role, ch.Permission)
	if err != nil || !allowed {
		logf("WARN: [CDAP %s] 403 upgrade rejected for %s (role=%s needs %s)", ch.Label, u.Path, role, ch.Permission)
		u.Refuse(http.StatusForbidden)
		return
	}
	if status := p.inScope(u.R.Context(), session.User(), deviceID); status != 0 {
		logf("WARN: [CDAP %s] %d upgrade rejected for %s (device scope)", ch.Label, status, u.Path)
		u.Refuse(status)
		return
	}
	off := p.featuresOff(u.R.Context(), session.UserID())
	if ch.Feature != "" && slices.Contains(off, ch.Feature) {
		logf("WARN: [CDAP %s] 403 upgrade rejected for %s (client feature %s is off for the account)", ch.Label, u.Path, ch.Feature)
		u.Refuse(http.StatusForbidden)
		return
	}
	key := accountChannel{account: session.UserID(), channel: ch.Name}
	if !p.open.take(key) {
		logf("WARN: [CDAP %s] 429 upgrade rejected for %s (%d sockets open)", ch.Label, u.Path, MaxSocketsPerAccount)
		u.Refuse(http.StatusTooManyRequests)
		return
	}
	defer p.open.release(key)
	var client heard
	client.listen(u)
	conn, ok := u.Accept(ws.AcceptOptions{})
	if !ok {
		return
	}
	defer conn.Wait()
	conn.KeepAlive(p.opts.PingInterval)
	defer client.closeSilent(conn, 2*p.opts.PingInterval)()
	logf("[CDAP %s] Proxy started for device %s (role=%s)", ch.Label, deviceID, role)
	target := "/api/cdap/devices/" + deviceID + "/" + ch.Name
	account := ""
	if user := session.User(); user != nil {
		account = user.Username
	}
	pass := ws.PassOptions{
		// The API server records the account as the one who started the
		// session (goapi.ActingFor)
		Dial: func(ctx context.Context) (*ws.Upstream, error) {
			return ws.DialHandler(goapi.ActingFor(ctx, account), p.opts.API, target, ws.DialOptions{Subprotocols: []string{ch.Subprotocol}})
		},
		Queue:    true,
		Binary:   ch.Binary,
		FailText: failText,
		OnFail: func(err error) {
			logf("ERROR: [CDAP %s] Go WS error for %s: %v", ch.Label, deviceID, err)
		},
	}
	if ch.Name == "desktop" {
		pass.ToUpstream, pass.ToSocket = desktopFilters(off)
	}
	ws.Pass(conn, pass)
}

// inScope checks the device of a socket as /ws/remote-viewer does: 0 when
// the account sees it, 404 for a device that is unknown or outside the
// account's scope, 403 when the scope check fails.
func (p *Proxy) inScope(ctx context.Context, user *session.User, id string) int {
	var device *goapi.Object
	if p.opts.Devices != nil {
		device = p.opts.Devices.GetDeviceByID(ctx, id, false)
	}
	if device == nil {
		return http.StatusNotFound
	}
	if p.opts.CanAccess == nil {
		return http.StatusForbidden
	}
	visible, err := p.opts.CanAccess(ctx, user, device)
	switch {
	case err != nil:
		logf("WARN: [CDAP] device scope check failed for %s: %v", id, err)
		return http.StatusForbidden
	case !visible:
		return http.StatusNotFound
	}
	return 0
}

func logf(format string, args ...any) { logging.Logf("console", format, args...) }
