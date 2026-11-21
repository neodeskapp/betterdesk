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
