/**
 * The viewer's video path without WebCodecs (src/lib/remote/client/video.ts):
 * H.264 goes through JMuxer into a hidden video element. jsdom has neither
 * WebCodecs nor MSE; JMuxer is a fake that records what it is given.
 */
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

const muxers = vi.hoisted(() => [] as { options: Record<string, unknown>; fed: unknown[] }[]);
vi.mock('jmuxer', () => ({
	default: class {
		fed: unknown[] = [];
		constructor(public options: Record<string, unknown>) {
			muxers.push(this);
		}
		feed(data: unknown) {
			this.fed.push(data);
		}
		destroy() {}
	}
}));

const { RDVideo } = await import('../../../src/lib/remote/client/video.ts');

beforeEach(() => {
	muxers.length = 0;
	vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => {});
	vi.spyOn(HTMLMediaElement.prototype, 'play').mockResolvedValue(undefined);
});

afterEach(() => vi.restoreAllMocks());

describe('the JMuxer path', () => {
	test('lets JMuxer remove played video, so the media buffer does not grow for the whole session', async () => {
		const v = new RDVideo();
		await v.init('h264');
		expect(v.fallbackMode).toBe(true);
		expect(muxers[0]?.options.clearBuffer).toBe(true);
		v.close();
	});
});

describe('the hidden video element', () => {
	const elementErrors = (spy: { mock: { calls: unknown[][] } }) =>
		spy.mock.calls.filter((c) => String(c[0]).includes('Video element error'));

	test('closing the path reports no error for the element it lets go', async () => {
		const logged = vi.spyOn(console, 'error').mockImplementation(() => {});
		const v = new RDVideo();
		await v.init('h264');
		const el = document.getElementById('rd-jmuxer-video')!;
		v.close();
		el.dispatchEvent(new Event('error'));
		expect(elementErrors(logged)).toEqual([]);
	});

	test("after a restart of JMuxer, only the new element's errors are reported, once", async () => {
		const logged = vi.spyOn(console, 'error').mockImplementation(() => {});
		const v = new RDVideo();
		await v.init('h264');
		const old = document.getElementById('rd-jmuxer-video')!;
		v._reinitJMuxer();
		const current = document.getElementById('rd-jmuxer-video')!;
		expect(current).not.toBe(old);
		old.dispatchEvent(new Event('error'));
		expect(elementErrors(logged)).toEqual([]);
		current.dispatchEvent(new Event('error'));
		expect(elementErrors(logged)).toHaveLength(1);
		v.close();
	});
});

describe('codecs JMuxer cannot play', () => {
	test('frames of another codec than H.264 are dropped, not fed to JMuxer', async () => {
		vi.spyOn(console, 'warn').mockImplementation(() => {});
		const v = new RDVideo();
		await v.init('vp9');
		v._jmuxerReady = true;
		await v.decode({ data: new Uint8Array([1, 2, 3, 4]), key: true });
		expect(muxers[0]?.fed).toEqual([]);
		expect(v.droppedFrames).toBe(1);
		v.close();
	});

	test('H.264 frames go to JMuxer', async () => {
		const v = new RDVideo();
		await v.init('h264');
		v._jmuxerReady = true;
		await v.decode({ data: new Uint8Array([0, 0, 0, 1, 0x65]), key: true });
		expect(muxers[0]?.fed).toHaveLength(1);
		v.close();
	});
});
