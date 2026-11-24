/**
 * Remote sessions inside the console (console-sessions.svelte.ts): one
 * viewer above the routes that outlives the session page, tabs per
 * device, parked (connected, no keyboard, mouse or clipboard) while the
 * console shows another page, the standalone viewer for what the console
 * can't hold, refusals and sign-in, Open in new tab, and the way back when
 * the last session closes.
 */
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

type Listener = (...args: unknown[]) => void;

class FakeClient {
	static made: FakeClient[] = [];
	listeners = new Map<string, Listener[]>();
	disconnect = vi.fn();
	setSessionActive = vi.fn();
	renderer = { resize: vi.fn() };
	constructor(
		public canvas: HTMLCanvasElement,
		public opts: Record<string, unknown>
	) {
		FakeClient.made.push(this);
	}
	on(ev: string, fn: Listener) {
		this.listeners.set(ev, [...(this.listeners.get(ev) ?? []), fn]);
	}
	emit(ev: string, ...args: unknown[]) {
		for (const fn of this.listeners.get(ev) ?? []) fn(...args);
	}
	connect = vi.fn(async () => {});
	authenticate = vi.fn();
}

const api = vi.hoisted(() => ({ viewerData: vi.fn(), loaded: vi.fn() }));

vi.mock('../../../src/lib/remote/rdclient.ts', () => ({
	createClient: (canvas: HTMLCanvasElement, opts: Record<string, unknown>) => new FakeClient(canvas, opts),
	loadRdClient: api.loaded,
	fileSheet: () => null,
	touchInput: () => null,
	videoDecodingSupported: () => true
}));
vi.mock('../../../src/lib/api/remote.ts', () => {
	return {
		getViewerData: api.viewerData,
		acceptCertificate: vi.fn(),
		getBillingCheck: vi.fn(async () => null),
		getConnectPassword: vi.fn(async () => ''),
		getPendingBillingSession: vi.fn(async () => null),
		submitWorkReport: vi.fn()
	};
});
vi.mock('../../../src/lib/api/devices.ts', () => ({ getDevice: vi.fn(async () => ({})) }));
vi.mock('../../../src/lib/api/onboard.ts', () => ({ takeDevicePassword: vi.fn(async () => '') }));
vi.mock('../../../src/lib/remote/secure-store.ts', () => ({
	secureStore: () => ({
		loadPeerPassword: async () => '',
		savePeerPassword: async () => {},
		clearPeerPassword: async () => {}
	})
}));

const { ConsoleSessions, sessionPath, standalonePath } =
	await import('../../../src/lib/remote/console-sessions.svelte.ts');
const { ApiError } = await import('../../../src/lib/api/client.ts');

function data(id: string, caps: Record<string, unknown> = { transport: 'rd' }, user = true) {
	return {
		success: true,
		deviceId: id,
		device: { name: 'host-' + id, platform: 'Windows' },
		capabilities: caps,
		serverPubKey: 'KEY',
		connection: null,
		guestToken: '',
		mockRemote: null,
		user: user ? { id: 1, name: 'admin' } : null,
		workReports: false,
		csrfToken: ''
	};
}

/** The console's viewer drew the session's screen: it connects */
function attachAll(cs: InstanceType<typeof ConsoleSessions>) {
	for (const s of cs.viewer?.sessions ?? [])
		if (!s.started) cs.viewer?.attach(s, document.createElement('canvas'));
}

beforeEach(() => {
	FakeClient.made = [];
	api.viewerData.mockImplementation(async (id: string) => data(id));
	api.loaded.mockResolvedValue(undefined);
	vi.useFakeTimers();
});
afterEach(() => {
	vi.useRealTimers();
	vi.restoreAllMocks();
});

describe('remote sessions in the console', () => {
	test('one viewer for the console: a tab per device, an open one is picked again', async () => {
		const cs = new ConsoleSessions();
		cs.setShown(true);
		expect(await cs.open('111111111')).toBe('opened');
		const viewer = cs.viewer;
		expect(api.loaded).toHaveBeenCalledOnce();
		expect(await cs.open('222222222')).toBe('opened');
		expect(cs.viewer).toBe(viewer);
		expect(cs.viewer?.sessions.map((s) => s.deviceId)).toEqual(['111111111', '222222222']);
		expect(cs.viewer?.activeId).toBe('222222222');
		expect(await cs.open('111111111')).toBe('opened');
		expect(cs.viewer?.activeId).toBe('111111111');
		expect(api.viewerData).toHaveBeenCalledTimes(2);
		expect(cs.count).toBe(2);
	});

	test('another page parks the sessions: still connected, no keyboard, mouse or clipboard; back on the page they take them again', async () => {
		const cs = new ConsoleSessions();
		cs.setShown(true);
		await cs.open('111111111');
		attachAll(cs);
		const c = FakeClient.made[0]!;
		c.emit('state', 'streaming');
		expect(c.setSessionActive).toHaveBeenLastCalledWith(true);
		expect(cs.viewer?.clipboardOff()).toBe('');
		cs.setShown(false);
		expect(c.setSessionActive).toHaveBeenLastCalledWith(false);
		expect(cs.viewer?.clipboardOff()).toBe('not_streaming');
		expect(c.disconnect).not.toHaveBeenCalled();
		expect(cs.count).toBe(1);
		cs.setShown(true);
		expect(c.setSessionActive).toHaveBeenLastCalledWith(true);
	});

	test('a viewer made while another page shows starts parked', async () => {
		const cs = new ConsoleSessions();
		await cs.open('111111111');
		expect(cs.viewer?.parked).toBe(true);
	});

	test('guest links, RDP and VNC targets, other transports and pages without an account go to the standalone viewer', async () => {
		const cs = new ConsoleSessions();
		api.viewerData.mockResolvedValueOnce(data('111111111', { transport: 'rd', guest_access: true }));
		api.viewerData.mockResolvedValueOnce(data('rt_abc', { transport: 'rdp', remote_target: true }));
		api.viewerData.mockResolvedValueOnce(data('333333333', { transport: 'rd' }, false));
		const results = [await cs.open('111111111'), await cs.open('rt_abc'), await cs.open('333333333')];
		expect(results).toEqual(['standalone', 'standalone', 'standalone']);
		expect(cs.viewer).toBeNull();
		await cs.open('444444444');
		api.viewerData.mockResolvedValueOnce(data('555555555', { transport: 'cdap' }));
		expect(await cs.open('555555555')).toBe('standalone');
		expect(cs.viewer?.sessions.map((s) => s.deviceId)).toEqual(['444444444']);
	});

	test('a refusal is a problem on the page, an expired sign-in goes to sign-in', async () => {
		const cs = new ConsoleSessions();
		api.viewerData.mockRejectedValueOnce(new ApiError('no', 403, { code: 'forbidden' }));
		expect(await cs.open('111111111')).toBe('problem');
		expect(cs.problem?.deviceId).toBe('111111111');
		expect(cs.problem?.title).toBeTruthy();
		api.viewerData.mockRejectedValueOnce(new ApiError('no', 401));
		expect(await cs.open('111111111')).toBe('signin');
		expect(cs.viewer).toBeNull();
	});

	test('Open in new tab moves the session to the standalone viewer and ends it here', async () => {
		const cs = new ConsoleSessions();
		cs.setShown(true);
		await cs.open('111111111');
		await cs.open('222222222');
		attachAll(cs);
		const open = vi.spyOn(window, 'open').mockReturnValue({} as Window);
		cs.openInNewTab('111111111');
		expect(open).toHaveBeenCalledWith('/remote/111111111?standalone=1', '_blank');
		expect(FakeClient.made[0]!.disconnect).toHaveBeenCalled();
		expect(cs.viewer?.sessions.map((s) => s.deviceId)).toEqual(['222222222']);
		// A blocked pop-up keeps the session here
		open.mockReturnValue(null);
		cs.openInNewTab('222222222');
		expect(cs.count).toBe(1);
	});

	test('closing the last session goes back to the page the person came from', async () => {
		const cs = new ConsoleSessions();
		const went: string[] = [];
		cs.navigate = (path) => void went.push(path);
		cs.returnTo = '/devices?folder=3';
		cs.setShown(true);
		await cs.open('111111111');
		cs.close('111111111');
		expect(went).toEqual(['/devices?folder=3']);
	});

	test('leaving the console ends every session', async () => {
		const cs = new ConsoleSessions();
		cs.setShown(true);
		await cs.open('111111111');
		await cs.open('222222222');
		attachAll(cs);
		cs.teardown();
		expect(FakeClient.made.every((c) => c.disconnect.mock.calls.length > 0)).toBe(true);
		expect(cs.viewer).toBeNull();
		expect(cs.count).toBe(0);
	});

	test('addresses: the console page, and the standalone viewer that keeps the query', () => {
		expect(sessionPath('123 456')).toBe('/remote/session/123%20456');
		expect(standalonePath('123456789', '?mock=1&standalone=0')).toBe('/remote/123456789?mock=1&standalone=1');
	});
});
