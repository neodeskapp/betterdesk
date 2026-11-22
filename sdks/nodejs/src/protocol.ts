/**
 * CDAP protocol constants, message helpers, and auth payload builders.
 */

/** The WebSocket subprotocol the gateway offers */
export const SUBPROTOCOL = 'cdap-v1';

/** A device id the gateway takes: 1 to 64 letters, digits, - and _ */
export const DEVICE_ID_PATTERN = /^[A-Za-z0-9_-]{1,64}$/;

// ── Widget types ─────────────────────────────────────────────────────

export const WidgetType = Object.freeze({
  TOGGLE: 'toggle',
  GAUGE: 'gauge',
  BUTTON: 'button',
  LED: 'led',
  CHART: 'chart',
  SELECT: 'select',
  SLIDER: 'slider',
  TEXT: 'text',
  TABLE: 'table',
  TERMINAL: 'terminal',
  DESKTOP: 'desktop',
  VIDEO_STREAM: 'video_stream',
  FILE_BROWSER: 'file_browser',
} as const);

// ── Command actions ──────────────────────────────────────────────────

export const Action = Object.freeze({
  SET: 'set',
  TRIGGER: 'trigger',
  EXECUTE: 'execute',
  RESET: 'reset',
  QUERY: 'query',
} as const);

// ── Severities ───────────────────────────────────────────────────────

export const Severity = Object.freeze({
  CRITICAL: 'critical',
  WARNING: 'warning',
  INFO: 'info',
} as const);

// ── Message envelope ─────────────────────────────────────────────────

/** A message as parseMessage returns it */
export interface Message<P = Record<string, unknown>> {
  type: string;
  payload: P;
  id: string | null;
  timestamp: string | null;
}

export function createMessage(type: string, payload: unknown, id?: string): string {
  const msg: { type: string; payload: unknown; timestamp: string; id?: string } = {
    type,
    payload,
    timestamp: new Date().toISOString(),
  };
  if (id) msg.id = id;
  return JSON.stringify(msg);
}

/** Parses a message from a string, or from a Buffer or anything else by its text */
export function parseMessage(raw: string | { toString(): string }): Message {
  const data = typeof raw === 'string' ? JSON.parse(raw) : JSON.parse(raw.toString());
  return {
    type: data.type || '',
    payload: data.payload || {},
    id: data.id || null,
    timestamp: data.timestamp || null,
  };
}

// ── Auth payload builders ────────────────────────────────────────────

export function authApiKey(key: string, deviceId = '', clientVersion = '1.0.0') {
  return { method: 'api_key', key, device_id: deviceId, client_version: clientVersion };
}

export function authDeviceToken(token: string, deviceId = '', clientVersion = '1.0.0') {
  return { method: 'device_token', token, device_id: deviceId, client_version: clientVersion };
}

export function authUserPassword(username: string, password: string, deviceId = '', clientVersion = '1.0.0') {
  return { method: 'user_password', username, password, device_id: deviceId, client_version: clientVersion };
}
