/**
 * A device's saved sign-in in the viewer (BUGS L469), on a fake RustDesk
 * client: offered as a button with the switch off, signed in by itself
 * with the switch on only while nobody is signed in, once per connection
 * and once per device by itself, typing what the server handed out
 * without keeping it.
 */
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

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
	plugins: { id: string; content: Uint8Array }[] = [];
	sendPluginRequest = vi.fn((id: string, content: Uint8Array) => (this.plugins.push({ id, content }), true));
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
	useSealedSignin: vi.fn(async () => ({
		packet: new Uint8Array(64).fill(3),
		nonce: 'nonce-1',
		notAfter: 1800000060
	})),
	refusalCode: vi.fn((err: unknown) => (err as { code?: string }).code ?? '')
}));
vi.mock('../../../src/lib/api/savedSignin.ts', () => signinApi);

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

/** The connection signs in and the picture starts; the device says who is signed in */
function stream(
	c: FakeClient,
	peer: { username?: string; sasEnabled?: boolean; platform?: string; platformAdditions?: string } = {}
) {
	c.emit('login_success');
	c.emit('peer_info', { platform: 'Windows', username: '', sasEnabled: true, ...peer });
	c.emit('state', 'streaming');
	c.emit('session_start');
}

beforeEach(() => {
	FakeClient.made = [];
	remembered.clear();
	confirm.mockClear();
	for (const fn of Object.values(signinApi)) fn.mockClear();
	answerApi.answerConnectPassword.mockClear();
});
afterEach(() => vi.useRealTimers());

describe('saved sign-in (L469)', () => {
	test('the switch is off: a Sign in button, nothing typed by itself', async () => {
		const { s, c } = setup();
		stream(c);
		await vi.waitFor(() => expect(s.signinOffer.kind).toBe('button'));
		await new Promise((r) => setTimeout(r, 900));
		expect(signinApi.useSignin).not.toHaveBeenCalled();
		expect(c.keys).toEqual([]);
	});

	test('with the switch on and nobody signed in it signs in once, by itself, with what the server handed out', async () => {
		signinApi.getSignin.mockResolvedValueOnce({ ...(await signinApi.getSignin()), auto: true });
		const { s, c } = setup();
		stream(c);
		await vi.waitFor(() => expect(c.keys.at(-1)).toBe('key:Return'), { timeout: 4000 });
		expect(signinApi.useSignin).toHaveBeenCalledWith('900100012', 'relay-uuid-1');
		expect(c.keys).toEqual(['sas', 'text:made-up-pass', 'key:Return']);
		expect(s.signinNote).toMatch(/were typed/);
		// Nothing of the session holds the password
		for (const text of [s.password, s.signinNote, s.loginError, s.rememberedPassword, s.submittedPassword]) {
			expect(text).not.toContain('made-up-pass');
		}
		// One attempt per connection: the button does not come back
		await new Promise((r) => setTimeout(r, 10));
		expect(s.signinOffer.kind).toBe('none');
	});

	test('with the switch on but someone signed in it asks before it types', async () => {
		signinApi.getSignin.mockResolvedValueOnce({ ...(await signinApi.getSignin()), auto: true });
		const { v, s, c } = setup();
		stream(c, { username: 'alice' });
		await vi.waitFor(() =>
			expect(s.signinOffer).toEqual({
				kind: 'button',
				signedIn: true,
				noLockSignal: true,
				confirm: true,
				path: 'typed'
			})
		);
		expect(signinApi.useSignin).not.toHaveBeenCalled();
		confirm.mockResolvedValueOnce(false);
		await v.signIn(s);
		expect(confirm).toHaveBeenCalledTimes(1);
		expect(signinApi.useSignin).not.toHaveBeenCalled();
		expect(c.keys).toEqual([]);
	});

	test('one attempt per connection, also after a refusal', async () => {
		signinApi.useSignin.mockRejectedValueOnce(Object.assign(new Error(''), { code: 'session_not_open' }));
		const { v, s, c } = setup();
		stream(c);
		await vi.waitFor(() => expect(s.signinOffer.kind).toBe('button'));
		await v.signIn(s);
		expect(s.signinNote).toMatch(/does not see this session as open/);
		await v.signIn(s);
		expect(signinApi.useSignin).toHaveBeenCalledTimes(1);
		expect(c.keys).toEqual([]);
	});

	test('nothing over a session that is not encrypted end to end with the device', async () => {
		signinApi.getSignin.mockResolvedValueOnce({ ...(await signinApi.getSignin()), auto: true });
		const { v, s, c } = setup();
		c.encrypted = false;
		stream(c);
		await new Promise((r) => setTimeout(r, 900));
		expect(s.signinOffer.kind).toBe('none');
		await v.signIn(s);
		expect(signinApi.useSignin).not.toHaveBeenCalled();
		expect(c.keys).toEqual([]);
	});

	test('not offered on a computer that is not Windows or runs NeoDesk without its service', async () => {
		for (const peer of [{ platform: 'Linux' }, { sasEnabled: false }]) {
			const { s, c } = setup();
			stream(c, peer);
			await new Promise((r) => setTimeout(r, 10));
			expect(['none', 'unsupported']).toContain(s.signinOffer.kind);
			expect(c.keys).toEqual([]);
		}
	});

	test('with the reported lock screen and the switch on it signs in by itself without asking, also when someone is signed in', async () => {
		signinApi.getSignin.mockResolvedValueOnce({ ...(await signinApi.getSignin()), auto: true });
		const { s, c } = setup();
		stream(c, { username: 'alice', platformAdditions: '{"logon_state":"locked"}' });
		await vi.waitFor(() => expect(c.keys.at(-1)).toBe('key:Return'), { timeout: 4000 });
		expect(confirm).not.toHaveBeenCalled();
		expect(s.signinNote).toMatch(/were typed/);
	});

	test('nothing on a reported open desktop, and the button without a question once it reports the lock screen', async () => {
		signinApi.getSignin.mockResolvedValueOnce({ ...(await signinApi.getSignin()), auto: true });
		const { v, s, c } = setup();
		stream(c, { username: 'alice', platformAdditions: '{"logon_state":"desktop"}' });
		await new Promise((r) => setTimeout(r, 900));
		expect(s.signinOffer.kind).toBe('none');
		expect(c.keys).toEqual([]);
		c.emit('plugin_request', 'nd.logon_state', new TextEncoder().encode('{"logon_state":"locked"}'));
		await vi.waitFor(() => expect(s.signinOffer).toMatchObject({ kind: 'button', confirm: false }));
		// A lock screen reported later gets the button, not the sign-in by itself
		await new Promise((r) => setTimeout(r, 900));
		expect(signinApi.useSignin).not.toHaveBeenCalled();
		await v.signIn(s);
		expect(confirm).not.toHaveBeenCalled();
		expect(c.keys).toEqual(['sas', 'text:made-up-pass', 'key:Return']);
	});

	test('nothing on the sign-in screen of an unlocked session, and the button goes when that screen comes up', async () => {
		signinApi.getSignin.mockResolvedValueOnce({ ...(await signinApi.getSignin()), auto: true });
		const first = setup();
		stream(first.c, { username: 'alice', platformAdditions: '{"logon_state":"logon_ui","nd_signin":1}' });
		await new Promise((r) => setTimeout(r, 900));
		expect(first.s.signinOffer.kind).toBe('none');
		await first.v.signIn(first.s);
		expect(signinApi.useSealedSignin).not.toHaveBeenCalled();
		expect(first.c.plugins).toEqual([]);

		const { v, s, c } = setup();
		stream(c, { username: 'alice', platformAdditions: '{"logon_state":"locked","nd_signin":1}' });
		await vi.waitFor(() => expect(s.signinOffer).toMatchObject({ kind: 'button', path: 'sealed' }));
		c.emit('plugin_request', 'nd.logon_state', new TextEncoder().encode('{"logon_state":"logon_ui"}'));
		await vi.waitFor(() => expect(s.signinOffer.kind).toBe('none'));
		await v.signIn(s);
		expect(signinApi.useSealedSignin).not.toHaveBeenCalled();
		expect(signinApi.useSignin).not.toHaveBeenCalled();
		expect(c.plugins).toEqual([]);
		expect(c.keys).toEqual([]);
	});

	test('a computer on the sealed way, in a session whose client lacks it: no readable details asked, the bar says why', async () => {
		signinApi.getSignin.mockResolvedValueOnce({
			...(await signinApi.getSignin()),
			auto: true,
			sealed_signin: true
		} as Awaited<ReturnType<typeof signinApi.getSignin>>);
		const { v, s, c } = setup();
		stream(c);
		await vi.waitFor(() => expect(s.signinOffer).toEqual({ kind: 'unsupported', why: 'sealed' }));
		await new Promise((r) => setTimeout(r, 900));
		await v.signIn(s);
		expect(signinApi.useSignin).not.toHaveBeenCalled();
		expect(signinApi.useSealedSignin).not.toHaveBeenCalled();
		expect(c.keys).toEqual([]);
	});

	test('a computer whose NeoDesk opens sealed sign-ins gets the sealed packet; the page never asks for the password', async () => {
		signinApi.getSignin.mockResolvedValueOnce({ ...(await signinApi.getSignin()), auto: true });
		const { s, c } = setup();
		stream(c, { platformAdditions: '{"logon_state":"prelogin","nd_signin":1}' });
		await vi.waitFor(() => expect(c.plugins).toHaveLength(1), { timeout: 4000 });
		expect(signinApi.useSealedSignin).toHaveBeenCalledWith('900100012', 'relay-uuid-1');
		expect(signinApi.useSignin).not.toHaveBeenCalled();
		expect(c.plugins[0]!.id).toBe('nd.signin');
		expect(c.plugins[0]!.content).toEqual(new Uint8Array(64).fill(3));
		expect(c.keys).toEqual([]);
		expect(s.signinNote).toMatch(/Sent to NeoDesk/);
		// An answer to another request is not this one's
		c.emit(
			'plugin_request',
			'nd.signin_result',
			new TextEncoder().encode('{"result":"typed","nonce":"other"}')
		);
		expect(s.signinNote).toMatch(/Sent to NeoDesk/);
		c.emit(
			'plugin_request',
			'nd.signin_result',
			new TextEncoder().encode('{"result":"not_at_lock_screen","nonce":"nonce-1"}')
		);
		expect(s.signinNote).toMatch(/typed nothing/);
		expect(s.signinBusy).toBe(false);
		expect(s.signinOffer.kind).toBe('none');
	});

	test('the computer that never answers a sealed sign-in is reported, and nothing is tried again', async () => {
		const { v, s, c } = setup();
		stream(c, { platformAdditions: '{"logon_state":"locked","nd_signin":1}' });
		await vi.waitFor(() => expect(s.signinOffer).toMatchObject({ kind: 'button', path: 'sealed' }));
		vi.useFakeTimers();
		await v.signIn(s);
		expect(c.plugins).toHaveLength(1);
		vi.advanceTimersByTime(16000);
		expect(s.signinNote).toMatch(/didn't answer/);
		await v.signIn(s);
		expect(signinApi.useSealedSignin).toHaveBeenCalledTimes(1);
	});
});
