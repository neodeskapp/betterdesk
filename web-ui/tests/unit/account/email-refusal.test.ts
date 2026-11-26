/**
 * A refused new address (lib/account/email-refusal.ts): the text comes
 * from the code, and says the other changes are saved when the answer
 * names any (A274).
 */
import { afterEach, beforeEach, describe, expect, test } from 'vitest';
import { emailRefusal } from '../../../src/lib/account/email-refusal.ts';
import { ApiError } from '../../../src/lib/api/client.ts';
import { setStrings } from '../../../src/lib/i18n/i18n.svelte.ts';

beforeEach(() => {
	setStrings('de', 'ltr', {
		users: {
			email_change_needs_mail: 'Braucht die E-Mail-Einstellungen.',
			email_confirm_mail_failed: 'Der Mailserver hat abgelehnt.',
			email_not_changed_rest_saved: 'Der Rest ist gespeichert. {reason}'
		}
	});
});
afterEach(() => setStrings('en', 'ltr', {}));

const refused = (status: number, code: string, data: Record<string, unknown> = {}) =>
	new ApiError('English from the server', status, {
		code,
		data: { success: false, error: 'x', code, ...data }
	});

describe('a refused address', () => {
	test('the mail server: its own text, and that the rest is saved when it is', () => {
		expect(emailRefusal(refused(502, 'email_confirmation_not_sent', { saved: ['phone'] }))).toEqual({
			code: 'email_confirmation_not_sent',
			othersSaved: true,
			message: 'Der Rest ist gespeichert. Der Mailserver hat abgelehnt.'
		});
		expect(emailRefusal(refused(502, 'email_confirmation_not_sent'))?.message).toBe(
			'Der Mailserver hat abgelehnt.'
		);
	});

	test('no email settings', () => {
		expect(emailRefusal(refused(409, 'email_change_needs_mail', { saved: [] }))).toEqual({
			code: 'email_change_needs_mail',
			othersSaved: false,
			message: 'Braucht die E-Mail-Einstellungen.'
		});
	});

	test('any other error is none', () => {
		expect(emailRefusal(refused(400, 'time_zone_invalid'))).toBeNull();
		expect(emailRefusal(new ApiError('Bad gateway', 502))).toBeNull();
		expect(emailRefusal(new Error('x'))).toBeNull();
	});
});
