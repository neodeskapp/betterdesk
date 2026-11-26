/**
 * Settings › Email without a DOM: the form's values, the checks per field
 * before anything is sent (the server checks the same and more, and its
 * refusal names the field), and the alert addresses as a list.
 */
import { tf } from '#lib/i18n/i18n.svelte.ts';
import { hostProblem } from './general.ts';
import { wholeInRange } from './numbers.ts';

export interface SmtpValues {
	host: string;
	port: number | null;
	secure: boolean;
	/** The form's own switch: on when a user name is saved */
	auth: boolean;
	user: string;
	/** The server's mask while a password is saved */
	pass: string;
	from: string;
	alert_email: string[];
	[key: string]: unknown;
}

const ADDRESS = /^[^\s@<>,;]+@[^\s@<>,;]+\.[^\s@<>,;]+$/;

/** What is wrong with one email address ('' when nothing) */
export function emailProblem(raw: string): string {
	const v = raw.trim();
	if (!v || ADDRESS.test(v)) return '';
	return tf('settings.email.err_address', 'This is not an email address. For example ops@example.com.');
}

/** One sender: an address, or a name with the address in angle brackets */
function fromProblem(raw: string): string {
	const v = raw.trim();
	if (!v) return '';
	const named = /^[^<>]*<([^<>]+)>$/.exec(v);
	const address = named ? (named[1] ?? '').trim() : v;
	return ADDRESS.test(address)
		? ''
		: tf(
				'settings.email.err_from_invalid',
				'Enter one email address, for example neodesk@example.com or NeoDesk <neodesk@example.com>.'
			);
}

/** The problems of the form by field */
export function smtpProblems(v: SmtpValues): Partial<Record<keyof SmtpValues & string, string>> {
	const out: Partial<Record<string, string>> = {};
	if (!v.host.trim())
		out.host = tf('settings.email.err_host_required', 'Enter the address of the mail server.');
	else {
		const host = hostProblem(v.host);
		if (host)
			out.host = tf(
				'settings.email.err_host_alone',
				'Enter only the host name or IP address, without a port. For example smtp.example.com.'
			);
	}
	if (wholeInRange(v.port, 1, 65535) === null)
		out.port = tf('settings.email.port_error', 'Enter a port from 1 to 65535.');
	const from = fromProblem(v.from);
	if (from) out.from = from;
	if (v.alert_email.some((a) => emailProblem(a)))
		out.alert_email = tf('settings.email.err_alert_email', 'Enter email addresses separated by commas.');
	return out;
}

/** The stored alert addresses as a list */
export function splitEmails(raw: unknown): string[] {
	return String(raw ?? '')
		.split(/[\s,;]+/)
		.map((e) => e.trim())
		.filter(Boolean);
}
