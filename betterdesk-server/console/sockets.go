package console

import "github.com/unitronix/betterdesk-server/console/kit"

// registerSockets adds the websockets that have moved to Go, in the order
// web-nodejs/server.js set its websocket services up. An upgrade to any
// other path goes to the Node console while it is behind this one.
func registerSockets(d *kit.Deps) {
	d.Relay.Register(d.Sockets)      // services/bdRelay.js: /ws/bd-relay, /ws/bd-signal
	d.StatusPush.Register(d.Sockets) // services/deviceStatusPush.js: /ws/device-status, /ws/panel-events
}
