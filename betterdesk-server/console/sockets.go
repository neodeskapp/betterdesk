package console

import (
	"github.com/unitronix/betterdesk-server/console/cdapproxy"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/meshproxy"
	"github.com/unitronix/betterdesk-server/console/routes/chat"
)

// registerSockets adds the websockets that have moved to Go, in the order
// web-nodejs/server.js set its websocket services up. An upgrade to any
// other path goes to the Node console while it is behind this one.
func registerSockets(d *kit.Deps) {
	d.Relay.Register(d.Sockets) // services/bdRelay.js: /ws/bd-relay, /ws/bd-signal
	// services/chatRelay.js: /ws/chat/<id>, /ws/chat-operator/<id>
	chat.NewRelay(chat.RelayOptions{API: d.GoAPI, VerifyDevice: d.Authn.VerifyDeviceWSAuth}).Register(d.Sockets)
	// services/cdapTerminalProxy.js, services/cdapMediaProxy.js: /api/cdap/devices/<id>/terminal, desktop, video, files, audio
	cdapproxy.New(cdapproxy.Options{API: d.GoAPI.Handler()}).Register(d.Sockets)
	meshproxy.New(meshproxy.Options{API: d.GoAPI.Handler()}).Register(d.Sockets) // services/meshAshxProxy.js: /agent.ashx, /meshrelay.ashx, /control.ashx
	d.StatusPush.Register(d.Sockets)                                             // services/deviceStatusPush.js: /ws/device-status, /ws/panel-events
}
