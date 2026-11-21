/**
 * The wrapped RustDesk client (static/rdclient/client.js) against a fake
 * peer: messages go in through the relay handler, what the client sends
 * is caught at the relay socket. No rendezvous, relay or device runs.
 */
import { beforeEach, describe, expect, test, vi } from 'vitest';
import { loadScripts, RD_CORE, type Loaded } from './rdclient-scripts.ts';

const g: Loaded = loadScripts(
	[
		...RD_CORE,
		'video.js',
		'audio.js',
		'renderer.js',
		'keyboard-scancode.js',
		'keyboard-encoder.js',
		'input.js',
		'compress.js',
		'clipboard.js',
		'filetransfer.js',
		'client.js'
	],
	['RDClient', 'RDCrypto', 'RDClipboard', 'nacl']
);

beforeEach(() => {
	// jsdom has no 2D canvas; the renderer copes with a missing context
	vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(null);
});

async function client(opts: Record<string, unknown> = {}) {
	const c = new g.RDClient(document.createElement('canvas'), { deviceId: '900100012', ...opts });
	await c.proto.load();
	return c;
}

describe('watch-only guest', () => {
	test('asks the device to ignore the keyboard and never starts input capture', async () => {
		const c = await client({ viewOnly: true });
		expect(c.viewOnly).toBe(true);
		const sent: Record<string, unknown>[] = [];
		c._sendPeerMessage = (m: Record<string, unknown>) => sent.push(m);
		await c.authenticate('pw');
		const login = sent.find((m) => m.loginRequest) as { loginRequest: { option: Record<string, unknown> } };
		expect(login.loginRequest.option.disableKeyboard).toBe(2);

		const start = vi.spyOn(c.input, 'start');
		c._state = 'streaming';
		c.setSessionActive(true);
		c.setViewOnly(false);
		expect(start).not.toHaveBeenCalled();
		expect(c.viewOnly).toBe(true);
	});

	test('an operator session leaves the keyboard option out', async () => {
		const c = await client();
		const sent: Record<string, unknown>[] = [];
		c._sendPeerMessage = (m: Record<string, unknown>) => sent.push(m);
		await c.authenticate('pw');
		const login = sent.find((m) => m.loginRequest) as { loginRequest: { option: Record<string, unknown> } };
		expect(login.loginRequest.option.disableKeyboard).toBeUndefined();
	});
});
