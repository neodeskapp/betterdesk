/**
 * The RustDesk client's mouse and touch input (src/lib/remote/client/input.ts and
 * touch.ts): a button pressed on the remote screen is released on the
 * device, wherever the pointer or finger lets go and when input stops.
 */
import { describe, expect, test, vi } from 'vitest';
import type { MessageInit } from '../../../src/lib/remote/client/generated/protos.ts';
import { RDInput } from '../../../src/lib/remote/client/input.ts';
import { RDTouch } from '../../../src/lib/remote/client/touch.ts';

const g = { RDInput, RDTouch };

/** A 100 x 100 remote screen drawn 1:1 at the canvas' top left; jsdom's canvas sits at 0,0 */
const renderer = {
	remoteWidth: 100,
	remoteHeight: 100,
	canvasToRemote: (x: number, y: number) => ({ x, y })
};

type Sent = MessageInit;
const UP_LEFT = 2 | (1 << 3);
const DOWN_LEFT = 1 | (1 << 3);
const masks = (sent: Sent[]) => sent.map((m) => m.mouseEvent?.mask);

describe('mouse buttons', () => {
	test('a drag released outside the screen still releases the button on the device', () => {
		const sent: Sent[] = [];
		const canvas = document.createElement('canvas');
		document.body.append(canvas);
		const input = new g.RDInput(canvas, renderer, (m: Sent) => sent.push(m));
		input.start();
		canvas.dispatchEvent(new MouseEvent('mousedown', { button: 0, clientX: 10, clientY: 10, bubbles: true }));
		window.dispatchEvent(new MouseEvent('mouseup', { button: 0, clientX: 500, clientY: 40, bubbles: true }));
		expect(masks(sent)).toEqual([DOWN_LEFT, UP_LEFT]);
		expect(sent[1]!.mouseEvent).toMatchObject({ x: 100, y: 40 });
		// A release with no press before it sends nothing
		window.dispatchEvent(new MouseEvent('mouseup', { button: 0, clientX: 5, clientY: 5, bubbles: true }));
		expect(sent).toHaveLength(2);
		input.stop();
		canvas.remove();
	});

	test('stopping input (tab switch, view only, end) releases a held button', () => {
		const sent: Sent[] = [];
		const canvas = document.createElement('canvas');
		const input = new g.RDInput(canvas, renderer, (m: Sent) => sent.push(m));
		input.start();
		canvas.dispatchEvent(new MouseEvent('mousedown', { button: 0, clientX: 20, clientY: 30 }));
		input.stop();
		expect(masks(sent)).toEqual([DOWN_LEFT, UP_LEFT]);
		expect(sent[1]!.mouseEvent).toMatchObject({ x: 20, y: 30 });
	});
});

describe('touch drag', () => {
	const touch = (x: number, y: number) => ({ identifier: 1, clientX: x, clientY: y });
	type Point = ReturnType<typeof touch>;
	const ev = (touches: Point[], changed: Point[]) => ({
		preventDefault: () => {},
		touches,
		changedTouches: changed
	});

	function dragging(sent: Sent[]) {
		vi.spyOn(performance, 'now').mockReturnValue(1e6);
		const t = new g.RDTouch(document.createElement('canvas'), renderer, (m: Sent) => sent.push(m));
		t.start();
		t._handleTouchStart(ev([touch(10, 10)], [touch(10, 10)]));
		t._handleTouchMove(ev([touch(40, 40)], [touch(40, 40)]));
		return t;
	}

	test('a drag that ends outside the screen releases the button where it last was', () => {
		const sent: Sent[] = [];
		const t = dragging(sent);
		t._handleTouchEnd(ev([], [touch(500, 500)]));
		expect(masks(sent).filter((m) => m !== 0)).toEqual([DOWN_LEFT, UP_LEFT]);
		expect(sent.at(-1)!.mouseEvent).toMatchObject({ x: 40, y: 40 });
	});

	test('stopping touch input in the middle of a drag releases the button', () => {
		const sent: Sent[] = [];
		const t = dragging(sent);
		t.stop();
		expect(masks(sent).filter((m) => m !== 0)).toEqual([DOWN_LEFT, UP_LEFT]);
	});
});
