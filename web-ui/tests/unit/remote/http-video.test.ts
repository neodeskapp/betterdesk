/**
 * The viewer on a console opened over plain HTTP from another computer: no
 * secure context, so no WebCodecs, and the only video it can play is
 * H.264 through JMuxer. jsdom has no VideoDecoder, which is that case. A
 * device without an H.264 encoder sends VP9 whatever the viewer asks for
 * (RustDesk takes VP9 as the floor), so the session must end with a
 * message about HTTPS instead of a black screen.
 */
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

vi.mock('jmuxer', () => ({
	default: class {
		feed() {}
		destroy() {}
	}
}));

const { RDClient } = await import('../../../src/lib/remote/client/client.ts');
const { RDProtocol } = await import('../../../src/lib/remote/client/protocol.ts');
const { classifyEnd } = await import('../../../src/lib/remote/states.ts');
const { LoginResponse, VideoFrame } = await import('../../../src/lib/remote/client/generated/protos.ts');

beforeEach(() => {
	vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(null);
	vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => {});
	vi.spyOn(HTMLMediaElement.prototype, 'play').mockResolvedValue(undefined);
	vi.spyOn(console, 'error').mockImplementation(() => {});
	vi.spyOn(console, 'warn').mockImplementation(() => {});
});

afterEach(() => vi.restoreAllMocks());

async function client() {
	const c = new RDClient(document.createElement('canvas'), { deviceId: '900100012', disableAudio: true });
	await c.proto.load();
	c.conn.sendRelay = () => {};
	vi.spyOn(c.renderer, 'startRenderLoop').mockImplementation(() => {});
	const errors: string[] = [];
	c.on('error', (msg: unknown) => errors.push(String(msg)));
	return { c, errors };
}

const frameOf = (codec: 'vp9s' | 'h264s') =>
	VideoFrame.fromObject({ [codec]: { frames: [{ data: new Uint8Array([0, 0, 0, 1, 9]), key: true }] } });

describe('what the viewer tells the device', () => {
	test('it never prefers a codec it cannot play: a saved VP9 choice is not sent over HTTP', async () => {
		const proto = new RDProtocol();
		await proto.load();
		const abilities = { vp9: false, h264: true, av1: false, vp8: false, h265: false };
		const decoding = proto.buildSupportedDecoding({
			abilities,
			prefer: 'VP9',
			hasWebCodecs: false,
			hasJMuxer: true
		});
		expect(decoding.abilityVp9).toBe(0);
		expect(decoding.abilityH264).toBe(1);
		expect(proto.preferCodecValue('VP9')).not.toBe(decoding.prefer);
		// The device takes a VP9 preference without looking at the abilities
		expect([proto.preferCodecValue('H264'), proto.preferCodecValue('Auto')]).toContain(decoding.prefer);
	});

	test('a codec it can play stays the preference', async () => {
		const proto = new RDProtocol();
		await proto.load();
		const abilities = { vp9: true, h264: true, av1: false, vp8: false, h265: false };
		const decoding = proto.buildSupportedDecoding({
			abilities,
			prefer: 'VP9',
			hasWebCodecs: true,
			hasJMuxer: true
		});
		expect(decoding.prefer).toBe(proto.preferCodecValue('VP9'));
	});
});

describe('a device that sends video the viewer cannot play', () => {
	test('a VP9 frame ends the session with a message about HTTPS', async () => {
		const { c, errors } = await client();
		c._startSession();
		await c._handleVideoFrame(frameOf('vp9s'));
		expect(c.state).toBe('error');
		expect(errors).toHaveLength(1);
		expect(classifyEnd(errors[0], null, { name: 'ndlab-linux', everStreamed: true }).kind).toBe('https');
	});

	test('an H.264 frame goes on to the decoder', async () => {
		const { c, errors } = await client();
		c._startSession();
		await c._handleVideoFrame(frameOf('h264s'));
		expect(c.state).toBe('streaming');
		expect(errors).toEqual([]);
		expect(c.video.currentCodec).toBe('h264');
		c.disconnect();
	});

	test('a device that says it has no H.264 encoder is stopped at sign-in, before the screen', async () => {
		const { c, errors } = await client();
		c._handleLoginResponse(
			LoginResponse.fromObject({
				peerInfo: { hostname: 'ndlab-linux', encoding: { vp8: true, h264: false } }
			})
		);
		expect(c.state).toBe('error');
		expect(classifyEnd(errors[0], null, { name: 'ndlab-linux', everStreamed: false }).kind).toBe('https');
	});

	test('a device with an H.264 encoder starts the session', async () => {
		const { c, errors } = await client();
		c._handleLoginResponse(
			LoginResponse.fromObject({ peerInfo: { hostname: 'ndlab-win11', encoding: { vp8: true, h264: true } } })
		);
		expect(c.state).toBe('streaming');
		expect(errors).toEqual([]);
		c.disconnect();
	});
});
