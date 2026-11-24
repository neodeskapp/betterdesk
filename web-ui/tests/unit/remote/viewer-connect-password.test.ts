/**
 * The viewer with the organisation's stored device password (BUGS L454),
 * on a fake RustDesk client: the page signs in with the server's answer to
 * the device's question and never holds the device password.
 */
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

type Listener = (...args: unknown[]) => void;

class FakeClient {
	static made: FakeClient[] = [];
	listeners = new Map<string, Listener[]>();
	viewOnly = false;
	renderer = { resize: vi.fn() };
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
vi.mock('../../../src/lib/api/devices.ts', () => ({ getDevice: vi.fn(async () => ({})) }));
vi.mock('../../../src/lib/api/remote.ts', () => ({
	acceptCertificate: vi.fn(),
	getBillingCheck: vi.fn(async () => null),
	getPendingBillingSession: vi.fn(async () => null),
	submitWorkReport: vi.fn()
}));
vi.mock('../../../src/lib/api/onboard.ts', () => ({ takeDevicePassword: vi.fn(async () => '') }));
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

const { Viewer } = await import('../../../src/lib/remote/viewer.svelte.ts');
type ViewerData = ConstructorParameters<typeof Viewer>[0]['data'];

function setup() {
	const d = {
		success: true,
		deviceId: '900100012',
		device: { name: 'lab-pc', platform: 'Windows' },
		capabilities: { transport: 'rd' },
		serverPubKey: 'KEY',
		connection: null,
		guestToken: '',
		mockRemote: null,
		user: { id: 1, name: 'boss' },
		csrfToken: ''
	} as unknown as ViewerData;
	const v = new Viewer({ data: d, onleave: vi.fn() });
	v.open(d.deviceId, d.device.name, d.device.platform);
	const s = v.sessions[0]!;
	v.attach(s, document.createElement('canvas'));
	return { v, s, c: FakeClient.made.at(-1)! };
}

beforeEach(() => {
	FakeClient.made = [];
	remembered.clear();
	confirm.mockClear();
	answerApi.answerConnectPassword.mockClear();
});
afterEach(() => vi.useRealTimers());

describe("the organisation's stored device password (L454)", () => {
	test('signs in with the server answer and never puts the password into the page', async () => {
		remembered.set('900100012', 'old-pw');
		const answer = new Uint8Array(32).fill(7);
		answerApi.answerConnectPassword.mockResolvedValueOnce(answer);
		const { s, c } = setup();
		c.emit('password_required');
		await vi.waitFor(() => expect(c.answers).toHaveLength(1));
		expect(answerApi.answerConnectPassword).toHaveBeenCalledWith('900100012', 'salt-1', 'challenge-1');
		expect(c.answers[0]).toBe(answer);
		expect(c.authenticate).not.toHaveBeenCalled();
		expect(s.password).toBe('');
		expect(s.remember).toBe(false);
		c.emit('login_success');
		await vi.waitFor(() => expect(remembered.has('900100012')).toBe(false));
	});

	test('a refused stored password asks for the password and is not tried again on this connection', async () => {
		answerApi.answerConnectPassword.mockResolvedValue(new Uint8Array(32).fill(1));
		const { s, c } = setup();
		c.emit('password_required');
		await vi.waitFor(() => expect(c.authenticateWith).toHaveBeenCalledTimes(1));
		c.emit('login_error', 'Wrong Password');
		expect(s.view).toBe('password');
		expect(s.loginError).toMatch(/saved for your organisation was not accepted/);
		c.emit('password_required');
		await Promise.resolve();
		await Promise.resolve();
		expect(c.authenticateWith).toHaveBeenCalledTimes(1);
		answerApi.answerConnectPassword.mockResolvedValue(null);
	});

	test('without a stored password the remembered one is filled in as before', async () => {
		remembered.set('900100012', 'old-pw');
		const { s, c } = setup();
		c.emit('password_required');
		await vi.waitFor(() => expect(s.password).toBe('old-pw'));
		expect(s.remember).toBe(true);
	});
});
