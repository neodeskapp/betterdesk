/**
 * Video of several displays over one connection
 * (src/lib/remote/client/display-streams.ts): each frame goes to the
 * decoder of its display, the device streams exactly the displays someone
 * sees, and each display's cost is counted on its own.
 */
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { RDDisplayStreams, type RDDisplayDecoder } from '../../../src/lib/remote/client/display-streams.ts';
import type { RDDecodedFrame } from '../../../src/lib/remote/client/video.ts';

function decoder(name: string): RDDisplayDecoder & { name: string; closed: boolean } {
	return {
		name,
		closed: false,
		initialized: false,
		currentCodec: null,
		frameCount: 0,
		_needKeyframe: false,
		onFrame: null,
		init: async () => {},
		decode: async () => {},
		setBackgroundMode: () => {},
		getStats: () => ({ videoFps: 0 }),
		close() {
			this.closed = true;
		}
	};
}

const screen = () => ({
	frames: [] as RDDecodedFrame[],
	pushFrame(f: RDDecodedFrame) {
		this.frames.push(f);
	}
});
const frame = { displayWidth: 10, displayHeight: 10, close() {} } as unknown as RDDecodedFrame;

let now = 0;
let sent: number[][];
let refreshed: number[];
let main: ReturnType<typeof decoder>;

function streams(opts: { together?: boolean; current?: number } = {}) {
	const s = new RDDisplayStreams({
		main,
		capture: (set) => sent.push(set),
		refresh: (idx) => refreshed.push(idx),
		now: () => now,
		slowEveryMs: 4000,
		snapshotMs: 2500
	});
	s.setTogether(opts.together ?? true, opts.current ?? 0);
	return s;
}

beforeEach(() => {
	now = 10_000;
	sent = [];
	refreshed = [];
	main = decoder('main');
	vi.useFakeTimers();
});
afterEach(() => {
	vi.useRealTimers();
});

describe('frame routing', () => {
	test("a device that streams one display at a time: every frame goes to the session's screen", () => {
		const s = streams({ together: false });
		s.attach(1, decoder('one'), screen(), null);
		expect(s.decoderFor(0, 100)).toBe(main);
		expect(s.decoderFor(1, 100)).toBe(main);
		expect(s.decoderFor(7, 100)).toBe(main);
		expect(sent).toEqual([]);
	});

	test("each display's frames go to its own decoder; the session's screen gets only its display", () => {
		const s = streams();
		const one = decoder('one');
		const two = decoder('two');
		s.attach(1, one, screen(), null);
		s.attach(2, two, screen(), null);
		expect(s.decoderFor(0, 100)).toBe(main);
		expect(s.decoderFor(1, 100)).toBe(one);
		expect(s.decoderFor(2, 100)).toBe(two);
		// Nobody shows display 3
		expect(s.decoderFor(3, 100)).toBeNull();
		// Every display on its own: the session's screen shows none
		s.setMainDisplay(null);
		s.attach(0, decoder('zero'), screen(), null);
		expect(s.decoderFor(0, 100)).not.toBe(main);
	});

	test("a display's pictures reach its own screen; one shown no more drops its frames and closes its decoder", () => {
		const s = streams();
		const one = decoder('one');
		const sc = screen();
		s.attach(1, one, sc, null);
		one.onFrame?.(frame);
		expect(sc.frames).toEqual([frame]);
		s.detach(1);
		expect(one.closed).toBe(true);
		expect(one.onFrame).toBeNull();
		expect(s.decoderFor(1, 100)).toBeNull();
	});
});

describe('the displays the device streams', () => {
	test('it starts with the one it streams; a new display joins with a fresh picture', () => {
		const s = streams();
		expect(sent).toEqual([]);
		const one = decoder('one');
		s.attach(1, one, screen(), null);
		expect(sent).toEqual([[0, 1]]);
		expect(refreshed).toEqual([1]);
		expect(one._needKeyframe).toBe(true);
		// Nothing changed: nothing is sent again
		s.sync();
		expect(sent).toHaveLength(1);
	});

	test('a hidden or minimised window stops its display, showing it again brings it back', () => {
		const s = streams();
		s.attach(1, decoder('one'), screen(), null);
		s.setVisible(1, false);
		expect(sent.at(-1)).toEqual([0]);
		s.setMainVisible(false);
		expect(sent.at(-1)).toEqual([]);
		s.setVisible(1, true);
		s.setMainVisible(true);
		expect(sent.at(-1)).toEqual([0, 1]);
		expect(refreshed).toEqual([1, 1, 0]);
	});

	test('background displays: live keeps them, paused stops them', () => {
		const s = streams();
		s.setMainDisplay(null);
		for (const idx of [0, 1, 2]) s.attach(idx, decoder(String(idx)), screen(), null);
		s.setBackground(1, true);
		s.setBackground(2, true);
		expect(s.captureSet()).toEqual([0, 1, 2]);
		s.setBackgroundMode('paused');
		expect(sent.at(-1)).toEqual([0]);
		s.setBackgroundMode('live');
		expect(sent.at(-1)).toEqual([0, 1, 2]);
	});

	test('slow: a background display streams for one picture every few seconds', () => {
		const s = streams();
		s.setMainDisplay(null);
		const one = decoder('one');
		s.attach(0, decoder('zero'), screen(), null);
		s.attach(1, one, screen(), null);
		s.setBackground(1, true);
		s.setBackgroundMode('slow');
		expect(sent.at(-1)).toEqual([0]);
		vi.advanceTimersByTime(4000);
		expect(sent.at(-1)).toEqual([0, 1]);
		expect(refreshed.at(-1)).toBe(1);
		// Its picture arrived: it stops until the next one
		one.onFrame?.(frame);
		expect(sent.at(-1)).toEqual([0]);
		// A picture that never comes ends the turn too
		vi.advanceTimersByTime(4000);
		expect(sent.at(-1)).toEqual([0, 1]);
		vi.advanceTimersByTime(2500);
		expect(sent.at(-1)).toEqual([0]);
		// Back in front: it streams all the time
		s.setBackground(1, false);
		expect(sent.at(-1)).toEqual([0, 1]);
		const count = sent.length;
		vi.advanceTimersByTime(20_000);
		expect(sent).toHaveLength(count);
	});
});

describe('what each display costs', () => {
	test('bytes and pictures per display over the last second', () => {
		const s = streams();
		const one = decoder('one');
		one.getStats = () => ({ videoFps: 24 });
		main.getStats = () => ({ videoFps: 30 });
		s.attach(1, one, screen(), null);
		s.decoderFor(0, 50_000);
		s.decoderFor(1, 25_000);
		now += 600;
		s.decoderFor(1, 25_000);
		// The first frame of display 0 is more than a second old now
		now += 500;
		expect(s.traffic(3)).toEqual([
			{ idx: 0, fps: 30, kbps: 0, streaming: true, shown: 'main' },
			{ idx: 1, fps: 24, kbps: 200, streaming: true, shown: 'own' },
			{ idx: 2, fps: 0, kbps: 0, streaming: false, shown: 'none' }
		]);
	});
});
