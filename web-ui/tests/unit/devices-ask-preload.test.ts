/**
 * The device list fetches the device dialogs' code while nobody needs it
 * yet (preloadDialogs, ask.svelte.ts). Leaving the page cuts that fetch off,
 * and Firefox then fails the stylesheets it was loading ("Unable to preload
 * CSS for ..."). Nothing waits for a preload, so its failure is no error on
 * the page, and the first dialog fetches the code again (BUGS L314).
 */
import { afterEach, expect, test, vi } from 'vitest';

const load = vi.hoisted(() => ({ attempts: 0, failFirst: true }));

vi.mock('../../src/lib/devices/DeviceDialogs.svelte', () => {
	load.attempts++;
	if (load.failFirst && load.attempts === 1) {
		throw new Error('Unable to preload CSS for /_ui/immutable/assets/Terminal.css');
	}
	return { default: () => {} };
});
vi.mock('svelte', async (original) => ({
	...(await original<typeof import('svelte')>()),
	mount: vi.fn()
}));

const unhandled: unknown[] = [];
const onUnhandled = (reason: unknown) => unhandled.push(reason);
process.on('unhandledRejection', onUnhandled);
afterEach(() => process.off('unhandledRejection', onUnhandled));

test('a failed preload is no error, and the first dialog loads the code again', async () => {
	const { preloadDialogs, ask } = await import('../../src/lib/devices/ask.svelte.ts');
	const { mount } = await import('svelte');

	preloadDialogs();
	// Let the rejection settle and Node report it if nobody handled it
	await new Promise((done) => setTimeout(done, 20));
	expect(load.attempts).toBe(1);
	expect(unhandled, 'the failed preload reached the page as an error').toEqual([]);

	void ask('ban', { device: { id: 'A1001' } });
	await vi.waitFor(() => expect(vi.mocked(mount)).toHaveBeenCalledTimes(1));
	expect(load.attempts).toBe(2);
});
