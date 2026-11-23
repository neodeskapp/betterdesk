# NeoDesk CDAP Python SDK

Build CDAP device bridges in Python for IoT sensors, industrial equipment, and automation systems.

## Installation

```bash
pip install neodesk-cdap
# or from source
pip install -e sdks/python/
```

## What a device needs

- The gateway's address, `ws://<server>:21122/cdap` (or `wss://` with TLS).
- An API key, a console account or a device token. An API key's or
  account's role needs the Approve enrollment permission.
- A device ID of 1 to 64 letters, digits, `-` and `_`. `CDAPBridge` raises
  `ValueError` without one.
- A device type the gateway takes: `scada`, `iot` (the default), `os_agent`,
  `network`, `camera`, `desktop` or `custom`.

## Quick Start

`examples/greenhouse.py` is a simulated greenhouse that uses every call
below:

```bash
CDAP_SERVER=ws://203.0.113.10:21122/cdap CDAP_API_KEY=your-api-key python examples/greenhouse.py
```

The short version:

```python
import os

from neodesk_cdap import CDAPBridge, gauge, toggle, button

# Create bridge
bridge = CDAPBridge(
    server="ws://203.0.113.10:21122/cdap",
    auth_method="api_key",
    api_key="your-api-key",
    device_id="sensor-zone-a",
    device_name="Temperature Sensor",
    device_type="iot",
    tags=["factory-floor", "zone-a"],
)

# Define widgets and alerts
temp = bridge.add_widget(gauge("temp", "Temperature", unit="°C", max_val=50, warning_high=40))
humidity = bridge.add_widget(gauge("humidity", "Humidity", unit="%", max_val=100))
heater = bridge.add_widget(toggle("heater", "Heater", group="Controls"))
reboot = bridge.add_widget(button("reboot", "Reboot Device", confirm=True, icon="restart_alt"))
bridge.add_alert("hot", "temp > 40", label="Too hot", severity="critical", message="{temp} °C")

# Handle commands
@bridge.on_command("heater")
async def handle_heater(action, value, **kw):
    if action == "set":
        set_heater(value)  # your hardware control
        return {"new_state": value}

@bridge.on_command("reboot")
async def handle_reboot(action, **kw):
    if action == "trigger":
        os.system("reboot")
        return "rebooting"

# Run (blocks forever with auto-reconnect)
bridge.run()
```

## Async Usage

```python
import asyncio

async def main():
    bridge = CDAPBridge(server="ws://host:21122/cdap", api_key="key", device_id="sensor-1")
    bridge.add_widget(gauge("temp", "Temperature", unit="°C", max_val=100))

    # Start bridge in background
    task = asyncio.create_task(bridge.run_async())

    # Push state updates from your sensor loop
    while True:
        temp_value = read_sensor()
        await bridge.update_state("temp", temp_value)
        await asyncio.sleep(5)

asyncio.run(main())
```

Values set before the bridge is registered are kept and go out with the
heartbeat right after registration.

## Commands

A handler gets `action` (`set`, `trigger`, `execute`, `reset` or `query`),
`value`, `widget_id`, `operator` and `reason` as keyword arguments, and may
be a coroutine function or a plain function. What it returns goes back as
the result with status `ok`; an exception goes back as `error` with its
message. After a `set` the SDK sends the new value at once, so the console
shows the control's new state.

## Alerts

`add_alert(alert_id, condition, label=, severity=, message=)` puts an alert
into the manifest. The gateway raises it under **Active alerts** on the
device's page while the condition holds, and clears it when it no longer
does.

- `condition` is `<widget id> <operator> <value>` with `==`, `!=`, `>`, `<`,
  `>=` or `<=`, such as `temp < 2` or `door == 'open'`. The widget ID has to
  start with a letter or `_` and hold only letters, digits, `_` and `.`.
- `severity` is `critical`, `warning` (the default) or `info`.
- `{widget id}` in `message` is replaced with the widget's value.

## API Reference

### CDAPBridge

| Method | Description |
|--------|-------------|
| `add_widget(widget)` | Register a widget for the manifest |
| `add_alert(alert_id, condition, ...)` | Add an alert to the manifest |
| `on_command(widget_id)` | Decorator to handle commands |
| `update_state(widget_id, value)` | Push widget state update |
| `bulk_update(updates)` | Push multiple state updates |
| `send_log(level, message, context)` | Send a log entry (`debug`, `info`, `warning`, `error`, `critical`) |
| `run()` | Blocking main loop |
| `run_async()` | The main loop as a coroutine |
| `stop()` | Signal shutdown |

`update_state`, `bulk_update` and `send_log` are coroutines to `await`.

### Widget Helpers

| Helper | Type | Key Options |
|--------|------|-------------|
| `gauge(id, label)` | Gauge | `unit`, `min_val`, `max_val`, `warning_high` |
| `toggle(id, label)` | Toggle | `value` |
| `button(id, label)` | Button | `icon`, `confirm`, `cooldown` |
| `text_widget(id, label)` | Text | `readonly`, `value` |
| `led(id, label)` | LED | `value` |
| `slider(id, label)` | Slider | `min_val`, `max_val`, `step` |
| `select(id, label)` | Select | `options` |
| `chart(id, label)` | Chart | `chart_type`, `points`, `series` |
| `table(id, label)` | Table | `columns`, `max_rows`, `sortable` |

## Requirements

- Python 3.9+
- `websockets >= 12.0`
