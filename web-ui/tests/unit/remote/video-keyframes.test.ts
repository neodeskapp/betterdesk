/**
 * The viewer's WebCodecs path (src/lib/remote/client/video.ts): every delta
 * frame builds on the one before it, so after the decoder skips a frame it
 * must wait for a keyframe and ask the device for one. Feeding the next delta
 * instead leaves a smeared picture where only changed areas update.
 * jsdom has no WebCodecs; the decoder below records what it is given.
 */
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

const decoders = vi.hoisted(() => [] as FakeDecoder[]);

class FakeDecoder {
	state = 'unconfigured';
	decodeQueueSize = 0;
	fed: string[] = [];
	constructor() {
		decoders.push(this);
	}
	static async isConfigSupported(config: VideoDecoderConfig) {
		return { supported: true, config };
	}
	configure() {
		this.state = 'configured';
	}
	decode(chunk: { type: string }) {
		this.fed.push(chunk.type);
	}
	close() {
		this.state = 'closed';
	}
	async flush() {}
}

beforeEach(() => {
	decoders.length = 0;
	vi.stubGlobal('VideoDecoder', FakeDecoder);
	vi.stubGlobal(
		'EncodedVideoChunk',
		class {
			type: string;
			constructor(init: { type: string }) {
				this.type = init.type;
			}
		}
	);
});

afterEach(() => {
	vi.unstubAllGlobals();
	vi.restoreAllMocks();
});

const { RDVideo } = await import('../../../src/lib/remote/client/video.ts');

const key = { data: new Uint8Array([1, 2, 3]), key: true };
const delta = { data: new Uint8Array([4, 5, 6]), key: false };

async function started() {
	const v = new RDVideo();
	const asked = vi.fn();
	v.onNeedKeyframe = asked;
	await v.init('vp9');
	await v.decode(key);
	asked.mockClear();
	return { v, d: decoders[0]!, asked };
}

describe('a decoder that falls behind', () => {
	test('skips to the next keyframe instead of decoding deltas after a dropped one', async () => {
		const { v, d, asked } = await started();
		d.decodeQueueSize = 31;
		await v.decode(delta);
		expect(asked).toHaveBeenCalledTimes(1);
		d.decodeQueueSize = 0;
		await v.decode(delta);
		await v.decode(delta);
		expect(d.fed).toEqual(['key']);
		await v.decode(key);
		await v.decode(delta);
		expect(d.fed).toEqual(['key', 'key', 'delta']);
		v.close();
	});

	test('asks again while it waits, at most once a second', async () => {
		let now = 0;
		vi.spyOn(performance, 'now').mockImplementation(() => now);
		const { v, d, asked } = await started();
		now = 5000;
		d.decodeQueueSize = 31;
		await v.decode(delta);
		d.decodeQueueSize = 0;
		now = 5500;
		await v.decode(delta);
		expect(asked).toHaveBeenCalledTimes(1);
		now = 6100;
		await v.decode(delta);
		expect(asked).toHaveBeenCalledTimes(2);
		v.close();
	});
});

describe('a decoder in the background', () => {
	test('resumes from a keyframe, not from the deltas after the frames it skipped', async () => {
		const { v, d } = await started();
		v.setBackgroundMode(true);
		await v.decode(delta);
		v.setBackgroundMode(false);
		await v.decode(delta);
		expect(d.fed).toEqual(['key']);
		await v.decode(key);
		expect(d.fed).toEqual(['key', 'key']);
		v.close();
	});
});
