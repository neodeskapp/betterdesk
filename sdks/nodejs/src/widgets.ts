/**
 * Widget definition helpers for CDAP manifest construction.
 */

/** Who may read and who may control a widget (viewer, operator, admin) */
export interface WidgetPermissions {
  read?: string;
  control?: string;
}

/** The optional fields of a widget; any other field is kept on the widget too */
export interface WidgetOptions {
  group?: string;
  value?: unknown;
  readonly?: boolean;
  unit?: string;
  min?: number;
  max?: number;
  step?: number;
  precision?: number;
  warning_low?: number;
  warning_high?: number;
  confirm?: boolean;
  confirm_message?: string;
  style?: string;
  icon?: string;
  cooldown?: number;
  options?: unknown[];
  chart_type?: string;
  points?: number;
  series?: unknown[];
  retention?: number;
  columns?: unknown[];
  max_rows?: number;
  sortable?: boolean;
  permissions?: WidgetPermissions;
  [key: string]: unknown;
}

const OPTIONAL_KEYS = [
  'group', 'value', 'readonly', 'unit', 'min', 'max', 'step', 'precision',
  'warning_low', 'warning_high', 'confirm', 'confirm_message', 'style',
  'icon', 'cooldown', 'options', 'chart_type', 'points', 'series',
  'retention', 'columns', 'max_rows', 'sortable', 'permissions',
] as const;

export class Widget {
  type: string;
  id: string;
  label: string;
  declare permissions?: WidgetPermissions;
  [key: string]: unknown;

  constructor(type: string, id: string, label: string, opts: WidgetOptions = {}) {
    this.type = type;
    this.id = id;
    this.label = label;
    Object.assign(this, opts);
  }

  toJSON(): Record<string, unknown> {
    const d: Record<string, unknown> = { type: this.type, id: this.id, label: this.label };
    for (const key of OPTIONAL_KEYS) {
      const v = this[key];
      if (v !== undefined && v !== null && v !== 0 && v !== '' && v !== false) {
        d[key] = v;
      }
    }
    // Always include permissions if present (even if empty sub-fields)
    if (this.permissions) d.permissions = this.permissions;
    return d;
  }
}

// ── Factory helpers ──────────────────────────────────────────────────

export function gauge(id: string, label: string, opts: WidgetOptions = {}): Widget {
  return new Widget('gauge', id, label, {
    unit: '%', min: 0, max: 100, precision: 1,
    permissions: { read: 'viewer' },
    ...opts,
  });
}

export function toggle(id: string, label: string, opts: WidgetOptions = {}): Widget {
  return new Widget('toggle', id, label, {
    value: false,
    permissions: { read: 'viewer', control: 'operator' },
    ...opts,
  });
}

export function button(id: string, label: string, opts: WidgetOptions = {}): Widget {
  return new Widget('button', id, label, {
    permissions: { control: 'operator' },
    ...opts,
  });
}

export function textWidget(id: string, label: string, opts: WidgetOptions = {}): Widget {
  return new Widget('text', id, label, {
    readonly: true,
    permissions: { read: 'viewer' },
    ...opts,
  });
}

export function led(id: string, label: string, opts: WidgetOptions = {}): Widget {
  return new Widget('led', id, label, {
    value: false,
    permissions: { read: 'viewer' },
    ...opts,
  });
}

export function slider(id: string, label: string, opts: WidgetOptions = {}): Widget {
  return new Widget('slider', id, label, {
    min: 0, max: 100, step: 1,
    permissions: { read: 'viewer', control: 'operator' },
    ...opts,
  });
}

export function select(id: string, label: string, opts: WidgetOptions = {}): Widget {
  return new Widget('select', id, label, {
    options: [],
    permissions: { read: 'viewer', control: 'operator' },
    ...opts,
  });
}

export function chart(id: string, label: string, opts: WidgetOptions = {}): Widget {
  return new Widget('chart', id, label, {
    chart_type: 'line', points: 60, series: [],
    permissions: { read: 'viewer' },
    ...opts,
  });
}

export function table(id: string, label: string, opts: WidgetOptions = {}): Widget {
  return new Widget('table', id, label, {
    columns: [], max_rows: 100, sortable: true,
    permissions: { read: 'viewer' },
    ...opts,
  });
}
