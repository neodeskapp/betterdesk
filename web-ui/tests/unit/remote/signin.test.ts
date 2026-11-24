/**
 * Saved sign-in in the viewer (BUGS L469): when it is offered, when it
 * signs in by itself, and what it types. No password is typed into a
 * desktop someone is signed in on without the person asking, and never
 * outside a Windows computer whose NeoDesk runs as its service.
 */
import { describe, expect, test, vi } from 'vitest';
import {
	signinOffer,
	signinSteps,
	typeSignin,
	SAS_WAIT_MS,
	type PeerFacts
} from '../../../src/lib/remote/signin.ts';
import type { SigninStatus } from '../../../src/lib/api/savedSignin.ts';

const saved = (extra: Partial<SigninStatus> = {}): SigninStatus => ({
	available: true,
	set: true,
	username: 'LAB\\admin',
	form: 'password',
	auto: false,
	updated_by: 'boss',
	updated_at: '2026-11-24T10:00:00Z',
	may_use: true,
	...extra
});
const atSignInScreen: PeerFacts = { platform: 'Windows', username: '', sasEnabled: true };
const someoneSignedIn: PeerFacts = { platform: 'Windows', username: 'alice', sasEnabled: true };
const fresh = { tried: false, autoDone: false };

describe('when saved sign-in is offered', () => {
	test('nothing without details, without the permission, or before the device said what it is', () => {
		expect(signinOffer(atSignInScreen, null, fresh).kind).toBe('none');
		expect(signinOffer(atSignInScreen, saved({ set: false }), fresh).kind).toBe('none');
		expect(signinOffer(atSignInScreen, saved({ may_use: false }), fresh).kind).toBe('none');
		expect(signinOffer(null, saved(), fresh).kind).toBe('none');
	});

	test('the switch is off by default: a button, never by itself', () => {
		expect(signinOffer(atSignInScreen, saved(), fresh)).toEqual({
			kind: 'button',
			signedIn: false,
			noLockSignal: false
		});
	});

	test('with the switch on it signs in by itself only when nobody is signed in', () => {
		expect(signinOffer(atSignInScreen, saved({ auto: true }), fresh)).toEqual({ kind: 'auto' });
		expect(signinOffer({ ...atSignInScreen, username: 'SYSTEM' }, saved({ auto: true }), fresh)).toEqual({
			kind: 'auto'
		});
		// Someone is signed in: locked or not, the device does not say; the button and why
		expect(signinOffer(someoneSignedIn, saved({ auto: true }), fresh)).toEqual({
			kind: 'button',
			signedIn: true,
			noLockSignal: true
		});
	});

	test('once per session, and by itself once per device in this page', () => {
		expect(signinOffer(atSignInScreen, saved({ auto: true }), { tried: true, autoDone: false }).kind).toBe(
			'none'
		);
		expect(signinOffer(atSignInScreen, saved({ auto: true }), { tried: false, autoDone: true })).toEqual({
			kind: 'button',
			signedIn: false,
			noLockSignal: false
		});
	});

	test('only on Windows with NeoDesk running as its service', () => {
		expect(signinOffer({ ...atSignInScreen, platform: 'Linux' }, saved({ auto: true }), fresh)).toEqual({
			kind: 'unsupported',
			why: 'platform'
		});
		expect(signinOffer({ ...atSignInScreen, platform: 'Mac OS' }, saved(), fresh)).toEqual({
			kind: 'unsupported',
			why: 'platform'
		});
		expect(signinOffer({ ...atSignInScreen, sasEnabled: false }, saved({ auto: true }), fresh)).toEqual({
			kind: 'unsupported',
			why: 'service'
		});
	});
});

describe('what is typed', () => {
	test('Ctrl+Alt+Del first, then the password and Enter', () => {
		expect(signinSteps('password')).toEqual([
			{ kind: 'sas' },
			{ kind: 'wait', ms: SAS_WAIT_MS },
			{ kind: 'text', field: 'password' },
			{ kind: 'key', key: 'Return' }
		]);
	});

	test('a screen that asks for both gets the name, Tab, the password and Enter', () => {
		expect(signinSteps('name_password')).toEqual([
			{ kind: 'sas' },
			{ kind: 'wait', ms: SAS_WAIT_MS },
			{ kind: 'text', field: 'username' },
			{ kind: 'key', key: 'Tab' },
			{ kind: 'wait', ms: 200 },
			{ kind: 'text', field: 'password' },
			{ kind: 'key', key: 'Return' }
		]);
	});

	test('the keys go out in order, the text as typed characters', async () => {
		const sent: string[] = [];
		const keys = {
			sendCtrlAltDel: () => void sent.push('sas'),
			sendText: (t: string) => (sent.push('text:' + t), true),
			sendKeyPress: (k: 'Tab' | 'Return') => (sent.push('key:' + k), true)
		};
		const sleep = vi.fn(async () => {});
		const ok = await typeSignin(
			keys,
			signinSteps('name_password'),
			{ username: 'LAB\\admin', password: 'p@ss€ä' },
			{
				sleep,
				alive: () => true
			}
		);
		expect(ok).toBe(true);
		expect(sent).toEqual(['sas', 'text:LAB\\admin', 'key:Tab', 'text:p@ss€ä', 'key:Return']);
		expect(sleep).toHaveBeenCalledWith(SAS_WAIT_MS);
	});

	test('a session that ends on the way stops the typing', async () => {
		const sent: string[] = [];
		let alive = true;
		const keys = {
			sendCtrlAltDel: () => void sent.push('sas'),
			sendText: (t: string) => (sent.push('text:' + t), true),
			sendKeyPress: (k: 'Tab' | 'Return') => (sent.push('key:' + k), true)
		};
		const ok = await typeSignin(
			keys,
			signinSteps('password'),
			{ username: 'x', password: 'secret-pass' },
			{
				sleep: async () => {
					alive = false;
				},
				alive: () => alive
			}
		);
		expect(ok).toBe(false);
		expect(sent).toEqual(['sas']);
	});

	test('a transport that refuses the text stops before Enter', async () => {
		const sent: string[] = [];
		const keys = {
			sendCtrlAltDel: () => void sent.push('sas'),
			sendText: () => false,
			sendKeyPress: (k: 'Tab' | 'Return') => (sent.push('key:' + k), true)
		};
		const ok = await typeSignin(
			keys,
			signinSteps('password'),
			{ username: 'x', password: 'y' },
			{
				sleep: async () => {},
				alive: () => true
			}
		);
		expect(ok).toBe(false);
		expect(sent).toEqual(['sas']);
	});
});
