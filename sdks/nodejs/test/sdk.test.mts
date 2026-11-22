// Tests the built package (dist) through its own name, as an application
// that installed it imports it.
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import test from 'node:test';

import {
  Action,
  CDAPBridge,
  Severity,
  Widget,
  WidgetType,
  button,
  createMessage,
  gauge,
  parseMessage,
  toggle,
} from 'betterdesk-cdap';
import { authApiKey, authDeviceToken, authUserPassword } from 'betterdesk-cdap/protocol';

/** The bridge's private parts the tests drive without a network */
interface BridgeInternals {
  _send(type: string, payload: unknown): void;
  _register(): void;
  _registered: boolean;
  _handleCommand(msg: { id: string | null; payload: Record<string, unknown> }): void;
}

interface Sent {
  type: string;
  payload: Record<string, any>;
}

test('protocol messages round-trip with strings and buffers', () => {
  const raw = createMessage('telemetry', { temperature: 21 }, 'message-1');
  const parsed = parseMessage(raw);
  assert.equal(parsed.type, 'telemetry');
  assert.deepEqual(parsed.payload, { temperature: 21 });
  assert.equal(parsed.id, 'message-1');
  assert.match(parsed.timestamp ?? '', /^\d{4}-\d{2}-\d{2}T/);

  const fromBuffer = parseMessage(Buffer.from(raw));
  assert.deepEqual(fromBuffer, parsed);
});

test('protocol constants and authentication payloads are stable', () => {
  assert.equal(Action.SET, 'set');
  assert.equal(Severity.WARNING, 'warning');
  assert.equal(WidgetType.GAUGE, 'gauge');
  assert.deepEqual(authApiKey('key', 'device', '2.0.0'), {
    method: 'api_key',
    key: 'key',
    device_id: 'device',
    client_version: '2.0.0',
  });
  assert.deepEqual(authDeviceToken('token'), {
    method: 'device_token',
    token: 'token',
    device_id: '',
    client_version: '1.0.0',
  });
  assert.deepEqual(authUserPassword('admin', 'password'), {
    method: 'user_password',
    username: 'admin',
    password: 'password',
    device_id: '',
    client_version: '1.0.0',
  });
});

test('widget factories produce CDAP-compatible manifests', () => {
  const temperature = gauge('temperature', 'Temperature', { unit: 'C', max: 50 });
  const heater = toggle('heater', 'Heater');
  const restart = button('restart', 'Restart', { confirm: true });

  assert.ok(temperature instanceof Widget);
  assert.deepEqual(temperature.toJSON(), {
    type: 'gauge',
    id: 'temperature',
    label: 'Temperature',
    unit: 'C',
    max: 50,
    precision: 1,
    permissions: { read: 'viewer' },
  });
  assert.deepEqual(heater.toJSON(), {
    type: 'toggle',
    id: 'heater',
    label: 'Heater',
    permissions: { read: 'viewer', control: 'operator' },
  });
  assert.equal(restart.toJSON().confirm, true);
});

/** A bridge whose messages go into sent instead of a socket */
function offlineBridge(): { bridge: CDAPBridge; internals: BridgeInternals; sent: Sent[] } {
  const bridge = new CDAPBridge({
    server: 'ws://127.0.0.1:1/cdap',
    apiKey: 'test-only-key',
    deviceId: 'device-1',
    heartbeatSec: 5,
  });
  const internals = bridge as unknown as BridgeInternals;
  const sent: Sent[] = [];
  internals._send = (type, payload) => {
    sent.push({ type, payload: payload as Record<string, any> });
  };
  return { bridge, internals, sent };
}

test('a bridge needs a device id the gateway takes', () => {
  assert.throws(() => new CDAPBridge({ server: 'ws://127.0.0.1:1/cdap', deviceId: '' }), /deviceId is required/);
  assert.throws(() => new CDAPBridge({ server: 'ws://127.0.0.1:1/cdap', deviceId: 'a b' }), /deviceId is required/);
  assert.equal(new CDAPBridge({ server: 'ws://127.0.0.1:1/cdap', deviceId: 'plc_1-a' }).deviceType, 'custom');
});

test('the register message carries the manifest with widgets and alerts', () => {
  const { bridge, internals, sent } = offlineBridge();
  bridge.addWidget(toggle('heater', 'Heater'));
  bridge.addAlert('frost', 'temp < 2', { label: 'Frost', message: 'Temperature is {temp}' });
  assert.throws(() => bridge.addAlert('hot', 'temp > 30', { severity: 'loud' }), /severity/);
  internals._register();

  assert.equal(sent[0]?.type, 'register');
  const manifest = sent[0]?.payload.manifest;
  assert.deepEqual(manifest.device, { name: bridge.deviceName, type: 'custom' });
  assert.equal(manifest.widgets[0].id, 'heater');
  assert.deepEqual(manifest.capabilities, ['telemetry', 'commands', 'alerts', 'logs']);
  assert.deepEqual(manifest.alerts, [
    { id: 'frost', label: 'Frost', severity: 'warning', condition: 'temp < 2', message: 'Temperature is {temp}' },
  ]);
});

test('values wait for the registration, then go out as the gateway reads them', () => {
  const { bridge, internals, sent } = offlineBridge();
  bridge.bulkUpdate({ temp: 21 });
  bridge.sendLog('info', 'early');
  assert.equal(sent.length, 0);

  internals._registered = true;
  bridge.updateState('temp', 22);
  bridge.bulkUpdate({ temp: 23, humidity: 40 });
  bridge.sendLog('warning', 'late', { sensor: 'a' });
  assert.deepEqual(sent, [
    { type: 'state_update', payload: { widget_id: 'temp', value: 22 } },
    {
      type: 'bulk_update',
      payload: { updates: [{ widget_id: 'temp', value: 23 }, { widget_id: 'humidity', value: 40 }] },
    },
    { type: 'log', payload: { level: 'warning', message: 'late', context: { sensor: 'a' } } },
  ]);
});

test('a command gets an answer the gateway reads and its value goes out', async () => {
  const { bridge, internals, sent } = offlineBridge();
  internals._registered = true;
  bridge.addWidget(toggle('heater', 'Heater'));
  bridge.onCommand('heater', async ({ value }) => Boolean(value));
  bridge.onCommand('broken', () => {
    throw new Error('relay stuck');
  });

  internals._handleCommand({
    id: 'command-1',
    payload: { command_id: 'command-1', widget_id: 'heater', action: Action.SET, value: true },
  });
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(sent[0]?.type, 'command_response');
  assert.equal(sent[0]?.payload.command_id, 'command-1');
  assert.equal(sent[0]?.payload.status, 'ok');
  assert.equal(sent[0]?.payload.result, true);
  assert.equal(typeof sent[0]?.payload.execution_time_ms, 'number');
  assert.deepEqual(sent[1], { type: 'state_update', payload: { widget_id: 'heater', value: true } });

  internals._handleCommand({ id: null, payload: { command_id: 'command-2', widget_id: 'broken', action: 'trigger' } });
  internals._handleCommand({ id: null, payload: { command_id: 'command-3', widget_id: 'nothing', action: 'trigger' } });
  assert.deepEqual(
    sent.slice(2).map((m) => [m.payload.command_id, m.payload.status, m.payload.error_message]),
    [
      ['command-2', 'error', 'relay stuck'],
      ['command-3', 'error', 'No handler for widget nothing'],
    ],
  );
});

test('the package loads with require, as the README shows', () => {
  const require = createRequire(import.meta.url);
  const sdk = require('betterdesk-cdap');
  assert.deepEqual(Object.keys(sdk).sort(), [
    'Action',
    'CDAPBridge',
    'Severity',
    'Widget',
    'WidgetType',
    'button',
    'chart',
    'createMessage',
    'gauge',
    'led',
    'parseMessage',
    'select',
    'slider',
    'table',
    'textWidget',
    'toggle',
  ]);
  assert.equal(typeof require('betterdesk-cdap/widgets').gauge, 'function');
  assert.equal(typeof require('betterdesk-cdap/protocol').authApiKey, 'function');
});
