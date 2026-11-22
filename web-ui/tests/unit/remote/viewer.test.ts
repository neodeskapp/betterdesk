/**
 * The viewer's sessions with a fake transport client: the connecting steps,
 * the password prompt, streaming, the ways a session ends or fails, cancel,
 * reconnect (a new client; the old one's late events are ignored), tabs and
 * the guest allowlist.
 */
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { flushSync } from 'svelte';

type Listener = (...args: unknown[]) => void;

class FakeClient {
	static made: FakeClient[] = [];
	listeners = new Map<string, Listener[]>();
	disconnected = false;
	viewOnly = false;
	renderer = { resize: vi.fn() };
	sent: string[] = [];
	_state = 'idle';
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
	disconnect = vi.fn(() => {
		this.disconnected = true;
	});
	authenticate = vi.fn();
	submit2FA = vi.fn();
	sendChat = vi.fn((t: string) => this.sent.push(t));
	sendCtrlAltDel = vi.fn();
	setAudioMuted = vi.fn();
	setSessionActive = vi.fn();
	setQualityPreset = vi.fn();
	setScaleMode = vi.fn();
	setCodec = vi.fn();
	setBlockInput = vi.fn();
	setViewOnly = vi.fn();
	setDisableClipboard = vi.fn();
	getMonitors = () => [];
	switchMonitor = vi.fn();
	toggleVirtualDisplay = vi.fn();
	selectWindowsSession = vi.fn();
}

vi.mock('../../../src/lib/remote/rdclient.ts', () => ({
	createClient: (canvas: HTMLCanvasElement, opts: Record<string, unknown>) => new FakeClient(canvas, opts),
	fileSheet: () => null,
	touchInput: () => null,
	videoDecodingSupported: () => true
}));
vi.mock('../../../src/lib/api/devices.ts', () => ({ getDevice: vi.fn(async () => ({})) }));
vi.mock('../../../src/lib/api/remote.ts', () => ({
	acceptCertificate: vi.fn(),
	getBillingCheck: vi.fn(async () => null),
	getConnectPassword: vi.fn(async () => ''),
	getPendingBillingSession: vi.fn(async () => null),
	submitWorkReport: vi.fn()
}));
vi.mock('../../../src/lib/api/onboard.ts', () => ({ takeDevicePassword: vi.fn(async () => '') }));
/** Device passwords remembered in this browser */
const remembered = vi.hoisted(() => new Map<string, string>());
vi.mock('../../../src/lib/remote/secure-store.ts', () => ({
	secureStore: () => ({
		loadPeerPassword: async (id: string) => remembered.get(id) ?? '',
		savePeerPassword: async (id: string, pw: string) => void remembered.set(id, pw),
		clearPeerPassword: async (id: string) => void remembered.delete(id)
	})
}));

const { Viewer } = await import('../../../src/lib/remote/viewer.svelte.ts');
const remoteApi = await import('../../../src/lib/api/remote.ts');
const devicesApi = await import('../../../src/lib/api/devices.ts');
const onboardApi = await import('../../../src/lib/api/onboard.ts');
type ViewerData = ConstructorParameters<typeof Viewer>[0]['data'];

function data(extra: Partial<ViewerData> = {}): ViewerData {
	return {
		success: true,
		deviceId: '900100012',
		device: { name: 'demo-build-server', platform: 'Ubuntu 24.04' },
		capabilities: { transport: 'rd' },
		serverPubKey: 'KEY',
		connection: null,
		guestToken: '',
		mockRemote: null,
		user: { id: 1, name: 'demo_ui_admin' },
		csrfToken: '',
		...extra
	} as ViewerData;
}

function setup(d = data()) {
	const leave = vi.fn();
	const v = new Viewer({ data: d, onleave: leave });
	v.open(d.deviceId, d.device.name, d.device.platform);
	const s = v.sessions[0]!;
	v.attach(s, document.createElement('canvas'));
	return { v, s, c: FakeClient.made.at(-1)!, leave };
}

beforeEach(() => {
	FakeClient.made = [];
	vi.useFakeTimers();
});
afterEach(() => vi.useRealTimers());

describe('remote viewer sessions', () => {
	test('connects with the server key and the account name, then walks the steps', () => {
		const { s, c } = setup();
		expect(c.connect).toHaveBeenCalled();
		expect(c.opts).toMatchObject({ deviceId: '900100012', serverPubKey: 'KEY', myName: 'demo_ui_admin' });
		expect(s.view).toBe('connecting');
		expect(s.stepId).toBe('find');
		c.emit('log', 'Requesting relay (uuid: x)...');
		expect(s.stepId).toBe('route');
		expect(s.statusText).toBe('Setting up the connection through the relay server.');
		// Steps never go back
		c.emit('log', 'Connecting to rendezvous server...');
		expect(s.stepId).toBe('route');
		c.emit('state', 'waiting_password');
		expect(s.stepId).toBe('accept');
	});

	test('a long wait on one step shows what may be wrong', () => {
		const { s, c } = setup();
		c.emit('state', 'waiting_password');
		expect(s.slowHint).toBe('');
		vi.advanceTimersByTime(8000);
		expect(s.slowHint).toMatch(/may be asleep, or someone has to accept/);
	});

	test('password prompt, sign-in, then the picture', () => {
		const { s, c } = setup();
		c.emit('password_required');
		expect(s.view).toBe('password');
		s.password = 'demo';
		c.emit('login_error', 'Wrong Password');
		expect(s.loginError).toBe('That password was not accepted. Check it and try again.');
		expect(s.password).toBe('');
		c.emit('login_success');
		expect(s.view).toBe('connecting');
		expect(s.stepId).toBe('screen');
		c.emit('state', 'streaming');
		c.emit('session_start');
		expect(s.view).toBe('streaming');
		expect(s.everStreamed).toBe(true);
		expect(c.setSessionActive).toHaveBeenLastCalledWith(true);
	});

	test("the organisation's connection password wins over a remembered one and is not stored in the browser", async () => {
		remembered.clear();
		remembered.set('900100012', 'old-pw');
		vi.mocked(remoteApi.getConnectPassword).mockResolvedValueOnce('org-pw');
		const { s, c } = setup();
		c.emit('password_required');
		await vi.waitFor(() => expect(s.password).toBe('org-pw'));
		expect(s.remember).toBe(false);
		c.emit('login_success');
		await vi.waitFor(() => expect(remembered.has('900100012')).toBe(false));
	});

	test("a quick support session's password is sent at once and never stored in the browser", async () => {
		remembered.clear();
		remembered.set('900100012', 'old-pw');
		vi.mocked(remoteApi.getConnectPassword).mockResolvedValueOnce('org-pw');
		vi.mocked(onboardApi.takeDevicePassword).mockResolvedValueOnce('qs-pw');
		const { s, c } = setup();
		c.emit('password_required');
		await vi.waitFor(() => expect(c.authenticate).toHaveBeenCalledWith('qs-pw'));
		expect(onboardApi.takeDevicePassword).toHaveBeenCalledWith('900100012');
		expect(s.remember).toBe(false);
		c.emit('login_success');
		await vi.waitFor(() => expect(remembered.has('900100012')).toBe(false));
	});

	test('a remembered password the device refuses is forgotten', async () => {
		remembered.clear();
		remembered.set('900100012', 'old-pw');
		const { v, s, c } = setup();
		c.emit('password_required');
		await vi.waitFor(() => expect(s.password).toBe('old-pw'));
		expect(s.remember).toBe(true);
		v.authenticate(s);
		c.emit('login_error', 'Wrong Password');
		await vi.waitFor(() => expect(remembered.has('900100012')).toBe(false));
		expect(s.remember).toBe(false);
	});

	test('a typed password the device refuses leaves the remembered one alone', async () => {
		remembered.clear();
		remembered.set('900100012', 'old-pw');
		const { v, s, c } = setup();
		c.emit('password_required');
		await vi.waitFor(() => expect(s.password).toBe('old-pw'));
		s.password = 'typo';
		v.authenticate(s);
		c.emit('login_error', 'Wrong Password');
		await Promise.resolve();
		expect(remembered.get('900100012')).toBe('old-pw');
	});

	test('a failure says what happened and keeps the details', () => {
		const { s, c } = setup();
		c.emit('error', 'Connection refused: Device offline');
		expect(s.view).toBe('failed');
		expect(s.statusText).toBe('demo-build-server is offline.');
		expect(s.tryText).toMatch(/Turn it on or wake it up/);
		expect(s.endInfo?.reason).toBe('Connection refused: Device offline');
	});

	test('a device whose identity or encryption fails ends in a failure, not a warning to dismiss', () => {
		const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
		const a = setup();
		a.c.emit('error', 'Handshake failed: the device signature does not match its key');
		expect(a.s.view).toBe('failed');
		expect(a.s.statusText).toBe(
			'The connection to demo-build-server was stopped because it could not be secured.'
		);
		expect(a.s.tryText).toMatch(/No password was sent/);

		for (const ev of ['signature_warning', 'encryption_warning']) {
			const b = setup();
			b.c.emit('password_required');
			b.c.emit(ev, 'Ed25519 signature verification failed. Connection may be intercepted.');
			expect(b.s.view).toBe('failed');
			expect(b.c.disconnected).toBe(true);
		}
		expect(warn).toHaveBeenCalled();
	});

	test('a drop after the picture is a drop, not "closed before it answered"', () => {
		const { s, c } = setup();
		c.emit('state', 'streaming');
		c.emit('disconnected', 'Relay connection closed (WebSocket 1006)', { closeCode: 1006 });
		expect(s.view).toBe('failed');
		expect(s.outcomeKind).toBe('dropped');
	});

	test('Disconnect ends the session by you; Cancel while connecting says so', () => {
		const a = setup();
		a.c.emit('state', 'streaming');
		a.v.disconnect();
		expect(a.c.disconnected).toBe(true);
		a.c.emit('state', 'disconnected');
		vi.advanceTimersByTime(1);
		expect(a.s.view).toBe('ended');
		expect(a.s.statusText).toBe('You ended the session with demo-build-server.');

		const b = setup();
		b.v.cancel(b.s);
		expect(b.s.view).toBe('ended');
		expect(b.s.statusText).toBe('You cancelled the connection.');
	});

	test('Reconnect starts a new client; the old one cannot change the screen any more', () => {
		const { v, s, c } = setup();
		c.emit('error', 'timeout');
		v.reconnect(s);
		const next = FakeClient.made.at(-1)!;
		expect(next).not.toBe(c);
		expect(c.disconnected).toBe(true);
		expect(s.view).toBe('connecting');
		c.emit('error', 'Connection refused: Device offline');
		expect(s.view).toBe('connecting');
		next.emit('session_start');
		expect(s.view).toBe('streaming');
	});

	test('tabs: one per device, closing the last leaves the viewer', () => {
		const { v, leave } = setup();
		v.open('900100001');
		expect(v.sessions.map((x) => x.deviceId)).toEqual(['900100012', '900100001']);
		expect(v.activeId).toBe('900100001');
		v.open('900100012');
		expect(v.sessions).toHaveLength(2);
		expect(v.activeId).toBe('900100012');
		v.close('900100012');
		expect(v.activeId).toBe('900100001');
		expect(leave).not.toHaveBeenCalled();
		v.close('900100001');
		expect(leave).toHaveBeenCalledOnce();
	});

	test('a guest link opens only its own devices', () => {
		const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
		const { v } = setup(
			data({ capabilities: { transport: 'rd', guest_access: true, guest_peer_ids: ['900100012'] } })
		);
		v.open('900100001');
		expect(v.sessions.map((x) => x.deviceId)).toEqual(['900100012']);
		expect(warn).toHaveBeenCalled();
	});

	test('a watch-only guest link starts the client in view only, and the guest cannot turn it off', () => {
		const { v, s, c } = setup(
			data({
				capabilities: {
					transport: 'rd',
					guest_access: true,
					guest_view_only: true,
					guest_peer_ids: ['900100012']
				}
			})
		);
		expect(c.opts.viewOnly).toBe(true);
		expect(c.setViewOnly).toHaveBeenCalledWith(true);
		expect(s.viewOnly).toBe(true);
		c.emit('state', 'streaming');
		c.emit('session_start');
		v.toggleViewOnly();
		expect(c.setViewOnly).not.toHaveBeenCalledWith(false);
		expect(s.viewOnly).toBe(true);
		v.ctrlAltDel();
		v.setToggle('blockInput', true);
		expect(c.sendCtrlAltDel).not.toHaveBeenCalled();
		expect(c.setBlockInput).not.toHaveBeenCalled();
		expect(v.typeText('rm -rf /')).toBe(false);
	});

	test('an operator starts with input on', () => {
		const { s, c } = setup();
		expect(c.opts.viewOnly).toBe(false);
		expect(c.setViewOnly).not.toHaveBeenCalled();
		expect(s.viewOnly).toBe(false);
	});

	test('Disable clipboard stays on for the new client after a reconnect', () => {
		const { v, s, c } = setup();
		v.setToggle('disableClipboard', true);
		expect(c.setDisableClipboard).toHaveBeenCalledWith(true);
		v.reconnect(s);
		const next = FakeClient.made.at(-1)!;
		expect(next).not.toBe(c);
		expect(next.setDisableClipboard).toHaveBeenCalledWith(true);
	});

	test('chat goes to the device and replies show in the session', () => {
		const { v, s, c } = setup();
		expect(v.sendChat(s, '  hello ')).toBe(true);
		expect(c.sent).toEqual(['hello']);
		c.emit('chat', 'hi back');
		flushSync();
		expect(s.chat.map((m) => [m.type, m.text])).toEqual([
			['sent', 'hello'],
			['received', 'hi back']
		]);
		expect(v.sendChat(s, '   ')).toBe(false);
	});

	test('settings go to the client and stay the default for the account', () => {
		const { v, s, c } = setup();
		v.setPref('quality', 'Low');
		expect(c.setQualityPreset).toHaveBeenCalledWith('speed');
		expect(s.prefs.quality).toBe('Low');
		v.setToggle('blockInput', true);
		expect(c.setBlockInput).toHaveBeenCalledWith(true);
		expect(s.toggles.blockInput).toBe(true);
	});
});

describe('remote target or device', () => {
	test('a device whose id starts with rt_ is a device when the server says so', async () => {
		vi.mocked(devicesApi.getDevice).mockClear();
		const v = new Viewer({
			data: data({ deviceId: 'rt_device1', capabilities: { transport: 'rd' } }),
			onleave: vi.fn()
		});
		v.open('rt_device1');
		await vi.waitFor(() => expect(devicesApi.getDevice).toHaveBeenCalledWith('rt_device1'));
		expect(v.remoteTarget).toBe(false);
	});

	test('a remote target the server names has no device details to load', async () => {
		vi.mocked(devicesApi.getDevice).mockClear();
		const v = new Viewer({
			data: data({ deviceId: 'target9', capabilities: { transport: 'rdp', remote_target: true } }),
			onleave: vi.fn()
		});
		v.open('target9');
		await Promise.resolve();
		expect(devicesApi.getDevice).not.toHaveBeenCalled();
		expect(v.remoteTarget).toBe(true);
	});
});
