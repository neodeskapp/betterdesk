/** Recent sign-ins and security events without a DOM: what a row says. */

import type { SecurityEvent } from '#lib/api/security-events.ts';
import { actionLabel } from '#lib/people/audit.ts';

type T = (key: string, params?: Record<string, string | number>) => string;

/** The row's title: "<admin> viewed NeoDesk as you", else the audit log's label of the action */
export function eventTitle(e: SecurityEvent, t: T): string {
	if (e.action === 'impersonation_started') {
		return t('account.security_event_impersonation', { admin: e.actor || '?' });
	}
	return actionLabel(e.action, t);
}

/** The facts under the title: who did it when that was someone else, the reason, the address */
export function eventFacts(e: SecurityEvent, t: T): string[] {
	const out: string[] = [];
	if (e.actor && e.action !== 'impersonation_started')
		out.push(t('account.security_event_by', { actor: e.actor }));
	if (e.reason) out.push(t('account.security_event_reason', { reason: e.reason }));
	if (e.ip) out.push(`${t('sessions.address')}: ${e.ip}`);
	return out;
}
