import { afterEach, expect, test, vi } from 'vitest';

const loaded = vi.hoisted(() => ({ video: 0 }));

vi.mock('../../../src/lib/remote/client/video.ts', () => {
	loaded.video++;
	return { RDVideo: function RDVideo() {} };
});

afterEach(() => {
	vi.unstubAllGlobals();
});

test('the H.264 helper is the remote viewer video module, loaded only when MSE is the way to decode', async () => {
	vi.stubGlobal('MediaSource', { isTypeSupported: () => true });
	const { loadMseH264 } = await import('../../../src/lib/devices-area/cdap/desktop.ts');
	expect(loaded.video).toBe(0);
	await expect(loadMseH264()).resolves.toBe(true);
	expect(loaded.video).toBe(1);
	// No classic scripts any more
	expect(document.head.querySelectorAll('script')).toHaveLength(0);
	await expect(loadMseH264()).resolves.toBe(true);
	expect(loaded.video).toBe(1);
});

test('a browser without MSE H.264 does not load it', async () => {
	vi.resetModules();
	loaded.video = 0;
	vi.stubGlobal('MediaSource', { isTypeSupported: () => false });
	const { loadMseH264 } = await import('../../../src/lib/devices-area/cdap/desktop.ts');
	await expect(loadMseH264()).resolves.toBe(false);
	expect(loaded.video).toBe(0);
});
