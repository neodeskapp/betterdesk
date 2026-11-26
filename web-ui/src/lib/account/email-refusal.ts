/**
 * A new email address the server refused (A272, A274): without the
 * console's email settings for one's own address (409
 * email_change_needs_mail), or because the mail server didn't take the
 * confirmation mail (502 email_confirmation_not_sent). The page's text
 * comes from the code, not from the server's English. A save with other
 * fields beside the address (the Users sheet, the operator profile) keeps
 * them: the answer names them in saved, and the text then says that only
 * the address was not changed.
 */

import { ApiError } from '#lib/api/client.ts';
import { t } from '#lib/i18n/i18n.svelte.ts';

export const EMAIL_NEEDS_MAIL = 'email_change_needs_mail';
export const EMAIL_NOT_SENT = 'email_confirmation_not_sent';

const REASONS: Readonly<Record<string, string>> = {
	[EMAIL_NEEDS_MAIL]: 'users.email_change_needs_mail',
	[EMAIL_NOT_SENT]: 'users.email_confirm_mail_failed'
};

export interface EmailRefusal {
	code: typeof EMAIL_NEEDS_MAIL | typeof EMAIL_NOT_SENT;
	/** The save's other fields were stored */
	othersSaved: boolean;
	/** What the page says */
	message: string;
}

/** The refusal of a new address in err, or null for any other error */
export function emailRefusal(err: unknown): EmailRefusal | null {
	if (!(err instanceof ApiError) || !err.code) return null;
	const key = REASONS[err.code];
	if (!key) return null;
	const saved =
		err.data && typeof err.data === 'object' ? (err.data as { saved?: unknown }).saved : undefined;
	const othersSaved = Array.isArray(saved) && saved.length > 0;
	const reason = t(key);
	return {
		code: err.code as EmailRefusal['code'],
		othersSaved,
		message: othersSaved ? t('users.email_not_changed_rest_saved', { reason }) : reason
	};
}
