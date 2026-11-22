/**
 * long.js stays out of the web app: protobuf.js asks for it, the viewer
 * reads 64-bit fields as numbers, and long.js starts a WebAssembly module
 * when it loads, which the console's CSP refuses (a "wasm-eval" violation
 * on every viewer page).
 */
import { expect, test } from 'vitest';

test('"long" resolves to the empty stand-in, not long.js', async () => {
	const m: Record<string, unknown> = await import('long');
	expect(m.default).toBeUndefined();
	expect(Object.keys(m)).toEqual([]);
});
