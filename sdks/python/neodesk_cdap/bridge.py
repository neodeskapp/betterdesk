"""CDAPBridge — main class for building CDAP device bridges in Python.

Usage example:

    from betterdesk_cdap import CDAPBridge, gauge, toggle

    bridge = CDAPBridge(
        server="ws://203.0.113.10:21122/cdap",
        auth_method="api_key",
        api_key="your-key",
        device_id="sensor-1",
        device_name="Temperature Sensor",
        device_type="iot",
    )

    temp = bridge.add_widget(gauge("temp", "Temperature", unit="°C", max_val=50))
    heater = bridge.add_widget(toggle("heater", "Heater"))
    bridge.add_alert("hot", "temp > 40", label="Too hot", severity="critical")

    @bridge.on_command("heater")
    async def handle_heater(action, value, **kw):
        GPIO.output(HEATER_PIN, value)
        return {"status": "ok"}

    bridge.run()
"""

from __future__ import annotations

import asyncio
import inspect
import logging
import random
import re
import time
from typing import Any, Callable

import websockets
import websockets.exceptions

from betterdesk_cdap.protocol import (
    SEVERITY_CRITICAL,
    SEVERITY_INFO,
    SEVERITY_WARNING,
    SUBPROTOCOL,
    Message,
    auth_payload_api_key,
    auth_payload_device_token,
    auth_payload_user_password,
)
from betterdesk_cdap.widgets import Widget

logger = logging.getLogger("betterdesk_cdap")

# A device id the gateway takes
DEVICE_ID_PATTERN = re.compile(r"^[A-Za-z0-9_-]{1,64}$")

SEVERITIES = (SEVERITY_CRITICAL, SEVERITY_WARNING, SEVERITY_INFO)


class CDAPBridge:
    """Python bridge that connects to a BetterDesk CDAP gateway."""

    def __init__(
        self,
        server: str,
        *,
        auth_method: str = "api_key",
        api_key: str = "",
        device_token: str = "",
        username: str = "",
        password: str = "",
        device_id: str = "",
        device_name: str = "Python Bridge",
        device_type: str = "iot",
        vendor: str = "",
        model: str = "",
        firmware: str = "1.0.0",
        tags: list[str] | None = None,
        capabilities: list[str] | None = None,
        heartbeat_sec: int = 15,
        reconnect_sec: int = 5,
        max_reconnect: int = 300,
        # Bridge metadata (optional)
        bridge_name: str = "",
        bridge_version: str = "",
        bridge_protocol: str = "",
        target_host: str = "",
        target_port: int = 0,
    ):
        if not DEVICE_ID_PATTERN.match(device_id):
            raise ValueError("device_id is required: 1 to 64 letters, digits, - and _")
        self.server = server
        self.auth_method = auth_method
        self.api_key = api_key
        self.device_token = device_token
        self.username = username
        self.password = password
        self.device_id = device_id
        self.device_name = device_name
        self.device_type = device_type
        self.vendor = vendor
        self.model = model
        self.firmware = firmware
        self.tags = tags or []
        self.capabilities = capabilities or ["telemetry", "commands"]
        self.heartbeat_sec = max(5, min(300, heartbeat_sec))
        self.reconnect_sec = max(1, reconnect_sec)
        self.max_reconnect = max(reconnect_sec, max_reconnect)

        self.bridge_name = bridge_name
        self.bridge_version = bridge_version or firmware
        self.bridge_protocol = bridge_protocol
        self.target_host = target_host
        self.target_port = target_port

        # Internal state
        self._ws: Any = None
        self._widgets: list[Widget] = []
        self._alerts: list[dict[str, str]] = []
        self._widget_values: dict[str, Any] = {}
        self._command_handlers: dict[str, Callable] = {}
        self._global_command_handler: Callable | None = None
        self._token: str = ""
        self._assigned_device_id: str = ""
        self._role: str = ""
        self._running = False
        self._connected = False

    # ── Widget Registration ───────────────────────────────────────────

    def add_widget(self, widget: Widget) -> Widget:
        """Register a widget for the device manifest."""
        self._widgets.append(widget)
        if widget.value is not None:
            self._widget_values[widget.id] = widget.value
        return widget

    def add_alert(
        self,
        alert_id: str,
        condition: str,
        *,
        label: str = "",
        severity: str = SEVERITY_WARNING,
        message: str = "",
    ) -> None:
        """Add an alert to the manifest, before the bridge runs.

        The gateway raises it under Active alerts while the condition holds
        and clears it when it no longer does. The condition is
        ``<widget id> <operator> <value>`` with ==, !=, >, <, >= or <=, such
        as ``temp < 2`` or ``door == 'open'``. ``{widget id}`` in the message
        is replaced with the widget's value.
        """
        if severity not in SEVERITIES:
            raise ValueError(f"alert {alert_id}: severity must be critical, warning or info")
        self._alerts.append({
            "id": alert_id,
            "label": label or alert_id,
            "severity": severity,
            "condition": condition,
            "message": message,
        })

    def on_command(self, widget_id: str | None = None):
        """Decorator to register a command handler for a widget (or all widgets).

        The handler may be a coroutine function or a plain function.
        """
        def decorator(fn: Callable[..., Any]):
            if widget_id:
                self._command_handlers[widget_id] = fn
            else:
                self._global_command_handler = fn
            return fn
        return decorator

    # ── State Updates ─────────────────────────────────────────────────
    #
    # Values set before the bridge is registered are kept and go out with
    # the heartbeat right after registration.

    async def update_state(self, widget_id: str, value: Any) -> None:
        """Push a single widget state update to the server."""
        self._widget_values[widget_id] = value
        if not self._connected:
            return
        await self._send("state_update", {
            "widget_id": widget_id,
            "value": value,
            "timestamp": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
        })

    async def bulk_update(self, updates: dict[str, Any]) -> None:
        """Push multiple widget state updates."""
        for wid, val in updates.items():
            self._widget_values[wid] = val
        if not self._connected:
            return
        await self._send("bulk_update", {
            "updates": [
                {"widget_id": wid, "value": val}
                for wid, val in updates.items()
            ],
        })

    # ── Logging ───────────────────────────────────────────────────────

    async def send_log(
        self, level: str, message: str, context: Any = None
    ) -> None:
        """Send a log entry to the server; level is debug, info, warning,
        error or critical. Dropped while the bridge is not registered."""
        if not self._connected:
            return
        payload: dict[str, Any] = {"level": level, "message": message}
        if context is not None:
            payload["context"] = context
        await self._send("log", payload)

    # ── Lifecycle ─────────────────────────────────────────────────────

    def run(self) -> None:
        """Blocking entry point — connect and run the message loop."""
        asyncio.run(self._run_forever())

    async def run_async(self) -> None:
        """Connect and run the message loop in a running event loop, with
        the same reconnects as run()."""
        await self._run_forever()

    async def _run_forever(self) -> None:
        self._running = True
        delay = self.reconnect_sec

        while self._running:
            try:
                await self._connect_once()
                delay = self.reconnect_sec  # reset on success
            except Exception as exc:
                logger.warning("Connection lost: %s", exc)

            if not self._running:
                break

            jitter = random.uniform(0, delay * 0.25)
            wait = delay + jitter
            logger.info("Reconnecting in %.1fs...", wait)
            await asyncio.sleep(wait)
            delay = min(delay * 2, self.max_reconnect)

    async def _connect_once(self) -> None:
        logger.info("Connecting to %s...", self.server)
        async with websockets.connect(
            self.server,
            subprotocols=[SUBPROTOCOL],
            max_size=4 * 1024 * 1024,
            open_timeout=30,
        ) as ws:
            self._ws = ws
            await self._authenticate()
            await self._register()
            self._connected = True
            logger.info(
                "Connected as %r (device_id=%s, role=%s)",
                self.device_name,
                self._assigned_device_id,
                self._role,
            )

            heartbeat_task = asyncio.create_task(self._heartbeat_loop())
            try:
                await self._message_loop()
            finally:
                heartbeat_task.cancel()
                self._connected = False
                self._ws = None

    def stop(self) -> None:
        """Signal the bridge to stop."""
        self._running = False

    # ── Authentication ────────────────────────────────────────────────

    async def _authenticate(self) -> None:
        if self.auth_method == "api_key":
            payload = auth_payload_api_key(self.api_key, self.device_id, self.firmware)
        elif self.auth_method == "device_token":
            payload = auth_payload_device_token(self.device_token, self.device_id, self.firmware)
        elif self.auth_method == "user_password":
            payload = auth_payload_user_password(
                self.username, self.password, self.device_id, self.firmware
            )
        else:
            raise ValueError(f"Unknown auth method: {self.auth_method}")

        await self._send("auth", payload)
        msg = await self._recv()
        if msg.type == "error":
            raise RuntimeError(f"Sign-in refused: {msg.payload.get('message', 'unknown')}")
        if msg.type != "auth_result":
            raise RuntimeError(f"Expected auth_result, got {msg.type}")
        if not msg.payload.get("success"):
            raise RuntimeError(f"Auth failed: {msg.payload.get('error', 'unknown')}")

        self._token = msg.payload.get("token", "")
        self._assigned_device_id = msg.payload.get("device_id", "")
        self._role = msg.payload.get("role", "")

    # ── Registration ──────────────────────────────────────────────────

    async def _register(self) -> None:
        manifest = self._build_manifest()
        await self._send("register", {"manifest": manifest})
        msg = await self._recv()
        if msg.type == "error":
            raise RuntimeError(f"Registration refused: {msg.payload.get('message', 'unknown')}")
        if msg.type != "registered":
            raise RuntimeError(f"Expected registered, got {msg.type}")

    def _build_manifest(self) -> dict[str, Any]:
        device: dict[str, Any] = {
            "name": self.device_name,
            "type": self.device_type,
        }
        if self.vendor:
            device["vendor"] = self.vendor
        if self.model:
            device["model"] = self.model
        if self.firmware:
            device["firmware"] = self.firmware
        if self.tags:
            device["tags"] = self.tags

        m: dict[str, Any] = {
            "manifest_version": "1.0",
            "device": device,
            "capabilities": self.capabilities,
            "heartbeat_interval": self.heartbeat_sec,
            "widgets": [w.to_dict() for w in self._widgets],
        }
        if self._alerts:
            m["alerts"] = list(self._alerts)

        if self.bridge_name:
            m["bridge"] = {
                "name": self.bridge_name,
                "version": self.bridge_version,
                "protocol": self.bridge_protocol,
                "target_host": self.target_host,
                "target_port": self.target_port,
            }

        return m

    # ── Heartbeat ─────────────────────────────────────────────────────

    async def _heartbeat_loop(self) -> None:
        while self._connected:
            try:
                payload: dict[str, Any] = {}
                if self._widget_values:
                    payload["widget_values"] = dict(self._widget_values)
                await self._send("heartbeat", payload)
            except Exception:
                return
            await asyncio.sleep(self.heartbeat_sec)

    # ── Message Loop ──────────────────────────────────────────────────

    async def _message_loop(self) -> None:
        while True:
            msg = await self._recv()
            await self._dispatch(msg)

    async def _dispatch(self, msg: Message) -> None:
        if msg.type == "command":
            await self._handle_command(msg.payload)
        elif msg.type == "ping":
            pass  # heartbeat ACK
        elif msg.type == "error":
            code = msg.payload.get("code", 0)
            error_msg = msg.payload.get("message", "")
            logger.error("Server error %d: %s", code, error_msg)
        else:
            logger.debug("Unhandled message type: %s", msg.type)

    async def _handle_command(self, payload: dict[str, Any]) -> None:
        """Runs the widget's handler and answers the command. After a set,
        the new value goes to the server at once, so the console shows the
        control's new state."""
        command_id = payload.get("command_id", "")
        widget_id = payload.get("widget_id", "")
        action = payload.get("action", "")
        value = payload.get("value")

        start = time.monotonic()
        handler = self._command_handlers.get(widget_id, self._global_command_handler)

        resp: dict[str, Any] = {"command_id": command_id}
        if handler is None:
            resp["status"] = "error"
            resp["error_message"] = f"No handler for widget {widget_id}"
        else:
            try:
                result = handler(
                    action=action,
                    value=value,
                    widget_id=widget_id,
                    operator=payload.get("operator", ""),
                    reason=payload.get("reason", ""),
                )
                if inspect.isawaitable(result):
                    result = await result
                resp["status"] = "ok"
                resp["result"] = result
            except Exception as exc:
                resp["status"] = "error"
                resp["error_message"] = str(exc)

        resp["execution_time_ms"] = int((time.monotonic() - start) * 1000)
        await self._send("command_response", resp)
        if resp["status"] == "ok" and action == "set" and value is not None:
            await self.update_state(widget_id, value)

    # ── Wire I/O ──────────────────────────────────────────────────────

    async def _send(self, msg_type: str, payload: dict[str, Any]) -> None:
        """Sends a message; on a connection that just closed it sends
        nothing, and the message loop reconnects."""
        if not self._ws:
            return
        msg = Message(type=msg_type, payload=payload)
        try:
            await self._ws.send(msg.to_json())
        except websockets.exceptions.ConnectionClosed:
            logger.debug("Not sent, the connection is closed: %s", msg_type)

    async def _recv(self) -> Message:
        if not self._ws:
            raise RuntimeError("Not connected")
        raw = await self._ws.recv()
        return Message.from_json(raw)
