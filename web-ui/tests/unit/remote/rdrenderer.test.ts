/**
 * The screen of a remote session (src/lib/remote/client/renderer.ts): the
 * frames per second the session info sheet shows. jsdom has no 2D canvas;
 * the renderer copes with a missing context. The clock is the test's.
 */
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { RDRenderer } from '../../../src/lib/remote/client/renderer.ts';

let now = 0;
beforeEach(() => {
	now = 1000;
	vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(null);
	vi.spyOn(performance, 'now').mockImplementation(() => now);
	vi.stubGlobal('requestAnimationFrame', () => 1);
	vi.stubGlobal('cancelAnimationFrame', () => {});
});
afterEach(() => {
	vi.unstubAllGlobals();
	vi.restoreAllMocks();
});

/** Draws the frames, 20 ms apart, then reads the count one second after the start */
function fpsOf(frames: object[]) {
	const r = new RDRenderer(document.createElement('canvas'));
	r.startRenderLoop();
	for (const frame of frames) {
		r.pushFrame(frame as VideoFrame);
		now += 20;
		r._renderTick();
	}
	now = 2001;
	r._renderTick();
	r.stopRenderLoop();
	return r.fps;
}

const decoded = () => ({ displayWidth: 640, displayHeight: 360, close() {} });
const proxy = (isNew: boolean) => ({ ...decoded(), _source: document.createElement('video'), _isNew: isNew });

describe('frames per second', () => {
	test('counts every frame WebCodecs decoded', () => {
		expect(fpsOf(Array.from({ length: 30 }, decoded))).toBe(30);
	});

	test('on the JMuxer path counts only frames with new video', () => {
		expect(fpsOf([proxy(true), proxy(false), proxy(true), proxy(false)])).toBe(2);
	});
});
