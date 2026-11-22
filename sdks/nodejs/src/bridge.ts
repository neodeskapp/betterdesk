import { EventEmitter } from 'node:events';
import os from 'node:os';
import WebSocket from 'ws';
import { authApiKey, authDeviceToken, authUserPassword, createMessage, parseMessage, type Message } from './protocol.js';
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
  deviceId?: string;
  deviceName?: string;
  /** default bridge */
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

/** What a command handler receives */
export interface CommandPayload {
  widget_id: string;
  action?: string;
  value?: unknown;
  params?: unknown;
}

/** A command handler; what it returns (or resolves to) becomes the widget's state */
export type CommandHandler = (command: CommandPayload) => unknown;

/**
 * CDAPBridge — connects a Node.js process to BetterDesk CDAP Gateway.
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
  private _state = new Map<string, unknown>();
  private _handlers = new Map<string, CommandHandler>();
  private _ws: WebSocket | null = null;
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
    this.deviceType = options.deviceType || 'bridge';
    this.bridgeName = options.bridgeName || '';
    this.bridgeVersion = options.bridgeVersion || '1.0.0';
    this.heartbeatSec = options.heartbeatSec || 15;
    this.reconnectSec = options.reconnectSec || 5;
    this.maxReconnect = options.maxReconnect || 120;
    this._backoff = this.reconnectSec;
  }

  // ── Widget registration ──────────────────────────────────────────

  /** Add a widget to the manifest. Must be called before connect(). */
  addWidget(widget: Widget): this {
    this._widgets.set(widget.id, widget);
    return this;
  }

  /** Register a command handler for a specific widget. */
  onCommand(widgetId: string, handler: CommandHandler): this {
    this._handlers.set(widgetId, handler);
    return this;
  }

  // ── State updates ────────────────────────────────────────────────

  /** Update a single widget value and push to server. */
  updateState(widgetId: string, value: unknown): void {
    this._state.set(widgetId, value);
    this._send('state_update', { widget_id: widgetId, value });
  }

  /** Bulk-update multiple widget values: { widgetId: value, ... } */
  bulkUpdate(updates: Record<string, unknown>): void {
    const values: Record<string, unknown> = {};
    for (const [k, v] of Object.entries(updates)) {
      this._state.set(k, v);
      values[k] = v;
    }
    this._send('state_update', { values });
  }

  // ── Alerts & logging ─────────────────────────────────────────────

  /** Fire an alert; severity is critical, warning or info. */
  fireAlert(alertId: string, severity: string, message: string, details?: Record<string, unknown>): void {
    this._send('alert', { alert_id: alertId, severity, message, details: details || {} });
  }

  /** Resolve a previously fired alert. */
  resolveAlert(alertId: string): void {
    this._send('alert_resolve', { alert_id: alertId });
  }

  /** Send a log entry; level is debug, info, warn or error. */
  sendLog(level: string, message: string, data?: Record<string, unknown>): void {
    this._send('log', { level, message, data: data || {} });
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
    this._clearHeartbeat();
    if (this._ws && this._ws.readyState === WebSocket.OPEN) {
      this._ws.close(1000, 'bridge stopping');
    }
    this._ws = null;
  }

  // ── Internal ─────────────────────────────────────────────────────

  private async _connectOnce(): Promise<void> {
    return new Promise((resolve, reject) => {
      const ws = new WebSocket(this.server, 'cdap', {
        headers: { 'User-Agent': `betterdesk-cdap-nodejs/${this.bridgeVersion}` },
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
            this.emit('registered', msg.payload);
            this._startHeartbeat();
            break;

          case 'command':
            this._handleCommand(msg);
            break;

          case 'heartbeat_ack':
            // noop
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

    const capabilities = ['telemetry', 'commands', 'alerts', 'logs'];
    const manifest: Record<string, unknown> = {
      manifest_version: '1.0',
      device: {
        id: this.deviceId,
        name: this.deviceName,
        type: this.deviceType,
        firmware_version: this.bridgeVersion,
        hostname: os.hostname(),
        os: `${os.type()} ${os.release()}`,
        platform: os.platform(),
      },
      capabilities,
      widgets,
      heartbeat_interval: this.heartbeatSec,
    };

    if (this.bridgeName) {
      manifest.bridge = { name: this.bridgeName, version: this.bridgeVersion };
    }

    this._send('register', manifest);
  }

  private _startHeartbeat(): void {
    this._clearHeartbeat();
    this._heartbeatTimer = setInterval(() => {
      const widgetValues: Record<string, unknown> = {};
      for (const [k, v] of this._state) {
        widgetValues[k] = v;
      }
      this._send('heartbeat', {
        uptime: Math.floor(process.uptime()),
        widget_values: widgetValues,
      });
    }, this.heartbeatSec * 1000);
  }

  private _clearHeartbeat(): void {
    if (this._heartbeatTimer) {
      clearInterval(this._heartbeatTimer);
      this._heartbeatTimer = null;
    }
  }

  private _handleCommand(msg: Pick<Message, 'id' | 'payload'>): void {
    const { widget_id, action, value, params } = msg.payload as unknown as CommandPayload;
    const handler = this._handlers.get(widget_id);

    this.emit('command', msg.payload);

    if (!handler) {
      this._send('command_response', {
        id: msg.id,
        widget_id,
        success: false,
        error: `No handler for widget ${widget_id}`,
      });
      return;
    }

    const start = Date.now();
    try {
      const result = handler({ widget_id, action, value, params });

      // Handle both sync and async handlers
      if (isThenable(result)) {
        result
          .then((val) => {
            this._sendCommandResponse(msg.id, widget_id, true, val, Date.now() - start);
            if (val !== undefined) {
              this._state.set(widget_id, val);
            }
          })
          .catch((err: Error) => {
            this._sendCommandResponse(msg.id, widget_id, false, null, Date.now() - start, err.message);
          });
      } else {
        this._sendCommandResponse(msg.id, widget_id, true, result, Date.now() - start);
        if (result !== undefined) {
          this._state.set(widget_id, result);
        }
      }
    } catch (err) {
      this._sendCommandResponse(msg.id, widget_id, false, null, Date.now() - start, (err as Error).message);
    }
  }

  private _sendCommandResponse(
    id: string | null,
    widgetId: string,
    success: boolean,
    value: unknown,
    durationMs: number,
    error?: string,
  ): void {
    const payload: Record<string, unknown> = { id, widget_id: widgetId, success, duration_ms: durationMs };
    if (value !== undefined && value !== null) payload.value = value;
    if (error) payload.error = error;
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
