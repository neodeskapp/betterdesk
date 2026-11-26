/**
 * Why a connection failed, as a reason code, and what the viewer says about
 * it: what happened, why, what to do and the buttons that do it. A reason
 * comes from where the failure starts (the server's PunchHoleResponse
 * failure, its refusal values in server-refusals.json, the device's login
 * refusals in device-refusals.json, the client's own checks), never from
 * reading an English message. The message stays for Details only.
 *
 * The texts are the failure.<reason>.<part> keys of the language files;
 * docs/failure-reasons.md lists every reason with its English and German
 * texts (failures.test.ts keeps the three in step).
 */

import { tf } from '#lib/i18n/i18n.svelte.ts';
import deviceRefusals from './device-refusals.json';
import { FAILURE_TEXTS, type Reason } from './failure-texts.ts';
import serverRefusals from './server-refusals.json';

export type { Reason };

export const REASONS = Object.keys(FAILURE_TEXTS) as Reason[];

export function isReason(value: unknown): value is Reason {
	return typeof value === 'string' && Object.hasOwn(FAILURE_TEXTS, value);
}

/** An error that knows its reason; message is for Details */
export class ReasonError extends Error {
	readonly reason: Reason;
	constructor(reason: Reason, message: string) {
		super(message);
		this.name = 'ReasonError';
		this.reason = reason;
	}
}

/** The reason an error carries, or null */
export function reasonOf(err: unknown): Reason | null {
	if (err instanceof ReasonError) return err.reason;
	if (err && typeof err === 'object' && 'reason' in err && isReason((err as { reason: unknown }).reason))
		return (err as { reason: Reason }).reason;
	return null;
}

const SERVER_REFUSALS: Record<string, string> = serverRefusals;
const DEVICE_REFUSALS: Record<string, string> = deviceRefusals;

/** The reason of a refusal value the signal server sends (RelayResponse refuse_reason, PunchHoleResponse other_failure) */
export function serverRefusalReason(value: string): Reason {
	const r = SERVER_REFUSALS[value.trim()];
	return isReason(r) ? r : 'relay_refused';
}

/** The reason of a login refusal the device sends (LoginResponse error), null for one it has no code for */
export function deviceRefusalReason(value: string): Reason | null {
	const r = DEVICE_REFUSALS[value.trim()];
	return isReason(r) ? r : null;
}

/** PunchHoleResponse.Failure (rendezvous.proto) to a reason */
export function punchHoleReason(failure: number): Reason {
	switch (failure) {
		case 2:
			return 'device_offline';
		case 3:
			return 'license_blocked';
		case 4:
			return 'session_limit';
	}
	return 'device_unknown';
}

/** A button of a failure; the viewer wires each to its step */
export type FailAction = 'reconnect' | 'unblock' | 'open_device' | 'https_help' | 'copy_details';

export interface Failure {
	reason: Reason;
	what: string;
	why: string;
	todo: string;
	actions: FailAction[];
}

export interface FailureContext {
	/** The computer, as the console names it */
	name: string;
	/** The person at the computer (a quick support session's name); the computer's name when unknown */
	person?: string;
	/** The account may unblock the computer (reach can_unblock) */
	canUnblock?: boolean;
}

/** Reasons the device panel helps with: unban, restore, approve the key */
const PANEL_REASONS: ReadonlySet<Reason> = new Set([
	'device_banned',
	'device_removed',
	'key_changed',
	'device_offline'
] as Reason[]);

/** Reasons a reconnect can't change until someone acts elsewhere */
const NO_RECONNECT: ReadonlySet<Reason> = new Set([
	'device_unknown',
	'device_banned',
	'device_removed',
	'device_rejected',
	'other_machine',
	'join_proof',
	'feature_off',
	'license_blocked',
	'billing_blocked',
	'not_authorized'
] as Reason[]);

function text(reason: Reason, part: string, fallback: string, p: Record<string, string>): string {
	return tf(`failure.${reason}.${part}`, fallback, p);
}

/** What the viewer says about a failure, in the console language */
export function describeFailure(reason: Reason, ctx: FailureContext): Failure {
	const t = FAILURE_TEXTS[reason] as Record<string, string>;
	const p = { name: ctx.name, person: ctx.person || ctx.name };
	const blockedKind = reason === 'device_revoked' || reason === 'device_blocked';
	const todoPart = blockedKind && !ctx.canUnblock ? 'todo_ask' : 'todo';
	const actions: FailAction[] = [];
	if (blockedKind && ctx.canUnblock) actions.push('unblock');
	if (!NO_RECONNECT.has(reason)) actions.push('reconnect');
	if (reason === 'https_required') actions.push('https_help');
	if (PANEL_REASONS.has(reason)) actions.push('open_device');
	actions.push('copy_details');
	return {
		reason,
		what: text(reason, 'what', t.what ?? '', p),
		why: text(reason, 'why', t.why ?? '', p),
		todo: text(reason, todoPart, t[todoPart] ?? t.todo ?? '', p),
		actions
	};
}

/**
 * Texts that must never reach a person: transport messages, error codes on
 * their own, and the empty phrases. A rendered failure that matches one is a
 * bug (failures.test.ts and the viewer's tests check every reason).
 */
export const RAW_TEXT = [
	/connection refused:/i,
	/rendezvous/i,
	/relay refused/i,
	/handshake failed/i,
	/websocket \d{4}/i,
	/something went wrong/i,
	/failed to parse/i,
	/\bundefined\b|\bnull\b|\[object /,
	/^[A-Z][A-Z0-9_]+$/,
	/\bID_NOT_EXIST\b|\bOFFLINE\b/,
	/punch ?hole|\bpk\b|\bhbbs\b|\bhbbr\b/i,
	/\{[a-z]+\}/
];

export function isRawText(value: string): boolean {
	return RAW_TEXT.some((re) => re.test(value));
}
