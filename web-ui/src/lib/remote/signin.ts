/**
 * Saved sign-in in the viewer (BUGS L469): whether to offer it, when it
 * signs in by itself, and what it types at a Windows lock or sign-in
 * screen. No DOM and no state here; viewer.svelte.ts calls it.
 *
 * What the device tells: at the session's sign-in a RustDesk device sends
 * PeerInfo with its platform, the user of its console session (empty, or
 * SYSTEM, while nobody is signed in) and sas_enabled (NeoDesk runs as the
 * Windows service and can send Ctrl+Alt+Del and type at the secure
 * desktop). Nothing tells whether a signed-in desktop is locked, and
 * nothing is sent when the screen changes during the session. So the
 * viewer signs in by itself only when nobody is signed in, and offers a
 * button otherwise.
 *
 * What is typed: Ctrl+Alt+Del, a wait for the sign-in screen, then the
 * text as characters (KeyEvent.seq: the device types each character as
 * Unicode, whatever its keyboard layout) and Tab and Enter as keys.
 */

import type { SigninForm, SigninStatus } from '#lib/api/savedSignin.ts';

/** How long the sign-in screen takes to come up after Ctrl+Alt+Del */
export const SAS_WAIT_MS = 1500;

/** What the device said about itself (PeerInfo) */
export interface PeerFacts {
	platform: string;
	/** The user of the device's console session; '' or SYSTEM while nobody is signed in */
	username: string;
	/** NeoDesk runs as its service: Ctrl+Alt+Del works and input reaches the secure desktop */
	sasEnabled: boolean;
}

export type SigninOffer =
	| { kind: 'none' }
	/** Details are saved, but this computer can't be signed in this way */
	| { kind: 'unsupported'; why: 'platform' | 'service' }
	/**
	 * A Sign in button. signedIn: the device reports a signed-in user (ask
	 * first); noLockSignal: the switch is on, but the device can't say
	 * whether that desktop is locked
	 */
	| { kind: 'button'; signedIn: boolean; noLockSignal: boolean }
	/** Sign in now, by itself */
	| { kind: 'auto' };

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
 * since the page opened (a reconnect does not sign in by itself again).
 */
export function signinOffer(
	peer: PeerFacts | null,
	status: SigninStatus | null,
	state: { tried: boolean; autoDone: boolean }
): SigninOffer {
	if (!status?.set || !status.may_use || !peer || state.tried) return { kind: 'none' };
	if (!isWindows(peer.platform)) return { kind: 'unsupported', why: 'platform' };
	if (!peer.sasEnabled) return { kind: 'unsupported', why: 'service' };
	const nobody = nobodySignedIn(peer);
	if (status.auto && nobody && !state.autoDone) return { kind: 'auto' };
	return { kind: 'button', signedIn: !nobody, noLockSignal: status.auto && !nobody };
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
