/**
 * Saved sign-in in the viewer (BUGS L469): whether to offer it, when it
 * signs in by itself, and what it types at a Windows lock or sign-in
 * screen. No DOM and no state here; viewer.svelte.ts calls it.
 *
 * What the device tells: at the session's sign-in a RustDesk device sends
 * PeerInfo with its platform, the user of its console session (empty, or
 * SYSTEM, while nobody is signed in) and sas_enabled (NeoDesk runs as the
 * Windows service and can send Ctrl+Alt+Del and type at the secure
 * desktop). A NeoDesk client that knows sealed sign-in also names, in
 * platform_additions, its lock screen state ("logon_state") and that it
 * opens sealed packets ("nd_signin"), and sends the state again when it
 * changes (wire format: neodesk-server/crypto/devicevault/sealed.go).
 *
 * With the state, saved sign-in acts only on the lock or sign-in screen
 * (locked, prelogin): by itself with the switch on at the session's start,
 * else a Sign in button that needs no confirmation. On an open desktop and
 * on the sign-in screens of an unlocked session (logon_ui, such as the
 * Ctrl+Alt+Del options) nothing is offered. Without the state, the viewer
 * signs in by itself only when nobody is signed in, and asks first when
 * someone is.
 *
 * Two ways: a client that opens sealed packets gets the server's packet
 * and types at its own lock screen (the password never reaches this
 * page); for any other the viewer types: Ctrl+Alt+Del, a wait for the
 * sign-in screen, then the text as characters (KeyEvent.seq: the device
 * types each character as Unicode, whatever its keyboard layout) and Tab
 * and Enter as keys. A computer the server knows on the sealed way
 * (sealed_signin) gets no typing from a client that lacks it.
 */

import type { SigninForm, SigninStatus } from '#lib/api/savedSignin.ts';

/** How long the sign-in screen takes to come up after Ctrl+Alt+Del */
export const SAS_WAIT_MS = 1500;

/** The lock screen state a NeoDesk client reports; '' when it reports none */
export type LogonState = '' | 'prelogin' | 'locked' | 'logon_ui' | 'desktop';

const LOGON_STATES: readonly LogonState[] = ['prelogin', 'locked', 'logon_ui', 'desktop'];

/** What the device said about itself (PeerInfo, and NeoDesk's state messages) */
export interface PeerFacts {
	platform: string;
	/** The user of the device's console session; '' or SYSTEM while nobody is signed in */
	username: string;
	/** NeoDesk runs as its service: Ctrl+Alt+Del works and input reaches the secure desktop */
	sasEnabled: boolean;
	/** The lock screen state; '' when the client reports none */
	logonState: LogonState;
	/** The client opens sealed sign-in packets (format 1) */
	sealed: boolean;
}

/** A lock screen state from a client's text; '' for anything else */
export function logonStateOf(value: unknown): LogonState {
	return LOGON_STATES.includes(value as LogonState) ? (value as LogonState) : '';
}

/** What PeerInfo.platform_additions (a JSON object as text) says about sign-in */
export function signinAdditions(json: string | undefined | null): Pick<PeerFacts, 'logonState' | 'sealed'> {
	try {
		const o = JSON.parse(json || '{}') as Record<string, unknown>;
		return { logonState: logonStateOf(o.logon_state), sealed: Number(o.nd_signin) >= 1 };
	} catch {
		return { logonState: '', sealed: false };
	}
}

/** The content of an "nd.logon_state" message, '' when it names none */
export function logonStateFromMessage(content: Uint8Array): LogonState {
	try {
		const o = JSON.parse(new TextDecoder().decode(content)) as Record<string, unknown>;
		return logonStateOf(o.logon_state);
	} catch {
		return '';
	}
}

export type SigninResult = 'typed' | 'not_at_lock_screen' | 'expired' | 'bad_blob';

/** The content of an "nd.signin_result" message; null when it is not one */
export function signinResultOf(content: Uint8Array): { result: SigninResult; nonce: string } | null {
	try {
		const o = JSON.parse(new TextDecoder().decode(content)) as Record<string, unknown>;
		const result = o.result;
		if (
			result !== 'typed' &&
			result !== 'not_at_lock_screen' &&
			result !== 'expired' &&
			result !== 'bad_blob'
		)
			return null;
		return { result, nonce: typeof o.nonce === 'string' ? o.nonce : '' };
	} catch {
		return null;
	}
}

/** sealed: the computer's NeoDesk opens a sealed packet; typed: the viewer types */
export type SigninPath = 'sealed' | 'typed';

export type SigninOffer =
	| { kind: 'none' }
	/**
	 * Details are saved, but this computer can't be signed in this way;
	 * sealed: the server knows the computer on the sealed way, and the
	 * client in this session lacks it
	 */
	| { kind: 'unsupported'; why: 'platform' | 'service' | 'sealed' }
	/**
	 * A Sign in button. signedIn: the device reports a signed-in user and no
	 * lock state, so the viewer asks first (confirm); noLockSignal: the
	 * switch is on, but the device can't say whether that desktop is locked
	 */
	| { kind: 'button'; signedIn: boolean; noLockSignal: boolean; confirm: boolean; path: SigninPath }
	/** Sign in now, by itself */
	| { kind: 'auto'; path: SigninPath };

export function isWindows(platform: string): boolean {
	return /windows/i.test(platform);
}

/** Nobody is signed in at the device's console */
export function nobodySignedIn(peer: PeerFacts): boolean {
	const user = peer.username.trim().toLowerCase();
	return user === '' || user === 'system';
}

/**
 * What the viewer offers for a session. tried: this session used its one
 * attempt; autoDone: the viewer already signed in by itself on this device
 * since the page opened (a reconnect does not sign in by itself again);
 * atStart: this is the screen the session opened on (a state that changes
 * later gets the button, never the sign-in by itself).
 */
export function signinOffer(
	peer: PeerFacts | null,
	status: SigninStatus | null,
	state: { tried: boolean; autoDone: boolean; atStart?: boolean }
): SigninOffer {
	if (!status?.set || !status.may_use || !peer || state.tried) return { kind: 'none' };
	const path: SigninPath = peer.sealed ? 'sealed' : 'typed';
	if (path === 'typed') {
		// The server hands out no readable details for it: say why
		if (status.sealed_signin) return { kind: 'unsupported', why: 'sealed' };
		if (!isWindows(peer.platform)) return { kind: 'unsupported', why: 'platform' };
		if (!peer.sasEnabled) return { kind: 'unsupported', why: 'service' };
	}
	const atStart = state.atStart !== false;
	if (peer.logonState) {
		// The computer says where it is: only its lock or sign-in screen,
		// never an open desktop or a screen of an unlocked session
		if (peer.logonState !== 'locked' && peer.logonState !== 'prelogin') return { kind: 'none' };
		if (status.auto && atStart && !state.autoDone) return { kind: 'auto', path };
		return { kind: 'button', signedIn: false, noLockSignal: false, confirm: false, path };
	}
	const nobody = nobodySignedIn(peer);
	if (status.auto && nobody && atStart && !state.autoDone) return { kind: 'auto', path };
	return { kind: 'button', signedIn: !nobody, noLockSignal: status.auto && !nobody, confirm: !nobody, path };
}

export type SigninStep =
	| { kind: 'sas' }
	| { kind: 'wait'; ms: number }
	| { kind: 'text'; field: 'username' | 'password' }
	| { kind: 'key'; key: 'Tab' | 'Return' };

/**
 * The keys for a sign-in screen: Ctrl+Alt+Del (it brings the sign-in
 * screen up from the lock screen and from the screen after a restart),
 * then the password and Enter, or for a screen that asks for both the
 * account name (as saved: name, DOMAIN\name or name@domain), Tab, the
 * password and Enter.
 */
export function signinSteps(form: SigninForm): SigninStep[] {
	const steps: SigninStep[] = [{ kind: 'sas' }, { kind: 'wait', ms: SAS_WAIT_MS }];
	if (form === 'name_password') {
		steps.push({ kind: 'text', field: 'username' }, { kind: 'key', key: 'Tab' }, { kind: 'wait', ms: 200 });
	}
	steps.push({ kind: 'text', field: 'password' }, { kind: 'key', key: 'Return' });
	return steps;
}

export interface SigninKeys {
	sendCtrlAltDel(): void;
	sendText(text: string): boolean | undefined;
	sendKeyPress(key: 'Tab' | 'Return'): boolean;
}

/**
 * Types the steps. It stops, and answers false, as soon as the session is
 * gone or a key does not go out: nothing is typed into a screen the
 * sequence did not prepare. details is read here and nowhere kept.
 */
export async function typeSignin(
	keys: SigninKeys,
	steps: SigninStep[],
	details: { username: string; password: string },
	opts: { sleep?: (ms: number) => Promise<void>; alive: () => boolean }
): Promise<boolean> {
	const sleep = opts.sleep ?? ((ms: number) => new Promise<void>((r) => setTimeout(r, ms)));
	for (const step of steps) {
		if (!opts.alive()) return false;
		switch (step.kind) {
			case 'sas':
				keys.sendCtrlAltDel();
				break;
			case 'wait':
				await sleep(step.ms);
				break;
			case 'text':
				if (keys.sendText(step.field === 'username' ? details.username : details.password) === false)
					return false;
				break;
			case 'key':
				if (!keys.sendKeyPress(step.key)) return false;
				break;
		}
	}
	return opts.alive();
}
