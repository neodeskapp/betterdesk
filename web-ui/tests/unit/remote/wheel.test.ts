/**
 * Wheel to notches (client/wheel.ts): a mouse wheel gives one notch per
 * click in both browsers (Chromium's 100 px, Firefox's 3 lines or its
 * pixel steps, a Mac mouse's small first steps), a touchpad's stream of
 * small deltas adds up to a notch per threshold, a change of direction
 * starts over. The sequences are the deltas and times the browsers report.
 */
import { describe, expect, test } from 'vitest';
import { RDInput } from '../../../src/lib/remote/client/input.ts';
import { WheelAccumulator, WHEEL_STEP } from '../../../src/lib/remote/client/wheel.ts';

const PIXEL = 0;
const LINE = 1;

/** [deltaY, deltaMode, ms since the previous event] */
type Step = [number, number, number];

function notches(steps: Step[], deltaX = false): number[] {
	const acc = new WheelAccumulator();
	let t = 1000;
	const out: number[] = [];
	for (const [d, mode, gap] of steps) {
		t += gap;
		const n = acc.push(deltaX ? d : 0, deltaX ? 0 : d, mode, t);
		if (n) out.push(deltaX ? n.dx : n.dy);
	}
	return out;
}

describe('wheel notches', () => {
	test('Chromium, mouse wheel: 100 px per click, also when clicks come fast', () => {
		expect(
			notches([
				[100, PIXEL, 500],
				[100, PIXEL, 120],
				[100, PIXEL, 40],
				[100, PIXEL, 30]
			])
		).toEqual([-1, -1, -1, -1]);
		expect(
			notches([
				[-100, PIXEL, 500],
				[-100, PIXEL, 60]
			])
		).toEqual([1, 1]);
	});

	test('Firefox, mouse wheel: 3 lines per click (Linux) or about 48 to 114 px (Windows)', () => {
		expect(
			notches([
				[3, LINE, 500],
				[3, LINE, 50],
				[3, LINE, 40]
			])
		).toEqual([-1, -1, -1]);
		expect(
			notches([
				[48, PIXEL, 500],
				[48, PIXEL, 60],
				[114, PIXEL, 50]
			])
		).toEqual([-1, -1, -1]);
	});

	test('a Mac mouse: a small first step per click still scrolls once', () => {
		expect(
			notches([
				[4, PIXEL, 500],
				[4, PIXEL, 400],
				[4, PIXEL, 400]
			])
		).toEqual([-1, -1, -1]);
	});

	test('a touchpad swipe: many small deltas add up, about one notch per step', () => {
		// Chromium on a Mac trackpad, one swipe down, 16 ms apart
		const swipe: Step[] = [2, 3, 5, 8, 12, 14, 14, 12, 10, 8, 6, 4, 3, 2, 1, 1].map((d, i) => [
			d,
			PIXEL,
			i ? 16 : 500
		]);
		const total = swipe.reduce((s, [d]) => s + d, 0);
		const got = notches(swipe);
		expect(got.every((n) => n === -1)).toBe(true);
		// The first event, then one per WHEEL_STEP of what followed
		expect(got.length).toBe(1 + Math.floor((total - 2) / WHEEL_STEP));
		expect(got.length).toBeLessThan(swipe.length / 3);
	});

	test('a change of direction starts the sum over', () => {
		const steps: Step[] = [
			[10, PIXEL, 500],
			[30, PIXEL, 16],
			[-10, PIXEL, 16],
			[-15, PIXEL, 16],
			[-20, PIXEL, 16]
		];
		// down at once; 30 is not yet a step; up: the sum of 30 down is dropped, 45 up is no step yet
		expect(notches(steps)).toEqual([-1]);
		expect(notches([...steps, [-10, PIXEL, 16]])).toEqual([-1, 1]);
	});

	test('sideways, with the same rules', () => {
		expect(
			notches(
				[
					[100, PIXEL, 500],
					[20, PIXEL, 16],
					[20, PIXEL, 16],
					[20, PIXEL, 16]
				],
				true
			)
		).toEqual([-1, -1]);
	});

	test('no delta, no notch', () => {
		expect(notches([[0, PIXEL, 500]])).toEqual([]);
	});
});

describe('the remote screen', () => {
	test('a touchpad swipe sends a few notches, not one per event', () => {
		const sent: { mouseEvent?: { mask?: number | null; y?: number | null } }[] = [];
		const canvas = document.createElement('canvas');
		const renderer = {
			remoteWidth: 100,
			remoteHeight: 100,
			canvasToRemote: (x: number, y: number) => ({ x, y })
		};
		const input = new RDInput(canvas, renderer, (m) => void sent.push(m as (typeof sent)[number]));
		input.start();
		const swipe = [2, 3, 5, 8, 12, 14, 14, 12, 10, 8, 6, 4, 3, 2, 1, 1];
		for (const d of swipe)
			canvas.dispatchEvent(new WheelEvent('wheel', { deltaY: d, bubbles: true, cancelable: true }));
		const wheel = sent.filter((m) => m.mouseEvent?.mask === RDInput.MOUSE_TYPE_WHEEL);
		expect(wheel.length).toBe(1 + Math.floor((swipe.reduce((a, b) => a + b, 0) - 2) / WHEEL_STEP));
		expect(wheel.every((m) => m.mouseEvent?.y === -1)).toBe(true);
		input.stop();
	});
});
