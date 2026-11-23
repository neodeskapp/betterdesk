// A simulated greenhouse on a NeoDesk server's CDAP gateway. It shows a
// temperature, a humidity, a heater switch and a status text, raises a
// frost alert below 2 °C, and warms up while the heater is on.
//
// Build the SDK (npm ci && npm run build), then run it with the server's
// address and an API key whose role may approve enrollment:
//
//   CDAP_SERVER=ws://desk.example.com:21122/cdap CDAP_API_KEY=... node examples/greenhouse.mts
//
// CDAP_DEVICE_ID sets the device's id (default greenhouse-1).
import { CDAPBridge, gauge, textWidget, toggle } from 'neodesk-cdap';

const bridge = new CDAPBridge({
  server: process.env.CDAP_SERVER ?? 'ws://localhost:21122/cdap',
  apiKey: process.env.CDAP_API_KEY ?? '',
  deviceId: process.env.CDAP_DEVICE_ID ?? 'greenhouse-1',
  deviceName: 'Greenhouse',
  deviceType: 'iot',
});

bridge
  .addWidget(gauge('temp', 'Temperature', { unit: '°C', min: -10, max: 40 }))
  .addWidget(gauge('humidity', 'Humidity'))
  .addWidget(toggle('heater', 'Heater'))
  .addWidget(textWidget('status', 'Status', { value: 'Idle' }))
  .addAlert('frost', 'temp < 2', { label: 'Frost', severity: 'warning', message: 'Temperature is {temp} °C' });

let heater = false;

// What the handler returns becomes the heater's value in the console.
bridge.onCommand('heater', ({ value }) => {
  heater = Boolean(value);
  bridge.updateState('status', heater ? 'Heating' : 'Idle');
  bridge.sendLog('info', heater ? 'Heater on' : 'Heater off');
  return heater;
});

bridge.on('registered', () => console.log(`Registered as ${bridge.deviceId}`));
bridge.on('error', (err: Error) => console.error(err.message));

// The simulated sensor: it cools down slowly and warms up with the heater.
let temp = 1.5;
setInterval(() => {
  temp = heater ? Math.min(25, temp + 0.5) : Math.max(0, temp - 0.1);
  temp = Math.round(temp * 10) / 10;
  bridge.bulkUpdate({ temp, humidity: Math.round(70 - temp) });
}, 2000);

void bridge.run();
