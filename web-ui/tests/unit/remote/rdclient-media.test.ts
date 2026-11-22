/**
 * The RustDesk session's screen, cursor, sound and recording
 * (src/lib/remote/client/client.ts) once it streams. jsdom has no canvas
 * drawing, audio or media recorder: the parts the client touches are fakes.
 */
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { RDClient } from '../../../src/lib/remote/client/client.ts';

beforeEach(() => {
	vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(null);
});

afterEach(() => {
	vi.unstubAllGlobals();
	vi.restoreAllMocks();
});

/** Makes RDAudio.isSupported() true; the tests replace what would touch it */
class SomeAudio {
	state = 'suspended';
}

async function streaming(opts: Record<string, unknown> = {}) {
	const frame = document.createElement('div');
	const canvas = document.createElement('canvas');
	frame.append(canvas);
	const c = new RDClient(canvas, { deviceId: '900100012', disableAudio: true, ...opts });
	await c.proto.load();
	c.conn.sendRelay = () => {};
	vi.spyOn(c.renderer, 'startRenderLoop').mockImplementation(() => {});
	c._startSession();
	return { c, frame, canvas };
}

describe('the device cursor', () => {
	test("once the device sent its cursor, the screen hides the operator's crosshair", async () => {
		const s = await streaming();
		expect(s.frame.classList.contains('has-remote-cursor')).toBe(false);
		s.c.renderer.onCursorReady?.(true);
		expect(s.frame.classList.contains('has-remote-cursor')).toBe(true);
		s.c.disconnect();
	});
});

describe('recording the session', () => {
	test("takes the device's sound along with the screen", async () => {
		const s = await streaming();
		const tracks: unknown[] = [];
		const screen = { addTrack: (t: unknown) => tracks.push(t), getTracks: () => tracks };
		Object.defineProperty(s.canvas, 'captureStream', { configurable: true, value: () => screen });
		class FakeRecorder {
			static isTypeSupported = () => true;
			state = 'recording';
			mimeType = 'video/webm';
			constructor(public stream: unknown) {}
			start() {}
			stop() {}
		}
		vi.stubGlobal('MediaRecorder', FakeRecorder);
		const sound = { kind: 'audio' };
		const destination = { stream: { getAudioTracks: () => [sound] } };
		const gainConnect = vi.fn();
		s.c.audio.audioCtx = {
			state: 'running',
			createMediaStreamDestination: () => destination
		} as unknown as AudioContext;
		s.c.audio.gainNode = { connect: gainConnect } as unknown as GainNode;
		expect(s.c.startRecording()).toBe(true);
		expect(gainConnect).toHaveBeenCalledWith(destination);
		expect(tracks).toEqual([sound]);
		s.c.audio.audioCtx = null;
		s.c.audio.gainNode = null;
		s.c.disconnect();
	});
});

describe("the device's sound format", () => {
	test('the sample rate and channels the device announces are the ones the sound plays with', async () => {
		vi.stubGlobal('AudioContext', SomeAudio);
		const s = await streaming({ disableAudio: false });
		vi.spyOn(s.c.audio, 'init').mockResolvedValue(undefined);
		const configure = vi.spyOn(s.c.audio, 'configure').mockResolvedValue(undefined);
		const wire = s.c.proto.serializeMessage({ misc: { audioFormat: { sampleRate: 44100, channels: 1 } } });
		s.c._dispatchMessage(s.c.proto.decodeMessage(wire));
		expect(configure).toHaveBeenCalledWith({ sampleRate: 44100, channels: 1 });
		s.c.disconnect();
	});

	test('a session with sound off does not start the sound for it', async () => {
		vi.stubGlobal('AudioContext', SomeAudio);
		const s = await streaming({ disableAudio: true });
		const configure = vi.spyOn(s.c.audio, 'configure').mockResolvedValue(undefined);
		const wire = s.c.proto.serializeMessage({ misc: { audioFormat: { sampleRate: 44100, channels: 1 } } });
		s.c._dispatchMessage(s.c.proto.decodeMessage(wire));
		expect(configure).not.toHaveBeenCalled();
		s.c.disconnect();
	});
});

describe('the relay socket fails', () => {
	test('the session ends with a reason in words, not "undefined" or "[object Event]"', async () => {
		const c = new RDClient(document.createElement('canvas'), { deviceId: '900100012' });
		vi.spyOn(c.conn, 'connectRendezvous').mockResolvedValue({} as WebSocket);
		vi.spyOn(c.conn, 'sendRendezvous').mockImplementation(() => {});
		vi.spyOn(c.conn, 'closeRendezvous').mockImplementation(() => {});
		vi.spyOn(c.conn, 'connectRelay').mockResolvedValue({} as WebSocket);
		vi.spyOn(c.conn, 'sendRelay').mockImplementation(() => {});
		vi.spyOn(c, '_waitForRendezvousResponse').mockResolvedValue({
			uuid: '1b4e28ba-2fa1-41d2-883f-0016d3cca427',
			relayServer: 'relay.example.com:21117'
		});
		const ended: [string, { error?: string }][] = [];
		c.on('disconnected', (reason: string, info: { error?: string }) => ended.push([reason, info]));
		await c.connect();
		c.conn._emit('relay:error', new Event('error'));
		expect(ended).toHaveLength(1);
		const [reason, info] = ended[0]!;
		expect(reason).toMatch(/^Relay error: /);
		expect(reason).not.toMatch(/undefined/);
		expect(info.error).not.toBe('[object Event]');
		expect(info.error).toBeTruthy();
	});
});
