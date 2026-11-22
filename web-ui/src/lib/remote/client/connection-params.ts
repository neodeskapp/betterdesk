/**
 * Normalize Go /api/health `connection` snapshot for RdClient web timeouts.
 */

/** The server's connection settings, as the client reads them */
export interface RDConnectionSettings {
	p2p_first: boolean;
	always_use_relay: boolean;
	p2p_fallback_ms: number;
	same_nat_relay: boolean;
	allow_shared_nat_initiator: boolean;
	relay_servers: string;
}

/** How long the client waits for the rendezvous and relay answers */
export interface RDConnectTimeouts {
	rendezvousMs: number;
	signalRelayMs: number;
	connection: RDConnectionSettings;
}

const DEFAULTS: RDConnectionSettings = {
	p2p_first: true,
	always_use_relay: false,
	p2p_fallback_ms: 2000,
	same_nat_relay: true,
	allow_shared_nat_initiator: false,
	relay_servers: ''
};

function isRecord(value: unknown): value is Record<string, unknown> {
	return typeof value === 'object' && value !== null;
}

/**
 * @param connection - the `connection` object of /api/health (anything else reads as empty)
 */
function normalizeConnection(connection: unknown): RDConnectionSettings {
	const src: Record<string, unknown> = connection && isRecord(connection) ? connection : {};
	const fallbackRaw = Number(src.p2p_fallback_ms);
	return {
		p2p_first: src.p2p_first !== false && src.p2p_first !== 'false',
		always_use_relay: src.always_use_relay === true || src.always_use_relay === 'true',
		p2p_fallback_ms:
			Number.isFinite(fallbackRaw) && fallbackRaw >= 0 ? fallbackRaw : DEFAULTS.p2p_fallback_ms,
		same_nat_relay: src.same_nat_relay !== false && src.same_nat_relay !== 'false',
		allow_shared_nat_initiator:
			src.allow_shared_nat_initiator === true || src.allow_shared_nat_initiator === 'true',
		relay_servers: String(src.relay_servers || '')
	};
}

/**
 * Browser Web Remote always force-relays; still size waits from server fallback.
 */
function resolveConnectTimeouts(connection: unknown): RDConnectTimeouts {
	const normalized = normalizeConnection(connection);
	const waitMs = Math.max(15000, normalized.p2p_fallback_ms + 10000);
	return {
		rendezvousMs: waitMs,
		signalRelayMs: waitMs,
		connection: normalized
	};
}

export const RDConnectionParams = {
	DEFAULTS,
	normalizeConnection,
	resolveConnectTimeouts
};
