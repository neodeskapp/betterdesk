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
  _handleCommand(msg: { id: string; payload: Record<string, unknown> }): void;
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

test('bridge registration and command handling are testable without a network', async () => {
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

  bridge.addWidget(toggle('heater', 'Heater'));
  bridge.onCommand('heater', ({ value }) => Boolean(value));
  internals._register();

  assert.equal(sent[0]?.type, 'register');
  assert.equal(sent[0]?.payload.widgets[0].id, 'heater');
  assert.deepEqual(sent[0]?.payload.capabilities, ['telemetry', 'commands', 'alerts', 'logs']);

  await internals._handleCommand({
    id: 'command-1',
    payload: { widget_id: 'heater', action: Action.SET, value: true },
  });
  const response = sent.at(-1);
  assert.equal(response?.type, 'command_response');
  assert.equal(response?.payload.success, true);
  assert.equal(response?.payload.value, true);
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
