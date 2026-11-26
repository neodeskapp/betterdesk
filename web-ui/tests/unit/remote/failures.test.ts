/**
 * The failure model (src/lib/remote/failures.ts): every reason has its
 * three texts in English and German, the language files say what the code
 * says, the reference table in docs/failure-reasons.md lists the same, and
 * no rendered failure carries a raw message (L556).
 */
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { afterEach, describe, expect, test } from 'vitest';
import { setStrings } from '../../../src/lib/i18n/i18n.svelte.ts';
import {
	REASONS,
	ReasonError,
	describeFailure,
	deviceRefusalReason,
	isRawText,
	punchHoleReason,
	reasonOf,
	serverRefusalReason
} from '../../../src/lib/remote/failures.ts';
import { FAILURE_TEXTS } from '../../../src/lib/remote/failure-texts.ts';

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../../..');
const LANG = path.join(ROOT, 'neodesk-server/console/static/files/lang');
const lang = (code: string) => JSON.parse(fs.readFileSync(path.join(LANG, code + '.json'), 'utf8'));
const EN = lang('en');
const DE = lang('de');

afterEach(() => setStrings('en', 'ltr', {}));

const parts = (reason: string) => Object.keys(FAILURE_TEXTS[reason as keyof typeof FAILURE_TEXTS]);

describe('every reason', () => {
	test.each(REASONS)('%s has what, why and what to do in English and German', (reason) => {
		for (const part of ['what', 'why', 'todo']) expect(parts(reason)).toContain(part);
		for (const part of parts(reason)) {
			const text = (FAILURE_TEXTS as Record<string, Record<string, string>>)[reason]![part]!;
			expect(EN.failure[reason][part]).toBe(text);
			const de = DE.failure[reason][part] as string;
			expect(de).toBeTruthy();
			expect(de).not.toBe(text);
			// German says du (A259)
			expect(de).not.toMatch(/\b(Sie|Ihr|Ihre|Ihren|Ihrem|Ihrer|Ihnen)\b/);
			for (const s of [text, de]) {
				// No em dashes, and no jargon outside Details
				expect(s).not.toMatch(/—|--/);
				expect(s).not.toMatch(/\brelay\b|punch|\bPK\b|rendezvous|hbbs/i);
			}
		}
	});

	test.each(REASONS)('%s renders without a raw text, in English and in German', (reason) => {
		for (const strings of [EN, DE]) {
			setStrings(strings === EN ? 'en' : 'de', 'ltr', strings);
			for (const canUnblock of [true, false]) {
				const f = describeFailure(reason, { name: 'desktop-3mu2dcg', person: 'Ana', canUnblock });
				for (const s of [f.what, f.why, f.todo]) {
					expect(s.length).toBeGreaterThan(5);
					expect(isRawText(s), s).toBe(false);
				}
				expect(f.actions).toContain('copy_details');
			}
		}
	});

	test('docs/failure-reasons.md lists every reason with its English and German texts', () => {
		const doc = fs.readFileSync(path.join(ROOT, 'docs/failure-reasons.md'), 'utf8');
		for (const reason of REASONS) {
			expect(doc).toContain('`' + reason + '`');
			for (const part of parts(reason)) {
				expect(doc).toContain(EN.failure[reason][part]);
				expect(doc).toContain(DE.failure[reason][part]);
			}
		}
	});
});

describe('reasons come from where the failure starts', () => {
	test('the PunchHoleResponse failure codes', () => {
		expect(punchHoleReason(0)).toBe('device_unknown');
		expect(punchHoleReason(2)).toBe('device_offline');
		expect(punchHoleReason(3)).toBe('license_blocked');
		expect(punchHoleReason(4)).toBe('session_limit');
	});
	test('the server refusal values, and one it has no code for', () => {
		expect(serverRefusalReason('Target offline')).toBe('device_offline');
		expect(serverRefusalReason('Billing blocked')).toBe('billing_blocked');
		expect(serverRefusalReason('File transfer is turned off for your account on this server')).toBe(
			'feature_off'
		);
		expect(serverRefusalReason('The person did not allow this in their quick support session')).toBe(
			'consent_scope'
		);
		expect(serverRefusalReason('Something new')).toBe('relay_refused');
	});
	test('the device login refusals', () => {
		expect(deviceRefusalReason('Wrong Password')).toBe('password_wrong');
		expect(deviceRefusalReason('Please try 1 minute later')).toBe('rate_limited');
		expect(deviceRefusalReason('Connection not allowed')).toBe('device_not_allowed');
		expect(deviceRefusalReason('Anything else')).toBeNull();
	});
	test('a login refusal that names the IPv6 prefix it counts', () => {
		expect(deviceRefusalReason('Too many wrong attempts for IPv6 prefix /64')).toBe('rate_limited');
		expect(deviceRefusalReason('Too many wrong attempts for IPv6 prefix /48')).toBe('rate_limited');
		expect(deviceRefusalReason('Too many wrong attempts for something else')).toBeNull();
	});
	test('an error carries its reason', () => {
		expect(reasonOf(new ReasonError('device_revoked', 'Connection refused: Device offline'))).toBe(
			'device_revoked'
		);
		expect(reasonOf(new Error('Connection refused: Device offline'))).toBeNull();
	});
});

describe('what an admin can do from the failure', () => {
	test('a revoked computer offers Unblock to an admin who may, and asks the others to ask', () => {
		const admin = describeFailure('device_revoked', { name: 'desktop-3mu2dcg', canUnblock: true });
		expect(admin.actions).toContain('unblock');
		const other = describeFailure('device_revoked', { name: 'desktop-3mu2dcg', canUnblock: false });
		expect(other.actions).not.toContain('unblock');
		expect(other.todo).toMatch(/Ask an administrator/);
	});
	test('offline is the device that has not checked in, with the device panel', () => {
		const f = describeFailure('device_offline', { name: 'pc' });
		expect(f.what).toBe('pc is offline.');
		expect(f.actions).toEqual(['reconnect', 'open_device', 'copy_details']);
	});
});
