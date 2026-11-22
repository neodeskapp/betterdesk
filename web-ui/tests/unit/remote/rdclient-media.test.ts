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
