/**
 * What the device turns off for a session. The server hands the device the
 * account's client features (L41); a RustDesk device then tells the viewer
 * per permission (PermissionInfo: Keyboard 0, Clipboard 2, File 4,
 * Restart 5), and the viewer follows: no input and view only without the
 * keyboard, no clipboard sync without the clipboard, no file sheet without
 * file transfer, no restart without restart.
 */
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

type Listener = (...args: unknown[]) => void;

class FakeClient {
	static made: FakeClient[] = [];
	listeners = new Map<string, Listener[]>();
	viewOnly = false;
	renderer = { resize: vi.fn() };
	fileTransfer = {};
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
	disconnect = vi.fn();
	setSessionActive = vi.fn();
	setViewOnly = vi.fn((on: boolean) => {
		this.viewOnly = on;
	});
	setDisableClipboard = vi.fn();
	sendCtrlAltDel = vi.fn();
	sendClipboard = vi.fn();
	sendRestartRemoteDevice = vi.fn();
	setAudioMuted = vi.fn();
	setQualityPreset = vi.fn();
	setScaleMode = vi.fn();
	setCodec = vi.fn();
	getMonitors = () => [];
}

const sheet = vi.hoisted(() => ({ open: false }));
vi.mock('../../../src/lib/remote/rdclient.ts', () => ({
	createClient: (canvas: HTMLCanvasElement, opts: Record<string, unknown>) => new FakeClient(canvas, opts),
	fileSheet: () => ({
		open: () => (sheet.open = true),
		close: () => (sheet.open = false),
		isOpen: () => sheet.open,
		_session: null,
		_el: null
	}),
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
const confirm = vi.hoisted(() => vi.fn(async () => true));
vi.mock('../../../src/lib/ui/dialogs.svelte.ts', () => ({ confirm }));

const { Viewer } = await import('../../../src/lib/remote/viewer.svelte.ts');
type ViewerData = ConstructorParameters<typeof Viewer>[0]['data'];

function streaming() {
	const d = {
		success: true,
		deviceId: '900100012',
		device: { name: 'demo-build-server', platform: 'Windows' },
		capabilities: { transport: 'rd' },
		serverPubKey: 'KEY',
		connection: null,
		guestToken: '',
		mockRemote: null,
		user: { id: 1, name: 'demo_ui_admin' },
		csrfToken: ''
	} as ViewerData;
	const v = new Viewer({ data: d, onleave: () => {} });
	v.open(d.deviceId, d.device.name, d.device.platform);
	const s = v.sessions[0]!;
	v.attach(s, document.createElement('canvas'));
	const c = FakeClient.made.at(-1)!;
	c.emit('session_start');
	return { v, s, c };
}

const KEYBOARD = 0;
const CLIPBOARD = 2;
const FILE = 4;
const RESTART = 5;

beforeEach(() => {
	FakeClient.made = [];
	sheet.open = false;
	confirm.mockClear();
});
afterEach(() => vi.restoreAllMocks());

describe('permissions the device turned off', () => {
	test('no keyboard: view only, nothing goes to the device, and the operator cannot turn it off', () => {
		const { v, s, c } = streaming();
		c.emit('permission', { permission: KEYBOARD, enabled: false });
		expect(c.setViewOnly).toHaveBeenLastCalledWith(true);
		expect(s.viewOnly).toBe(true);
		v.toggleViewOnly();
		expect(s.viewOnly).toBe(true);
		v.ctrlAltDel();
		expect(c.sendCtrlAltDel).not.toHaveBeenCalled();
		// The keyboard comes back: the operator's own choice returns
		c.emit('permission', { permission: KEYBOARD, enabled: true });
		expect(c.setViewOnly).toHaveBeenLastCalledWith(false);
		expect(s.viewOnly).toBe(false);
		v.ctrlAltDel();
		expect(c.sendCtrlAltDel).toHaveBeenCalledOnce();
	});

	test('no clipboard: the client keeps the clipboard off, also when the toggle is switched back', async () => {
		const { v, c } = streaming();
		c.emit('permission', { permission: CLIPBOARD, enabled: false });
		expect(c.setDisableClipboard).toHaveBeenLastCalledWith(true);
		v.setToggle('disableClipboard', false);
		expect(c.setDisableClipboard).toHaveBeenLastCalledWith(true);
		vi.stubGlobal('navigator', { ...navigator, clipboard: { readText: async () => 'text' } });
		await v.pasteClipboard();
		expect(c.sendClipboard).not.toHaveBeenCalled();
		c.emit('permission', { permission: CLIPBOARD, enabled: true });
		expect(c.setDisableClipboard).toHaveBeenLastCalledWith(false);
		vi.unstubAllGlobals();
	});

	test('no file transfer: an open file sheet closes and does not open again', () => {
		const { v, c } = streaming();
		v.openSheet('files');
		expect(sheet.open).toBe(true);
		c.emit('permission', { permission: FILE, enabled: false });
		expect(sheet.open).toBe(false);
		expect(v.sheet).toBeNull();
		v.openSheet('files');
		expect(sheet.open).toBe(false);
		c.emit('permission', { permission: FILE, enabled: true });
		v.openSheet('files');
		expect(sheet.open).toBe(true);
	});

	test('no restart: the restart command neither asks nor goes out', async () => {
		const { v, s, c } = streaming();
		c.emit('permission', { permission: RESTART, enabled: false });
		expect(s.allowed.restart).toBe(false);
		await v.restartRemote();
		expect(confirm).not.toHaveBeenCalled();
		expect(c.sendRestartRemoteDevice).not.toHaveBeenCalled();
		c.emit('permission', { permission: RESTART, enabled: true });
		await v.restartRemote();
		expect(c.sendRestartRemoteDevice).toHaveBeenCalledOnce();
	});
});
