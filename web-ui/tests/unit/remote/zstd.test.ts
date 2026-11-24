/**
 * The viewer's own zstd decoder against Node's zstd encoder (the same
 * library RustDesk links): text, pictures, random bytes, long inputs over
 * several blocks, every level, checksums, several frames in a row, and
 * the cap. Browsers have no zstd stream, so clipboard entries and file
 * blocks a remote computer compresses go through this decoder.
 */
import zlib from 'node:zlib';
import { describe, expect, test } from 'vitest';
import { ZstdTooLarge, zstdDecompress } from '../../../src/lib/remote/client/zstd.ts';
import { RDCompress } from '../../../src/lib/remote/client/compress.ts';

function zstd(data: Uint8Array, level = 3, checksum = false): Uint8Array {
	const params: Record<number, number> = { [zlib.constants.ZSTD_c_compressionLevel]: level };
	if (checksum) params[zlib.constants.ZSTD_c_checksumFlag] = 1;
	return new Uint8Array(zlib.zstdCompressSync(data, { params }));
}

/** Byte for byte (toEqual on typed arrays also compares the size of the memory behind them) */
function same(a: Uint8Array, b: Uint8Array): boolean {
	return (
		Buffer.compare(
			Buffer.from(a.buffer, a.byteOffset, a.length),
			Buffer.from(b.buffer, b.byteOffset, b.length)
		) === 0
	);
}

/** Deterministic bytes: a little generator, so a failure repeats */
function noise(n: number, seed = 7): Uint8Array {
	const out = new Uint8Array(n);
	let x = seed;
	for (let i = 0; i < n; i++) {
		x = (Math.imul(x, 1103515245) + 12345) >>> 0;
		out[i] = x >>> 24;
	}
	return out;
}

const text = new TextEncoder().encode(
	Array.from(
		{ length: 400 },
		(_, i) =>
			`Line ${i}: The remote computer copied this paragraph, with names like NeoDesk, numbers like ${i * 37} and words that come back.\n`
	).join('')
);

/** An RGBA screenshot: flat areas, gradients and a few edges, as a clipboard image looks */
function screenshot(w: number, h: number): Uint8Array {
	const out = new Uint8Array(w * h * 4);
	for (let y = 0; y < h; y++)
		for (let x = 0; x < w; x++) {
			const i = (y * w + x) * 4;
			const panel = x > w / 3 && y > h / 4;
			out[i] = panel ? 240 : (x * 3) & 255;
			out[i + 1] = panel ? 240 : (y * 2) & 255;
			out[i + 2] = panel ? 245 : 80;
			out[i + 3] = 255;
		}
	return out;
}

describe('zstd decoder', () => {
	test.each([1, 3, 9, 19, 22])('text at level %i', (level) => {
		expect(same(zstdDecompress(zstd(text, level), 1 << 26), text)).toBe(true);
	});

	test.each([1, 3, 19])('a screenshot over several blocks at level %i', (level) => {
		const image = screenshot(320, 240);
		const packed = zstd(image, level);
		expect(packed.length).toBeLessThan(image.length / 4);
		expect(same(zstdDecompress(packed, 1 << 26), image)).toBe(true);
	});

	test('random bytes (raw blocks) and short inputs', () => {
		for (const n of [0, 1, 5, 200, 70_000, 300_000]) {
			const data = noise(n, n + 1);
			expect(same(zstdDecompress(zstd(data), 1 << 26), data)).toBe(true);
		}
	});

	test('runs of one byte (RLE blocks and RLE sequences)', () => {
		const data = new Uint8Array(500_000).fill(0x41);
		data.set(noise(64), 250_000);
		expect(same(zstdDecompress(zstd(data, 3), 1 << 26), data)).toBe(true);
		expect(same(zstdDecompress(zstd(data, 19), 1 << 26), data)).toBe(true);
	});

	test('mixed content: text, then noise, then the text again (repeat offsets far back)', () => {
		const data = new Uint8Array([...text, ...noise(5000, 3), ...text.subarray(0, 20_000)]);
		for (const level of [1, 6, 15]) expect(same(zstdDecompress(zstd(data, level), 1 << 26), data)).toBe(true);
	});

	test('a frame with a content checksum, and two frames in a row', () => {
		const a = new TextEncoder().encode('first frame '.repeat(500));
		const b = screenshot(40, 40);
		const both = new Uint8Array([...zstd(a, 5, true), ...zstd(b, 2)]);
		expect(same(zstdDecompress(both, 1 << 26), new Uint8Array([...a, ...b]))).toBe(true);
	});

	test('the same output as Node for many sizes of text', () => {
		for (let n = 1; n < 6000; n += 397) {
			const slice = text.subarray(0, n);
			expect(same(zstdDecompress(zstd(slice, (n % 19) + 1), 1 << 26), slice)).toBe(true);
		}
	});

	test('stops past the cap, and refuses what is not zstd', () => {
		const big = zstd(new Uint8Array(3_000_000));
		expect(() => zstdDecompress(big, 1_000_000)).toThrow(ZstdTooLarge);
		expect(() => zstdDecompress(new Uint8Array([1, 2, 3, 4, 5]), 100)).toThrow(/not a zstd frame/);
		const damaged = zstd(text).slice(0, 200);
		expect(() => zstdDecompress(damaged, 1 << 26)).toThrow();
	});

	test('RDCompress inflates with it where the browser has no zstd stream, and keeps the cap', async () => {
		const packed = zstd(text);
		expect(same(await RDCompress.decompressZstd(packed, { force: true }), text)).toBe(true);
		await expect(RDCompress.decompressZstd(packed, { force: true, maxBytes: 1000 })).rejects.toMatchObject({
			tooLarge: true
		});
	});
});
