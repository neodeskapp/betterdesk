package console

import (
	"context"

	"github.com/unitronix/betterdesk-server/console/cdapproxy"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/meshproxy"
	"github.com/unitronix/betterdesk-server/console/remotetarget"
	"github.com/unitronix/betterdesk-server/console/routes/chat"
	"github.com/unitronix/betterdesk-server/console/statuspush"
	"github.com/unitronix/betterdesk-server/console/wsrelay"
)

// registerSockets adds the websockets, in the order web-nodejs/server.js
// set its websocket services up. An upgrade to any other path passes the
// pipeline like a plain request.
func registerSockets(d *kit.Deps) {
	// services/wsRelay.js: /ws/rendezvous, /ws/relay. A guest link that is
	// taken back ends its bridges at once; an account with a device scope
	// reaches the devices of its scope only, looked up as the remote
	// viewer's socket looks them up.
	webClient := wsrelay.New(wsrelay.Options{
		SignalHost:  d.Config.SignalHost,
		SignalPort:  d.Config.SignalPort,
		RelayHost:   d.Config.RelayHost,
		RelayPort:   d.Config.RelayPort,
		GoAPI:       d.GoAPI,
		GuestCookie: d.GuestAccess.CookieName(),
		Devices:     d.RemoteRelay.Device,
		CanAccess:   d.DeviceGroups.UserCanAccessDevice,
	})
	d.GuestAccess.OnRevoke(webClient.InvalidateGuestGrant)
	webClient.Register(d.Sockets)
	d.Relay.Register(d.Sockets) // services/bdRelay.js: /ws/bd-relay, /ws/bd-signal
	// services/chatRelay.js: /ws/chat/<id>, /ws/chat-operator/<id>
	chat.NewRelay(chat.RelayOptions{API: d.GoAPI, VerifyDevice: d.Authn.VerifyDeviceWSAuth, Scope: chat.ScopeOf(d), FilesDir: chat.FilesDir(d.Config)}).Register(d.Sockets)
	d.RemoteRelay.Register(d.Sockets) // services/remoteRelay.js: /ws/remote-agent/<id>, /ws/remote-viewer/<id>
	// services/cdapTerminalProxy.js, services/cdapMediaProxy.js: /api/cdap/devices/<id>/terminal, desktop, video, files, audio
	cdapproxy.New(cdapproxy.Options{API: d.GoAPI.Handler(), Devices: d.Backend, CanAccess: d.DeviceGroups.UserCanAccessDevice}).Register(d.Sockets)
	d.ServerMgmt.Register(d.Sockets) // services/serverTerminalProxy.js: /ws/server-management/terminal
	// services/meshAshxProxy.js: /agent.ashx, /meshrelay.ashx, /control.ashx
	meshproxy.New(meshproxy.Options{API: d.GoAPI.ForwardHandler(), ClientIP: d.Config.TrustProxy.ClientIP}).Register(d.Sockets)
	// services/remoteTargetGateway.js: /ws/remote-target/<id>, on the API
	// server's tunnel in this process
	remotetarget.New(remotetarget.Options{
		RemoteTarget: d.GoAPI.GetRemoteTarget,
		CanAccess:    d.DeviceGroups.UserCanAccessDevice,
		API:          d.GoAPI.Handler(),
	}).Register(d.Sockets)
	// services/deviceStatusPush.js: /ws/device-status, /ws/panel-events. A
	// message about a device goes to the accounts whose scope holds it.
	d.StatusPush.Register(d.Sockets, statuspush.Scope{
		Device: func(ctx context.Context, id string) *goapi.Object { return d.Backend.GetDeviceByID(ctx, id, true) },
		Of:     d.DeviceGroups.DeviceScopeForUser,
	})
}
