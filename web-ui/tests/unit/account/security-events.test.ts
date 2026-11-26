import { describe, expect, test } from 'vitest';
import { eventFacts, eventTitle } from '#lib/account/security-events.ts';
import type { SecurityEvent } from '#lib/api/security-events.ts';

const T: Record<string, string> = {
	'account.security_event_impersonation': '{admin} viewed NeoDesk as you',
	'account.security_event_by': 'By {actor}',
	'account.security_event_reason': 'Reason: {reason}',
	'audit.action_login': 'Signed in',
	'sessions.address': 'Address'
};
const t = (key: string, params: Record<string, string | number> = {}) =>
	(T[key] ?? key).replace(/\{(\w+)\}/g, (_, name: string) => String(params[name] ?? ''));

const event = (over: Partial<SecurityEvent>): SecurityEvent => ({
	id: 1,
	action: 'login',
	at: '2026-11-26 10:00:00',
	ip: '',
	actor: '',
	reason: '',
	...over
});

describe('recent sign-ins and security events', () => {
	test('names the admin who viewed NeoDesk as the person, with the reason and no address', () => {
		const e = event({ action: 'impersonation_started', actor: 'sam', reason: 'Ticket 4711' });
		expect(eventTitle(e, t)).toBe('sam viewed NeoDesk as you');
		expect(eventFacts(e, t)).toEqual(['Reason: Ticket 4711']);
	});

	test("labels the others as the audit log does, with the address of the person's own", () => {
		const e = event({ ip: '203.0.113.7' });
		expect(eventTitle(e, t)).toBe('Signed in');
		expect(eventFacts(e, t)).toEqual(['Address: 203.0.113.7']);
		expect(eventFacts(event({ action: 'password_reset', actor: 'sam' }), t)).toEqual(['By sam']);
	});
});
