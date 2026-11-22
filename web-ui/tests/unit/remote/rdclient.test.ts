/**
 * The edge between the viewer and the remote client: the client's modules
 * load once per page, the dev mock device's script loads only when the
 * viewer data has the mock on (the server sets it only with
 * BD_DEV_MOCK_REMOTE=1 and ?mock=1), and the transport picks the client.
 * The client modules are stand-ins that record what was made.
 */
import { beforeEach, describe, expect, test, vi } from 'vitest';

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

type Mod = typeof import('../../../src/lib/remote/rdclient.ts');

async function fresh(): Promise<Mod> {
	vi.resetModules();
	document.head.innerHTML = '';
	made.length = 0;
	imported.length = 0;
	for (const k of ['__mockRemote', 'MockRemoteSession', 'RDRenderer']) Reflect.deleteProperty(window, k);
	return import('../../../src/lib/remote/rdclient.ts');
}

const caps = { transport: 'rd' };
const load = (m: Mod, deviceId: string, mock: { scenario: string } | null = null) =>
	m.loadRdClient({ deviceId, capabilities: caps, guestToken: '', csrfToken: 't', mock });
const mockScript = () => document.head.querySelector<HTMLScriptElement>('script[data-rdclient]');
const w = window as unknown as Record<string, unknown>;

describe('loading the remote client', () => {
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
		const m = await fresh();
		await load(m, '900100012');
		expect(document.head.querySelector(`script[src="${m.MOCK_SCRIPT}"]`)).toBeNull();
		expect(w.__mockRemote).toBeNull();
		expect(w.RDRenderer).toBeUndefined();

		const m2 = await fresh();
		const p = load(m2, '900100012', { scenario: 'refused' });
		await vi.waitFor(() => expect(mockScript()).not.toBeNull());
		expect(mockScript()?.getAttribute('src')).toBe('/dev/mock-remote.js');
		expect(w.__mockRemote).toEqual({ scenario: 'refused' });
		// The mock draws through the global RDRenderer, which the loader puts there first
		expect(typeof w.RDRenderer).toBe('function');
		mockScript()?.onload?.(new Event('load'));
		await p;
	});

	test('a failed load lets the next attempt start over', async () => {
		const m = await fresh();
		const p = load(m, 'x01', { scenario: 'ok' });
		await vi.waitFor(() => expect(mockScript()).not.toBeNull());
		mockScript()?.onerror?.(new Event('error'));
		await expect(p).rejects.toThrow(/Could not load/);
		expect(document.head.querySelectorAll('script[data-rdclient]')).toHaveLength(0);
		const again = load(m, 'x01', { scenario: 'ok' });
		await vi.waitFor(() => expect(mockScript()).not.toBeNull());
		mockScript()?.onload?.(new Event('load'));
		await again;
	});

	test('the mock replaces every transport only when it is on; otherwise the transport picks the client', async () => {
		const m = await fresh();
		await load(m, '900100012');
		w.MockRemoteSession = stub('mock');
		const canvas = document.createElement('canvas');
		const opts = {} as Parameters<Mod['createClient']>[1];
		m.createClient(canvas, opts, 'rd');
		m.createClient(canvas, opts, 'cdap');
		m.createClient(canvas, opts, 'mesh');
		m.createClient(canvas, opts, 'rdp');
		m.createClient(canvas, opts, 'vnc');
		// The script is there but the server did not turn the mock on
		w.__mockRemote = null;
		m.createClient(canvas, opts, 'rd');
		w.__mockRemote = { scenario: 'ok' };
		m.createClient(canvas, opts, 'cdap');
		expect(made).toEqual(['rd', 'cdap', 'mesh', 'target', 'target', 'rd', 'mock']);
	});
});
