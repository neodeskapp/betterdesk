/**
 * The viewer's sign-in with the device password an organisation stored
 * (BUGS L454): the server answers the device's password question, so the
 * page never holds that password.
 */

import { request } from './client.ts';

function fromBase64(text: string): Uint8Array | null {
	try {
		const raw = atob(text);
		const out = new Uint8Array(raw.length);
		for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i);
		return out;
	} catch {
		return null;
	}
}

/**
 * The answer to the device's password question from the device password
 * an organisation stored, or null when there is none this account may use
 * (or the call failed).
 */
export async function answerConnectPassword(
	id: string,
	salt: string,
	challenge: string
): Promise<Uint8Array | null> {
	try {
		const data = await request(`/api/devices/${encodeURIComponent(id)}/connect-password/answer`, {
			method: 'POST',
			body: { salt, challenge },
			allowAnonymous: true
		});
		const o = data && typeof data === 'object' ? (data as Record<string, unknown>) : {};
		if (o.password_set !== true || typeof o.answer !== 'string') return null;
		const answer = fromBase64(o.answer);
		return answer && answer.length === 32 ? answer : null;
	} catch {
		return null;
	}
}
