"""A simulated greenhouse on a NeoDesk server's CDAP gateway.

It shows a temperature, a humidity, a heater switch and a status text,
raises a frost alert below 2 °C, and warms up while the heater is on.

Install the SDK (pip install ./sdks/python), then run it with the server's
address and an API key whose role may approve enrollment:

    CDAP_SERVER=ws://desk.example.com:21122/cdap CDAP_API_KEY=... python examples/greenhouse.py

CDAP_DEVICE_ID sets the device's id (default greenhouse-1).
"""

import asyncio
import logging
import os

from neodesk_cdap import CDAPBridge, gauge, text_widget, toggle

bridge = CDAPBridge(
    server=os.environ.get("CDAP_SERVER", "ws://localhost:21122/cdap"),
    api_key=os.environ.get("CDAP_API_KEY", ""),
    device_id=os.environ.get("CDAP_DEVICE_ID", "greenhouse-1"),
    device_name="Greenhouse",
    device_type="iot",
)
bridge.add_widget(gauge("temp", "Temperature", unit="°C", min_val=-10, max_val=40))
bridge.add_widget(gauge("humidity", "Humidity"))
bridge.add_widget(toggle("heater", "Heater"))
bridge.add_widget(text_widget("status", "Status", value="Idle"))
bridge.add_alert("frost", "temp < 2", label="Frost", severity="warning", message="Temperature is {temp} °C")

heater = False


# After a set, the SDK sends the new value, so the console shows the switch on.
@bridge.on_command("heater")
async def switch_heater(action, value, **kw):
    global heater
    heater = bool(value)
    await bridge.update_state("status", "Heating" if heater else "Idle")
    await bridge.send_log("info", "Heater on" if heater else "Heater off")
    return {"heater": heater}


async def main():
    connection = asyncio.create_task(bridge.run_async())
    # The simulated sensor: it cools down slowly and warms up with the heater.
    temp = 1.5
    while not connection.done():
        temp = min(25.0, temp + 0.5) if heater else max(0.0, temp - 0.1)
        temp = round(temp, 1)
        await bridge.bulk_update({"temp": temp, "humidity": round(70 - temp)})
        await asyncio.sleep(2)


logging.basicConfig(level=logging.INFO)
asyncio.run(main())
