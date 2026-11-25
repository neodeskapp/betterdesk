/**
 * Browser wheel events to the remote computer's wheel notches. A mouse
 * wheel click is one notch, whatever its size in the browser (Chromium
 * 100 px, Firefox 3 lines or its pixel steps, a Mac mouse a few pixels);
 * a touchpad's stream of small deltas adds up to one notch per WHEEL_STEP
 * pixels instead of one per event, which scrolled far too fast before.
 *
 * Per axis:
 * - an event of WHEEL_CLICK pixels or more is a wheel click: one notch;
 * - the first event after WHEEL_PAUSE_MS without one starts a gesture and
 *   scrolls at once (a slow mouse wheel with small steps stays responsive);
 * - otherwise the deltas add up, one notch per WHEEL_STEP, the rest kept;
 * - a change of direction drops what was added up.
 * Lines count as WHEEL_LINE pixels, pages as one notch each.
 */

export const WHEEL_STEP = 50;
export const WHEEL_CLICK = 40;
export const WHEEL_PAUSE_MS = 200;
const WHEEL_LINE = WHEEL_STEP / 3;

/** A notch on one axis: -1 or 1 as the remote's wheel takes it (down and right are -1) */
export interface WheelNotch {
	dx: number;
	dy: number;
}

class Axis {
	sum = 0;
	last = -Infinity;

	/** -1, 0 or 1 notches for a delta in pixels at time t */
	push(d: number, t: number): number {
		if (!d) return 0;
		const gesture = t - this.last > WHEEL_PAUSE_MS;
		this.last = t;
		if (Math.sign(d) !== Math.sign(this.sum)) this.sum = 0;
		if (Math.abs(d) >= WHEEL_CLICK || gesture) {
			this.sum = 0;
			return -Math.sign(d);
		}
		this.sum += d;
		if (Math.abs(this.sum) < WHEEL_STEP) return 0;
		this.sum -= Math.sign(this.sum) * WHEEL_STEP;
		return -Math.sign(d);
	}
}

export class WheelAccumulator {
	private x = new Axis();
	private y = new Axis();

	/**
	 * One wheel event: its deltas, its deltaMode (0 pixels, 1 lines, 2
	 * pages) and its time stamp. Returns the notch to send, or null.
	 */
	push(dx: number, dy: number, mode: number, t: number): WheelNotch | null {
		const scale = mode === 1 ? WHEEL_LINE : mode === 2 ? WHEEL_CLICK : 1;
		// The axis that moves most takes the event, as the remote's wheel has one per message
		const horizontal = Math.abs(dx) > Math.abs(dy);
		const n = horizontal ? this.x.push(dx * scale, t) : this.y.push(dy * scale, t);
		if (!n) return null;
		return horizontal ? { dx: n, dy: 0 } : { dx: 0, dy: n };
	}
}
