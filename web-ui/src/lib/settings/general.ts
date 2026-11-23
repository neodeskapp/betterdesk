/**
 * Settings › General without a DOM: the parts of its one save, the
 * connection strategy as the server reports it, and the addresses the
 * diagram draws (old settings.js loadConnectionMode, splitHostPort).
 */

import type { ConnectionMode, ConnectionModeSettings } from '#lib/api/settings.ts';

export type GeneralPart = 'endpoints' | 'connection' | 'scope' | 'availability';

/** The order the parts are saved in, as on the page */
export const GENERAL_PARTS: readonly GeneralPart[] = ['endpoints', 'connection', 'scope', 'availability'];

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
 * found automatically. PANEL_PUBLIC_HOST comes from the environment too.
 */
export function endpointSourceLabel(
	source: string | undefined,
	variable: string,
	t: (key: string, params?: Record<string, string>) => string
): string {
	if (source === 'saved') return t('settings.side.src_page');
	if (source === 'env') return t('settings.side.src_env', { name: variable });
	if (source === 'panel_host') return t('settings.side.src_env', { name: 'PANEL_PUBLIC_HOST' });
	return t('settings.side.src_auto');
}
