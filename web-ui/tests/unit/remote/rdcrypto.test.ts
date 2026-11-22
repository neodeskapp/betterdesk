/**
 * The remote viewer's password hash for RustDesk devices
 * (src/lib/remote/client/crypto.ts). On plain HTTP the browser has no
 * crypto.subtle, and the client hashes with its own SHA-256.
 */
import { createHash } from 'node:crypto';
import { afterEach, describe, expect, test, vi } from 'vitest';
import { RDCrypto } from '../../../src/lib/remote/client/crypto.ts';

const hex = (b: Uint8Array) => Array.from(b, (x) => x.toString(16).padStart(2, '0')).join('');
const sha256 = (b: Uint8Array) => createHash('sha256').update(b).digest();

afterEach(() => vi.unstubAllGlobals());

describe('SHA-256 without crypto.subtle', () => {
	test('matches SHA-256 for every input length from 0 to 199 bytes', () => {
		const wrong: number[] = [];
		for (let len = 0; len < 200; len++) {
			const input = Uint8Array.from({ length: len }, (_, i) => (i * 7 + len) & 0xff);
			if (hex(RDCrypto._sha256Fallback(input)) !== hex(sha256(input))) wrong.push(len);
		}
		expect(wrong).toEqual([]);
	});

	test('the device password hash on plain HTTP is the one the device checks', async () => {
		vi.stubGlobal('crypto', {});
		// password + salt is 48 bytes, 32 + challenge is 52: both lengths the padding got wrong
		const password = 'correct horse battery staple';
		const salt = 'abcdefghijklmnopqrst';
		const challenge = 'ABCDEFGHIJKLMNOPQRST';
		const intermediate = sha256(new TextEncoder().encode(password + salt));
		const expected = sha256(Buffer.concat([intermediate, Buffer.from(challenge)]));
		const hash = await new RDCrypto().hashPassword(password, salt, challenge);
		expect(hex(hash)).toBe(hex(expected));
	});
});
