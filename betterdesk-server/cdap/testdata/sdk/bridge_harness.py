"""Runs one of the reference bridges in bridges/ against a stand-in target.

The Modbus bridge talks to a real Modbus TCP server (pymodbus) started here
on the config's host and port. The SNMP agent and the polled HTTP APIs
answer from memory, so the test needs neither. The CDAP side (the Python
SDK, the bridge's widgets, values, commands, alerts and its webhook server)
runs as shipped.

    python3 bridge_harness.py <modbus|snmp|rest-webhook> <bridge script> <config.json>
"""

from __future__ import annotations

import asyncio
import importlib.util
import json
import socket
import sys
import threading
import time
from typing import Any


def load(path: str) -> Any:
    spec = importlib.util.spec_from_file_location("bridge_under_test", path)
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


# ── Modbus: a device with coils and holding registers ─────────────────


def modbus_device(module: Any, config: dict) -> None:
    """Serves the example config's registers: temperature 21.5, humidity
    45.5 and setpoint 20.0 (scale 0.1), and relay 1 off."""
    from pymodbus.datastore import ModbusDeviceContext, ModbusSequentialDataBlock, ModbusServerContext
    from pymodbus.server import StartAsyncTcpServer

    registers = [0] * 32
    registers[0], registers[1], registers[10] = 215, 455, 200
    # A block at 1 holds protocol address 0.
    device = ModbusDeviceContext(
        hr=ModbusSequentialDataBlock(1, registers),
        co=ModbusSequentialDataBlock(1, [False] * 32),
    )
    host, port = config["modbus"]["host"], config["modbus"]["port"]

    def serve() -> None:
        asyncio.run(StartAsyncTcpServer(context=ModbusServerContext(devices=device, single=True), address=(host, port)))

    threading.Thread(target=serve, daemon=True).start()
    deadline = time.monotonic() + 10
    while True:
        try:
            socket.create_connection((host, port), timeout=1).close()
            return
        except OSError:
            if time.monotonic() > deadline:
                raise
            time.sleep(0.05)


# ── SNMP: an agent with the example config's OIDs ─────────────────────

SNMP_VALUES: dict[str, Any] = {
    "1.3.6.1.2.1.1.5.0": "core-switch",
    "1.3.6.1.2.1.1.3.0": 8640000,  # one day in 1/100 s
    "1.3.6.1.4.1.2021.11.11.0": 75,  # idle, so the load is 25
    "1.3.6.1.4.1.2021.4.5.0": 8388608,  # KB
    "1.3.6.1.4.1.2021.4.6.0": 2097152,  # KB
    "1.3.6.1.2.1.2.2.1.10.1": 1000,
}


def fake_snmp(module: Any, config: dict) -> None:
    async def get(self: Any, oid: str) -> Any:
        return SNMP_VALUES.get(oid)

    module.SNMPBridge._snmp_get = get


# ── REST: the example config's two APIs ───────────────────────────────

REST_ANSWERS: dict[str, Any] = {
    "weather_temp": {"main": {"temp": -3.5}},
    "ha_light": {"state": "on"},
}


def fake_rest(module: Any, config: dict) -> None:
    async def poll(self: Any, src: dict) -> Any:
        data = REST_ANSWERS[src["widget_id"]]
        path = src.get("jmespath", "")
        return module.extract_path(data, path) if path else data

    async def write(self: Any, src: dict, *args: Any) -> Any:
        value = args[-1]
        REST_ANSWERS[src["widget_id"]] = {"state": "on" if value else "off"}
        return value

    module.RESTBridge._poll_source = poll
    module.RESTBridge._write_source = write


TARGETS = {"modbus": modbus_device, "snmp": fake_snmp, "rest-webhook": fake_rest}


def main() -> None:
    kind, script, config = sys.argv[1:4]
    module = load(script)
    with open(config, encoding="utf-8") as f:
        TARGETS[kind](module, json.load(f))
    sys.argv = [script, "--config", config, "--log-level", "DEBUG"]
    module.main()


if __name__ == "__main__":
    main()
