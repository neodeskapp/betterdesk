/**
 * The mesh desktop viewer (src/lib/remote/client/neoviewer.ts, MNG_KVM
 * over the mesh relay, wrapped by mesh-adapter.ts) fed by a fake agent: binary
 * messages go straight into its stream parser, and the remote viewer runs
 * the real mesh client over a fake relay socket. No relay or agent runs.
 */
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { NeoViewer } from '../../../src/lib/remote/client/neoviewer.ts';
import { FileTransferModal } from '../../../src/lib/remote/client/file-modal.ts';
import { MeshFileTransfer } from '../../../src/lib/remote/client/mesh-files.ts';
import { MeshSession, type MeshSessionOptions } from '../../../src/lib/remote/client/mesh-adapter.ts';

const g = { NeoViewer, MeshSession };

vi.mock('../../../src/lib/remote/rdclient.ts', async () => {
	const mesh = await import('../../../src/lib/remote/client/mesh-adapter.ts');
	return {
		createClient: (canvas: HTMLCanvasElement, opts: MeshSessionOptions) => new mesh.MeshSession(canvas, opts),
		fileSheet: () => null,
		touchInput: () => null,
		videoDecodingSupported: () => true
	};
});
vi.mock('../../../src/lib/api/devices.ts', () => ({ getDevice: vi.fn(async () => ({})) }));
vi.mock('../../../src/lib/api/remote.ts', () => ({
	acceptCertificate: vi.fn(),
	getBillingCheck: vi.fn(async () => null),
	getConnectPassword: vi.fn(async () => ''),
	getPendingBillingSession: vi.fn(async () => null),
	submitWorkReport: vi.fn()
}));

beforeEach(() => {
	vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(null);
});
afterEach(() => {
	vi.useRealTimers();
	vi.unstubAllGlobals();
});

/** A viewer whose relay said ready, with no socket */
function viewer() {
	vi.useFakeTimers();
	const v = new g.NeoViewer(document.createElement('canvas'), {});
	v._relayReady = true;
	v._state = 3;
	return v;
}

/** CMD_SCREEN: the agent's screen size */
function screenPacket(w: number, h: number) {
	return new Uint8Array([0, 7, 0, 8, w >> 8, w & 0xff, h >> 8, h & 0xff]);
}

function concat(...parts: Uint8Array[]) {
	const out = new Uint8Array(parts.reduce((n, p) => n + p.length, 0));
	let o = 0;
	for (const p of parts) {
		out.set(p, o);
		o += p.length;
	}
	return out;
}

describe('mesh stream parser', () => {
	test('skips junk in one pass and still reads the packet after it', () => {
		const v = viewer();
		const slice = vi.spyOn(Uint8Array.prototype, 'slice');
		const junk = new Uint8Array(48 * 1024).fill(0xff);
		v._onBinary(concat(junk, screenPacket(1280, 720)).buffer);
		expect(v.canvas.width).toBe(1280);
		expect(v.canvas.height).toBe(720);
		// The old parser copied the rest of the buffer once per junk byte
		expect(slice.mock.calls.length).toBeLessThan(10);
	});

	test('a packet split over two messages is read once both halves are in', () => {
		const v = viewer();
		const p = screenPacket(800, 600);
		v._onBinary(p.slice(0, 5).buffer);
		expect(v.canvas.width).not.toBe(800);
		v._onBinary(p.slice(5).buffer);
		expect(v.canvas.width).toBe(800);
	});

	test('gives up on a stream that is mostly junk, without freezing the tab', () => {
		const v = viewer();
		const ended: unknown[] = [];
		v.on('disconnected', (reason: unknown) => ended.push(reason));
		v._onBinary(new Uint8Array(1 << 20).fill(0xff).buffer);
		expect(ended).toHaveLength(1);
		expect(String(ended[0])).toMatch(/not a desktop stream/);
		expect(v._accum.length).toBe(0);
	});
});

describe('mesh file transfer list', () => {
	const f = { MeshFileTransfer, FileTransferModal };
	const planted = '"><meta http-equiv="refresh" content="0;url=https://evil.example">';

	test('an agent error names our own transfer, never an id the agent made up', () => {
		const events: [string, Record<string, unknown>][] = [];
		const ft = new f.MeshFileTransfer('900100012', (ev: string, data: Record<string, unknown>) =>
			events.push([ev, data])
		);
		ft._transfers.set(7, {
			id: 7,
			type: 'download',
			reqid: 3,
			status: 'transferring',
			chunks: [],
			remotePath: '/x',
			fileName: 'x',
			fileSize: 0,
			receivedBytes: 0
		});
		ft._onJson(JSON.stringify({ action: 'downloaderror', reqid: planted }));
		expect(events).toEqual([]);
		ft._onJson(JSON.stringify({ action: 'downloaderror', reqid: 3 }));
		expect(events).toEqual([['file_transfer_error', { id: 7, error: 'downloaderror' }]]);
		expect(ft._transfers.has(7)).toBe(false);
	});

	test('a transfer row puts its id into the page as text', () => {
		const modal = new f.FileTransferModal();
		const host = document.createElement('div');
		host.innerHTML = modal._renderTransferRowHtml({
			// An id the page did not make: it must end up as text
			id: planted as unknown as number,
			fileName: 'report.pdf',
			type: 'download',
			phase: 'error',
			percent: 0,
			transferred: 0,
			total: 0,
			error: 'downloaderror',
			resumable: false,
			isFolder: false,
			currentFile: null
		});
		expect(host.querySelector('meta')).toBeNull();
		expect(host.querySelectorAll('.ft-transfer-row')).toHaveLength(1);
		expect(host.querySelector<HTMLElement>('.ft-transfer-row')?.dataset.id).toBe(planted);
	});
});

const { Viewer } = await import('../../../src/lib/remote/viewer.svelte.ts');
type ViewerData = ConstructorParameters<typeof Viewer>[0]['data'];

/** The mesh relay socket the client opens; the test plays the agent */
class FakeSocket {
	static made: FakeSocket[] = [];
	readyState = 1;
	binaryType = '';
	sent: unknown[] = [];
	listeners: ((ev: { data: unknown }) => void)[] = [];
	onerror: unknown = null;
	onclose: (() => void) | null = null;
	constructor(public url: string) {
		FakeSocket.made.push(this);
	}
	addEventListener(_type: string, fn: (ev: { data: unknown }) => void) {
		this.listeners.push(fn);
	}
	send(d: unknown) {
		this.sent.push(d);
	}
	close() {
		this.readyState = 3;
	}
	receive(data: unknown) {
		for (const fn of this.listeners) fn({ data });
	}
	keyPackets() {
		return this.sent.filter((d) => d instanceof Uint8Array && d[1] === 1);
	}
}

describe('remote viewer with a mesh session', () => {
	function meshViewer() {
		FakeSocket.made = [];
		vi.stubGlobal('WebSocket', FakeSocket);
		vi.stubGlobal(
			'fetch',
			vi.fn(async () => ({ ok: true, json: async () => ({ browser_url: '/ws/mesh/relay' }) }))
		);
		const d = {
			success: true,
			deviceId: 'mesh01',
			device: { name: 'office-pc', platform: 'Windows 11' },
			capabilities: { transport: 'mesh' },
			serverPubKey: '',
			connection: null,
			guestToken: '',
			mockRemote: null,
			user: { id: 1, name: 'demo_ui_admin' },
			csrfToken: 't'
		} as ViewerData;
		const v = new Viewer({ data: d, onleave: () => {} });
		const open = (id: string) => {
			v.open(id, id, 'Windows 11');
			const s = v.sessions.find((x) => x.deviceId === id)!;
			v.attach(s, document.createElement('canvas'));
			return s;
		};
		return { v, open };
	}
	// eslint-disable-next-line @typescript-eslint/no-explicit-any
	const desktopOf = (s: { client: unknown }) => (s.client as any)._desktop;

	test('a mesh session that starts takes keyboard and mouse input and counts frames', async () => {
		const { open } = meshViewer();
		const s = open('mesh01');
		await vi.waitFor(() => expect(FakeSocket.made).toHaveLength(1));
		FakeSocket.made[0]!.receive('c');
		expect(s.view).toBe('streaming');
		expect(desktopOf(s)._inputGrabbed).toBe(true);
		expect(desktopOf(s)._statsTimer).not.toBeNull();
		document.dispatchEvent(new KeyboardEvent('keydown', { key: 'a', keyCode: 65 }));
		expect(FakeSocket.made[0]!.keyPackets()).toHaveLength(1);
	});

	test('keys go only to the active tab, and closing a tab removes its key listeners', async () => {
		const add = vi.spyOn(document, 'addEventListener');
		const remove = vi.spyOn(document, 'removeEventListener');
		const { v, open } = meshViewer();
		const first = open('mesh01');
		await vi.waitFor(() => expect(FakeSocket.made).toHaveLength(1));
		open('mesh02');
		await vi.waitFor(() => expect(FakeSocket.made).toHaveLength(2));
		// The first tab becomes ready in the background
		FakeSocket.made[0]!.receive('c');
		expect(first.view).toBe('streaming');
		document.dispatchEvent(new KeyboardEvent('keydown', { key: 'a', keyCode: 65 }));
		expect(FakeSocket.made[0]!.keyPackets()).toHaveLength(0);

		v.switchTo('mesh01');
		document.dispatchEvent(new KeyboardEvent('keydown', { key: 'a', keyCode: 65 }));
		expect(FakeSocket.made[0]!.keyPackets()).toHaveLength(1);

		const keyListeners = add.mock.calls.filter((c) => c[0] === 'keydown' || c[0] === 'keyup');
		expect(keyListeners.length).toBeGreaterThan(0);
		v.close('mesh01');
		for (const call of keyListeners) expect(remove).toHaveBeenCalledWith(...call);
	});

	test.each([
		['the operator ends it', (ms: InstanceType<typeof g.MeshSession>) => ms.disconnect()],
		['the relay closes the socket', () => FakeSocket.made[0]!.onclose?.()]
	])('a mesh session says once that it ended: %s', async (_how, end) => {
		FakeSocket.made = [];
		vi.stubGlobal('WebSocket', FakeSocket);
		vi.stubGlobal(
			'fetch',
			vi.fn(async () => ({ ok: true, json: async () => ({ browser_url: '/ws/mesh/relay' }) }))
		);
		const ms = new g.MeshSession(document.createElement('canvas'), { deviceId: 'mesh01' });
		const said: string[] = [];
		ms.on('state', (st: string) => said.push('state ' + st));
		ms.on('disconnected', () => said.push('disconnected'));
		await ms.connect();
		FakeSocket.made[0]!.receive('c');
		said.length = 0;
		end(ms);
		// The socket's own close event comes after
		FakeSocket.made[0]!.onclose?.();
		expect(said).toEqual(['state disconnected', 'disconnected']);
	});

	test('a listener that throws does not stop the mesh client or the desktop viewer', () => {
		const err = vi.spyOn(console, 'error').mockImplementation(() => {});
		const nv = new g.NeoViewer(document.createElement('canvas'), {});
		nv.on('session_start', () => {
			throw new Error('listener broke');
		});
		nv._onRelayReady();
		expect(nv._inputGrabbed).toBe(true);
		expect(nv._statsTimer).not.toBeNull();
		nv.disconnect();

		const ms = new g.MeshSession(document.createElement('canvas'), { deviceId: 'mesh01' });
		const after = vi.fn();
		ms.on('state', () => {
			throw new Error('listener broke');
		});
		ms.on('state', after);
		ms._emit('state', 'streaming');
		expect(after).toHaveBeenCalledWith('streaming');
		expect(err).toHaveBeenCalled();
	});
});

describe('mesh file transfer', () => {
	const f = { MeshFileTransfer };

	test('the files tunnel request carries the CSRF token', async () => {
		(window as unknown as { NeoDesk: Record<string, unknown> }).NeoDesk = { csrfToken: 'csrf-1' };
		const fetchMock = vi.fn(async () => ({ ok: false, json: async () => ({ error: 'stop here' }) }));
		vi.stubGlobal('fetch', fetchMock);
		const ft = new f.MeshFileTransfer('mesh01', () => {});
		await expect(ft._ensureConnected()).rejects.toThrow('stop here');
		const init = (fetchMock.mock.calls[0] as unknown as [string, RequestInit])[1];
		expect((init.headers as Record<string, string>)['X-CSRF-Token']).toBe('csrf-1');
	});

	test('an upload sends each chunk once, in order, then says it is done', async () => {
		const sent: unknown[] = [];
		const events: [string, Record<string, unknown>][] = [];
		const ft = new f.MeshFileTransfer('mesh01', (ev: string, data: Record<string, unknown>) =>
			events.push([ev, data])
		);
		ft._ws = { readyState: WebSocket.OPEN, send: (d: unknown) => sent.push(d) } as unknown as WebSocket;
		ft._relayReady = true;
		ft.enable();
		const bytes = new Uint8Array(150 * 1024).map((_, i) => i % 251);
		const id = ft.uploadFile(new File([bytes], 'tool.bin'), '/tmp');
		const binary = () => sent.filter((d): d is Uint8Array => d instanceof Uint8Array);
		await vi.waitFor(() => expect(binary()).toHaveLength(1));
		const start = JSON.parse(sent[0] as string);
		expect(start).toMatchObject({ action: 'upload', path: '/tmp/tool.bin' });
		for (const n of [2, 3]) {
			ft._onJson(JSON.stringify({ action: 'uploadack', reqid: start.reqid }));
			await vi.waitFor(() => expect(binary()).toHaveLength(n));
		}
		ft._onJson(JSON.stringify({ action: 'uploadack', reqid: start.reqid }));
		await vi.waitFor(() => expect(events.some(([ev]) => ev === 'file_transfer_complete')).toBe(true));
		const body = binary().map((c) => c.slice(1));
		expect(body.map((c) => c.length)).toEqual([65536, 65536, 150 * 1024 - 131072]);
		const joined = new Uint8Array(body.reduce((n, c) => n + c.length, 0));
		let o = 0;
		for (const c of body) {
			joined.set(c, o);
			o += c.length;
		}
		expect(joined).toEqual(bytes);
		expect(JSON.parse(sent.at(-1) as string)).toEqual({ action: 'uploaddone', reqid: start.reqid });
		expect(events.find(([ev]) => ev === 'file_transfer_complete')?.[1]).toEqual({ id, type: 'upload' });
	});
});

describe('mesh file download', () => {
	async function downloading() {
		const sent: unknown[] = [];
		const events: [string, Record<string, unknown>][] = [];
		const ft = new MeshFileTransfer('mesh01', (ev: string, data: Record<string, unknown>) =>
			events.push([ev, data])
		);
		ft._ws = { readyState: WebSocket.OPEN, send: (d: unknown) => sent.push(d) } as unknown as WebSocket;
		ft._relayReady = true;
		ft.enable();
		const id = ft.downloadFile('/tmp', { name: 'a.bin', size: 8 });
		await vi.waitFor(() => expect(sent).toHaveLength(1));
		return { ft, events, id };
	}

	test('shows its progress in the file sheet while it runs', async () => {
		const d = await downloading();
		d.ft._onBinary(new Uint8Array([0, 1, 2, 3, 4]));
		const progress = d.events.filter(([ev]) => ev === 'file_transfer_progress').map(([, data]) => data);
		expect(progress).toEqual([{ id: d.id, transferred: 4, fileSize: 8, percent: 50 }]);
	});

	test('goes into the folder chosen in the file sheet', async () => {
		const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {});
		const d = await downloading();
		const saved: [string, string][] = [];
		d.ft._saveDownload = async (name: string, blob: Blob) => {
			saved.push([name, await blob.text()]);
			return true;
		};
		d.ft._onBinary(new Uint8Array([0, 0x61, 0x62, 0x63, 0x64]));
		d.ft._onBinary(new Uint8Array([0, 0x65, 0x66, 0x67, 0x68]));
		await vi.waitFor(() => expect(d.events.some(([ev]) => ev === 'file_transfer_complete')).toBe(true));
		expect(saved).toEqual([['a.bin', 'abcdefgh']]);
		expect(click).not.toHaveBeenCalled();
	});
});

describe('file sheet: a second factor for the file transfer session', () => {
	const f = { FileTransferModal };

	test('asks for the code inside the sheet and sends it to the file transfer session', () => {
		const listeners = new Map<string, ((...a: never[]) => void)[]>();
		const client = {
			on(ev: string, fn: (...a: never[]) => void) {
				listeners.set(ev, [...(listeners.get(ev) ?? []), fn]);
			},
			fileTransfer: {
				browseDir: vi.fn(),
				browseParent: vi.fn(),
				uploadFile: vi.fn(),
				downloadFile: vi.fn(),
				cancelTransfer: vi.fn(),
				enabled: true,
				currentPath: ''
			},
			submitFile2FA: vi.fn()
		};
		const emit = (ev: string, ...a: unknown[]) =>
			(listeners.get(ev) ?? []).forEach((fn) => fn(...(a as never[])));
		const modal = new f.FileTransferModal();
		modal.open({ deviceId: '900100012', client });
		emit('file_2fa_required');
		const list = modal._el?.querySelector('.ft-remote-list') as HTMLElement;
		const input = list.querySelector('input') as HTMLInputElement;
		expect(input).not.toBeNull();
		input.value = '123 456';
		(list.querySelector('form') as HTMLFormElement).requestSubmit();
		expect(client.submitFile2FA).toHaveBeenCalledWith('123456');

		emit('file_2fa_error', 'Wrong 2FA Code');
		expect(list.textContent).toContain('Wrong 2FA Code');
		expect(list.querySelector('input')).not.toBeNull();
		modal.close();
	});
});
