/**
 * Settings › General and Joining without a DOM: the parts their one save
 * sends (each to its own endpoint), the checks of the addresses per field,
 * the connection rules that do nothing in the current state, the
 * connection strategy as the server reports it, and the addresses the
 * diagram draws (old settings.js loadConnectionMode, splitHostPort).
 */

import type { ConnectionMode, ConnectionModeSettings } from '#lib/api/settings.ts';
import { tf } from '#lib/i18n/i18n.svelte.ts';
import { PartlySaved, type Change } from '#lib/ui/settings/draft.svelte.ts';
import { wholeInRange } from './numbers.ts';

export type GeneralPart = 'endpoints' | 'connection' | 'scope' | 'availability';

/** The order the parts are saved in, as on the page */
export const GENERAL_PARTS: readonly GeneralPart[] = ['endpoints', 'connection', 'scope', 'availability'];

/** The draft fields of each part */
export const partFields: Readonly<Record<GeneralPart, readonly string[]>> = {
	endpoints: ['public_server_id', 'public_relay_server', 'public_api_url'],
	connection: [
		'mode',
		'p2p_fallback_ms',
		'same_nat_relay',
		'allow_shared_nat_initiator',
		'logged_in_only_initiator',
		'operator_only_outbound'
	],
	scope: ['scope'],
	availability: ['availability_enabled', 'availability_days']
};

/** The parts of a tab that have a change, in the order given */
export function partsWithChanges<P extends string>(
	changes: readonly Pick<Change, 'field'>[],
	table: Readonly<Record<P, readonly string[]>>,
	order: readonly P[]
): P[] {
	const fields = new Set(changes.map((c) => c.field));
	return order.filter((p) => table[p].some((f) => fields.has(f)));
}

/** The parts Settings › General or Joining send: those with a change, in the order of the page */
export function changedParts(changes: readonly Pick<Change, 'field'>[]): GeneralPart[] {
	return partsWithChanges(changes, partFields, GENERAL_PARTS);
}

/**
 * Save the parts one after the other with `run`. When one fails the rest
 * still run, and PartlySaved names the fields that were stored (the draft
 * takes them as saved) and says which parts were saved and which not, and
 * why.
 */
export async function saveInParts<P extends string>(
	parts: readonly P[],
	table: Readonly<Record<P, readonly string[]>>,
	title: (part: P) => string,
	run: (part: P) => Promise<unknown>
): Promise<void> {
	const saved: string[] = [];
	const savedTitles: string[] = [];
	const failed: string[] = [];
	for (const part of parts) {
		try {
			await run(part);
			saved.push(...table[part]);
			savedTitles.push(title(part));
		} catch (err) {
			const error =
				err instanceof Error && err.message ? err.message : tf('errors.server_error', 'Server error');
			failed.push(
				tf('settings.general.part_failed', '{part}: not saved. {error}', { part: title(part), error })
			);
		}
	}
	if (!failed.length) return;
	const line = savedTitles.length
		? tf('settings.general.parts_saved', 'Saved: {parts}.', { parts: savedTitles.join(', ') })
		: '';
	throw new PartlySaved([line, ...failed].filter(Boolean).join(' '), saved);
}

/** The fields of Settings › General: the public addresses and the connection strategy */
export interface GeneralValues {
	public_server_id: string;
	public_relay_server: string;
	public_api_url: string;
	mode: 'p2p_first' | 'relay_only';
	p2p_fallback_ms: number | null;
	same_nat_relay: boolean;
	allow_shared_nat_initiator: boolean;
	logged_in_only_initiator: boolean;
	operator_only_outbound: boolean;
	[key: string]: unknown;
}

export const GENERAL_DEFAULTS: GeneralValues = {
	public_server_id: '',
	public_relay_server: '',
	public_api_url: '',
	mode: 'p2p_first',
	p2p_fallback_ms: 2000,
	same_nat_relay: true,
	allow_shared_nat_initiator: false,
	logged_in_only_initiator: false,
	operator_only_outbound: false
};

const LABEL = /^[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?$/;
const DOTTED = /^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$/;

function validIPv6(text: string): boolean {
	try {
		return new URL(`http://[${text}]/`).hostname.length > 2;
	} catch {
		return false;
	}
}

/**
 * What is wrong with an ID or relay server address ('' when nothing): a
 * host name or an IP address alone, as the server stores it (serverinfo
 * NormalizeServerHost and PublicHostProblem). Empty is fine: the address
 * found automatically applies.
 */
export function hostProblem(raw: string): string {
	const v = raw.trim();
	if (!v) return '';
	const extra = tf(
		'settings.general.host_extra',
		'Only the host name or IP address, without http://, a port or a path.'
	);
	if (/^[a-z][a-z0-9+.-]*:\/\//i.test(v) || v.includes('/')) return extra;
	const bare = v.replace(/^\[(.*)\]$/, '$1');
	if (bare.includes(':')) return validIPv6(bare) ? '' : v.startsWith('[') ? invalidHost() : extra;
	if (DOTTED.test(v)) return v.split('.').every((n) => Number(n) <= 255) ? '' : invalidHost();
	const name = v.replace(/\.$/, '');
	return name.length <= 253 && name.split('.').every((l) => LABEL.test(l)) ? '' : invalidHost();
}

function invalidHost(): string {
	return tf(
		'settings.general.host_invalid',
		'Not a host name or IP address. Use letters, digits, dots and hyphens, like remote.example.com.'
	);
}

/** What is wrong with the public API address ('' when nothing): a full http or https address */
export function apiUrlProblem(raw: string): string {
	const v = raw.trim();
	if (!v) return '';
	const wrong = tf(
		'settings.general.api_url_invalid',
		'Use a full address that starts with https:// or http://, like https://api.example.com.'
	);
	if (!/^https?:\/\//i.test(v) || /\s/.test(v)) return wrong;
	try {
		const u = new URL(v);
		return u.hostname ? '' : wrong;
	} catch {
		return wrong;
	}
}

/** The problems of Settings › General by field; the wait counts only while direct connections are tried */
export function generalProblems(v: GeneralValues): Partial<Record<keyof GeneralValues & string, string>> {
	const out: Partial<Record<string, string>> = {};
	for (const f of ['public_server_id', 'public_relay_server'] as const) {
		const p = hostProblem(v[f]);
		if (p) out[f] = p;
	}
	const api = apiUrlProblem(v.public_api_url);
	if (api) out.public_api_url = api;
	if (v.mode !== 'relay_only' && wholeInRange(v.p2p_fallback_ms, 0, 30000) === null)
		out.p2p_fallback_ms = tf(
			'settings.general.conn_fallback_error',
			'Enter a whole number of milliseconds from 0 to 30000.'
		);
	return out;
}

/**
 * Why a connection rule does nothing in the current state (PATTERN.md rule
 * 6), by field. The signal server lets only signed-in apps start sessions
 * when either sign-in rule is on, which skips the shared-address fallback;
 * "Only accounts that may connect" includes the signed-in check.
 */
export function connectionNotes(v: GeneralValues): Partial<Record<string, string>> {
	const out: Partial<Record<string, string>> = {};
	if (v.mode === 'relay_only') {
		const relay = tf(
			'settings.general.conn_no_effect_relay',
			'Has no effect: every session goes through the relay server.'
		);
		out.p2p_fallback_ms = relay;
		out.same_nat_relay = relay;
	}
	if (v.operator_only_outbound)
		out.logged_in_only_initiator = tf(
			'settings.general.conn_no_effect_operator',
			'Already in force: Only accounts that may connect to devices can start sessions includes this rule.'
		);
	if (v.operator_only_outbound || v.logged_in_only_initiator)
		out.allow_shared_nat_initiator = tf(
			'settings.general.conn_no_effect_signed_in',
			'Has no effect while only signed-in apps can start sessions.'
		);
	return out;
}

export interface ConnectionFacts {
	settings: ConnectionModeSettings;
	/** The console can store a change (it found the server's service settings) */
	writable: boolean;
	/** What the running server uses, when it answered */
	runtime: { relayOnly: boolean; ms: string } | null;
}

export function connectionFacts(d: ConnectionMode): ConnectionFacts {
	const fallback = Number(d.p2p_fallback_ms);
	const source = d.source || d.deployment || 'defaults';
	return {
		settings: {
			mode: d.mode === 'relay_only' ? 'relay_only' : 'p2p_first',
			p2p_fallback_ms:
				d.p2p_fallback_ms === null || d.p2p_fallback_ms === undefined || !Number.isFinite(fallback)
					? 2000
					: fallback,
			same_nat_relay: d.same_nat_relay !== false,
			allow_shared_nat_initiator: d.allow_shared_nat_initiator === true,
			logged_in_only_initiator: d.logged_in_only_initiator === true,
			operator_only_outbound: d.operator_only_outbound === true
		},
		writable: d.writable !== false && source !== 'defaults',
		runtime: d.runtime
			? {
					relayOnly: d.runtime.always_use_relay === true,
					ms:
						d.runtime.p2p_fallback_ms === null || d.runtime.p2p_fallback_ms === undefined
							? '-'
							: String(d.runtime.p2p_fallback_ms)
				}
			: null
	};
}

/** A mesh group ID as the hint asks for it: a short name without spaces (BUGS L384) */
export function meshGroupIdValid(id: string): boolean {
	return /^[A-Za-z0-9._-]{1,64}$/.test(id);
}

/** "host:port", "[v6]:port" or a URL to { host, port } */
export function splitHostPort(value: string, fallbackPort: number): { host: string; port: number } {
	const v = String(value || '')
		.trim()
		.replace(/^[a-z][a-z0-9+.-]*:\/\//i, '')
		.replace(/\/.*$/, '');
	if (!v) return { host: '', port: fallbackPort };
	const v6 = /^\[([^\]]+)\](?::(\d+))?$/.exec(v);
	if (v6) return { host: v6[1] ?? '', port: v6[2] ? Number(v6[2]) : fallbackPort };
	const parts = v.split(':');
	if (parts.length === 2 && /^\d+$/.test(parts[1] ?? ''))
		return { host: parts[0] ?? '', port: Number(parts[1]) };
	return { host: v, port: fallbackPort };
}

/** "Saved: A, B." or '' when nothing was saved */
export function savedLine(saved: readonly string[], sentence: (parts: string) => string): string {
	return saved.length ? sentence(saved.join(', ')) : '';
}

/** The variable that sets each address of the side column */
export const ENDPOINT_VARIABLES = {
	host: 'PUBLIC_SERVER_ID',
	relay: 'PUBLIC_RELAY_SERVER',
	api: 'PUBLIC_API_URL'
} as const;

/**
 * Where an address of the side column comes from, as its label says:
 * saved on this page, a variable of the server's environment (named), or
 * found automatically. PANEL_PUBLIC_HOST and RELAY_SERVERS (the address
 * the Docker installer found) come from the environment too.
 */
export function endpointSourceLabel(
	source: string | undefined,
	variable: string,
	t: (key: string, params?: Record<string, string>) => string
): string {
	if (source === 'saved') return t('settings.side.src_page');
	if (source === 'env') return t('settings.side.src_env', { name: variable });
	if (source === 'panel_host') return t('settings.side.src_env', { name: 'PANEL_PUBLIC_HOST' });
	if (source === 'relay_servers') return t('settings.side.src_env', { name: 'RELAY_SERVERS' });
	return t('settings.side.src_auto');
}
