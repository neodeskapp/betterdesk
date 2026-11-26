import { describe, expect, test } from 'vitest';
import { timeLeft, viewingTitle } from '#lib/shell/viewing-as.ts';
import {
	applyBootstrap,
	canWrite,
	clearSession,
	sectionWritable,
	session
} from '#lib/state/session.svelte.ts';

describe('the Sign in as bar', () => {
	test('writes the time left as minutes and seconds, never below zero', () => {
		expect(timeLeft(15 * 60_000)).toBe('15:00');
		expect(timeLeft(14 * 60_000 + 5_400)).toBe('14:06');
		expect(timeLeft(9_000)).toBe('0:09');
		expect(timeLeft(-5_000)).toBe('0:00');
	});

	test('puts the account first in the tab title', () => {
		expect(viewingTitle('Viewing as olga', 'Devices - NeoDesk')).toBe('Viewing as olga - Devices - NeoDesk');
	});
});

describe('the console while viewing as someone', () => {
	const boot = {
		success: true as const,
		user: { id: 5, username: 'olga', role: 'operator', preferredLanguage: null, avatarVersion: null },
		permissions: ['device.view', 'device.edit'],
		access: null,
		isSuperAdmin: false,
		emergencyMode: false,
		nav: [],
		lang: { code: 'en', dir: 'ltr' as const, stringsUrl: '/x', available: [] },
		branding: {
			appName: 'NeoDesk',
			appDescription: '',
			themeMode: 'system',
			logoType: null,
			logoIcon: '',
			logoSvg: '',
			logoUrl: '',
			logoText: '',
			logoTextAccent: '',
			footerText: '',
			showPoweredBy: true,
			revision: 1
		},
		palettes: { light: null, dark: null },
		sessionPolicy: null,
		csrfToken: 't',
		appVersion: '1'
	};

	test('offers no write: the server refuses them all', () => {
		applyBootstrap({ ...boot, impersonation: null });
		expect(canWrite('device.edit')).toBe(true);
		expect(sectionWritable()).toBe(true);
		applyBootstrap({
			...boot,
			impersonation: {
				adminUsername: 'sam',
				reason: 'r',
				startedAt: 1,
				expiresAt: 2,
				canExtend: true,
				serverNow: 1
			}
		});
		expect(session.impersonation?.adminUsername).toBe('sam');
		expect(canWrite('device.edit')).toBe(false);
		expect(sectionWritable()).toBe(false);
		clearSession();
		expect(session.impersonation).toBeNull();
	});
});
