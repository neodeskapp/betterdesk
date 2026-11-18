package console

import (
	"github.com/unitronix/betterdesk-server/console/cdapproxy"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/meshproxy"
	"github.com/unitronix/betterdesk-server/console/remotetarget"
	"github.com/unitronix/betterdesk-server/console/routes/chat"
	"github.com/unitronix/betterdesk-server/console/wsrelay"
)

// registerSockets adds the websockets that have moved to Go, in the order
// web-nodejs/server.js set its websocket services up. An upgrade to any
// other path goes to the Node console while it is behind this one.
func registerSockets(d *kit.Deps) {
	// services/wsRelay.js: /ws/rendezvous, /ws/relay. A guest link that is
	// taken back ends its bridges at once.
	webClient := wsrelay.New(wsrelay.Options{
		SignalHost:  d.Config.SignalHost,
		SignalPort:  d.Config.SignalPort,
		RelayHost:   d.Config.RelayHost,
		RelayPort:   d.Config.RelayPort,
		GoAPI:       d.GoAPI,
		GuestCookie: d.GuestAccess.CookieName(),
	})
	d.GuestAccess.OnRevoke(webClient.InvalidateGuestGrant)
	webClient.Register(d.Sockets)
	d.Relay.Register(d.Sockets) // services/bdRelay.js: /ws/bd-relay, /ws/bd-signal
	// services/chatRelay.js: /ws/chat/<id>, /ws/chat-operator/<id>
	chat.NewRelay(chat.RelayOptions{API: d.GoAPI, VerifyDevice: d.Authn.VerifyDeviceWSAuth}).Register(d.Sockets)
	d.RemoteRelay.Register(d.Sockets) // services/remoteRelay.js: /ws/remote-agent/<id>, /ws/remote-viewer/<id>
	// services/cdapTerminalProxy.js, services/cdapMediaProxy.js: /api/cdap/devices/<id>/terminal, desktop, video, files, audio
	cdapproxy.New(cdapproxy.Options{API: d.GoAPI.Handler()}).Register(d.Sockets)
	meshproxy.New(meshproxy.Options{API: d.GoAPI.Handler()}).Register(d.Sockets) // services/meshAshxProxy.js: /agent.ashx, /meshrelay.ashx, /control.ashx
	// services/remoteTargetGateway.js: /ws/remote-target/<id>, on the API
	// server's tunnel in this process
	remotetarget.New(remotetarget.Options{
		RemoteTarget: d.GoAPI.GetRemoteTarget,
		CanAccess:    d.DeviceGroups.UserCanAccessDevice,
		API:          d.GoAPI.Handler(),
	}).Register(d.Sockets)
	d.StatusPush.Register(d.Sockets) // services/deviceStatusPush.js: /ws/device-status, /ws/panel-events
}
