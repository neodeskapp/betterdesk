import {
	can,
	canWrite,
	clearSession,
	sectionDenied,
	session,
	startSession
} from '#lib/state/session.svelte.ts';

const boot = {
	success: true,
	user: {
		id: 1,
		username: 'demo_ui_admin',
		role: 'admin',
		preferredLanguage: null as string | null,
		avatarVersion: 'pic-1'
	},
	permissions: ['device.view'],
	isSuperAdmin: true,
	emergencyMode: false,
	nav: [],
	lang: {
		code: 'en',
		dir: 'ltr',
		stringsUrl: '/api/console/strings/en?v=1',
		available: [{ code: 'en', name: 'English', native: 'English', flag: 'en', rtl: false }]
	},
	branding: {
		appName: 'NeoDesk',
		appDescription: '',
		themeMode: 'system',
		logoType: 'text',
		logoText: 'Acme',
		showPoweredBy: false,
		revision: 1
	},
	palettes: { light: null, dark: null },
	sessionPolicy: null,
	csrfToken: 'token',
	appVersion: '3.0.0'
};

const ok = (body: unknown) => new Response(JSON.stringify(body), { status: 200 });

type BootWindow = { __ND_BOOT__?: Record<string, unknown> };

afterEach(() => {
	delete (window as unknown as BootWindow).__ND_BOOT__;
	clearSession();
});

describe('startSession', () => {
	test('uses the requests the boot script started, and asks again on a second start', async () => {
		const fetchMock = vi.fn((url: string) => Promise.resolve(ok(url.includes('strings') ? {} : boot)));
		vi.stubGlobal('fetch', fetchMock);
		(window as unknown as BootWindow).__ND_BOOT__ = {
			lang: 'en',
			dir: 'ltr',
			stringsUrl: '/api/console/strings/en?v=1',
			strings: Promise.resolve(ok({})),
			session: Promise.resolve(ok(boot))
		};

		await startSession();
		expect(fetchMock).not.toHaveBeenCalled();
		expect(session.ready).toBe(true);

		// Try again after a failed start: the early responses are used up
		session.ready = false;
		await startSession();
		expect(fetchMock.mock.calls.map((c) => c[0])).toEqual([
			'/api/console/strings/en?v=1',
			'/api/console/bootstrap'
		]);
		expect(session.ready).toBe(true);
		vi.unstubAllGlobals();
	});

	test('the branding details come with the bootstrap, with defaults for missing ones', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn((url: string) => Promise.resolve(ok(url.includes('strings') ? {} : boot)))
		);
		await startSession();
		expect(session.raw?.branding).toMatchObject({
			logoType: 'text',
			logoText: 'Acme',
			logoSvg: '',
			footerText: '',
			showPoweredBy: false
		});
		expect(session.user?.avatarVersion).toBe('pic-1');
		vi.unstubAllGlobals();
	});

	describe('a language picked in this browser before', () => {
		const withDe = {
			...boot,
			lang: {
				...boot.lang,
				available: [
					...boot.lang.available,
					{ code: 'de', name: 'German', native: 'Deutsch', flag: 'de', rtl: false }
				]
			}
		};
		const setup = (b: typeof withDe) => {
			const fetchMock = vi.fn((url: string) =>
				Promise.resolve(ok(url.includes('strings') ? {} : url.includes('/i18n/set/') ? { success: true } : b))
			);
			vi.stubGlobal('fetch', fetchMock);
			return () => fetchMock.mock.calls.map((c) => c[0]);
		};
		afterEach(() => {
			localStorage.clear();
			vi.unstubAllGlobals();
		});

		test('comes back when the account has none', async () => {
			localStorage.setItem('neodesk_lang', 'de');
			const urls = setup(withDe);
			await startSession();
			await vi.waitFor(() => expect(urls()).toContain('/api/i18n/set/de'));
			expect(urls()).toContain('/api/console/strings/de');
		});

		test('stays away when the account has its own language, and the current one is remembered', async () => {
			localStorage.setItem('neodesk_lang', 'de');
			const urls = setup({ ...withDe, user: { ...withDe.user, preferredLanguage: 'en' } });
			await startSession();
			await new Promise((r) => setTimeout(r, 20));
			expect(urls().some((u) => u.includes('/i18n/set/'))).toBe(false);
			expect(localStorage.getItem('neodesk_lang')).toBe('en');
		});
	});
});

describe('panel access policies (as the server applies them)', () => {
	const access = {
		sections: { dashboard: 'read', devices: 'read', users: 'none' } as Record<
			string,
			'none' | 'read' | 'write'
		>,
		pages: [
			{ path: '/', section: 'dashboard' },
			{ prefix: '/devices', section: 'devices' },
			{ prefix: '/users', section: 'users' }
		],
		catalog: [
			{ key: 'devices', read: 'device.view', write: 'device.edit' },
			{ key: 'users', read: 'user.view', write: 'user.edit' }
		]
	};
	beforeEach(() => {
		session.permissions = ['device.view', 'device.edit', 'user.view'];
		session.isSuperAdmin = false;
		session.access = access;
	});

	test('a read-only section turns off its write permission on its pages only', () => {
		session.path = '/devices';
		expect(can('device.view')).toBe(true);
		expect(can('device.edit')).toBe(false);
		session.path = '/devices/900100012';
		expect(can('device.edit')).toBe(false);
		// A page outside any section: the role decides
		session.path = '/pairing';
		expect(can('device.edit')).toBe(true);
	});

	test('a section with one permission for read and write: read opens the page, write is asked by canWrite', () => {
		session.access = {
			...access,
			sections: { ...access.sections, reports: 'read' },
			pages: [...access.pages, { prefix: '/reports', section: 'reports' }],
			catalog: [...access.catalog, { key: 'reports', read: 'audit.view', write: 'audit.view' }]
		};
		session.permissions = ['audit.view', 'device.view', 'device.edit'];
		session.path = '/reports';
		expect(can('audit.view')).toBe(true);
		expect(canWrite('audit.view')).toBe(false);
		session.access = { ...session.access, sections: { ...session.access.sections, reports: 'write' } };
		expect(canWrite('audit.view')).toBe(true);
		// The section only takes away: without the permission in the role, write is no help
		session.permissions = ['device.view'];
		expect(can('audit.view')).toBe(false);
		expect(canWrite('audit.view')).toBe(false);
	});

	test('canWrite in a section with two permissions, and outside any section', () => {
		session.path = '/devices';
		expect(canWrite('device.view')).toBe(false);
		expect(canWrite('device.edit')).toBe(false);
		session.path = '/pairing';
		expect(canWrite('device.edit')).toBe(true);
		expect(canWrite('user.edit')).toBe(false);
	});

	test('super admins keep every permission, as requestHasPermission does', () => {
		session.isSuperAdmin = true;
		session.path = '/devices';
		expect(can('device.edit')).toBe(true);
	});

	test('a section without read access is refused, as the guard does; other pages and no policy never are', () => {
		expect(sectionDenied('/users')).toBe(true);
		expect(sectionDenied('/users/7')).toBe(true);
		expect(sectionDenied('/usersfoo')).toBe(false);
		expect(sectionDenied('/devices')).toBe(false);
		expect(sectionDenied('/pairing')).toBe(false);
		session.access = null;
		expect(sectionDenied('/users')).toBe(false);
	});

	test('the bootstrap brings the access block, and signing out forgets it', async () => {
		const withAccess = { ...boot, access };
		vi.stubGlobal(
			'fetch',
			vi.fn((url: string) => Promise.resolve(ok(url.includes('strings') ? {} : withAccess)))
		);
		await startSession();
		expect(session.access?.sections.users).toBe('none');
		clearSession();
		expect(session.access).toBeNull();
	});
});
