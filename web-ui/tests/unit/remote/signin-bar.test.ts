/**
 * The saved sign-in bar over the remote screen: its status and the way the
 * sign-in goes read as two sentences with a space between them (L509), and
 * a computer that is not Windows gets the reason instead of no bar (L510).
 */
import { render } from '@testing-library/svelte';
import { describe, expect, test, vi } from 'vitest';
import type { SigninOffer } from '../../../src/lib/remote/signin.ts';

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

/** The bar's text once the session streams and the viewer has this offer */
async function barWith(offer: SigninOffer): Promise<string> {
	const data = {
		success: true,
		deviceId: '900100012',
		device: { name: 'lab-pc', platform: 'Windows' },
		capabilities: { transport: 'rd' },
		serverPubKey: 'KEY',
		connection: null,
		guestToken: '',
		mockRemote: null,
		user: null,
		csrfToken: ''
	} as ViewerData;
	const viewer = new Viewer({ data, onleave: () => {} });
	viewer.open(data.deviceId, data.device.name, data.device.platform);
	const session = viewer.sessions[0]!;
	const { container, unmount } = render(SessionPanel, { viewer, session, active: true });
	(session.client as unknown as FakeClient).emit('session_start');
	await vi.waitFor(() => expect(session.view).toBe('streaming'));
	session.signinOffer = offer;
	let text = '';
	await vi.waitFor(() => {
		text = container.querySelector('.signin-text')?.textContent ?? '';
		expect(text).not.toBe('');
	});
	unmount();
	return text.replace(/\s+/g, ' ').trim();
}

describe('the saved sign-in bar', () => {
	test('puts a space between what it says and who types (L509)', async () => {
		expect(
			await barWith({ kind: 'button', signedIn: true, noLockSignal: false, confirm: true, path: 'typed' })
		).toBe(
			'Someone is signed in on this computer. Use Sign in only while the screen shows the lock screen. This browser types it.'
		);
		expect(
			await barWith({ kind: 'button', signedIn: false, noLockSignal: false, confirm: false, path: 'sealed' })
		).toMatch(/^A sign-in is saved for this computer\. NeoDesk on the computer types it/);
	});

	test('says on a computer that is not Windows why there is no Sign in (L510)', async () => {
		expect(await barWith({ kind: 'unsupported', why: 'platform' })).toBe(
			'Saved sign-in works on Windows computers only.'
		);
	});
});
