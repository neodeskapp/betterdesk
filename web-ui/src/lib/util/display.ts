/**
 * Pure helpers behind the data display components (GUIDE.md section 10).
 * Same rules as the old Utils.* helpers, so both consoles agree.
 */

export type Tone = 'good' | 'waiting' | 'neutral' | 'warning' | 'danger';

const TONES: Record<Tone, readonly string[]> = {
	good: [
		'online',
		'ok',
		'success',
		'active',
		'running',
		'enabled',
		'on',
		'approved',
		'accepted',
		'passed',
		'done',
		'completed',
		'connected',
		'resolved',
		'healthy',
		'valid',
		'allowed',
		'yes'
	],
	waiting: [
		'pending',
		'waiting',
		'queued',
		'checking',
		'scheduled',
		'in_progress',
		'starting',
		'sending',
		'open'
	],
	neutral: [
		'offline',
		'stopped',
		'inactive',
		'disabled',
		'off',
		'rejected',
		'revoked',
		'expired',
		'deleted',
		'closed',
		'cancelled',
		'canceled',
		'skipped',
		'paused',
		'draft',
		'unknown',
		'low',
		'no',
		'none'
	],
	warning: ['warning', 'degraded', 'no_signal', 'partial', 'expiring', 'outdated', 'medium'],
	danger: [
		'error',
		'failed',
		'failure',
		'banned',
		'blocked',
		'denied',
		'invalid',
		'unreachable',
		'down',
		'critical',
		'high'
	]
};

/** Tone for a status word ("approved" → good); unknown words are neutral */
export function statusTone(word: string | null | undefined): Tone {
	const w = String(word ?? '')
		.trim()
		.toLowerCase()
		.replace(/[\s-]+/g, '_');
	if (w in TONES) return w as Tone;
	for (const [tone, words] of Object.entries(TONES) as [Tone, readonly string[]][]) {
		if (words.includes(w)) return tone;
	}
	return 'neutral';
}

/**
 * Initials, letters and digits only: first letter of the first two words
 * ("Linus Rath" → "LR", "demo_ui_admin" → "DU"), or the first two of a
 * single word ("admin" → "AD"). '' when there are none.
 */
export function initials(name: string | null | undefined, locale?: string): string {
	const words = String(name ?? '')
		.split(/[\s._@-]+/)
		.map((w) => Array.from(w).filter((ch) => /[\p{L}\p{N}]/u.test(ch)))
		.filter((chars) => chars.length > 0);
	if (!words.length) return '';
	const first = words[0] as string[];
	const picked =
		words.length > 1
			? (first[0] as string) + ((words[1] as string[])[0] as string)
			: first.slice(0, 2).join('');
	let upper: string;
	try {
		upper = picked.toLocaleUpperCase(locale);
	} catch {
		upper = picked.toUpperCase();
	}
	return Array.from(upper).slice(0, 2).join('');
}

/** Avatar colour 1..6 for a name; the same name always gets the same colour */
export function avatarColor(name: string | null | undefined): number {
	let h = 0;
	for (const ch of String(name ?? '')
		.trim()
		.toLowerCase())
		h = (h * 31 + (ch.codePointAt(0) ?? 0)) >>> 0;
	return (h % 6) + 1;
}

export interface PersonLike {
	id?: number | string | null;
	username?: string | null;
	name?: string | null;
	display_name?: string | null;
	first_name?: string | null;
	last_name?: string | null;
	has_avatar?: boolean | null;
	avatar_version?: string | number | null;
}

/** display_name, else first + last name, else username */
export function displayName(user: PersonLike | string | null | undefined): string {
	if (!user || typeof user !== 'object') return String(user ?? '').trim();
	const full = [user.first_name, user.last_name]
		.map((x) => String(x ?? '').trim())
		.filter(Boolean)
		.join(' ');
	return String(user.display_name ?? '').trim() || full || String(user.username ?? user.name ?? '').trim();
}

/** Profile picture URL of a console user, or '' for initials */
export function avatarUrl(user: PersonLike | null | undefined): string {
	if (!user) return '';
	const version = user.has_avatar === false ? '' : user.avatar_version;
	const id = user.id;
	if (!version || id === undefined || id === null || !/^\d+$/.test(String(id))) return '';
	return `/api/users/${encodeURIComponent(String(id))}/avatar?v=${encodeURIComponent(String(version))}`;
}

export type OsId = 'windows' | 'apple' | 'android' | 'chrome' | 'linux' | '';

/** Mark in /img/os-icons.svg for a platform string */
export function platformId(platform: string | null | undefined): OsId {
	if (!platform) return '';
	const p = String(platform).toLowerCase();
	const rules: [OsId, RegExp][] = [
		['windows', /windows|win32|win64|^win\b/],
		['apple', /mac ?os|macintosh|darwin|\bmac\b|\bios\b|iphone|ipad|apple/],
		['android', /android/],
		['chrome', /chrome ?os|chromeos/],
		[
			'linux',
			/linux|ubuntu|debian|raspberry|raspbian|fedora|centos|red ?hat|rhel|\barch\b|manjaro|alpine|mint|suse|kali|bsd/
		]
	];
	return rules.find(([, re]) => re.test(p))?.[0] ?? '';
}

/**
 * The system and its main version from what the client reports:
 * "windows / Windows 11 Pro - 11 (26200)" → "Windows 11",
 * "windows / Windows Server 2025 Standard Evaluation - 11 (26100)" → "Windows Server 2025",
 * "ubuntu / Linux 24.04 Ubuntu" → "Ubuntu 24.04", "cachyos / Linux rolling CachyOS Linux" →
 * "CachyOS Linux", "macos / macOS 14.5 Sonoma" → "macOS 14". Text it can't read stays as it is.
 */
export function shortPlatform(platform: string | null | undefined): string {
	const full = String(platform ?? '').trim();
	// "<distribution id> / <long name> - <build>" (the desktop client): the long name
	const slash = full.indexOf(' / ');
	let s = slash >= 0 ? full.slice(slash + 3).trim() || full.slice(0, slash).trim() : full;
	s = s.replace(/\s+-\s+.*$/, '').trim();
	if (!s) return full;

	// Linux: "Linux <VERSION_ID> <NAME>"; the version id is already the release
	const linux = /^Linux\s+(\S+)\s+(.+)$/i.exec(s);
	if (linux) {
		const name = linux[2].replace(/\s+GNU\/Linux$/i, '').trim();
		return /^\d/.test(linux[1]) ? `${name} ${linux[1]}` : name;
	}

	const win = /^(Windows(?:\s+Server)?)\s+(\d+)\b/i.exec(s);
	if (win) return `${win[1]} ${win[2]}`;

	// Name, then the first version: major only (macOS 10.x keeps its minor)
	const m = /^(.*?[A-Za-z].*?)\s+v?(\d+)((?:\.\d+)*)\b/.exec(s);
	if (!m) return s;
	const minor = /^mac ?os/i.test(m[1]) && m[2] === '10' ? (m[3].match(/^\.\d+/)?.[0] ?? '') : '';
	if (platformId(m[1]) === 'linux') return `${m[1]} ${m[2]}${m[3]}`;
	return `${m[1]} ${m[2]}${minor}`;
}

/** "v1.4.2" → "1.4.2" */
export function cleanVersion(v: unknown): string {
	return String(v ?? '')
		.trim()
		.replace(/^v(?=\d)/i, '');
}

/**
 * One icon per meaning (Material Symbols names), so the same action looks
 * the same everywhere. Same table as Utils.ICONS.
 */
export const ICONS = {
	connect: 'desktop_windows',
	connect_app: 'open_in_new',
	guest_link: 'share',
	snapshot: 'photo_camera',
	details: 'info',
	edit: 'edit',
	rename: 'drive_file_rename_outline',
	tags: 'sell',
	groups: 'workspaces',
	folder: 'folder',
	access_policy: 'shield',
	ban: 'block',
	unban: 'lock_open',
	delete: 'delete',
	delete_forever: 'delete_forever',
	restore: 'restore_from_trash',
	copy: 'content_copy',
	download: 'download',
	upload: 'upload',
	export: 'download',
	refresh: 'refresh',
	add: 'add',
	approve: 'check',
	reject: 'close',
	accept: 'check',
	resolve: 'task_alt',
	open: 'arrow_forward',
	history: 'history',
	password: 'key',
	organization: 'business',
	user: 'person',
	terminal: 'terminal',
	files: 'folder_open',
	run: 'play_arrow',
	link: 'link',
	unlink: 'link_off',
	network_port: 'settings_ethernet',
	power_on: 'power_settings_new',
	sleep: 'bedtime',
	restart: 'restart_alt',
	power_off: 'power_off',
	enable: 'toggle_on',
	disable: 'toggle_off',
	qr: 'qr_code_2',
	billing: 'payments',
	settings: 'settings',
	search: 'search',
	send: 'send',
	print: 'print',
	check_now: 'network_ping',
	revoke: 'cancel',
	view: 'visibility',
	hide: 'visibility_off',
	retry: 'replay',
	reload: 'autorenew',
	duplicate: 'control_point_duplicate',
	connect_terminal: 'terminal',
	start: 'play_arrow',
	stop: 'stop',
	compliance_scan: 'fact_check',
	remediate: 'build',
	encode: 'code',
	decode: 'text_fields',
	generate: 'summarize',
	save: 'save',
	reset: 'settings_backup_restore',
	import: 'upload',
	help_request: 'support_agent',
	enrollment_request: 'how_to_reg'
} as const;

export type IconMeaning = keyof typeof ICONS;

/** Glyph name for a meaning, or the name itself (letters, digits, underscores only) */
export function iconName(meaningOrName: string): string {
	const name = (ICONS as Record<string, string>)[meaningOrName] ?? meaningOrName;
	return name.replace(/[^a-z0-9_]/g, '');
}
