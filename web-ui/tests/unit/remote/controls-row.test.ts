/**
 * The session controls in the viewer's bar for a streaming session: what a
 * watch-only guest is offered (nothing that sends input to the device) next
 * to what a signed-in operator gets, and settings that show up in the bar
 * while they are on and switch off from there.
 */
import { fireEvent, render, screen } from '@testing-library/svelte';
import { describe, expect, test, vi } from 'vitest';

type Listener = (...args: unknown[]) => void;

class FakeClient {
	listeners = new Map<string, Listener[]>();
	viewOnly = false;
	renderer = { resize: vi.fn() };
	_state = 'idle';
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
	setViewOnly = vi.fn((on: boolean) => {
		this.viewOnly = on;
	});
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
const { default: ControlsRow } = await import('../../../src/lib/remote/viewer/ControlsRow.svelte');
type ViewerData = ConstructorParameters<typeof Viewer>[0]['data'];

function streamingViewer(capabilities: ViewerData['capabilities']) {
	const d = {
		success: true,
		deviceId: '900100012',
		device: { name: 'demo-build-server', platform: 'Ubuntu 24.04' },
		capabilities,
		serverPubKey: 'KEY',
		connection: null,
		guestToken: capabilities.guest_access ? 'tok' : '',
		mockRemote: null,
		user: capabilities.guest_access ? null : { id: 1, name: 'demo_ui_admin' },
		csrfToken: ''
	} as ViewerData;
	const v = new Viewer({ data: d, onleave: () => {} });
	v.open(d.deviceId, d.device.name, d.device.platform);
	const s = v.sessions[0]!;
	v.attach(s, document.createElement('canvas'));
	(s.client as unknown as FakeClient).emit('session_start');
	return v;
}

describe('remote viewer controls', () => {
	test('a watch-only guest gets no control that sends input to the device', () => {
		const v = streamingViewer({
			transport: 'rd',
			guest_access: true,
			guest_view_only: true,
			guest_peer_ids: ['900100012']
		});
		render(ControlsRow, { viewer: v, id: 'ctl' });
		expect(screen.queryByTitle('Keys')).toBeNull();
		expect(screen.queryByTitle('Chat')).toBeNull();
		expect(screen.queryByTitle('File transfer')).toBeNull();
		expect(screen.queryByTitle('View only')).toBeNull();
		// Watching and leaving stay
		expect(screen.getByTitle('View')).toBeInTheDocument();
		expect(screen.getByTitle('More')).toBeInTheDocument();
		expect(screen.getByTitle('Disconnect')).toBeInTheDocument();
	});

	test('an operator gets Keys, View, Chat, File transfer and More, and no switched-on settings yet', () => {
		const v = streamingViewer({ transport: 'rd' });
		render(ControlsRow, { viewer: v, id: 'ctl' });
		for (const name of ['Keys', 'View', 'Chat', 'File transfer', 'More', 'Disconnect'])
			expect(screen.getByTitle(name)).toBeInTheDocument();
		expect(screen.queryByTitle('View only')).toBeNull();
		expect(screen.queryByTitle('Turn sound on')).toBeNull();
		expect(screen.queryByTitle('Stop recording')).toBeNull();
	});

	test('view only shows in the bar while it is on, and a click there turns it off', async () => {
		const v = streamingViewer({ transport: 'rd' });
		render(ControlsRow, { viewer: v, id: 'ctl' });
		const c = v.sessions[0]!.client as unknown as FakeClient;
		v.toggleViewOnly();
		const btn = await screen.findByTitle('View only');
		expect(btn).toHaveAttribute('aria-pressed', 'true');
		await fireEvent.click(btn);
		expect(c.setViewOnly).toHaveBeenLastCalledWith(false);
		await vi.waitFor(() => expect(screen.queryByTitle('View only')).toBeNull());
	});

	test('muted sound shows in the bar, and a click there turns it on again', async () => {
		const v = streamingViewer({ transport: 'rd' });
		render(ControlsRow, { viewer: v, id: 'ctl' });
		const c = v.sessions[0]!.client as unknown as FakeClient;
		v.toggleAudio();
		await fireEvent.click(await screen.findByTitle('Turn sound on'));
		expect(c.setAudioMuted).toHaveBeenLastCalledWith(false);
		await vi.waitFor(() => expect(screen.queryByTitle('Turn sound on')).toBeNull());
	});

	test('what the device turned off for the account cannot be used: view only stays, no file sheet', async () => {
		const v = streamingViewer({ transport: 'rd' });
		render(ControlsRow, { viewer: v, id: 'ctl' });
		const c = v.sessions[0]!.client as unknown as FakeClient;
		expect(screen.getByTitle('File transfer')).toBeEnabled();
		c.emit('permission', { permission: 0, enabled: false });
		c.emit('permission', { permission: 4, enabled: false });
		await vi.waitFor(() => expect(screen.getByTitle('View only')).toBeDisabled());
		expect(screen.getByTitle('View only')).toHaveAttribute('aria-pressed', 'true');
		expect(screen.getByTitle('File transfer')).toBeDisabled();
	});
});
