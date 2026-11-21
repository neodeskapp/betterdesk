/**
 * The mesh desktop viewer (static/rdclient/betterviewer.js, MNG_KVM over
 * the mesh relay) fed by a fake agent: binary messages go straight into
 * its stream parser. No relay or agent runs.
 */
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { loadScripts, type Loaded } from './rdclient-scripts.ts';

const g: Loaded = loadScripts(['betterviewer.js', 'mesh-adapter.js'], ['BetterViewer', 'MeshSession']);

beforeEach(() => {
	vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(null);
});
afterEach(() => vi.useRealTimers());

/** A viewer whose relay said ready, with no socket */
function viewer() {
	vi.useFakeTimers();
	const v = new g.BetterViewer(document.createElement('canvas'), {});
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
	const f: Loaded = loadScripts(
		['mesh-files.js', 'file-modal.js'],
		['MeshFileTransfer', 'FileTransferModal']
	);
	const planted = '"><meta http-equiv="refresh" content="0;url=https://evil.example">';

	test('an agent error names our own transfer, never an id the agent made up', () => {
		const events: [string, Record<string, unknown>][] = [];
		const ft = new f.MeshFileTransfer('900100012', (ev: string, data: Record<string, unknown>) =>
			events.push([ev, data])
		);
		ft._transfers.set(7, { id: 7, type: 'download', reqid: 3, status: 'transferring', chunks: [] });
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
			id: planted,
			fileName: 'report.pdf',
			type: 'download',
			phase: 'error',
			percent: 0,
			transferred: 0,
			total: 0,
			error: 'downloaderror'
		});
		expect(host.querySelector('meta')).toBeNull();
		expect(host.querySelectorAll('.ft-transfer-row')).toHaveLength(1);
		expect(host.querySelector<HTMLElement>('.ft-transfer-row')?.dataset.id).toBe(planted);
	});
});
