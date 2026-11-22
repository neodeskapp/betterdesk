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
