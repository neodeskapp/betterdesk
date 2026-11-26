/**
 * Settings › Email as a draft (lib/settings/email.ts): the checks per
 * field before anything is sent, and the alert addresses as a list.
 */
import { describe, expect, test } from 'vitest';
import { emailProblem, smtpProblems, splitEmails } from '../../../src/lib/settings/email.ts';

const ok = {
	host: 'smtp.example.com',
	port: 587,
	secure: false,
	auth: true,
	user: 'alerts',
	pass: '********',
	from: 'NeoDesk <neodesk@example.com>',
	alert_email: ['ops@example.com']
};

describe('the mail server, checked per field', () => {
	test('a complete server has no problems', () => {
		expect(smtpProblems(ok)).toEqual({});
		expect(smtpProblems({ ...ok, from: '' })).toEqual({});
	});

	test('the host is needed and stands alone; the port is in range', () => {
		expect(smtpProblems({ ...ok, host: ' ' }).host).toMatch(/address of the mail server/);
		expect(smtpProblems({ ...ok, host: 'smtp.example.com:587' }).host).toMatch(/without/);
		expect(smtpProblems({ ...ok, port: 0 }).port).toBeTruthy();
		expect(smtpProblems({ ...ok, port: null }).port).toBeTruthy();
	});

	test('the sender is one address, with or without a name', () => {
		expect(smtpProblems({ ...ok, from: 'neodesk@example.com' })).toEqual({});
		expect(smtpProblems({ ...ok, from: 'a@x.com, b@x.com' }).from).toBeTruthy();
		expect(smtpProblems({ ...ok, from: 'not an address' }).from).toBeTruthy();
	});

	test('a wrong alert address is said on its row and blocks the save', () => {
		expect(emailProblem('ops@example.com')).toBe('');
		expect(emailProblem('ops@')).toBeTruthy();
		expect(smtpProblems({ ...ok, alert_email: ['ops@example.com', 'x'] }).alert_email).toBeTruthy();
	});
});

describe('the alert addresses', () => {
	test('the stored text becomes a list, at commas, semicolons and spaces', () => {
		expect(splitEmails('a@x.com, b@x.com;c@x.com  d@x.com')).toEqual([
			'a@x.com',
			'b@x.com',
			'c@x.com',
			'd@x.com'
		]);
		expect(splitEmails('')).toEqual([]);
	});
});
