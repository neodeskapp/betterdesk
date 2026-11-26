/**
 * The remote screen keeps every key, Tab included, so it says how to get
 * out (BUGS L327): its description names F8, and a hint shows on the
 * screen when it takes focus (also when the page gives it focus on open),
 * though not after a click on it.
 */
import { fireEvent, render } from '@testing-library/svelte';
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
	setViewOnly = vi.fn();
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
const { default: SessionPanel } = await import('../../../src/lib/remote/viewer/SessionPanel.svelte');
type ViewerData = ConstructorParameters<typeof Viewer>[0]['data'];

const { isReleaseKey } = await import('../../../src/lib/remote/screen-focus.ts');

describe('the keys that leave the remote screen', () => {
	const key = (init: KeyboardEventInit) => new KeyboardEvent('keydown', init);
	test('F8 alone, or Ctrl+Alt+Shift+K for keyboards without function keys', () => {
		expect(isReleaseKey(key({ key: 'F8', code: 'F8' }))).toBe(true);
		expect(isReleaseKey(key({ key: 'K', code: 'KeyK', ctrlKey: true, altKey: true, shiftKey: true }))).toBe(
			true
		);
		// Other layouts put another letter there: the physical key counts
		expect(isReleaseKey(key({ key: 'Ü', code: 'KeyK', ctrlKey: true, altKey: true, shiftKey: true }))).toBe(
			true
		);
	});
	test('not with other modifiers, and not other keys', () => {
		expect(isReleaseKey(key({ key: 'F8', code: 'F8', ctrlKey: true }))).toBe(false);
		expect(isReleaseKey(key({ key: 'k', code: 'KeyK', ctrlKey: true, altKey: true }))).toBe(false);
		expect(
			isReleaseKey(
				key({ key: 'K', code: 'KeyK', ctrlKey: true, altKey: true, shiftKey: true, metaKey: true })
			)
		).toBe(false);
		expect(isReleaseKey(key({ key: 'Escape', code: 'Escape' }))).toBe(false);
	});
});

describe('the remote screen', () => {
	test('says that F8 leaves it, and shows that when it takes focus', async () => {
		const data = {
			success: true,
			deviceId: '900100012',
			device: { name: 'demo-build-server', platform: 'Ubuntu 24.04' },
			capabilities: { transport: 'rd' },
			serverPubKey: 'KEY',
			connection: null,
			guestToken: '',
			mockRemote: null,
			user: { id: 1, name: 'demo_ui_admin' },
			csrfToken: ''
		} as ViewerData;
		const viewer = new Viewer({ data, onleave: () => {} });
		viewer.open(data.deviceId, data.device.name, data.device.platform);
		const session = viewer.sessions[0]!;
		const { container } = render(SessionPanel, { viewer, session, active: true });
		(session.client as unknown as FakeClient).emit('session_start');

		const canvas = container.querySelector('canvas')!;
		await vi.waitFor(() => expect(canvas.tabIndex).toBe(0));
		const hint = document.getElementById(canvas.getAttribute('aria-describedby') ?? '');
		expect(hint?.textContent, 'the screen is described by the way out').toMatch(/F8/);
		expect(hint?.textContent, 'and by a way out without function keys').toMatch(/Ctrl\+Alt\+Shift\+K/);
		// A screen reader passes keys through and says what it is
		expect(canvas).toHaveAttribute('role', 'application');
		expect(canvas).toHaveAttribute('aria-roledescription', 'remote screen');
		expect(canvas).toHaveAccessibleName('Screen of demo-build-server');
		canvas.focus();
		await vi.waitFor(() => expect(hint!.classList.contains('is-shown')).toBe(true));
		canvas.blur();
		await vi.waitFor(() => expect(hint!.classList.contains('is-shown')).toBe(false));

		// A click on the screen gives it focus too: no hint then
		await fireEvent.pointerDown(canvas);
		canvas.focus();
		await new Promise((done) => setTimeout(done, 20));
		expect(hint!.classList.contains('is-shown')).toBe(false);
	});
});
