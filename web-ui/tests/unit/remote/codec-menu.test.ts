/**
 * The codecs the viewer's Display menu offers. Without WebCodecs (a console
 * opened over plain HTTP) a RustDesk session plays only H.264, so the menu
 * offers nothing else; picking VP9 there gave a black screen.
 */
import { describe, expect, test, vi } from 'vitest';

const decoding = vi.hoisted(() => ({ supported: false }));

vi.mock('../../../src/lib/remote/rdclient.ts', () => ({
	createClient: () => ({}),
	fileSheet: () => null,
	touchInput: () => null,
	videoDecodingSupported: () => decoding.supported
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
type ViewerData = ConstructorParameters<typeof Viewer>[0]['data'];

function viewer(capabilities: Record<string, unknown> = {}) {
	const data = {
		success: true,
		deviceId: '900100012',
		device: { name: 'ndlab-linux', platform: 'Linux' },
		capabilities,
		serverPubKey: 'KEY',
		connection: null,
		guestToken: '',
		mockRemote: null,
		user: { id: 1, name: 'admin' },
		csrfToken: ''
	} as unknown as ViewerData;
	return new Viewer({ data, onleave: () => {} });
}

describe('the codec menu', () => {
	test('without WebCodecs it offers Auto and H264 only', () => {
		decoding.supported = false;
		expect(viewer().codecChoices).toEqual(['Auto', 'H264']);
	});

	test('with WebCodecs it offers every codec', () => {
		decoding.supported = true;
		expect(viewer().codecChoices).toEqual(['Auto', 'VP9', 'AV1', 'H264', 'VP8', 'H265']);
	});
});
