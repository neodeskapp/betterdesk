/**
 * Zstandard decompression (RFC 8878) for browsers whose
 * DecompressionStream has no "zstd": Chromium and Firefox have none. A
 * remote computer compresses its clipboard and file blocks with zstd
 * whenever that makes them smaller, so without this the viewer drops them.
 *
 * Frames with raw, RLE and compressed blocks, Huffman literals (one or
 * four streams, the tree kept for later blocks), FSE sequences in all four
 * modes, repeat offsets, skippable frames and several frames in a row. No
 * dictionaries (RustDesk uses none); the content checksum is skipped. The
 * output stops with an error past maxBytes.
 */

/** The error of an output that grows past maxBytes (RDCompress marks it tooLarge) */
export class ZstdTooLarge extends Error {
	tooLarge = true;
}

const MAGIC = 0xfd2fb528;

/* ── Bits ── */

/** Bits of a little-endian bit stream: bit i is bit (i & 7) of byte (i >> 3) */
function bitsAt(data: Uint8Array, start: number, end: number, offset: number, n: number): number {
	if (n === 0) return 0;
	if (n > 24) {
		const low = bitsAt(data, start, end, offset, 16);
		return low + bitsAt(data, start, end, offset + 16, n - 16) * 65536;
	}
	let p = start + (offset >> 3);
	const shift = offset & 7;
	let v = 0;
	for (let i = 0; i < 4; i++, p++) v |= (p < end ? (data[p] ?? 0) : 0) << (8 * i);
	return ((v >>> shift) & ((1 << n) - 1)) >>> 0;
}

/** A bit stream read from its end (FSE and Huffman streams): the last byte's highest set bit marks the start */
class BackBits {
	private data: Uint8Array;
	private start: number;
	private end: number;
	/** Bits left; below 0 once more were read than the stream has */
	left: number;

	constructor(data: Uint8Array, start: number, end: number) {
		if (end <= start) throw new Error('zstd: empty bit stream');
		const last = data[end - 1] ?? 0;
		if (last === 0) throw new Error('zstd: bit stream without its end mark');
		this.data = data;
		this.start = start;
		this.end = end;
		this.left = (end - start) * 8 - (8 - (31 - Math.clz32(last)));
	}

	read(n: number): number {
		const v = this.peek(n);
		this.left -= n;
		return v;
	}

	peek(n: number): number {
		if (n === 0) return 0;
		if (this.left >= n) return bitsAt(this.data, this.start, this.end, this.left - n, n);
		// Past the start the stream reads zeros
		if (this.left <= 0) return 0;
		return bitsAt(this.data, this.start, this.end, 0, this.left) * 2 ** (n - this.left);
	}
}

/* ── FSE ── */

interface FseTable {
	log: number;
	symbol: Uint8Array;
	nbBits: Uint8Array;
	base: Uint16Array;
}

function highBit(v: number): number {
	return 31 - Math.clz32(v);
}

function buildFse(probs: number[], log: number): FseTable {
	const size = 1 << log;
	const symbol = new Uint8Array(size);
	const nbBits = new Uint8Array(size);
	const base = new Uint16Array(size);
	const next = new Array<number>(probs.length).fill(0);
	let high = size - 1;
	for (let s = 0; s < probs.length; s++) {
		if (probs[s] === -1) {
			symbol[high--] = s;
			next[s] = 1;
		} else next[s] = probs[s] ?? 0;
	}
	const step = (size >> 1) + (size >> 3) + 3;
	const mask = size - 1;
	let pos = 0;
	for (let s = 0; s < probs.length; s++) {
		for (let i = 0; i < (probs[s] ?? 0); i++) {
			symbol[pos] = s;
			do pos = (pos + step) & mask;
			while (pos > high);
		}
	}
	if (pos !== 0) throw new Error('zstd: bad FSE distribution');
	for (let u = 0; u < size; u++) {
		const s = symbol[u] ?? 0;
		const state = next[s] ?? 0;
		next[s] = state + 1;
		const bits = log - highBit(state);
		nbBits[u] = bits;
		base[u] = (state << bits) - size;
	}
	return { log, symbol, nbBits, base };
}

/** An FSE table description at data[pos]; returns the table and the bytes it took */
function readFse(
	data: Uint8Array,
	pos: number,
	end: number,
	maxSymbol: number,
	maxLog: number
): [FseTable, number] {
	let bit = 0;
	const get = (n: number) => bitsAt(data, pos, end, bit, n);
	const log = get(4) + 5;
	bit += 4;
	if (log > maxLog) throw new Error('zstd: FSE accuracy too high');
	let remaining = (1 << log) + 1;
	let threshold = 1 << log;
	let nbBits = log + 1;
	const probs: number[] = [];
	let previous0 = false;
	while (remaining > 1 && probs.length <= maxSymbol) {
		if (previous0) {
			let zeros = 0;
			for (;;) {
				const r = get(2);
				bit += 2;
				zeros += r;
				if (r !== 3) break;
			}
			for (let i = 0; i < zeros; i++) probs.push(0);
			if (probs.length > maxSymbol) break;
		}
		const max = 2 * threshold - 1 - remaining;
		let count: number;
		const low = get(nbBits - 1);
		if (low < max) {
			count = low;
			bit += nbBits - 1;
		} else {
			count = get(nbBits);
			if (count >= threshold) count -= max;
			bit += nbBits;
		}
		count -= 1;
		remaining -= count < 0 ? -count : count;
		probs.push(count);
		previous0 = count === 0;
		while (remaining < threshold) {
			nbBits--;
			threshold >>= 1;
		}
	}
	if (remaining !== 1 || probs.length > maxSymbol + 1) throw new Error('zstd: bad FSE table description');
	return [buildFse(probs, log), (bit + 7) >> 3];
}

/* ── Huffman ── */

interface HufTable {
	log: number;
	symbol: Uint8Array;
	nbBits: Uint8Array;
}

/** A Huffman tree description at data[pos]; returns the table and the bytes it took */
function readHuffman(data: Uint8Array, pos: number, end: number): [HufTable, number] {
	const header = data[pos] ?? 0;
	const weights: number[] = [];
	let used: number;
	if (header < 128) {
		// FSE-compressed weights: two states take turns
		const stop = pos + 1 + header;
		if (stop > end) throw new Error('zstd: Huffman weights past the block');
		const [table, size] = readFse(data, pos + 1, stop, 255, 6);
		const bits = new BackBits(data, pos + 1 + size, stop);
		let s1 = bits.read(table.log);
		let s2 = bits.read(table.log);
		for (;;) {
			weights.push(table.symbol[s1] ?? 0);
			s1 = (table.base[s1] ?? 0) + bits.read(table.nbBits[s1] ?? 0);
			if (bits.left < 0) {
				weights.push(table.symbol[s2] ?? 0);
				break;
			}
			weights.push(table.symbol[s2] ?? 0);
			s2 = (table.base[s2] ?? 0) + bits.read(table.nbBits[s2] ?? 0);
			if (bits.left < 0) {
				weights.push(table.symbol[s1] ?? 0);
				break;
			}
			if (weights.length > 255) throw new Error('zstd: too many Huffman weights');
		}
		used = 1 + header;
	} else {
		const count = header - 127;
		used = 1 + ((count + 1) >> 1);
		for (let i = 0; i < count; i++) {
			const byte = data[pos + 1 + (i >> 1)] ?? 0;
			weights.push(i & 1 ? byte & 15 : byte >> 4);
		}
	}
	let total = 0;
	for (const w of weights) if (w > 0) total += 1 << (w - 1);
	if (total === 0) throw new Error('zstd: empty Huffman tree');
	const log = highBit(total) + 1;
	if (log > 11) throw new Error('zstd: Huffman tree too deep');
	const rest = (1 << log) - total;
	if (rest & (rest - 1)) throw new Error('zstd: bad Huffman weights');
	weights.push(highBit(rest) + 1);
	const size = 1 << log;
	const symbol = new Uint8Array(size);
	const nbBits = new Uint8Array(size);
	const rankStart = new Array<number>(log + 2).fill(0);
	const rankCount = new Array<number>(log + 2).fill(0);
	for (const w of weights) rankCount[w] = (rankCount[w] ?? 0) + 1;
	let next = 0;
	for (let w = 1; w <= log; w++) {
		rankStart[w] = next;
		next += (rankCount[w] ?? 0) << (w - 1);
	}
	for (let s = 0; s < weights.length; s++) {
		const w = weights[s] ?? 0;
		if (!w) continue;
		const length = 1 << (w - 1);
		const at = rankStart[w] ?? 0;
		symbol.fill(s, at, at + length);
		nbBits.fill(log + 1 - w, at, at + length);
		rankStart[w] = at + length;
	}
	return [{ log, symbol, nbBits }, used];
}

function decodeHuffmanStream(
	data: Uint8Array,
	start: number,
	end: number,
	table: HufTable,
	out: Uint8Array,
	from: number,
	count: number
): void {
	const bits = new BackBits(data, start, end);
	for (let i = 0; i < count; i++) {
		const e = bits.peek(table.log);
		out[from + i] = table.symbol[e] ?? 0;
		bits.left -= table.nbBits[e] ?? 0;
	}
	if (bits.left !== 0) throw new Error('zstd: Huffman stream size mismatch');
}

/* ── Sequences ── */

const LL_BASE = [
	0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 18, 20, 22, 24, 28, 32, 40, 48, 64, 128, 256, 512,
	1024, 2048, 4096, 8192, 16384, 32768, 65536
];
const LL_BITS = [
	0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 2, 2, 3, 3, 4, 6, 7, 8, 9, 10, 11, 12, 13, 14,
	15, 16
];
const ML_BASE = [
	3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31,
	32, 33, 34, 35, 37, 39, 41, 43, 47, 51, 59, 67, 83, 99, 131, 259, 515, 1027, 2051, 4099, 8195, 16387, 32771,
	65539
];
const ML_BITS = [
	0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1,
	2, 2, 3, 3, 4, 4, 5, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16
];
const LL_DEFAULT = buildFse(
	[
		4, 3, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 1, 1, 1, 2, 2, 2, 2, 2, 2, 2, 2, 2, 3, 2, 1, 1, 1, 1, 1, -1, -1,
		-1, -1
	],
	6
);
const ML_DEFAULT = buildFse(
	[
		1, 4, 3, 2, 2, 2, 2, 2, 2, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1,
		1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, -1, -1, -1, -1, -1, -1, -1
	],
	6
);
const OF_DEFAULT = buildFse(
	[1, 1, 1, 1, 1, 1, 2, 2, 2, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, -1, -1, -1, -1, -1],
	5
);

function rleTable(symbol: number): FseTable {
	return {
		log: 0,
		symbol: Uint8Array.of(symbol),
		nbBits: Uint8Array.of(0),
		base: Uint16Array.of(0)
	};
}

/* ── Output ── */

class Output {
	buf: Uint8Array;
	length = 0;
	private max: number;

	constructor(hint: number, max: number) {
		this.max = max;
		this.buf = new Uint8Array(Math.min(Math.max(hint, 1024), max || 1024));
	}

	reserve(n: number): void {
		const need = this.length + n;
		if (need > this.max) throw new ZstdTooLarge(`Decompressed data is larger than ${this.max} bytes`);
		if (need <= this.buf.length) return;
		const next = new Uint8Array(Math.min(this.max, Math.max(need, this.buf.length * 2)));
		next.set(this.buf.subarray(0, this.length));
		this.buf = next;
	}

	bytes(src: Uint8Array, from: number, n: number): void {
		this.reserve(n);
		this.buf.set(src.subarray(from, from + n), this.length);
		this.length += n;
	}

	repeat(byte: number, n: number): void {
		this.reserve(n);
		this.buf.fill(byte, this.length, this.length + n);
		this.length += n;
	}

	match(offset: number, n: number, frameStart: number): void {
		if (offset > this.length - frameStart || offset === 0)
			throw new Error('zstd: match before the frame start');
		this.reserve(n);
		const b = this.buf;
		let from = this.length - offset;
		let to = this.length;
		if (offset >= n) b.copyWithin(to, from, from + n);
		else for (let i = 0; i < n; i++) b[to++] = b[from++] ?? 0;
		this.length += n;
	}
}

/* ── Frames ── */

interface FrameState {
	huffman: HufTable | null;
	ll: FseTable | null;
	of: FseTable | null;
	ml: FseTable | null;
	reps: [number, number, number];
}

function readLiterals(data: Uint8Array, pos: number, end: number, st: FrameState): [Uint8Array, number] {
	const b0 = data[pos] ?? 0;
	const type = b0 & 3;
	const format = (b0 >> 2) & 3;
	if (type === 0 || type === 1) {
		let size: number;
		let head: number;
		if (format === 0 || format === 2) {
			size = b0 >> 3;
			head = 1;
		} else if (format === 1) {
			size = (b0 >> 4) + ((data[pos + 1] ?? 0) << 4);
			head = 2;
		} else {
			size = (b0 >> 4) + ((data[pos + 1] ?? 0) << 4) + ((data[pos + 2] ?? 0) << 12);
			head = 3;
		}
		if (type === 0) {
			if (pos + head + size > end) throw new Error('zstd: literals past the block');
			return [data.subarray(pos + head, pos + head + size), head + size];
		}
		return [new Uint8Array(size).fill(data[pos + head] ?? 0), head + 1];
	}
	let regen: number;
	let comp: number;
	let head: number;
	const streams = format === 0 ? 1 : 4;
	if (format === 0 || format === 1) {
		const h = b0 | ((data[pos + 1] ?? 0) << 8) | ((data[pos + 2] ?? 0) << 16);
		regen = (h >>> 4) & 0x3ff;
		comp = (h >>> 14) & 0x3ff;
		head = 3;
	} else if (format === 2) {
		const h =
			(b0 | ((data[pos + 1] ?? 0) << 8) | ((data[pos + 2] ?? 0) << 16) | ((data[pos + 3] ?? 0) << 24)) >>> 0;
		regen = (h >>> 4) & 0x3fff;
		comp = h >>> 18;
		head = 4;
	} else {
		const h =
			(b0 | ((data[pos + 1] ?? 0) << 8) | ((data[pos + 2] ?? 0) << 16) | ((data[pos + 3] ?? 0) << 24)) >>> 0;
		regen = (h >>> 4) & 0x3ffff;
		comp = (h >>> 22) + ((data[pos + 4] ?? 0) << 10);
		head = 5;
	}
	let p = pos + head;
	const stop = p + comp;
	if (stop > end) throw new Error('zstd: literals past the block');
	if (type === 2) {
		const [table, used] = readHuffman(data, p, stop);
		st.huffman = table;
		p += used;
	} else if (!st.huffman) throw new Error('zstd: treeless literals without an earlier tree');
	const table = st.huffman;
	const out = new Uint8Array(regen);
	if (streams === 1) decodeHuffmanStream(data, p, stop, table, out, 0, regen);
	else {
		const s1 = (data[p] ?? 0) | ((data[p + 1] ?? 0) << 8);
		const s2 = (data[p + 2] ?? 0) | ((data[p + 3] ?? 0) << 8);
		const s3 = (data[p + 4] ?? 0) | ((data[p + 5] ?? 0) << 8);
		p += 6;
		const each = (regen + 3) >> 2;
		const starts = [p, p + s1, p + s1 + s2, p + s1 + s2 + s3, stop];
		if ((starts[3] ?? 0) > stop) throw new Error('zstd: bad jump table');
		for (let i = 0; i < 4; i++) {
			const count = i < 3 ? each : regen - 3 * each;
			decodeHuffmanStream(data, starts[i] ?? 0, starts[i + 1] ?? 0, table, out, i * each, count);
		}
	}
	return [out, stop - pos];
}

function pickTable(
	mode: number,
	data: Uint8Array,
	pos: number,
	end: number,
	fallback: FseTable,
	previous: FseTable | null,
	maxSymbol: number,
	maxLog: number
): [FseTable, number] {
	switch (mode) {
		case 0:
			return [fallback, 0];
		case 1:
			return [rleTable(data[pos] ?? 0), 1];
		case 2:
			return readFse(data, pos, end, maxSymbol, maxLog);
		default:
			if (!previous) throw new Error('zstd: repeat mode without an earlier table');
			return [previous, 0];
	}
}

function decodeBlock(
	data: Uint8Array,
	pos: number,
	end: number,
	out: Output,
	st: FrameState,
	frameStart: number
): void {
	const [literals, litSize] = readLiterals(data, pos, end, st);
	let p = pos + litSize;
	let count = data[p++] ?? 0;
	if (count === 0) {
		out.bytes(literals, 0, literals.length);
		return;
	}
	if (count >= 128) {
		if (count === 255) {
			count = (data[p] ?? 0) + ((data[p + 1] ?? 0) << 8) + 0x7f00;
			p += 2;
		} else {
			count = ((count - 128) << 8) + (data[p] ?? 0);
			p += 1;
		}
	}
	const modes = data[p++] ?? 0;
	let used: number;
	[st.ll, used] = pickTable(modes >> 6, data, p, end, LL_DEFAULT, st.ll, 35, 9);
	p += used;
	[st.of, used] = pickTable((modes >> 4) & 3, data, p, end, OF_DEFAULT, st.of, 31, 8);
	p += used;
	[st.ml, used] = pickTable((modes >> 2) & 3, data, p, end, ML_DEFAULT, st.ml, 52, 9);
	p += used;
	const ll = st.ll;
	const of = st.of;
	const ml = st.ml;
	const bits = new BackBits(data, p, end);
	let sLL = bits.read(ll.log);
	let sOF = bits.read(of.log);
	let sML = bits.read(ml.log);
	const reps = st.reps;
	let lit = 0;
	for (let i = 0; i < count; i++) {
		const ofCode = of.symbol[sOF] ?? 0;
		const mlCode = ml.symbol[sML] ?? 0;
		const llCode = ll.symbol[sLL] ?? 0;
		if (ofCode > 31 || mlCode > 52 || llCode > 35) throw new Error('zstd: bad sequence code');
		const offsetValue = 2 ** ofCode + bits.read(ofCode);
		const matchLength = (ML_BASE[mlCode] ?? 0) + bits.read(ML_BITS[mlCode] ?? 0);
		const literalLength = (LL_BASE[llCode] ?? 0) + bits.read(LL_BITS[llCode] ?? 0);
		let offset: number;
		if (offsetValue > 3) {
			offset = offsetValue - 3;
			reps[2] = reps[1];
			reps[1] = reps[0];
			reps[0] = offset;
		} else {
			const idx = offsetValue - 1 + (literalLength === 0 ? 1 : 0);
			if (idx === 0) offset = reps[0];
			else if (idx === 3) {
				offset = reps[0] - 1;
				reps[2] = reps[1];
				reps[1] = reps[0];
				reps[0] = offset;
			} else {
				offset = reps[idx] ?? 0;
				if (idx === 2) reps[2] = reps[1];
				reps[1] = reps[0];
				reps[0] = offset;
			}
		}
		if (lit + literalLength > literals.length) throw new Error('zstd: literals overrun');
		out.bytes(literals, lit, literalLength);
		lit += literalLength;
		out.match(offset, matchLength, frameStart);
		if (i + 1 < count) {
			sLL = (ll.base[sLL] ?? 0) + bits.read(ll.nbBits[sLL] ?? 0);
			sML = (ml.base[sML] ?? 0) + bits.read(ml.nbBits[sML] ?? 0);
			sOF = (of.base[sOF] ?? 0) + bits.read(of.nbBits[sOF] ?? 0);
		}
	}
	if (bits.left !== 0) throw new Error('zstd: sequence stream size mismatch');
	out.bytes(literals, lit, literals.length - lit);
}

function readU32(data: Uint8Array, p: number): number {
	return (
		((data[p] ?? 0) | ((data[p + 1] ?? 0) << 8) | ((data[p + 2] ?? 0) << 16) | ((data[p + 3] ?? 0) << 24)) >>>
		0
	);
}

/** Decompresses zstd frames; throws ZstdTooLarge past maxBytes and Error on a damaged input */
export function zstdDecompress(input: Uint8Array, maxBytes: number): Uint8Array {
	let p = 0;
	let out: Output | null = null;
	while (p < input.length) {
		if (p + 4 > input.length) throw new Error('zstd: truncated frame');
		const magic = readU32(input, p);
		p += 4;
		if ((magic & 0xfffffff0) >>> 0 === 0x184d2a50) {
			p += 4 + readU32(input, p);
			continue;
		}
		if (magic !== MAGIC) throw new Error('zstd: not a zstd frame');
		const fhd = input[p++] ?? 0;
		const fcsFlag = fhd >> 6;
		const single = (fhd >> 5) & 1;
		const checksum = (fhd >> 2) & 1;
		const dictFlag = fhd & 3;
		if (!single) p += 1;
		const dictSize = dictFlag === 3 ? 4 : dictFlag;
		let dictId = 0;
		for (let i = 0; i < dictSize; i++) dictId += (input[p + i] ?? 0) * 256 ** i;
		if (dictId) throw new Error('zstd: dictionaries are not supported');
		p += dictSize;
		const fcsSize = fcsFlag === 0 ? single : 1 << fcsFlag;
		let contentSize = 0;
		for (let i = 0; i < fcsSize; i++) contentSize += (input[p + i] ?? 0) * 256 ** i;
		if (fcsSize === 2) contentSize += 256;
		p += fcsSize;
		if (contentSize > maxBytes) throw new ZstdTooLarge(`Decompressed data is larger than ${maxBytes} bytes`);
		out ??= new Output(contentSize || input.length * 4, maxBytes);
		const frameStart = out.length;
		const st: FrameState = { huffman: null, ll: null, of: null, ml: null, reps: [1, 4, 8] };
		for (;;) {
			if (p + 3 > input.length) throw new Error('zstd: truncated block');
			const h = (input[p] ?? 0) | ((input[p + 1] ?? 0) << 8) | ((input[p + 2] ?? 0) << 16);
			p += 3;
			const last = h & 1;
			const type = (h >> 1) & 3;
			const size = h >>> 3;
			if (type === 0) {
				if (p + size > input.length) throw new Error('zstd: truncated block');
				out.bytes(input, p, size);
				p += size;
			} else if (type === 1) {
				out.repeat(input[p] ?? 0, size);
				p += 1;
			} else if (type === 2) {
				if (p + size > input.length) throw new Error('zstd: truncated block');
				decodeBlock(input, p, p + size, out, st, frameStart);
				p += size;
			} else throw new Error('zstd: reserved block type');
			if (last) break;
		}
		if (checksum) p += 4;
	}
	return out ? out.buf.slice(0, out.length) : new Uint8Array(0);
}
