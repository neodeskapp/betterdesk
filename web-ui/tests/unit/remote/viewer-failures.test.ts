/**
 * What the viewer says when a connection fails or waits (L551, L556), on a
 * fake RustDesk client: the reason comes with the error, the server's
 * reach answer names a block that "offline" hid, a quick support session
 * waits for the person instead of asking for a password, and no rendered
 * failure carries a raw message.
 */
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { ReasonError, isRawText } from '../../../src/lib/remote/failures.ts';

type Listener = (...args: unknown[]) => void;

class FakeClient {
	static made: FakeClient[] = [];
	listeners = new Map<string, Listener[]>();
	viewOnly = false;
	renderer = { resize: vi.fn() };
	encrypted = true;
	relayUUID = 'relay-uuid-1';
	keys: string[] = [];
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
	askWithoutPassword = vi.fn(async () => {});
	disconnect = vi.fn();
	authenticate = vi.fn();
	answers: Uint8Array[] = [];
	authenticateWith = vi.fn(
		async (answer: (salt: string, challenge: string) => Promise<Uint8Array | null>) => {
			const hash = await answer('salt-1', 'challenge-1');
			if (!hash) return false;
			this.answers.push(hash);
			return true;
		}
	);
	sendCtrlAltDel = vi.fn(() => void this.keys.push('sas'));
	sendText = vi.fn((t: string) => (this.keys.push('text:' + t), true));
	sendKeyPress = vi.fn((k: string) => (this.keys.push('key:' + k), true));
	setAudioMuted = vi.fn();
	setSessionActive = vi.fn();
	setQualityPreset = vi.fn();
	setScaleMode = vi.fn();
	setCodec = vi.fn();
	setViewOnly = vi.fn();
	setDisableClipboard = vi.fn();
	getMonitors = () => [];
}

vi.mock('../../../src/lib/remote/rdclient.ts', () => ({
	createClient: (canvas: HTMLCanvasElement, opts: Record<string, unknown>) => new FakeClient(canvas, opts),
	fileSheet: () => null,
	touchInput: () => null,
	videoDecodingSupported: () => true
}));
const devicesApi = vi.hoisted(() => ({
	getDevice: vi.fn(async () => ({})),
	getDeviceReach: vi.fn(async () => ({ reason: 'device_offline', last_online: '', can_unblock: false })),
	unblockDevice: vi.fn(async () => {})
}));
vi.mock('../../../src/lib/api/devices.ts', () => devicesApi);
vi.mock('../../../src/lib/api/remote.ts', () => ({
	acceptCertificate: vi.fn(),
	getBillingCheck: vi.fn(async () => null),
	getPendingBillingSession: vi.fn(async () => null),
	submitWorkReport: vi.fn()
}));
const onboardApi = vi.hoisted(() => ({
	takeDevicePassword: vi.fn(async () => ({
		password: '',
		session: null as null | { name: string; status: string }
	})),
	supportCaller: vi.fn(async (): Promise<null | { state: string; appHttps: boolean | null }> => ({
		state: 'vouched',
		appHttps: true
	}))
}));
vi.mock('../../../src/lib/api/onboard.ts', () => onboardApi);
const remembered = vi.hoisted(() => new Map<string, string>());
vi.mock('../../../src/lib/remote/secure-store.ts', () => ({
	secureStore: () => ({
		loadPeerPassword: async (id: string) => remembered.get(id) ?? '',
		savePeerPassword: async (id: string, pw: string) => void remembered.set(id, pw),
		clearPeerPassword: async (id: string) => void remembered.delete(id)
	})
}));
const confirm = vi.hoisted(() => vi.fn(async () => true));
vi.mock('../../../src/lib/ui/dialogs.svelte.ts', () => ({ confirm }));
const answerApi = vi.hoisted(() => ({
	answerConnectPassword: vi.fn(async (): Promise<Uint8Array | null> => null)
}));
vi.mock('../../../src/lib/api/connectAnswer.ts', () => answerApi);
const signinApi = vi.hoisted(() => ({
	getSignin: vi.fn(async () => ({
		available: true,
		set: true,
		username: 'LAB\\admin',
		form: 'password' as 'password' | 'name_password',
		auto: false,
		updated_by: 'boss',
		updated_at: '',
		may_use: true
	})),
	useSignin: vi.fn(async () => ({
		username: 'LAB\\admin',
		password: 'made-up-pass',
		form: 'password' as const
	})),
	refusalCode: vi.fn((err: unknown) => (err as { code?: string }).code ?? '')
}));
vi.mock('../../../src/lib/api/savedSignin.ts', () => signinApi);

const { Viewer } = await import('../../../src/lib/remote/viewer.svelte.ts');
type ViewerData = ConstructorParameters<typeof Viewer>[0]['data'];

function setup() {
	const d = {
		success: true,
		deviceId: '828416401',
		device: { name: 'desktop-3mu2dcg', platform: 'Windows' },
		capabilities: { transport: 'rd' },
		serverPubKey: 'KEY',
		connection: null,
		guestToken: '',
		mockRemote: null,
		user: { id: 1, name: 'admin' },
		csrfToken: ''
	} as unknown as ViewerData;
	const v = new Viewer({ data: d, onleave: vi.fn() });
	v.open(d.deviceId, d.device.name, d.device.platform);
	const s = v.sessions[0]!;
	v.attach(s, document.createElement('canvas'));
	return { v, s, c: FakeClient.made.at(-1)! };
}

function shown(s: { statusText: string; whyText: string; tryText: string }) {
	for (const text of [s.statusText, s.whyText, s.tryText]) expect(isRawText(text), text).toBe(false);
}

beforeEach(() => {
	FakeClient.made = [];
	remembered.clear();
	devicesApi.getDeviceReach.mockClear();
	devicesApi.unblockDevice.mockClear();
	onboardApi.takeDevicePassword.mockResolvedValue({ password: '', session: null });
	onboardApi.supportCaller.mockReset();
	onboardApi.supportCaller.mockResolvedValue({ state: 'vouched', appHttps: true });
});
afterEach(() => vi.useRealTimers());

describe('a quick support session never asks the admin for a password (L551)', () => {
	test('without the session password the viewer waits for the person, who sees Allow', async () => {
		onboardApi.takeDevicePassword.mockResolvedValue({
			password: '',
			session: { name: 'Ana', status: 'allowed' }
		});
		const { s, c } = setup();
		c.emit('password_required');
		await vi.waitFor(() => expect(c.askWithoutPassword).toHaveBeenCalled());
		expect(s.view).toBe('connecting');
		expect(s.stepId).toBe('accept');
		expect(s.statusText).toBe('Waiting for Ana to accept on their screen.');
		// The consent window's buttons are Allow and Decline
		expect(s.waitHint).toMatch(/with Allow and Decline/);
		expect(c.authenticate).not.toHaveBeenCalled();
	});

	test('the person declines: the viewer says so, not that the connection dropped', async () => {
		onboardApi.takeDevicePassword.mockResolvedValue({
			password: '',
			session: { name: 'Ana', status: 'allowed' }
		});
		const { s, c } = setup();
		c.emit('password_required');
		await vi.waitFor(() => expect(c.askWithoutPassword).toHaveBeenCalled());
		c.emit('disconnected', 'Remote: ', { source: 'peer', peerReason: '' });
		await vi.waitFor(() => expect(s.view).toBe('failed'));
		expect(s.statusText).toBe('Ana declined the connection.');
		shown(s);
	});

	test('nobody accepts in time', async () => {
		vi.useFakeTimers();
		onboardApi.takeDevicePassword.mockResolvedValue({
			password: '',
			session: { name: 'Ana', status: 'allowed' }
		});
		const { s, c } = setup();
		c.emit('password_required');
		await vi.waitFor(() => expect(c.askWithoutPassword).toHaveBeenCalled());
		await vi.advanceTimersByTimeAsync(3 * 60 * 1000 + 10);
		expect(s.view).toBe('failed');
		expect(s.statusText).toBe('Ana did not answer.');
		expect(c.disconnect).toHaveBeenCalled();
	});

	test('a computer that accepts by click says who it waits for', async () => {
		const { s, c } = setup();
		c.emit('approval_wait');
		expect(s.view).toBe('connecting');
		expect(s.statusText).toBe('Waiting for someone at desktop-3mu2dcg to accept on their screen.');
	});
});

describe('without the server vouching, the helper hears at once why there is no Allow (A273)', () => {
	async function waiting(answer: { state: string; appHttps: boolean | null } | null) {
		vi.useFakeTimers();
		onboardApi.takeDevicePassword.mockResolvedValue({
			password: '',
			session: { name: 'Ana', status: 'allowed' }
		});
		onboardApi.supportCaller.mockResolvedValue(answer as never);
		const t = setup();
		t.c.emit('password_required');
		await vi.waitFor(() => expect(t.c.askWithoutPassword).toHaveBeenCalled());
		return t;
	}

	test('the person has not clicked Allow in the quick support window yet', async () => {
		const { s, c } = await waiting({ state: 'not_allowed_yet', appHttps: true });
		await vi.advanceTimersByTimeAsync(2000);
		expect(onboardApi.supportCaller).toHaveBeenCalledWith('828416401', 'relay-uuid-1');
		expect(s.view).toBe('failed');
		expect(s.statusText).toBe('Ana has not allowed quick support yet.');
		expect(s.tryText).toBe('Ask them to click Allow in the quick support window, then connect again.');
		expect(s.actions).toContain('reconnect');
		expect(c.disconnect).toHaveBeenCalled();
		shown(s);
	});

	test('the console is not on HTTPS: the computer asked and was refused', async () => {
		const { s, c } = await waiting({ state: 'not_https', appHttps: false });
		await vi.advanceTimersByTimeAsync(2000);
		expect(s.view).toBe('failed');
		expect(s.statusText).toBe("desktop-3mu2dcg can't let you in by click.");
		expect(s.whyText).toBe(
			'This console is not on HTTPS, so quick support takes only the one-time password.'
		);
		expect(s.actions).not.toContain('reconnect');
		expect(c.disconnect).toHaveBeenCalled();
		shown(s);
	});

	test('the console is not on HTTPS: the computer did not even ask', async () => {
		const { s } = await waiting({ state: 'not_asked', appHttps: false });
		await vi.advanceTimersByTimeAsync(2000);
		expect(s.view).toBe('failed');
		expect(s.whyText).toBe(
			'This console is not on HTTPS, so quick support takes only the one-time password.'
		);
	});

	test('a computer that never asks has a NeoDesk too old to accept by click', async () => {
		const { s, c } = await waiting({ state: 'not_asked', appHttps: true });
		await vi.advanceTimersByTimeAsync(5000);
		// It may still ask: the viewer waits
		expect(s.view).toBe('connecting');
		expect(c.disconnect).not.toHaveBeenCalled();
		await vi.advanceTimersByTimeAsync(5000);
		expect(onboardApi.supportCaller).toHaveBeenCalledTimes(3);
		expect(s.view).toBe('failed');
		expect(s.whyText).toBe('NeoDesk on desktop-3mu2dcg is too old to accept by click.');
		expect(s.tryText).toMatch(/Or ask Ana to update NeoDesk\.$/);
		expect(c.disconnect).toHaveBeenCalled();
		shown(s);
	});

	test('vouched, or a console that cannot say: the viewer keeps waiting for the person', async () => {
		for (const answer of [{ state: 'vouched', appHttps: true }, null]) {
			onboardApi.supportCaller.mockClear();
			const { s, c } = await waiting(answer);
			await vi.advanceTimersByTimeAsync(20000);
			expect(onboardApi.supportCaller).toHaveBeenCalledTimes(1);
			expect(s.view).toBe('connecting');
			expect(s.statusText).toBe('Waiting for Ana to accept on their screen.');
			expect(c.disconnect).not.toHaveBeenCalled();
			vi.useRealTimers();
		}
	});
});

describe('a failure says what happened, why and what to do (L556)', () => {
	test('"offline" from the signal server is checked with the server: a revoked computer is named, with Unblock', async () => {
		devicesApi.getDeviceReach.mockResolvedValueOnce({
			reason: 'device_revoked',
			last_online: '',
			can_unblock: true
		});
		const { v, s, c } = setup();
		c.emit('error', 'Connection refused: Device offline', { reason: 'device_offline' });
		await vi.waitFor(() => expect(s.statusText).toBe('desktop-3mu2dcg was revoked on this server.'));
		expect(devicesApi.getDeviceReach).toHaveBeenCalledWith('828416401');
		expect(s.actions).toContain('unblock');
		expect(s.whyText).toMatch(/revoked this computer in the console/);
		shown(s);
		await v.unblock(s);
		expect(devicesApi.unblockDevice).toHaveBeenCalledWith('828416401');
	});

	test('a computer that really has not registered stays offline', async () => {
		const { s, c } = setup();
		c.emit('error', 'Connection refused: Device offline', { reason: 'device_offline' });
		await vi.waitFor(() => expect(devicesApi.getDeviceReach).toHaveBeenCalled());
		expect(s.statusText).toBe('desktop-3mu2dcg is offline.');
		expect(s.actions).not.toContain('unblock');
	});

	test('the server is asked once per failure, also when it agrees and the account may unblock', async () => {
		devicesApi.getDeviceReach.mockResolvedValue({
			reason: 'device_offline',
			last_online: '',
			can_unblock: true
		});
		const { s, c } = setup();
		c.emit('error', 'Connection refused: Device offline', { reason: 'device_offline' });
		await vi.waitFor(() => expect(devicesApi.getDeviceReach).toHaveBeenCalled());
		await new Promise((r) => setTimeout(r, 20));
		expect(devicesApi.getDeviceReach).toHaveBeenCalledTimes(1);
		expect(s.statusText).toBe('desktop-3mu2dcg is offline.');
		devicesApi.getDeviceReach.mockResolvedValue({
			reason: 'device_offline',
			last_online: '',
			can_unblock: false
		});
	});

	test('an error without a reason says the cause is unknown and keeps the message for Details', async () => {
		const { v, s, c } = setup();
		c.emit('error', 'Connection refused: something new', {});
		await vi.waitFor(() => expect(s.view).toBe('failed'));
		expect(s.statusText).toBe('Could not connect to desktop-3mu2dcg.');
		expect(s.whyText).toMatch(/cause is not known/);
		shown(s);
		expect(v.details(s).some(([, value]) => value === 'Connection refused: something new')).toBe(true);
	});

	test('a reason the client gives wins over the message', async () => {
		const { s, c } = setup();
		c.emit('error', new ReasonError('no_answer', 'Rendezvous response timeout (30s)'), {
			reason: 'no_answer'
		});
		await vi.waitFor(() => expect(s.view).toBe('failed'));
		expect(s.statusText).toBe('desktop-3mu2dcg did not answer.');
		shown(s);
	});

	test('a login refusal of the computer is a failure with its reason, not a password error', async () => {
		const { s, c } = setup();
		c.emit('error', 'Login refused: Connection not allowed', { reason: 'device_not_allowed' });
		await vi.waitFor(() => expect(s.view).toBe('failed'));
		expect(s.statusText).toBe('desktop-3mu2dcg does not take this connection.');
		shown(s);
	});
});
