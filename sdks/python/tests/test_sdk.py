"""Offline tests for the BetterDesk CDAP Python SDK."""

from __future__ import annotations

import asyncio
import json
import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from betterdesk_cdap import CDAPBridge, gauge, toggle  # noqa: E402
from betterdesk_cdap.protocol import (  # noqa: E402
    Message,
    auth_payload_api_key,
    auth_payload_device_token,
    auth_payload_user_password,
)


class ProtocolTests(unittest.TestCase):
    def test_message_round_trip(self) -> None:
        original = Message(type="telemetry", payload={"temperature": 21}, id="message-1")
        encoded = original.to_json()
        decoded = Message.from_json(encoded)

        self.assertEqual(decoded.type, "telemetry")
        self.assertEqual(decoded.payload, {"temperature": 21})
        self.assertEqual(decoded.id, "message-1")
        self.assertIsNotNone(decoded.timestamp)
        self.assertEqual(json.loads(encoded)["type"], "telemetry")

    def test_auth_payloads(self) -> None:
        self.assertEqual(
            auth_payload_api_key("key", "device", "2.0.0"),
            {
                "method": "api_key",
                "key": "key",
                "device_id": "device",
                "client_version": "2.0.0",
            },
        )
        self.assertEqual(auth_payload_device_token("token")["method"], "device_token")
        self.assertEqual(auth_payload_user_password("admin", "password")["method"], "user_password")


def offline_bridge(**kwargs) -> tuple[CDAPBridge, list[dict]]:
    """A bridge whose messages go into the returned list instead of a socket."""
    bridge = CDAPBridge("ws://127.0.0.1:1/cdap", device_id="device-1", **kwargs)
    sent: list[dict] = []

    async def fake_send(message_type: str, payload: dict) -> None:
        sent.append({"type": message_type, "payload": payload})

    bridge._send = fake_send  # type: ignore[method-assign]
    return bridge, sent


class BridgeTests(unittest.TestCase):
    def test_widget_defaults_and_manifest(self) -> None:
        bridge = CDAPBridge(
            "ws://127.0.0.1:1/cdap",
            device_id="device-1",
            device_name="Test bridge",
            heartbeat_sec=1,
            bridge_name="test",
            bridge_version="2.1.0",
        )
        bridge.add_widget(gauge("temperature", "Temperature", max_val=50))
        bridge.add_widget(toggle("heater", "Heater"))
        bridge.add_alert("hot", "temperature > 40", label="Too hot", severity="critical", message="{temperature} °C")
        with self.assertRaises(ValueError):
            bridge.add_alert("loud", "temperature > 40", severity="loud")

        manifest = bridge._build_manifest()
        self.assertEqual(manifest["manifest_version"], "1.0")
        self.assertEqual(manifest["device"]["name"], "Test bridge")
        self.assertEqual(manifest["device"]["type"], "iot")
        self.assertEqual([widget["id"] for widget in manifest["widgets"]], ["temperature", "heater"])
        self.assertEqual(
            manifest["alerts"],
            [{"id": "hot", "label": "Too hot", "severity": "critical", "condition": "temperature > 40", "message": "{temperature} °C"}],
        )
        self.assertEqual(manifest["bridge"]["version"], "2.1.0")
        self.assertEqual(bridge.heartbeat_sec, 5)

    def test_device_id_is_required(self) -> None:
        for device_id in ("", "a b", "x" * 65):
            with self.assertRaises(ValueError):
                CDAPBridge("ws://127.0.0.1:1/cdap", device_id=device_id)

    def test_values_wait_for_the_registration(self) -> None:
        async def exercise() -> list[dict]:
            bridge, sent = offline_bridge()
            await bridge.bulk_update({"temp": 21})
            await bridge.send_log("info", "early")
            self.assertEqual(sent, [])
            self.assertEqual(bridge._widget_values, {"temp": 21})

            bridge._connected = True
            await bridge.bulk_update({"temp": 22, "humidity": 40})
            return sent

        sent = asyncio.run(exercise())
        self.assertEqual(
            sent,
            [{"type": "bulk_update", "payload": {"updates": [{"widget_id": "temp", "value": 22}, {"widget_id": "humidity", "value": 40}]}}],
        )

    def test_command_handler_success_and_failure(self) -> None:
        async def exercise() -> list[dict]:
            bridge, sent = offline_bridge()
            bridge._connected = True
            bridge.add_widget(toggle("heater", "Heater"))

            @bridge.on_command("heater")
            async def handle_heater(**kwargs):
                return kwargs["value"]

            @bridge.on_command("pump")
            def handle_pump(**kwargs):
                raise RuntimeError("pump stuck")

            await bridge._handle_command(
                {
                    "command_id": "command-1",
                    "widget_id": "heater",
                    "action": "set",
                    "value": True,
                }
            )
            await bridge._handle_command({"command_id": "command-2", "widget_id": "pump", "action": "trigger"})
            return sent

        sent = asyncio.run(exercise())
        self.assertEqual(sent[0]["type"], "command_response")
        self.assertEqual(sent[0]["payload"]["command_id"], "command-1")
        self.assertEqual(sent[0]["payload"]["status"], "ok")
        self.assertTrue(sent[0]["payload"]["result"])
        self.assertEqual(sent[1]["type"], "state_update")
        self.assertEqual((sent[1]["payload"]["widget_id"], sent[1]["payload"]["value"]), ("heater", True))
        self.assertEqual(sent[2]["payload"]["status"], "error")
        self.assertEqual(sent[2]["payload"]["error_message"], "pump stuck")


if __name__ == "__main__":
    unittest.main()
