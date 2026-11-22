/**
 * PunchHoleResponse success/failure interpretation for RdClient web.
 *
 * Proto3 `failure` defaults to 0 (ID_NOT_EXIST) when unset, so success cannot
 * be inferred from failure alone. Server #405 may return Failure=OFFLINE with
 * relay_server still set — explicit non-zero failure must win over hasRelay.
 */
import type { IPunchHoleResponse } from './generated/protos.ts';

/** Where the rendezvous server sends the client, or why it refused */
export type RDPunchHoleResult =
	| { error: string }
	| { relayServer: string; uuid: string; pk: Uint8Array | null; natType: number | undefined };

const FAILURE = {
	ID_NOT_EXIST: 0,
	OFFLINE: 2,
	LICENSE_MISMATCH: 3,
	LICENSE_OVERUSE: 4
};

const FAILURE_NAMES: Record<number, string> = {
	0: 'Device not found',
	2: 'Device offline',
	3: 'License mismatch',
	4: 'Too many connections'
};

/**
 * @param resp - decoded PunchHoleResponse
 */
function interpretPunchHoleResponse(resp: IPunchHoleResponse | null | undefined): RDPunchHoleResult {
	if (!resp || typeof resp !== 'object') {
		return { error: 'Empty PunchHoleResponse' };
	}

	const failure = Number(resp.failure);
	const failureCode = Number.isFinite(failure) ? failure : 0;
	const otherFailure = String(resp.otherFailure || '').trim();
	const relayServer = String(resp.relayServer || '');
	const socketAddr = resp.socketAddr || null;
	const hasRelay = relayServer.length > 0;
	const hasSocket = !!(socketAddr && socketAddr.length);

	// Explicit non-zero Failure (#405 OFFLINE / license) — even with relay_server.
	if (
		failureCode === FAILURE.OFFLINE ||
		failureCode === FAILURE.LICENSE_MISMATCH ||
		failureCode === FAILURE.LICENSE_OVERUSE ||
		otherFailure
	) {
		return {
			error: otherFailure || FAILURE_NAMES[failureCode] || `Unknown error (code: ${failureCode})`
		};
	}

	if (hasRelay || hasSocket) {
		const natType = resp.natType != null ? resp.natType : undefined;
		return {
			relayServer,
			uuid: '',
			pk: resp.pk || null,
			natType
		};
	}

	return {
		error: otherFailure || FAILURE_NAMES[FAILURE.ID_NOT_EXIST] || 'Device not found'
	};
}

export const RDPunchHoleResponse = {
	FAILURE,
	FAILURE_NAMES,
	interpretPunchHoleResponse
};
