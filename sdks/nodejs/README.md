# NeoDesk CDAP — Node.js SDK

Node.js SDK for the **Connected Device Automation Protocol (CDAP)** used by
NeoDesk server. Build custom bridges, IoT gateways, or automation agents
that expose widgets and handle commands through the NeoDesk web panel.

## Installation

```bash
npm install neodesk-cdap
# or from local path: build it first
(cd sdks/nodejs && npm ci && npm run build)
npm install ./sdks/nodejs
```

The SDK is written in TypeScript (`src/`) and ships the compiled JavaScript
with its type declarations (`dist/`), so it works with `require` and
`import` and gives editors the types. `npm test` builds it and runs the
tests against the build.

## What a device needs

- The gateway's address, `ws://<server>:21122/cdap` (or `wss://` with TLS).
- An API key, a console account or a device token. An API key's or
  account's role needs the Approve enrollment permission.
- A device ID of 1 to 64 letters, digits, `-` and `_`. The constructor
  throws without one.
- A device type the gateway takes: `scada`, `iot`, `os_agent`, `network`,
  `camera`, `desktop` or `custom` (the default).

## Quick start

`examples/greenhouse.mts` is a simulated greenhouse that uses every call
below. After `npm ci && npm run build`:

```bash
CDAP_SERVER=ws://your-server:21122/cdap CDAP_API_KEY=YOUR_API_KEY node examples/greenhouse.mts
```

The short version:

```js
const { CDAPBridge, gauge, toggle } = require('neodesk-cdap');

const bridge = new CDAPBridge({
  server: 'ws://your-server:21122/cdap',
  apiKey: 'YOUR_API_KEY',
  deviceId: 'room-sensor-1',
  deviceName: 'Room Sensor',
  deviceType: 'iot',
});

// Define widgets and alerts before run()
bridge
  .addWidget(gauge('temperature', 'Temperature', { unit: '°C', min: -10, max: 50 }))
  .addWidget(toggle('heater', 'Heater'))
  .addAlert('hot', 'temperature > 30', { label: 'Too hot', severity: 'critical', message: '{temperature} °C' });

// Handle commands: the returned value becomes the widget's value
bridge.onCommand('heater', ({ value }) => {
  console.log('Heater toggled to', value);
  return value;
});

bridge.on('registered', () => console.log('Registered'));
bridge.on('error', (err) => console.error(err.message));

// Start (reconnects automatically)
bridge.run();

// Push values at any time
setInterval(() => {
  bridge.updateState('temperature', 20 + Math.random() * 5);
}, 5000);
```

Values set before the device is registered are kept and go out with the
heartbeat right after registration.

## Async command handlers

Handlers can return promises:

```js
bridge.onCommand('restart', async ({ action, operator }) => {
  await restartService();
  return true;
});
```

A handler that throws or rejects answers the command with status `error`
and its message.

## Alerts

`addAlert(id, condition, { label, severity, message })` puts an alert into
the manifest. The gateway raises it under **Active alerts** on the device's
page while the condition holds, and clears it when it no longer does.

- `condition` is `<widget id> <operator> <value>` with `==`, `!=`, `>`, `<`,
  `>=` or `<=`, such as `temp < 2` or `door == 'open'`. The widget ID has to
  start with a letter or `_` and hold only letters, digits, `_` and `.`.
- `severity` is `critical`, `warning` (the default) or `info`.
- `{widget id}` in `message` is replaced with the widget's value.

## API reference

### `new CDAPBridge(options)`

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `server` | `string` | — | WebSocket URL (`ws://host:21122/cdap`) |
| `authMethod` | `string` | `'api_key'` | `api_key` / `device_token` / `user_password` |
| `apiKey` | `string` | `''` | API key (when `authMethod='api_key'`) |
| `deviceToken` | `string` | `''` | Device token |
| `username` | `string` | `''` | Username |
| `password` | `string` | `''` | Password |
| `deviceId` | `string` | — | Device ID, required |
| `deviceName` | `string` | hostname | Display name |
| `deviceType` | `string` | `'custom'` | Device type |
| `bridgeName` | `string` | `''` | Bridge identifier |
| `bridgeVersion` | `string` | `'1.0.0'` | Bridge version |
| `heartbeatSec` | `number` | `15` | Heartbeat interval (seconds) |
| `reconnectSec` | `number` | `5` | Initial reconnect delay |
| `maxReconnect` | `number` | `120` | Maximum reconnect delay |

### Methods

| Method | Description |
|--------|-------------|
| `addWidget(widget)` | Add a widget to the manifest |
| `addAlert(id, condition, opts?)` | Add an alert to the manifest |
| `onCommand(widgetId, handler)` | Register command handler for a widget |
| `updateState(widgetId, value)` | Send one widget's value |
| `bulkUpdate({ id: value })` | Send several widgets' values |
| `sendLog(level, msg, context?)` | Send a log entry (`debug`, `info`, `warning`, `error`, `critical`) |
| `run()` | Start (async, reconnects automatically) |
| `stop()` | Stop the bridge |

A command handler gets the command: `command_id`, `widget_id`, `action`
(`set`, `trigger`, `execute`, `reset` or `query`), `value`, `operator` and
`reason`.

### Events

| Event | Payload | Description |
|-------|---------|-------------|
| `connected` | — | WebSocket connected |
| `disconnected` | `{ code, reason }` | WebSocket closed |
| `registered` | payload | Registration confirmed |
| `command` | payload | Command received |
| `error` | `Error` | Error occurred |
| `message` | `{ type, payload }` | Any raw message |

Listen for `error`: like every Node.js event emitter, the bridge throws an
`error` event that has no listener.

### Widget helpers

```js
const { gauge, toggle, button, textWidget, led, slider, select, chart, table } = require('neodesk-cdap');
```

All helpers accept `(id, label, opts?)` and return a `Widget` object.

## Requirements

- Node.js 22 or newer (22.18 or newer to run the tests and the example, which Node runs as TypeScript)
- NeoDesk server with CDAP enabled (port 21122)

## License

AGPL-3.0
