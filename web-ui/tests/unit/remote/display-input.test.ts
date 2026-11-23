/**
 * Mouse, keyboard and cursor on one display among several
 * (src/lib/remote/client/renderer.ts and input.ts): a click on a display's
 * picture reaches the device as a point of its whole desktop, a display in a
 * window of its own takes that window's keys, the cursor shows only on the
 * display it is on, and a picture shown at its own size follows the pointer.
 */
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import type { MessageInit } from '../../../src/lib/remote/client/generated/protos.ts';
import { RDInput } from '../../../src/lib/remote/client/input.ts';
import { RDRenderer } from '../../../src/lib/remote/client/renderer.ts';
import { RDTouch } from '../../../src/lib/remote/client/touch.ts';

beforeEach(() => {
	vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(null);
});
afterEach(() => {
	vi.restoreAllMocks();
	document.body.replaceChildren();
});

/** A renderer showing a picture in a canvas of the given size (jsdom has no layout: set it by hand) */
function tile(picture: [number, number], canvas: [number, number], doc: Document = document): RDRenderer {
	const el = doc.createElement('canvas');
	doc.body.append(el);
	el.width = canvas[0];
	el.height = canvas[1];
	const r = new RDRenderer(el);
	r.setRemoteSize(picture[0], picture[1]);
	return r;
}

const DOWN_LEFT = 1 | (1 << 3);

function click(r: RDRenderer, x: number, y: number): MessageInit[] {
	const sent: MessageInit[] = [];
	const input = new RDInput(r.canvas, r, (m) => sent.push(m), { keyboard: false });
	input.start();
	r.canvas.dispatchEvent(new MouseEvent('mousedown', { button: 0, clientX: x, clientY: y, bubbles: true }));
	input.stop();
	return sent;
}

describe('a click on a display reaches the device as a point of its desktop', () => {
	test('a display at a negative origin, shown at a quarter of its size', () => {
		const r = tile([2560, 1440], [640, 360]);
		r.setPlace({ x: -1280, y: 360 });
		const sent = click(r, 320, 180);
		expect(sent[0]?.mouseEvent).toMatchObject({ mask: DOWN_LEFT, x: 0, y: 1080 });
		// The release when input stops goes to the same desktop point
		expect(sent[1]?.mouseEvent).toMatchObject({ x: 0, y: 1080 });
	});

	test('a Retina display the Mac does not treat as current: desktop units, half its pixels', () => {
		const r = tile([2560, 1440], [640, 360]);
		r.setPlace({ x: -1280, y: 360 }, 0.5);
		expect(click(r, 320, 180)[0]?.mouseEvent).toMatchObject({ x: -640, y: 720 });
	});

	test('the second display in the single layout: right of the first, not on top of it', () => {
		const r = tile([1280, 1024], [1280, 1024]);
		r.setPlace({ x: 1920, y: 0 });
		expect(click(r, 10, 20)[0]?.mouseEvent).toMatchObject({ x: 1930, y: 20 });
	});

	test('a touch on a tablet lands on the same desktop point', () => {
		const r = tile([1280, 1024], [1280, 1024]);
		r.setPlace({ x: 1920, y: 0 });
		const sent: MessageInit[] = [];
		vi.spyOn(performance, 'now').mockReturnValue(1e6);
		const t = new RDTouch(r.canvas, r, (m) => sent.push(m));
		t.start();
		const finger = { identifier: 1, clientX: 10, clientY: 20 };
		t._handleTouchStart({ preventDefault() {}, touches: [finger], changedTouches: [finger] });
		t._handleTouchEnd({ preventDefault() {}, touches: [], changedTouches: [finger] });
		const points = sent.filter((m) => m.mouseEvent?.mask).map((m) => [m.mouseEvent?.x, m.mouseEvent?.y]);
		expect(points.length).toBeGreaterThan(0);
		expect(points.every(([x, y]) => x === 1930 && y === 20)).toBe(true);
		t.stop();
	});
});

describe('keys', () => {
	test('a display in a window of its own takes the keys typed there, the main window keeps its own', () => {
		const frame = document.createElement('iframe');
		document.body.append(frame);
		const other = frame.contentDocument as Document;
		const r = tile([1920, 1080], [960, 540], other);
		const sent: MessageInit[] = [];
		const input = new RDInput(r.canvas, r, (m) => sent.push(m));
		input.start();
		const press = (doc: Document) =>
			doc.dispatchEvent(new KeyboardEvent('keydown', { code: 'KeyA', key: 'a', bubbles: true }));
		press(document);
		expect(sent.filter((m) => m.keyEvent)).toHaveLength(0);
		press(other);
		expect(sent.filter((m) => m.keyEvent)).toHaveLength(1);
		input.stop();
	});

	test('a tile takes only the mouse: the window has one keyboard', () => {
		const r = tile([1920, 1080], [960, 540]);
		const sent: MessageInit[] = [];
		const input = new RDInput(r.canvas, r, (m) => sent.push(m), { keyboard: false });
		input.start();
		document.dispatchEvent(new KeyboardEvent('keydown', { code: 'KeyA', key: 'a', bubbles: true }));
		expect(sent).toHaveLength(0);
		input.stop();
	});
});

describe('the cursor', () => {
	test('shows on the display it is on, at its picture pixel', () => {
		const r = tile([2560, 1440], [640, 360]);
		r.setPlace({ x: -1280, y: 360 });
		r.updateCursorPosition({ x: -1000, y: 400 });
		expect(r._cursorOnPicture()).toEqual({ x: 280, y: 40 });
		r.updateCursorPosition({ x: 100, y: 100 });
		expect(r._cursorOnPicture()).toBeNull();
	});
});

describe('a picture at its own size', () => {
	test('follows the pointer, so its far corner can be reached', () => {
		const r = tile([1920, 1080], [960, 540]);
		r.setScaleMode('1:1');
		r.panTo(0, 0);
		expect(r.canvasToRemote(0, 0)).toEqual({ x: 0, y: 0 });
		r.panTo(960, 540);
		expect(r.canvasToRemote(960, 540)).toEqual({ x: 1920, y: 1080 });
		r.panTo(480, 270);
		expect(r.canvasToRemote(480, 270)).toEqual({ x: 960, y: 540 });
	});

	test('a fitted picture stays put', () => {
		const r = tile([1920, 1080], [960, 540]);
		r.panTo(0, 0);
		expect(r.canvasToRemote(480, 270)).toEqual({ x: 960, y: 540 });
	});
});
