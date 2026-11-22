/**
 * The edge between the viewer and the remote client: the client's modules
 * load once per page, the dev mock device's module loads only when the
 * viewer data has the mock on (the server sets it only with
 * BD_DEV_MOCK_REMOTE=1 and ?mock=1), and the transport picks the client.
 * The client modules are stand-ins that record what was made.
 */
import { beforeAll, beforeEach, describe, expect, test, vi } from 'vitest';

const made = vi.hoisted(() => [] as string[]);
const imported = vi.hoisted(() => [] as string[]);

/** A stand-in client class for a transport */
function stub(name: string) {
	return function Client() {
		made.push(name);
	};
}

vi.mock('../../../src/lib/remote/client/client.ts', () => {
	imported.push('client');
	return { RDClient: stub('rd') };
});
vi.mock('../../../src/lib/remote/client/cdap-adapter.ts', () => ({ CDAPSession: stub('cdap') }));
vi.mock('../../../src/lib/remote/client/mesh-adapter.ts', () => ({ MeshSession: stub('mesh') }));
vi.mock('../../../src/lib/remote/client/remote-target.ts', () => ({ RemoteTargetSession: stub('target') }));
vi.mock('../../../src/lib/remote/client/touch.ts', () => ({ RDTouch: stub('touch') }));
vi.mock('../../../src/lib/remote/client/video.ts', () => ({ RDVideo: { isSupported: () => false } }));
vi.mock('../../../src/lib/remote/client/file-modal.ts', () => ({ fileTransferModal: () => null }));
vi.mock('../../../src/lib/remote/client/renderer.ts', () => ({ RDRenderer: stub('renderer') }));
const MOCK_MODULE = '../../../src/lib/remote/client/mock-session.ts';
vi.mock('../../../src/lib/remote/client/mock-session.ts', () => ({ MockRemoteSession: stub('mock') }));

type Mod = typeof import('../../../src/lib/remote/rdclient.ts');

async function fresh(): Promise<Mod> {
	vi.resetModules();
	document.head.innerHTML = '';
	made.length = 0;
	imported.length = 0;
	Reflect.deleteProperty(window, '__mockRemote');
	return import('../../../src/lib/remote/rdclient.ts');
}

const caps = { transport: 'rd' };
const load = (m: Mod, deviceId: string, mock: { scenario: string } | null = null) =>
	m.loadRdClient({ deviceId, capabilities: caps, guestToken: '', csrfToken: 't', mock });
const w = window as unknown as Record<string, unknown>;

describe('loading the remote client', () => {
	// The first import of the loader (i18n, capabilities) takes a few seconds on a busy machine
	beforeAll(async () => {
		await import('../../../src/lib/remote/rdclient.ts');
	}, 30_000);

	beforeEach(() => {
		document.head.innerHTML = '';
	});

	test('loads the client modules once, without classic scripts', async () => {
		const m = await fresh();
		await load(m, '900100012');
		expect(document.head.querySelectorAll('script')).toHaveLength(0);
		expect(imported).toEqual(['client']);
		// A second viewer on the page reuses the loaded client and only updates the globals
		await load(m, '900100013');
		expect(imported).toEqual(['client']);
		expect(w.__initialDeviceId).toBe('900100013');
		expect(m.videoDecodingSupported()).toBe(false);
		expect(m.touchInput()).not.toBeNull();
	});

	test("the viewer's console calls carry the CSRF token of the viewer data", async () => {
		const m = await fresh();
		const fetchMock = vi.fn(
			async (_url: string, _init: RequestInit) =>
				new Response('{"password":"x"}', { status: 200, headers: { 'content-type': 'application/json' } })
		);
		vi.stubGlobal('fetch', fetchMock);
		try {
			await load(m, '900100012');
			// The quick support password of the device, asked by the viewer before the password prompt
			const client = await import('../../../src/lib/api/client.ts');
			await client.request('/api/support/devices/900100012/password', {
				method: 'POST',
				allowAnonymous: true
			});
			const init = fetchMock.mock.calls[0]?.[1];
			expect((init?.headers as Record<string, string>)['X-CSRF-Token']).toBe('t');
		} finally {
			vi.unstubAllGlobals();
		}
	});

	test('before the client loaded, there is no client, sheet or touch input', async () => {
		const m = await fresh();
		const canvas = document.createElement('canvas');
		expect(() => m.createClient(canvas, {} as Parameters<Mod['createClient']>[1], 'rd')).toThrow(
			/not loaded/
		);
		expect(m.fileSheet()).toBeNull();
		expect(m.touchInput()).toBeNull();
		expect(m.videoDecodingSupported()).toBe(true);
	});

	test('never loads the dev mock unless the viewer data turned it on', async () => {
		const canvas = document.createElement('canvas');
		const opts = {} as Parameters<Mod['createClient']>[1];
		const m = await fresh();
		await load(m, '900100012');
		expect(w.__mockRemote).toBeNull();
		// Not loaded: even with the flag set later, the transport's client answers
		w.__mockRemote = { scenario: 'ok' };
		m.createClient(canvas, opts, 'rd');
		expect(made).toEqual(['rd']);

		const m2 = await fresh();
		await load(m2, '900100012', { scenario: 'refused' });
		expect(w.__mockRemote).toEqual({ scenario: 'refused' });
		m2.createClient(canvas, opts, 'rd');
		expect(made).toEqual(['mock']);
		expect(document.head.querySelectorAll('script')).toHaveLength(0);
	});

	test('a failed load lets the next attempt start over', async () => {
		const m = await fresh();
		vi.doMock(MOCK_MODULE, () => {
			throw new Error('mock failed to load');
		});
		await expect(load(m, 'x01', { scenario: 'ok' })).rejects.toThrow();
		const canvas = document.createElement('canvas');
		const opts = {} as Parameters<Mod['createClient']>[1];
		expect(() => m.createClient(canvas, opts, 'rd')).toThrow(/not loaded/);
		vi.doMock(MOCK_MODULE, () => ({ MockRemoteSession: stub('mock') }));
		await load(m, 'x01', { scenario: 'ok' });
		m.createClient(canvas, opts, 'rd');
		expect(made).toEqual(['mock']);
	});

	test('the mock replaces every transport only when it is on; otherwise the transport picks the client', async () => {
		const m = await fresh();
		await load(m, '900100012', { scenario: 'ok' });
		w.__mockRemote = null;
		const canvas = document.createElement('canvas');
		const opts = {} as Parameters<Mod['createClient']>[1];
		m.createClient(canvas, opts, 'rd');
		m.createClient(canvas, opts, 'cdap');
		m.createClient(canvas, opts, 'mesh');
		m.createClient(canvas, opts, 'rdp');
		m.createClient(canvas, opts, 'vnc');
		// The mock is loaded but the server did not turn it on for this viewer
		m.createClient(canvas, opts, 'rd');
		w.__mockRemote = { scenario: 'ok' };
		m.createClient(canvas, opts, 'cdap');
		expect(made).toEqual(['rd', 'cdap', 'mesh', 'target', 'target', 'rd', 'mock']);
	});
});
