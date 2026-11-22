"""Runs one of the reference bridges in bridges/ with its target faked.

The Modbus device, the SNMP agent and the polled HTTP APIs answer from
memory here, so the test needs none of them. The CDAP side (the Python SDK,
the bridge's widgets, values, commands, alerts and its webhook server) runs
as shipped.

    python3 bridge_harness.py <modbus|snmp|rest-webhook> <bridge script> <config.json>
"""

from __future__ import annotations

import importlib.util
import sys
from typing import Any


def load(path: str) -> Any:
    spec = importlib.util.spec_from_file_location("bridge_under_test", path)
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


# ── Modbus: a device with two coils and a few holding registers ──────


class Result:
    def __init__(self, bits: list[bool] | None = None, registers: list[int] | None = None):
        self.bits = bits or []
        self.registers = registers or []

    def isError(self) -> bool:  # noqa: N802 (pymodbus's name)
        return False


class FakeModbus:
    def __init__(self) -> None:
        self.coils: dict[int, bool] = {0: False}
        # temperature 21.5, humidity 45.5, setpoint 20.0 (scale 0.1)
        self.registers: dict[int, int] = {0: 215, 1: 455, 10: 200}

    async def read_coils(self, address: int, count: int = 1, **kw: Any) -> Result:
        return Result(bits=[self.coils.get(address + i, False) for i in range(count)])

    read_discrete_inputs = read_coils

    async def read_holding_registers(self, address: int, count: int = 1, **kw: Any) -> Result:
        return Result(registers=[self.registers.get(address + i, 0) for i in range(count)])

    read_input_registers = read_holding_registers

    async def write_coil(self, address: int, value: bool, **kw: Any) -> Result:
        self.coils[address] = bool(value)
        return Result()

    async def write_registers(self, address: int, values: list[int], **kw: Any) -> Result:
        for i, v in enumerate(values):
            self.registers[address + i] = v
        return Result()


def fake_modbus(module: Any) -> None:
    async def connect(self: Any) -> None:
        self.client = FakeModbus()

    module.ModbusBridge._connect_modbus = connect


# ── SNMP: an agent with the example config's OIDs ─────────────────────

SNMP_VALUES: dict[str, Any] = {
    "1.3.6.1.2.1.1.5.0": "core-switch",
    "1.3.6.1.2.1.1.3.0": 8640000,  # one day in 1/100 s
    "1.3.6.1.4.1.2021.11.11.0": 75,  # idle, so the load is 25
    "1.3.6.1.4.1.2021.4.5.0": 8388608,  # KB
    "1.3.6.1.4.1.2021.4.6.0": 2097152,  # KB
    "1.3.6.1.2.1.2.2.1.10.1": 1000,
}


def fake_snmp(module: Any) -> None:
    async def get(self: Any, oid: str) -> Any:
        return SNMP_VALUES.get(oid)

    module.SNMPBridge._snmp_get = get


# ── REST: the example config's two APIs ───────────────────────────────

REST_ANSWERS: dict[str, Any] = {
    "weather_temp": {"main": {"temp": -3.5}},
    "ha_light": {"state": "on"},
}


def fake_rest(module: Any) -> None:
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


FAKES = {"modbus": fake_modbus, "snmp": fake_snmp, "rest-webhook": fake_rest}


def main() -> None:
    kind, script, config = sys.argv[1:4]
    module = load(script)
    FAKES[kind](module)
    sys.argv = [script, "--config", config, "--log-level", "DEBUG"]
    module.main()


if __name__ == "__main__":
    main()
