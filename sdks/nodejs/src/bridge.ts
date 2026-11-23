import { EventEmitter } from 'node:events';
import os from 'node:os';
import WebSocket from 'ws';
import {
  DEVICE_ID_PATTERN,
  SUBPROTOCOL,
  Severity,
  authApiKey,
  authDeviceToken,
  authUserPassword,
  createMessage,
  parseMessage,
  type Message,
} from './protocol.js';
import type { Widget } from './widgets.js';

export interface CDAPBridgeOptions {
  /** WebSocket URL, e.g. ws://host:21122/cdap */
  server: string;
  /** api_key | device_token | user_password (default api_key) */
  authMethod?: string;
  apiKey?: string;
  deviceToken?: string;
  username?: string;
  password?: string;
  /** The id the device registers under: 1 to 64 letters, digits, - and _ */
  deviceId: string;
  deviceName?: string;
  /** scada, iot, os_agent, network, camera, desktop or custom (default custom) */
  deviceType?: string;
  bridgeName?: string;
  /** default 1.0.0 */
  bridgeVersion?: string;
  /** seconds between heartbeats (default 15) */
  heartbeatSec?: number;
  /** first reconnect delay in seconds (default 5) */
  reconnectSec?: number;
  /** longest reconnect delay in seconds (default 120) */
  maxReconnect?: number;
}

/** What a command handler receives: the command the gateway sent */
export interface CommandPayload {
  command_id: string;
  widget_id: string;
  /** set, trigger, execute, reset or query */
  action: string;
  value?: unknown;
  /** the console account that sent it */
  operator?: string;
  reason?: string;
}

/** A command handler; what it returns (or resolves to) becomes the widget's value */
export type CommandHandler = (command: CommandPayload) => unknown;

/** The optional parts of an alert */
export interface AlertOptions {
  /** shown with the alert (default the alert's id) */
  label?: string;
  /** critical, warning or info (default warning) */
  severity?: string;
  /** the alert's text; {widget id} in it is replaced with the widget's value */
  message?: string;
}

/** An alert of the manifest, as the gateway takes it */
export interface AlertDefinition {
  id: string;
  label: string;
  severity: string;
  condition: string;
  message: string;
}

const SEVERITIES: readonly string[] = Object.values(Severity);

/**
 * CDAPBridge — connects a Node.js process to NeoDesk CDAP Gateway.
 *
 * Events: connected, disconnected, registered, command, error, message,
 * and the type of any other message the server sends.
 */
export class CDAPBridge extends EventEmitter {
  server: string;
  authMethod: string;
  apiKey: string;
  deviceToken: string;
  username: string;
  password: string;
  deviceId: string;
  deviceName: string;
  deviceType: string;
  bridgeName: string;
  bridgeVersion: string;
  heartbeatSec: number;
  reconnectSec: number;
  maxReconnect: number;

  private _widgets = new Map<string, Widget>();
  private _alerts = new Map<string, AlertDefinition>();
  private _state = new Map<string, unknown>();
  private _handlers = new Map<string, CommandHandler>();
  private _ws: WebSocket | null = null;
  private _registered = false;
  private _heartbeatTimer: NodeJS.Timeout | null = null;
  private _token: string | null = null;
  private _running = false;
  private _backoff: number;

  constructor(options: CDAPBridgeOptions) {
    super();
    this.server = options.server;
    this.authMethod = options.authMethod || 'api_key';
    this.apiKey = options.apiKey || '';
    this.deviceToken = options.deviceToken || '';
    this.username = options.username || '';
    this.password = options.password || '';
    this.deviceId = options.deviceId || '';
    this.deviceName = options.deviceName || os.hostname();
    this.deviceType = options.deviceType || 'custom';
    this.bridgeName = options.bridgeName || '';
    this.bridgeVersion = options.bridgeVersion || '1.0.0';
    this.heartbeatSec = options.heartbeatSec || 15;
    this.reconnectSec = options.reconnectSec || 5;
    this.maxReconnect = options.maxReconnect || 120;
    this._backoff = this.reconnectSec;
    if (!DEVICE_ID_PATTERN.test(this.deviceId)) {
      throw new Error('deviceId is required: 1 to 64 letters, digits, - and _');
    }
  }

  // ── Manifest ─────────────────────────────────────────────────────

  /** Add a widget to the manifest. Must be called before run(). */
  addWidget(widget: Widget): this {
    this._widgets.set(widget.id, widget);
    if (widget.value !== undefined) this._state.set(widget.id, widget.value);
    return this;
  }

  /**
   * Add an alert to the manifest. Must be called before run(). The gateway
   * raises it under Active alerts while the condition holds, and clears it
   * when it no longer does. The condition is `<widget id> <operator> <value>`
   * with ==, !=, >, <, >= or <=, such as `temp < 2` or `door == 'open'`.
   */
  addAlert(id: string, condition: string, opts: AlertOptions = {}): this {
    const severity = opts.severity ?? Severity.WARNING;
    if (!SEVERITIES.includes(severity)) {
      throw new Error(`alert ${id}: severity must be critical, warning or info`);
    }
    this._alerts.set(id, { id, label: opts.label ?? id, severity, condition, message: opts.message ?? '' });
    return this;
  }

  /** Register a command handler for a specific widget. */
  onCommand(widgetId: string, handler: CommandHandler): this {
    this._handlers.set(widgetId, handler);
    return this;
  }

  // ── Values and logs ──────────────────────────────────────────────
  //
  // Values set before the device is registered are kept and go out with
  // the heartbeat right after registration.

  /** Set a widget's value and send it to the server. */
  updateState(widgetId: string, value: unknown): void {
    this._state.set(widgetId, value);
    if (this._registered) this._send('state_update', { widget_id: widgetId, value });
  }

  /** Set several widgets' values at once: { widgetId: value, ... } */
  bulkUpdate(updates: Record<string, unknown>): void {
    const list: { widget_id: string; value: unknown }[] = [];
    for (const [widgetId, value] of Object.entries(updates)) {
      this._state.set(widgetId, value);
      list.push({ widget_id: widgetId, value });
    }
    if (this._registered) this._send('bulk_update', { updates: list });
  }

  /** Send a log entry; level is debug, info, warning, error or critical. Dropped while not registered. */
  sendLog(level: string, message: string, context?: Record<string, unknown>): void {
    if (!this._registered) return;
    this._send('log', context === undefined ? { level, message } : { level, message, context });
  }

  // ── Connection lifecycle ─────────────────────────────────────────

  /**
   * Start the bridge — connects, authenticates, registers, and maintains heartbeat.
   * Reconnects automatically on disconnection. Resolves when stop() is called.
   */
  async run(): Promise<void> {
    this._running = true;
    this._backoff = this.reconnectSec;

    while (this._running) {
      try {
        await this._connectOnce();
        // If we get here, connection was cleanly closed
      } catch (err) {
        if (!this._running) break;
        this.emit('error', err);
      }

      if (!this._running) break;

      // Exponential backoff with jitter
      const jitter = Math.floor(Math.random() * (this._backoff / 4));
      const delay = this._backoff + jitter;
      await this._sleep(delay * 1000);
      this._backoff = Math.min(this._backoff * 2, this.maxReconnect);
    }
  }

  /** Stop the bridge gracefully. */
  stop(): void {
    this._running = false;
    this._registered = false;
    this._clearHeartbeat();
    if (this._ws && this._ws.readyState === WebSocket.OPEN) {
      this._ws.close(1000, 'bridge stopping');
    }
    this._ws = null;
  }

  // ── Internal ─────────────────────────────────────────────────────

  private async _connectOnce(): Promise<void> {
    return new Promise((resolve, reject) => {
      const ws = new WebSocket(this.server, SUBPROTOCOL, {
        headers: { 'User-Agent': `neodesk-cdap-nodejs/${this.bridgeVersion}` },
        handshakeTimeout: 10000,
      });

      let authenticated = false;

      ws.on('open', () => {
        this._ws = ws;
        this._backoff = this.reconnectSec; // reset backoff on success
        this.emit('connected');
        this._authenticate();
      });

      ws.on('message', (raw) => {
        let msg: Message;
        try {
          msg = parseMessage(raw);
        } catch {
          return;
        }

        this.emit('message', msg);

        switch (msg.type) {
          case 'auth_result':
            if (msg.payload.success) {
              authenticated = true;
              this._token = (msg.payload.token as string) || this._token;
              if (msg.payload.device_id) this.deviceId = msg.payload.device_id as string;
              this._register();
            } else {
              ws.close(4001, 'auth failed');
              reject(new Error(`Auth failed: ${msg.payload.error || 'unknown'}`));
            }
            break;

          case 'registered':
            this._registered = true;
            this.emit('registered', msg.payload);
            this._startHeartbeat();
            break;

          case 'command':
            this._handleCommand(msg);
            break;

          case 'error':
            this.emit('error', new Error((msg.payload.message as string) || 'server error'));
            break;

          default:
            // Forward unrecognized message types via event
            this.emit(msg.type, msg.payload);
            break;
        }
      });

      ws.on('close', (code, reason) => {
        this._registered = false;
        this._clearHeartbeat();
        this._ws = null;
        this.emit('disconnected', { code, reason: reason.toString() });
        if (authenticated) {
          resolve(); // normal reconnect
        } else if (code !== 4001) {
          resolve();
        }
      });

      ws.on('error', (err) => {
        // error fires before close, let close handler resolve/reject
        if (!authenticated) {
          reject(err);
        }
      });
    });
  }

  private _authenticate(): void {
    let payload;
    switch (this.authMethod) {
      case 'device_token':
        payload = authDeviceToken(this.deviceToken, this.deviceId, this.bridgeVersion);
        break;
      case 'user_password':
        payload = authUserPassword(this.username, this.password, this.deviceId, this.bridgeVersion);
        break;
      default:
        payload = authApiKey(this.apiKey, this.deviceId, this.bridgeVersion);
        break;
    }
    this._send('auth', payload);
  }

  private _register(): void {
    const widgets: unknown[] = [];
    for (const w of this._widgets.values()) {
      widgets.push(typeof w.toJSON === 'function' ? w.toJSON() : w);
    }

    const manifest: Record<string, unknown> = {
      manifest_version: '1.0',
      device: { name: this.deviceName, type: this.deviceType },
      capabilities: ['telemetry', 'commands', 'alerts', 'logs'],
      widgets,
      heartbeat_interval: this.heartbeatSec,
    };
    if (this._alerts.size > 0) manifest.alerts = [...this._alerts.values()];
    if (this.bridgeName) {
      manifest.bridge = { name: this.bridgeName, version: this.bridgeVersion };
    }

    this._send('register', { manifest });
  }

  /** Sends a heartbeat with every known value now, then every heartbeatSec. */
  private _startHeartbeat(): void {
    this._clearHeartbeat();
    const beat = () => this._send('heartbeat', { widget_values: Object.fromEntries(this._state) });
    beat();
    this._heartbeatTimer = setInterval(beat, this.heartbeatSec * 1000);
  }

  private _clearHeartbeat(): void {
    if (this._heartbeatTimer) {
      clearInterval(this._heartbeatTimer);
      this._heartbeatTimer = null;
    }
  }

  /**
   * Runs the widget's handler and answers the command. A value the handler
   * returns becomes the widget's value and goes to the server at once, so
   * the console shows the control's new state.
   */
  private _handleCommand(msg: Pick<Message, 'id' | 'payload'>): void {
    const command = msg.payload as unknown as CommandPayload;
    const commandId = command.command_id || msg.id || '';
    const widgetId = command.widget_id;
    const handler = this._handlers.get(widgetId);

    this.emit('command', command);

    if (!handler) {
      this._sendCommandResponse(commandId, 'error', undefined, 0, `No handler for widget ${widgetId}`);
      return;
    }

    const start = Date.now();
    const done = (value: unknown) => {
      this._sendCommandResponse(commandId, 'ok', value, Date.now() - start);
      if (value !== undefined) this.updateState(widgetId, value);
    };
    const fail = (err: unknown) => {
      const message = err instanceof Error ? err.message : String(err);
      this._sendCommandResponse(commandId, 'error', undefined, Date.now() - start, message);
    };
    try {
      const result = handler(command);
      if (isThenable(result)) {
        result.then(done, fail);
      } else {
        done(result);
      }
    } catch (err) {
      fail(err);
    }
  }

  private _sendCommandResponse(
    commandId: string,
    status: 'ok' | 'error',
    result: unknown,
    durationMs: number,
    error?: string,
  ): void {
    const payload: Record<string, unknown> = { command_id: commandId, status, execution_time_ms: durationMs };
    if (result !== undefined) payload.result = result;
    if (error) payload.error_message = error;
    this._send('command_response', payload);
  }

  private _send(type: string, payload: unknown): void {
    if (this._ws && this._ws.readyState === WebSocket.OPEN) {
      this._ws.send(createMessage(type, payload));
    }
  }

  private _sleep(ms: number): Promise<void> {
    return new Promise((resolve) => setTimeout(resolve, ms));
  }
}

/** result && typeof result.then === 'function' */
function isThenable(result: unknown): result is Promise<unknown> {
  return !!result && typeof (result as { then?: unknown }).then === 'function';
}
