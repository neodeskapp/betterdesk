// Package remote holds the data the web remote client asks for
// (routes/remote.routes.js of the old console): the sign-in and guest
// pages' data, the remote dashboard's check, everything the viewer needs
// for one device, the old addresses that redirect, and the remote
// relay's sessions. The pages themselves (/remote, /remote/:deviceId,
// /remote/login, /remote/guest) belong to the web app.
//
// It also holds POST /api/bd/remote-agent-token of the device API
// (routes/bd-api.routes.js), which gives an agent the single-use token
// of its remote relay socket: the token, the route that issues it and
// the socket that takes it live in one process (console/remoterelay).
//
// The dev mock of the viewer, /dev/mock-remote.js, is served only when
// BD_DEV_MOCK_REMOTE is 1; without it the address is no route of its own.
package remote

import (
	"context"
	_ "embed"
	"path/filepath"
	"time"

	"github.com/unitronix/betterdesk-server/console/branding"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/i18n"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/remoterelay"
	"github.com/unitronix/betterdesk-server/console/serverinfo"
)

// relay is what the routes use of the remote relay (console/remoterelay).
type relay interface {
	IssueRemoteAgentToken(deviceID string) (token string, expiresIn int)
	SessionState(deviceID string) *remoterelay.SessionState
	ActiveSessions() []string
	Device(ctx context.Context, id string) (*goapi.Object, error)
}

// sources are the services behind the routes; tests put their own in.
type sources struct {
	// relay is nil when the console runs without the remote relay.
	relay relay
	// serverKey is keyService.resolvePublicKey: the server's public key,
	// "" when there is none; an error stands for a failure.
	serverKey func(ctx context.Context) (string, error)
	// connectionMode is serverConnectionConfig.getConnectionMode.
	connectionMode func(ctx context.Context) (serverinfo.ConnectionMode, error)
	// branding is brandingService.getBranding().
	branding func(ctx context.Context) *branding.Object
	// languages is the language list of i18nService.getAvailableList.
	languages func() ([]*i18n.Object, error)
	// appVersion is config.appVersion.
	appVersion string
	// mock is the dev mock of the viewer; nil when BD_DEV_MOCK_REMOTE is
	// not 1.
	mock *mockScript
}

type handlers struct {
	d   *kit.Deps
	src sources
	log *kit.Logger
}

// Register adds the group's routes in the order of the route file:
// first the dev mock's script, then the data of the remote client.
func Register(d *kit.Deps) {
	register(d, defaultSources(d))
}

// RegisterAgentToken adds POST /api/bd/remote-agent-token of the device
// API to the early stage, where routes/bd-api.routes.js was mounted
// (server.js, app.use('/api/bd', ...)), behind the device token guards.
func RegisterAgentToken(d *kit.Deps) {
	registerAgentToken(d, defaultSources(d))
}

func register(d *kit.Deps, src sources) {
	h := &handlers{d: d, src: src, log: d.Log.Child("REMOTE")}
	page := d.Limits.RDClientPage.Middleware
	connect := d.RequireRdClientAuth("device.connect")
	r := d.Router
	r.Get("/dev/mock-remote.js", h.mockScript)
	r.Get("/api/remote/login-info", h.loginInfo, page)
	r.Get("/api/remote/guest-info", h.guestInfo, page)
	r.Get("/api/remote/desk", h.desk, page, connect)
	r.Get("/remote-dashboard", h.dashboard)
	r.Get("/api/remote/viewer/{deviceId}", h.viewer, page, h.requireRemoteAccess)
	r.Get("/remote-cdap/{deviceId}", h.cdapRedirect, page)
	r.Get("/remote-desktop/{deviceId}", h.desktopRedirect, page)
	r.Get("/api/remote/sessions", h.sessions, d.RequireRdClientAuth(""))
	r.Get("/api/remote/session/{deviceId}", h.session, d.RequireRdClientAuth(""))
}

func registerAgentToken(d *kit.Deps, src sources) {
	h := &handlers{d: d, src: src, log: d.Log.Child("BD-API")}
	d.Early.Post("/api/bd/remote-agent-token", h.remoteAgentToken, d.RequireDeviceToken, d.RequireTokenDeviceMatch)
}

// defaultSources are the console's own services.
func defaultSources(d *kit.Deps) sources {
	src := sources{
		serverKey: func(ctx context.Context) (string, error) {
			return string(d.ServerInfo.ResolvePublicKey(ctx)), nil
		},
		connectionMode: func(ctx context.Context) (serverinfo.ConnectionMode, error) {
			settings, err := d.ServerInfo.GetConnectionMode(ctx)
			return settings.ConnectionMode, err
		},
		branding: func(context.Context) *branding.Object {
			return d.Branding.GetBranding()
		},
		languages: func() ([]*i18n.Object, error) { return d.I18n.Bundle.AvailableList() },
	}
	if d.RemoteRelay != nil {
		src.relay = d.RemoteRelay
	}
	if d.Config != nil {
		dir := d.Config.ConsoleDir
		src.appVersion = serverinfo.ReadProductVersion(serverinfo.OSFS{}, filepath.Join(dir, ".."), dir, serverinfo.DefaultVersionFallback)
		if d.Config.Getenv != nil && jsbind.Trim(d.Config.Getenv("BD_DEV_MOCK_REMOTE")) == "1" {
			src.mock = newMockScript(time.Now())
		}
	}
	return src
}
