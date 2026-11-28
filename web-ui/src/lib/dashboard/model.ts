/**
 * The overview's dashboards without a DOM. A dashboard is a list of widgets
 * in reading order; each widget has a type, a width (s, m, l, full: a
 * third, half, two thirds or all of the row) and, for the custom ones, its
 * own settings. An account keeps its own dashboards (GET/PUT
 * /api/dashboards); people with server.config publish shared ones for every
 * account or for some roles. Without anything saved, the account sees the
 * built-in dashboard, which is the overview as it was before dashboards.
 *
 * The server checks ids, sizes and lengths but not widget types: a type
 * this console doesn't know (from a newer version) stays in the layout and
 * shows as unavailable.
 */

export type WidgetSize = 's' | 'm' | 'l' | 'full';
export const SIZES: readonly WidgetSize[] = ['s', 'm', 'l', 'full'];

/** Each width's string key and English text */
export const SIZE_LABELS: Record<WidgetSize, [string, string]> = {
	s: ['dashboard.size_s', 'A third'],
	m: ['dashboard.size_m', 'Half'],
	l: ['dashboard.size_l', 'Two thirds'],
	full: ['dashboard.size_full', 'Full width']
};

export interface Widget {
	id: string;
	type: string;
	size: WidgetSize;
	title?: string;
	config?: Record<string, unknown>;
}

export interface Board {
	id: string;
	name: string;
	widgets: Widget[];
}

/** Who sees a shared dashboard and how */
export interface Sharing {
	/** Roles that see it; empty means every account */
	roles: string[];
	/** Accounts can't hide it */
	locked: boolean;
	/** Opens first for the accounts that see it, unless they picked another */
	home: boolean;
}

export interface SharedBoard extends Board {
	/** Roles that see it; empty means every account */
	roles: string[];
	/** Accounts can't hide it */
	locked: boolean;
	/** Opens first for the accounts that see it, unless they picked another */
	home: boolean;
}

export interface OwnBoards {
	boards: Board[];
	/** The tab that opens first: an own id, "shared:<id>", or "" for the first */
	active: string;
	/** Shared boards this account hid */
	hidden: string[];
}

/* ── Limits, the same as the server's ── */

export const LIMITS = {
	boards: 20,
	widgets: 40,
	name: 80,
	title: 80,
	note: 4000,
	links: 20,
	linkLabel: 80,
	linkUrl: 500
} as const;

const ID = /^[a-z0-9][a-z0-9-]{0,39}$/;
export const isId = (s: unknown): s is string => typeof s === 'string' && ID.test(s);

/** A new id that isn't in taken: 8 hex characters */
export function newId(taken: Iterable<string> = []): string {
	const used = new Set(taken);
	for (;;) {
		const bytes = new Uint8Array(4);
		crypto.getRandomValues(bytes);
		const id = [...bytes].map((b) => b.toString(16).padStart(2, '0')).join('');
		// An id starting with a digit is fine for the server; keep it a letter for readable URLs
		const out = /^[0-9]/.test(id) ? `w${id.slice(1)}` : id;
		if (!used.has(out)) return out;
	}
}

/* ── The widget catalogue ── */

export type WidgetGroup = 'overview' | 'custom';

export interface WidgetKind {
	type: string;
	group: WidgetGroup;
	/** Name and one-line description in the Add widget list */
	nameKey: string;
	name: string;
	hintKey: string;
	hint: string;
	icon: string;
	size: WidgetSize;
	/** Widths it can take; the first wider than the content is the default */
	sizes: readonly WidgetSize[];
	/** Permission the account needs to see it; others don't get it offered and see nothing */
	needs?: string;
	/** Opens its settings dialog when it is added */
	configurable?: boolean;
	/** Only one on a dashboard */
	single?: boolean;
	/** Draws its own heading, so it takes no title */
	untitled?: boolean;
}

export const KINDS: readonly WidgetKind[] = [
	{
		type: 'attention',
		group: 'overview',
		nameKey: 'dashboard.widget.attention',
		name: 'Needs attention',
		hintKey: 'dashboard.widget.attention_hint',
		hint: 'Join requests, help requests, banned devices and server problems',
		icon: 'info',
		size: 'm',
		sizes: ['s', 'm', 'l', 'full'],
		single: true
	},
	{
		type: 'devices',
		group: 'overview',
		nameKey: 'dashboard.widget.devices',
		name: 'Devices',
		hintKey: 'dashboard.widget.devices_hint',
		hint: 'How many devices are online, offline and banned',
		icon: 'desktop_windows',
		size: 'm',
		sizes: ['s', 'm', 'l', 'full'],
		needs: 'device.view',
		single: true
	},
	{
		type: 'stat',
		group: 'overview',
		nameKey: 'dashboard.widget.stat',
		name: 'Single number',
		hintKey: 'dashboard.widget.stat_hint',
		hint: 'One count in large type, such as devices online or open help requests',
		icon: 'dashboard',
		size: 's',
		sizes: ['s', 'm', 'l', 'full'],
		configurable: true
	},
	{
		type: 'trends',
		group: 'overview',
		nameKey: 'dashboard.widget.trends',
		name: 'Trends',
		hintKey: 'dashboard.widget.trends_hint',
		hint: 'Remote sessions and help requests over the last 14 days',
		icon: 'history',
		size: 'full',
		sizes: ['l', 'full'],
		needs: 'audit.view',
		single: true,
		untitled: true
	},
	{
		type: 'client-setup',
		group: 'overview',
		nameKey: 'dashboard.widget.client_setup',
		name: 'Client setup',
		hintKey: 'dashboard.widget.client_setup_hint',
		hint: 'The values RustDesk clients need, with copy, QR code and pairing',
		icon: 'qr_code_2',
		size: 's',
		sizes: ['s', 'm', 'l', 'full'],
		single: true
	},
	{
		type: 'server-status',
		group: 'overview',
		nameKey: 'dashboard.widget.server_status',
		name: 'Server status',
		hintKey: 'dashboard.widget.server_status_hint',
		hint: 'Whether the ID and relay servers run, uptime and ports',
		icon: 'settings_ethernet',
		size: 's',
		sizes: ['s', 'm', 'l', 'full'],
		single: true
	},
	{
		type: 'note',
		group: 'custom',
		nameKey: 'dashboard.widget.note',
		name: 'Note',
		hintKey: 'dashboard.widget.note_hint',
		hint: 'Your own text: instructions, contacts, a checklist',
		icon: 'description',
		size: 'm',
		sizes: ['s', 'm', 'l', 'full'],
		configurable: true
	},
	{
		type: 'links',
		group: 'custom',
		nameKey: 'dashboard.widget.links',
		name: 'Links',
		hintKey: 'dashboard.widget.links_hint',
		hint: 'Shortcuts to console pages or other websites',
		icon: 'link',
		size: 's',
		sizes: ['s', 'm', 'l', 'full'],
		configurable: true
	},
	{
		type: 'boards',
		group: 'custom',
		nameKey: 'dashboard.widget.boards',
		name: 'Dashboards',
		hintKey: 'dashboard.widget.boards_hint',
		hint: 'Shortcuts to your other dashboards',
		icon: 'dashboard',
		size: 's',
		sizes: ['s', 'm', 'l', 'full'],
		configurable: true
	}
];

export function kindOf(type: string): WidgetKind | undefined {
	return KINDS.find((k) => k.type === type);
}

/** Kinds this account may add to a board right now: allowed, and not a single one that is already there */
export function addable(board: Board, can: (permission: string) => boolean): WidgetKind[] {
	return KINDS.filter(
		(k) => (!k.needs || can(k.needs)) && !(k.single && board.widgets.some((w) => w.type === k.type))
	);
}

/** Whether a widget shows for this account: a known type it may see. Unknown types show as unavailable in edit mode only. */
export function visibleTo(w: Widget, can: (permission: string) => boolean): boolean {
	const kind = kindOf(w.type);
	return !!kind && (!kind.needs || can(kind.needs));
}

/* ── The built-in dashboard: the overview as it was ── */

export const DEFAULT_BOARD_ID = 'overview';

export function defaultBoard(name: string): Board {
	return {
		id: DEFAULT_BOARD_ID,
		name,
		widgets: [
			// A third each in the first row, Client setup running down the right;
			// Trends and Server status fill the two thirds under the first two
			{ id: 'attention', type: 'attention', size: 's' },
			{ id: 'devices', type: 'devices', size: 's' },
			{ id: 'client-setup', type: 'client-setup', size: 's' },
			{ id: 'trends', type: 'trends', size: 'l' },
			{ id: 'server-status', type: 'server-status', size: 'l' }
		]
	};
}

/* ── Stat widget ── */

export const METRICS = ['online', 'offline', 'total', 'banned', 'registrations', 'help'] as const;
export type Metric = (typeof METRICS)[number];

export interface MetricInfo {
	key: string;
	label: string;
	href: string;
	/** Permissions the count needs, all of them, separated by spaces */
	needs?: string;
}

export function metricAllowed(info: MetricInfo, can: (permission: string) => boolean): boolean {
	return !info.needs || info.needs.split(' ').every(can);
}

export const METRIC_INFO: Record<Metric, MetricInfo> = {
	online: {
		key: 'dashboard.metric_online',
		label: 'Devices online',
		href: '/devices?status=online',
		needs: 'device.view'
	},
	offline: {
		key: 'dashboard.metric_offline',
		label: 'Devices offline',
		href: '/devices?status=offline',
		needs: 'device.view'
	},
	total: { key: 'dashboard.metric_total', label: 'Devices in total', href: '/devices', needs: 'device.view' },
	banned: {
		key: 'dashboard.metric_banned',
		label: 'Banned devices',
		href: '/devices?status=banned',
		needs: 'device.view'
	},
	registrations: {
		key: 'nav.registrations',
		label: 'Join requests',
		href: '/registrations',
		needs: 'enrollment.approve'
	},
	help: {
		key: 'nav.help_requests',
		label: 'Help requests',
		href: '/help-requests',
		needs: 'device.view chat.support'
	}
};

export function metricOf(w: Widget): Metric {
	const m = w.config?.metric;
	return typeof m === 'string' && (METRICS as readonly string[]).includes(m) ? (m as Metric) : 'online';
}

/* ── Note widget ── */

export function noteText(w: Widget): string {
	const text = w.config?.text;
	return typeof text === 'string' ? text : '';
}

/* ── Links widget ── */

export interface LinkItem {
	label: string;
	url: string;
}

/**
 * Whether a link can go on a dashboard: a console address ("/devices"),
 * or an http or https address. Everything else (javascript:, data:,
 * protocol-relative //host) is refused.
 */
export function safeUrl(raw: string): boolean {
	const url = raw.trim();
	if (!url || url.length > LIMITS.linkUrl) return false;
	if (url.startsWith('/')) return !url.startsWith('//') && !url.startsWith('/\\');
	try {
		const u = new URL(url);
		return u.protocol === 'http:' || u.protocol === 'https:';
	} catch {
		return false;
	}
}

export const isExternal = (url: string) => !url.trim().startsWith('/');

export function linksOf(w: Widget): LinkItem[] {
	const raw = w.config?.links;
	if (!Array.isArray(raw)) return [];
	return raw
		.filter(
			(l): l is LinkItem =>
				!!l && typeof l === 'object' && typeof l.label === 'string' && typeof l.url === 'string'
		)
		.filter((l) => safeUrl(l.url))
		.slice(0, LIMITS.links);
}

/* ── Dashboards widget ── */

export function boardLinksOf(w: Widget): string[] {
	const raw = w.config?.boards;
	return Array.isArray(raw) ? raw.filter((b): b is string => typeof b === 'string') : [];
}

/* ── Changing a board (each returns a new board) ── */

export function addWidget(board: Board, kind: WidgetKind, config?: Record<string, unknown>): Board {
	const widget: Widget = { id: newId(board.widgets.map((w) => w.id)), type: kind.type, size: kind.size };
	if (config) widget.config = config;
	return { ...board, widgets: [...board.widgets, widget] };
}

export function removeWidget(board: Board, id: string): Board {
	return { ...board, widgets: board.widgets.filter((w) => w.id !== id) };
}

export function updateWidget(board: Board, id: string, change: Partial<Omit<Widget, 'id'>>): Board {
	return { ...board, widgets: board.widgets.map((w) => (w.id === id ? clean({ ...w, ...change }) : w)) };
}

/** Moves a widget by steps places (negative: earlier); stays inside the list */
export function moveWidget(board: Board, id: string, steps: number): Board {
	const from = board.widgets.findIndex((w) => w.id === id);
	if (from < 0) return board;
	const to = Math.max(0, Math.min(board.widgets.length - 1, from + steps));
	return moveTo(board, id, to);
}

/** Puts a widget at index to (dragging) */
export function moveTo(board: Board, id: string, to: number): Board {
	const from = board.widgets.findIndex((w) => w.id === id);
	if (from < 0 || from === to) return board;
	const widgets = [...board.widgets];
	const [w] = widgets.splice(from, 1);
	if (!w) return board;
	widgets.splice(Math.max(0, Math.min(widgets.length, to)), 0, w);
	return { ...board, widgets };
}

/** Drops empty titles and settings, so what is saved stays small */
function clean(w: Widget): Widget {
	const out: Widget = { id: w.id, type: w.type, size: w.size };
	const title = w.title?.trim();
	if (title) out.title = title.slice(0, LIMITS.title);
	if (w.config && Object.keys(w.config).length) out.config = w.config;
	return out;
}

/** A copy with new ids, named name */
export function copyBoard(board: Board, name: string, taken: Iterable<string>): Board {
	return {
		id: newId(taken),
		name: name.slice(0, LIMITS.name),
		widgets: board.widgets.map((w) => clean({ ...w }))
	};
}

/* ── Reading what the server sent ── */

function readWidget(raw: unknown): Widget | null {
	if (!raw || typeof raw !== 'object') return null;
	const r = raw as Record<string, unknown>;
	if (!isId(r.id) || typeof r.type !== 'string' || !r.type) return null;
	const size = (SIZES as readonly unknown[]).includes(r.size) ? (r.size as WidgetSize) : 'm';
	const w: Widget = { id: r.id, type: r.type, size };
	if (typeof r.title === 'string' && r.title.trim()) w.title = r.title;
	if (r.config && typeof r.config === 'object' && !Array.isArray(r.config))
		w.config = r.config as Record<string, unknown>;
	return w;
}

export function readBoard(raw: unknown): Board | null {
	if (!raw || typeof raw !== 'object') return null;
	const r = raw as Record<string, unknown>;
	if (!isId(r.id) || typeof r.name !== 'string') return null;
	const widgets = Array.isArray(r.widgets) ? r.widgets.map(readWidget).filter((w): w is Widget => !!w) : [];
	return { id: r.id, name: r.name, widgets };
}

export function readShared(raw: unknown): SharedBoard | null {
	const board = readBoard(raw);
	if (!board) return null;
	const r = raw as Record<string, unknown>;
	return {
		...board,
		roles: Array.isArray(r.roles) ? r.roles.filter((x): x is string => typeof x === 'string') : [],
		locked: r.locked === true,
		home: r.home === true
	};
}

/** The body PUT /api/dashboards takes: only the keys the server knows */
export function ownBody(own: OwnBoards): OwnBoards {
	return {
		boards: own.boards.map((b) => ({ id: b.id, name: b.name.trim(), widgets: b.widgets.map(clean) })),
		active: own.active,
		hidden: [...new Set(own.hidden)]
	};
}

export function sharedBody(boards: SharedBoard[]): { boards: SharedBoard[] } {
	return {
		boards: boards.map((b) => ({
			id: b.id,
			name: b.name.trim(),
			widgets: b.widgets.map(clean),
			roles: [...new Set(b.roles)],
			locked: b.locked,
			home: b.home
		}))
	};
}

/* ── Which tabs an account sees ── */

export type TabKey = string;
export const ownKey = (id: string): TabKey => id;
export const sharedKey = (id: string): TabKey => `shared:${id}`;

export interface Tab {
	key: TabKey;
	board: Board;
	shared: SharedBoard | null;
}

/**
 * The tabs in order: the shared home board (if any) first, then the
 * account's own boards (or the built-in one when it has none), then the
 * other shared boards it didn't hide. Locked shared boards can't be hidden.
 */
export function tabsFor(own: OwnBoards | null, shared: SharedBoard[], builtIn: Board): Tab[] {
	const hidden = new Set(own?.hidden ?? []);
	const visibleShared = shared.filter((s) => s.locked || !hidden.has(s.id));
	const homes = visibleShared.filter((s) => s.home);
	const others = visibleShared.filter((s) => !s.home);
	const ownBoards = own?.boards.length ? own.boards : homes.length ? [] : [builtIn];
	return [
		...homes.map((s) => ({ key: sharedKey(s.id), board: s, shared: s })),
		...ownBoards.map((b) => ({ key: ownKey(b.id), board: b, shared: null })),
		...others.map((s) => ({ key: sharedKey(s.id), board: s, shared: s }))
	];
}

/** The tab that opens: the one asked for (URL), else the account's choice, else the first */
export function openTab(tabs: Tab[], asked: string | null, active: string): Tab | undefined {
	return (
		(asked ? tabs.find((t) => t.key === asked) : undefined) ??
		(active ? tabs.find((t) => t.key === active) : undefined) ??
		tabs[0]
	);
}
