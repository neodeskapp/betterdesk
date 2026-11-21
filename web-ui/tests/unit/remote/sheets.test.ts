/**
 * Session info beside the remote screen: "End-to-end encrypted" shows only
 * when it is true, a RustDesk session whose client confirmed the device
 * encrypts. CDAP, mesh, RDP and VNC streams pass through the server.
 */
import { render, screen } from '@testing-library/svelte';
import { describe, expect, test, vi } from 'vitest';

type Listener = (...args: unknown[]) => void;

class FakeClient {
	listeners = new Map<string, Listener[]>();
	renderer = { resize: vi.fn() };
	_state = 'idle';
	encrypted: boolean | undefined = undefined;
	constructor(
		public canvas: HTMLCanvasElement,
		public opts: Record<string, unknown>
	) {}
	on(ev: string, fn: Listener) {
		this.listeners.set(ev, [...(this.listeners.get(ev) ?? []), fn]);
	}
	emit(ev: string, ...args: unknown[]) {
		for (const fn of this.listeners.get(ev) ?? []) fn(...args);
	}
	connect = vi.fn(async () => {});
	disconnect = vi.fn();
	setSessionActive = vi.fn();
	setAudioMuted = vi.fn();
	setQualityPreset = vi.fn();
	setScaleMode = vi.fn();
	setCodec = vi.fn();
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
	getConnectPassword: vi.fn(async () => ''),
	getPendingBillingSession: vi.fn(async () => null),
	submitWorkReport: vi.fn()
}));

const { Viewer } = await import('../../../src/lib/remote/viewer.svelte.ts');
const { default: Sheets } = await import('../../../src/lib/remote/viewer/Sheets.svelte');
type ViewerData = ConstructorParameters<typeof Viewer>[0]['data'];

function infoSheet(transport: string, encrypted: boolean | undefined) {
	const d = {
		success: true,
		deviceId: '900100012',
		device: { name: 'demo-build-server', platform: 'Ubuntu 24.04' },
		capabilities: { transport },
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
	const c = s.client as unknown as FakeClient;
	c.encrypted = encrypted;
	c._state = 'streaming';
	c.emit('state', 'streaming');
	c.emit('session_start');
	v.sheet = 'info';
	render(Sheets, { viewer: v, onclose: () => {} });
}

describe('session info: encryption', () => {
	test('a RustDesk session the client confirmed as encrypted says so', () => {
		infoSheet('rd', true);
		expect(screen.getByText('End-to-end encrypted')).toBeInTheDocument();
	});

	test.each([
		['rd', undefined],
		['rd', false],
		['cdap', true],
		['mesh', undefined],
		['rdp', undefined],
		['vnc', undefined]
	])('%s with encrypted=%s does not claim end-to-end encryption', (transport, encrypted) => {
		infoSheet(transport, encrypted);
		expect(screen.getByText('Connected')).toBeInTheDocument();
		expect(screen.queryByText('End-to-end encrypted')).toBeNull();
	});
});
