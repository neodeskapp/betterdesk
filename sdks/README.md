# BetterDesk CDAP — SDKs & Bridges

Software Development Kits and reference bridge implementations for the
**Connected Device Automation Protocol (CDAP)** used by BetterDesk server.

## SDKs

| SDK | Directory | Language |
|-----|-----------|----------|
| **Python** | [`python/`](python/) | Python 3.9+ |
| **Node.js** | [`nodejs/`](nodejs/) | Node.js 22+, written in TypeScript |

Each SDK has a runnable example, a simulated greenhouse:
`python/examples/greenhouse.py` and `nodejs/examples/greenhouse.mts`. The
Go tests in `betterdesk-server/cdap/sdk_examples_test.go` run both, and the
three bridges, against a gateway.

## Reference Bridges

| Bridge | Directory | Protocol | Use Case |
|--------|-----------|----------|----------|
| **Modbus** | [`bridges/modbus/`](../bridges/modbus/) | Modbus TCP/RTU | PLCs, VFDs, power meters |
| **SNMP** | [`bridges/snmp/`](../bridges/snmp/) | SNMP v2c/v3 | Switches, routers, UPS |
| **REST/Webhook** | [`bridges/rest-webhook/`](../bridges/rest-webhook/) | HTTP | Home Assistant, cloud APIs |

## Quick Start

### Python Bridge

```bash
pip install ./sdks/python
pip install pymodbus  # for Modbus bridge
cd bridges/modbus
cp config.example.json config.json
# set server, api_key and device_id in config.json
python bridge_modbus.py -c config.json
```

### Node.js Bridge

```js
const { CDAPBridge, gauge } = require('betterdesk-cdap');

const bridge = new CDAPBridge({
  server: 'ws://your-server:21122/cdap',
  apiKey: 'KEY',
  deviceId: 'my-bridge-1',
  deviceName: 'My Bridge',
});
bridge.addWidget(gauge('temp', 'Temperature'));
bridge.run();
```

## CDAP Protocol Overview

CDAP uses WebSocket (port 21122, subprotocol `cdap-v1`) with JSON messages:

```
Device/Bridge → Server:
  auth → auth_result
  register (manifest with widgets and alerts) → registered
  heartbeat → ping
  state_update, bulk_update (widget values)
  command_response
  log, event

Server → Device/Bridge:
  command (command_id, widget_id, action, value)
  terminal_start / terminal_input / terminal_resize
  file_list / file_read / file_write / file_delete
  clipboard_set
  desktop_start / desktop_input
```

Alerts are part of the manifest: the server raises them while their
condition on a widget's value holds.

Authentication methods: `api_key`, `device_token`, `user_password`.

## License

AGPL-3.0
