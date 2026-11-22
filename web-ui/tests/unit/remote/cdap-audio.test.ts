/**
 * The audio stream of a CDAP device (src/lib/remote/client/cdap-audio.ts):
 * the operator's microphone goes to the device only while the session runs.
 * The socket, the audio context and the microphone are fakes.
 */
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { CDAPAudio } from '../../../src/lib/remote/client/cdap-audio.ts';

class FakeSocket {
	static OPEN = 1;
	static last: FakeSocket | null = null;
	readyState = 1;
	sent: string[] = [];
	onopen: (() => void) | null = null;
	onmessage: ((ev: { data: string }) => void) | null = null;
	onclose: (() => void) | null = null;
	onerror: (() => void) | null = null;
	constructor() {
		FakeSocket.last = this;
	}
	send(data: string) {
		this.sent.push(data);
	}
	close() {
		this.readyState = 3;
	}
}

class FakeAudioContext {
	state = 'running';
	destination = {};
	createGain() {
		return { gain: { value: 1 }, connect() {} };
	}
	createMediaStreamSource() {
		return { connect() {} };
	}
	createScriptProcessor() {
		return { connect() {}, disconnect() {}, onaudioprocess: null };
	}
	resume() {}
	close() {
		this.state = 'closed';
		return Promise.resolve();
	}
}

let grant: (stream: MediaStream) => void = () => {};
const track = { stop: vi.fn() };
const microphone = { getTracks: () => [track] } as unknown as MediaStream;

beforeEach(() => {
	track.stop.mockClear();
	document.body.innerHTML =
		'<div id="wval-w1"><span class="cdap-audio-status"></span><div class="cdap-audio-level-fill"></div></div>';
	vi.stubGlobal('WebSocket', FakeSocket);
	vi.stubGlobal('AudioContext', FakeAudioContext);
	Object.defineProperty(navigator, 'mediaDevices', {
		configurable: true,
		value: {
			// The browser's permission prompt is open until the test grants the microphone
			getUserMedia: () => new Promise<MediaStream>((resolve) => (grant = resolve))
		}
	});
});

afterEach(() => {
	CDAPAudio.close('dev1', 'w1');
	vi.unstubAllGlobals();
	Reflect.deleteProperty(navigator, 'mediaDevices');
});

describe('CDAP audio: the operator microphone', () => {
	test('is let go when the session ended while the browser asked for it', async () => {
		CDAPAudio.open('dev1', 'w1', { direction: 'bidirectional' });
		FakeSocket.last!.onmessage!({ data: JSON.stringify({ type: 'ready', session_id: 's1' }) });
		CDAPAudio.close('dev1', 'w1');
		grant(microphone);
		await vi.waitFor(() => expect(track.stop).toHaveBeenCalled());
	});

	test('stays on while the session runs, and is let go when it ends', async () => {
		CDAPAudio.open('dev1', 'w1', { direction: 'bidirectional' });
		FakeSocket.last!.onmessage!({ data: JSON.stringify({ type: 'ready', session_id: 's1' }) });
		grant(microphone);
		await new Promise((r) => setTimeout(r, 0));
		expect(track.stop).not.toHaveBeenCalled();
		CDAPAudio.close('dev1', 'w1');
		expect(track.stop).toHaveBeenCalled();
	});
});
