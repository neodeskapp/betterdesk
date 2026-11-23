/**
 * Code loaded on demand (lazy, util/lazy.ts). Leaving the page cuts an
 * import off, and Firefox then rejects it ("error loading dynamically
 * imported module"). A preload nobody waits for must not turn that into an
 * error on the page, and no failed load may be kept: the next use loads
 * again (BUGS L314, L422).
 */
import { afterEach, beforeEach, expect, test } from 'vitest';
import { lazy } from '../../src/lib/util/lazy.ts';
import { dismissAll, toasts } from '../../src/lib/ui/toasts.svelte.ts';

const unhandled: unknown[] = [];
const onUnhandled = (reason: unknown) => unhandled.push(reason);
beforeEach(() => {
	unhandled.length = 0;
	process.on('unhandledRejection', onUnhandled);
});
afterEach(() => {
	process.off('unhandledRejection', onUnhandled);
	dismissAll();
});

/** A loader whose first `failures` calls fail the way an aborted import does */
function loader(failures: number) {
	const calls = { n: 0 };
	const load = () => {
		calls.n++;
		return calls.n <= failures
			? Promise.reject(new TypeError('error loading dynamically imported module'))
			: Promise.resolve({ default: 'the module' });
	};
	return { calls, load };
}

/** Let rejections settle and Node report the ones nobody handled */
const settle = () => new Promise((done) => setTimeout(done, 20));

test('a failed preload is no error, and the use loads again', async () => {
	const { calls, load } = loader(1);
	const chunk = lazy(load);
	chunk.preload();
	await settle();
	expect(calls.n).toBe(1);
	expect(unhandled, 'the failed preload reached the page as an error').toEqual([]);
	await expect(chunk.load()).resolves.toEqual({ default: 'the module' });
	expect(calls.n).toBe(2);
});

test('a use shares the preload and keeps what loaded', async () => {
	const { calls, load } = loader(0);
	const chunk = lazy(load);
	chunk.preload();
	chunk.preload();
	await expect(chunk.load()).resolves.toEqual({ default: 'the module' });
	await expect(chunk.load()).resolves.toEqual({ default: 'the module' });
	expect(calls.n).toBe(1);
});

test('a failed use rejects for its caller, and the next use loads again', async () => {
	const { calls, load } = loader(1);
	const chunk = lazy(load);
	await expect(chunk.load()).rejects.toThrow('error loading dynamically imported module');
	await expect(chunk.load()).resolves.toEqual({ default: 'the module' });
	expect(calls.n).toBe(2);
});

test('a dialog whose code fails to load closes with a message, raises nothing, and opens again', async () => {
	const { calls, load } = loader(1);
	const chunk = lazy(load);
	let closed = 0;
	let shown: unknown = null;
	void chunk.open(() => closed++).then((m) => (shown = m));
	await settle();
	expect(closed).toBe(1);
	expect(shown, 'a failed load showed something').toBeNull();
	expect(unhandled, 'the failed load reached the page as an error').toEqual([]);
	expect(toasts.items.map((x) => x.type)).toEqual(['error']);

	await expect(chunk.open(() => closed++)).resolves.toEqual({ default: 'the module' });
	expect(closed).toBe(1);
	expect(calls.n).toBe(2);
});

test('a part the page can do without shows nothing when it fails, raises nothing, and loads again', async () => {
	const { calls, load } = loader(1);
	const chunk = lazy(load);
	let shown: unknown = null;
	void chunk.optional().then((m) => (shown = m));
	await settle();
	expect(shown, 'a failed load showed something').toBeNull();
	expect(unhandled, 'the failed load reached the page as an error').toEqual([]);
	expect(toasts.items, 'a part nobody asked for told about its failure').toEqual([]);

	await expect(chunk.optional()).resolves.toEqual({ default: 'the module' });
	expect(calls.n).toBe(2);
});
