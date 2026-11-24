/**
 * Saved sign-in in the viewer (BUGS L469): when it is offered, when it
 * signs in by itself, and what it types. No password is typed into a
 * desktop someone is signed in on without the person asking, and never
 * outside a Windows computer whose NeoDesk runs as its service.
 */
import { describe, expect, test, vi } from 'vitest';
import {
	logonStateFromMessage,
	signinAdditions,
	signinOffer,
	signinResultOf,
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
	sealed_signin: false,
	...extra
});
const atSignInScreen: PeerFacts = {
	platform: 'Windows',
	username: '',
	sasEnabled: true,
	logonState: '',
	sealed: false
};
const someoneSignedIn: PeerFacts = { ...atSignInScreen, username: 'alice' };
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
			noLockSignal: false,
			confirm: false,
			path: 'typed'
		});
	});

	test('with the switch on it signs in by itself only when nobody is signed in', () => {
		expect(signinOffer(atSignInScreen, saved({ auto: true }), fresh)).toEqual({
			kind: 'auto',
			path: 'typed'
		});
		expect(signinOffer({ ...atSignInScreen, username: 'SYSTEM' }, saved({ auto: true }), fresh)).toEqual({
			kind: 'auto',
			path: 'typed'
		});
		// Someone is signed in: locked or not, the device does not say; the button and why
		expect(signinOffer(someoneSignedIn, saved({ auto: true }), fresh)).toEqual({
			kind: 'button',
			signedIn: true,
			noLockSignal: true,
			confirm: true,
			path: 'typed'
		});
	});

	test('once per session, and by itself once per device in this page', () => {
		expect(signinOffer(atSignInScreen, saved({ auto: true }), { tried: true, autoDone: false }).kind).toBe(
			'none'
		);
		expect(signinOffer(atSignInScreen, saved({ auto: true }), { tried: false, autoDone: true })).toEqual({
			kind: 'button',
			signedIn: false,
			noLockSignal: false,
			confirm: false,
			path: 'typed'
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

describe('with the lock screen state of a NeoDesk client', () => {
	const at = (logonState: PeerFacts['logonState'], extra: Partial<PeerFacts> = {}): PeerFacts => ({
		...someoneSignedIn,
		logonState,
		...extra
	});

	test('platform_additions name the state and the sealed sign-in; anything else is no signal', () => {
		expect(signinAdditions('{"logon_state":"locked","nd_signin":1,"is_installed":true}')).toEqual({
			logonState: 'locked',
			sealed: true
		});
		expect(signinAdditions('{"logon_state":"asleep","nd_signin":0}')).toEqual({
			logonState: '',
			sealed: false
		});
		expect(signinAdditions('not json')).toEqual({ logonState: '', sealed: false });
		expect(signinAdditions(undefined)).toEqual({ logonState: '', sealed: false });
		const bytes = (text: string) => new TextEncoder().encode(text);
		expect(logonStateFromMessage(bytes('{"logon_state":"desktop"}'))).toBe('desktop');
		expect(logonStateFromMessage(bytes('nonsense'))).toBe('');
		expect(signinResultOf(bytes('{"result":"typed","nonce":"n1"}'))).toEqual({
			result: 'typed',
			nonce: 'n1'
		});
		expect(signinResultOf(bytes('{"result":"done"}'))).toBeNull();
	});

	test('by itself only on the lock or sign-in screen with the switch on, also when someone is signed in', () => {
		for (const state of ['locked', 'prelogin'] as const) {
			expect(signinOffer(at(state), saved({ auto: true }), fresh)).toEqual({ kind: 'auto', path: 'typed' });
		}
	});

	test('never on an open desktop, nor on a sign-in screen of an unlocked session (Ctrl+Alt+Del options)', () => {
		for (const state of ['desktop', 'logon_ui'] as const) {
			for (const peer of [at(state), at(state, { sealed: true })]) {
				expect(signinOffer(peer, saved({ auto: true }), fresh).kind).toBe('none');
				expect(signinOffer(peer, saved(), fresh).kind).toBe('none');
				expect(signinOffer(peer, saved({ auto: true }), { ...fresh, atStart: false }).kind).toBe('none');
			}
		}
	});

	test('the button asks nothing on a reported lock screen; a state that changes later gets no sign-in by itself', () => {
		expect(signinOffer(at('locked'), saved(), fresh)).toMatchObject({ kind: 'button', confirm: false });
		expect(signinOffer(at('locked'), saved({ auto: true }), { ...fresh, atStart: false })).toMatchObject({
			kind: 'button',
			confirm: false
		});
	});

	test('a client that opens sealed packets takes the sealed way, also without its service flag', () => {
		expect(signinOffer(at('locked', { sealed: true }), saved({ auto: true }), fresh)).toEqual({
			kind: 'auto',
			path: 'sealed'
		});
		expect(signinOffer(at('locked', { sealed: true, sasEnabled: false }), saved(), fresh)).toMatchObject({
			kind: 'button',
			path: 'sealed'
		});
	});

	test('a computer on the sealed way gets no typing from a client that lacks it: it says why instead', () => {
		const onSealedWay = saved({ auto: true, sealed_signin: true });
		for (const peer of [atSignInScreen, someoneSignedIn, at('locked')]) {
			expect(signinOffer(peer, onSealedWay, fresh)).toEqual({ kind: 'unsupported', why: 'sealed' });
		}
		expect(signinOffer(at('locked', { sealed: true }), onSealedWay, fresh)).toEqual({
			kind: 'auto',
			path: 'sealed'
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
