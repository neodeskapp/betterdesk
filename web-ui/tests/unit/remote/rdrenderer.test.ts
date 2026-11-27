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

describe('a display in a window of its own', () => {
	/** A canvas in a box of the given size */
	function boxed(size: { w: number; h: number }) {
		const box = document.createElement('div');
		box.getBoundingClientRect = () => ({ width: size.w, height: size.h }) as DOMRect;
		const canvas = document.createElement('canvas');
		box.append(canvas);
		document.body.append(box);
		return canvas;
	}

	test('the canvas follows the window when it is resized', () => {
		const size = { w: 800, h: 600 };
		const canvas = boxed(size);
		const r = new RDRenderer(canvas, { win: window });
		r.resize();
		expect([canvas.width, canvas.height]).toEqual([800, 600]);

		size.w = 1400;
		size.h = 900;
		window.dispatchEvent(new Event('resize'));
		expect([canvas.width, canvas.height]).toEqual([1400, 900]);
		expect(canvas.style.width).toBe('1400px');
	});

	test('a closed screen stops following the window', () => {
		const size = { w: 800, h: 600 };
		const canvas = boxed(size);
		const r = new RDRenderer(canvas, { win: window });
		r.resize();
		r.close();

		size.w = 400;
		window.dispatchEvent(new Event('resize'));
		expect(canvas.width).toBe(800);
	});
});

describe('the picture through layout changes', () => {
	/** A canvas in a box of the given size, with a 2D context that records what it draws */
	function screenIn(box: { width: number; height: number }) {
		const drawn: unknown[] = [];
		const ctx = { fillRect() {}, drawImage: (src: unknown) => drawn.push(src), fillStyle: '' };
		const options: unknown[] = [];
		vi.mocked(HTMLCanvasElement.prototype.getContext).mockImplementation(function (
			this: HTMLCanvasElement,
			_kind: string,
			opts?: unknown
		) {
			options.push(opts);
			// The cursor canvas gets no context; the screen's records
			return options.length === 1 ? ctx : null;
		} as never);
		const container = document.createElement('div');
		container.getBoundingClientRect = () =>
			({ ...box, top: 0, left: 0, right: 0, bottom: 0, x: 0, y: 0 }) as DOMRect;
		const canvas = document.createElement('canvas');
		container.append(canvas);
		const r = new RDRenderer(canvas);
		return { r, canvas, drawn, options };
	}

	test('a resize draws the last picture again before it returns, so the cleared canvas never shows', () => {
		const box = { width: 800, height: 450 };
		const { r, canvas, drawn } = screenIn(box);
		r.resize();
		r.startRenderLoop();
		const frame = decoded();
		r.pushFrame(frame as unknown as VideoFrame);
		r._renderTick();
		expect(drawn).toEqual([frame]);
		box.width = 1100;
		r.resize();
		expect(canvas.width).toBe(1100);
		expect(drawn).toEqual([frame, frame]);
		r.stopRenderLoop();
	});

	test('the screen is drawn through the page compositor, not desynchronized', () => {
		const { options } = screenIn({ width: 800, height: 450 });
		expect(options[0]).not.toMatchObject({ desynchronized: true });
	});
});
