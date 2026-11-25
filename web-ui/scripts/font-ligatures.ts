/**
 * Reads what a WOFF2 icon font draws: the strings its ligatures (`liga`, or
 * `rlig` in Google's own subsets) turn
 * into one glyph (Material Symbols draws an icon for its name typed as
 * text), and its version (name ID 5). Enough of WOFF2, cmap and GSUB for
 * tests/unit/icon-font.test.ts; the tables it reads are never transformed
 * in WOFF2, so the font's data only needs Brotli.
 */
import zlib from 'node:zlib';

const KNOWN_TAGS = [
	'cmap', 'head', 'hhea', 'hmtx', 'maxp', 'name', 'OS/2', 'post', 'cvt ', 'fpgm', 'glyf', 'loca', 'prep',
	'CFF ', 'VORG', 'EBDT', 'EBLC', 'gasp', 'hdmx', 'kern', 'LTSH', 'PCLT', 'VDMX', 'vhea', 'vmtx', 'BASE',
	'GDEF', 'GPOS', 'GSUB', 'EBSC', 'JSTF', 'MATH', 'CBDT', 'CBLC', 'COLR', 'CPAL', 'SVG ', 'sbix', 'acnt',
	'avar', 'bdat', 'bloc', 'bsln', 'cvar', 'fdsc', 'feat', 'fmtx', 'fvar', 'gvar', 'hsty', 'just', 'lcar',
	'mort', 'morx', 'opbd', 'prop', 'trak', 'Zapf', 'Silf', 'Glat', 'Gloc', 'Feat', 'Sill'
]; // prettier-ignore

/** The font's tables, unpacked (only the untransformed ones are usable) */
export function woff2Tables(file: Uint8Array): Map<string, Uint8Array> {
	const v = new DataView(file.buffer, file.byteOffset, file.byteLength);
	if (v.getUint32(0) !== 0x774f4632) throw new Error('not a WOFF2 file');
	const numTables = v.getUint16(12);
	const compressedSize = v.getUint32(20);
	let p = 48;
	const base128 = (): number => {
		let n = 0;
		for (let i = 0; i < 5; i++) {
			const b = file[p++] ?? 0;
			n = n * 128 + (b & 0x7f);
			if (!(b & 0x80)) return n;
		}
		throw new Error('bad UIntBase128');
	};
	const dir: { tag: string; length: number; usable: boolean }[] = [];
	for (let i = 0; i < numTables; i++) {
		const flags = file[p++] ?? 0;
		let tag = KNOWN_TAGS[flags & 0x3f] ?? '';
		if ((flags & 0x3f) === 63) {
			tag = String.fromCharCode(...file.subarray(p, p + 4));
			p += 4;
		}
		const version = flags >> 6;
		const orig = base128();
		const glyphLike = tag === 'glyf' || tag === 'loca';
		const transformed = glyphLike ? version !== 3 : version !== 0;
		const length = transformed ? base128() : orig;
		dir.push({ tag, length, usable: !transformed });
	}
	const data = zlib.brotliDecompressSync(file.subarray(p, p + compressedSize));
	const tables = new Map<string, Uint8Array>();
	let at = 0;
	for (const t of dir) {
		if (t.usable) tables.set(t.tag, new Uint8Array(data.buffer, data.byteOffset + at, t.length));
		at += t.length;
	}
	return tables;
}

function view(t: Uint8Array): DataView {
	return new DataView(t.buffer, t.byteOffset, t.byteLength);
}

/** Character to glyph id, from the Unicode subtable (format 4 or 12) */
function cmapOf(t: Uint8Array): Map<number, number> {
	const v = view(t);
	const out = new Map<number, number>();
	const count = v.getUint16(2);
	for (let i = 0; i < count; i++) {
		const offset = v.getUint32(4 + i * 8 + 4);
		const format = v.getUint16(offset);
		if (format === 12) {
			const groups = v.getUint32(offset + 12);
			for (let g = 0; g < groups; g++) {
				const o = offset + 16 + g * 12;
				const start = v.getUint32(o);
				const end = v.getUint32(o + 4);
				const gid = v.getUint32(o + 8);
				for (let c = start; c <= end; c++) out.set(c, gid + c - start);
			}
		} else if (format === 4) {
			const segs = v.getUint16(offset + 6) / 2;
			const ends = offset + 14;
			const starts = ends + segs * 2 + 2;
			const deltas = starts + segs * 2;
			const ranges = deltas + segs * 2;
			for (let s = 0; s < segs; s++) {
				const end = v.getUint16(ends + s * 2);
				const start = v.getUint16(starts + s * 2);
				const delta = v.getInt16(deltas + s * 2);
				const range = v.getUint16(ranges + s * 2);
				for (let c = start; c <= end && c !== 0xffff; c++) {
					let gid: number;
					if (range === 0) gid = (c + delta) & 0xffff;
					else {
						const g = v.getUint16(ranges + s * 2 + range + (c - start) * 2);
						gid = g ? (g + delta) & 0xffff : 0;
					}
					if (gid) out.set(c, gid);
				}
			}
		}
	}
	return out;
}

function coverageOf(v: DataView, o: number): number[] {
	const format = v.getUint16(o);
	const glyphs: number[] = [];
	if (format === 1) {
		const n = v.getUint16(o + 2);
		for (let i = 0; i < n; i++) glyphs.push(v.getUint16(o + 4 + i * 2));
	} else {
		const n = v.getUint16(o + 2);
		for (let i = 0; i < n; i++) {
			const r = o + 4 + i * 6;
			for (let g = v.getUint16(r); g <= v.getUint16(r + 2); g++) glyphs.push(g);
		}
	}
	return glyphs;
}

/** Ligatures of the `liga` and `rlig` features: the component glyph ids, joined by commas, to the ligature glyph */
function ligaturesOf(t: Uint8Array): Map<string, number> {
	const v = view(t);
	const features = v.getUint16(6);
	const lookups = v.getUint16(8);
	const wanted = new Set<number>();
	const fc = v.getUint16(features);
	for (let i = 0; i < fc; i++) {
		const r = features + 2 + i * 6;
		const tag = String.fromCharCode(v.getUint8(r), v.getUint8(r + 1), v.getUint8(r + 2), v.getUint8(r + 3));
		if (tag !== 'liga' && tag !== 'rlig') continue;
		const f = features + v.getUint16(r + 4);
		const n = v.getUint16(f + 2);
		for (let k = 0; k < n; k++) wanted.add(v.getUint16(f + 4 + k * 2));
	}
	const out = new Map<string, number>();
	for (const index of wanted) {
		const l = lookups + v.getUint16(lookups + 2 + index * 2);
		const type = v.getUint16(l);
		const subs = v.getUint16(l + 4);
		for (let s = 0; s < subs; s++) {
			let st = l + v.getUint16(l + 6 + s * 2);
			let kind = type;
			if (kind === 7) {
				kind = v.getUint16(st + 2);
				st += v.getUint32(st + 4);
			}
			if (kind !== 4) continue;
			const firsts = coverageOf(v, st + v.getUint16(st + 2));
			const sets = v.getUint16(st + 4);
			for (let i = 0; i < sets; i++) {
				const set = st + v.getUint16(st + 6 + i * 2);
				const ligs = v.getUint16(set);
				for (let j = 0; j < ligs; j++) {
					const lig = set + v.getUint16(set + 2 + j * 2);
					const glyph = v.getUint16(lig);
					const comps = [firsts[i] ?? -1];
					const count = v.getUint16(lig + 2);
					for (let c = 1; c < count; c++) comps.push(v.getUint16(lig + 4 + (c - 1) * 2));
					out.set(comps.join(','), glyph);
				}
			}
		}
	}
	return out;
}

/** The glyph a typed name draws, or null when the font draws it letter by letter */
export function ligatureReader(tables: Map<string, Uint8Array>): (name: string) => number | null {
	const cmap = tables.get('cmap');
	const gsub = tables.get('GSUB');
	if (!cmap || !gsub) throw new Error('the font has no cmap or no GSUB');
	const chars = cmapOf(cmap);
	const ligs = ligaturesOf(gsub);
	return (name) => {
		const ids = [...name].map((c) => chars.get(c.codePointAt(0) ?? 0) ?? -1);
		if (ids.includes(-1)) return null;
		return ligs.get(ids.join(',')) ?? null;
	};
}

/** A name of the name table (5 is the version) */
export function fontName(tables: Map<string, Uint8Array>, id: number): string {
	const t = tables.get('name');
	if (!t) return '';
	const v = view(t);
	const count = v.getUint16(2);
	const strings = v.getUint16(4);
	for (let i = 0; i < count; i++) {
		const r = 6 + i * 12;
		if (v.getUint16(r + 6) !== id || v.getUint16(r) !== 3) continue;
		const length = v.getUint16(r + 8);
		const offset = strings + v.getUint16(r + 10);
		let s = '';
		for (let k = 0; k < length; k += 2) s += String.fromCharCode(v.getUint16(offset + k));
		return s;
	}
	return '';
}
