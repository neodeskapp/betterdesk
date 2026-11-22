/**
 * A ZIP archive built in the browser, for downloading a remote folder in
 * one file. Entries are stored without compression (the remote files are
 * often compressed already, and storing keeps memory flat: the archive is a
 * Blob made of the file Blobs and small headers). UTF-8 names, folders as
 * their own entries so empty ones survive. No ZIP64: an archive of 4 GiB or
 * more, or with more than 65,535 entries, is refused.
 */

export interface ZipEntry {
	/** Path inside the archive with / between parts; a folder ends with / */
	path: string;
	/** The file's content; none for a folder */
	data?: Blob;
	/** Modification time, Unix seconds (0 or missing: now) */
	modifiedTime?: number;
}

const MAX_SIZE = 0xffffffff;
const MAX_ENTRIES = 0xffff;

let crcTable: Uint32Array | null = null;

function table(): Uint32Array {
	if (crcTable) return crcTable;
	const t = new Uint32Array(256);
	for (let n = 0; n < 256; n++) {
		let c = n;
		for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
		t[n] = c >>> 0;
	}
	crcTable = t;
	return t;
}

/** CRC-32 (IEEE) of some bytes, continuing from crc */
export function crc32(bytes: Uint8Array, crc = 0): number {
	const t = table();
	let c = ~crc >>> 0;
	for (const b of bytes) c = (t[(c ^ b) & 0xff] ?? 0) ^ (c >>> 8);
	return ~c >>> 0;
}

/** DOS time and date of a Unix time in seconds (local time, as unzip tools read it) */
function dosTime(seconds: number): { time: number; date: number } {
	const d = new Date(seconds > 0 ? seconds * 1000 : Date.now());
	const year = Math.max(1980, d.getFullYear());
	return {
		time: (d.getHours() << 11) | (d.getMinutes() << 5) | (d.getSeconds() >> 1),
		date: ((year - 1980) << 9) | ((d.getMonth() + 1) << 5) | d.getDate()
	};
}

async function blobCrc(blob: Blob): Promise<number> {
	let crc = 0;
	const chunk = 4 * 1024 * 1024;
	for (let at = 0; at < blob.size; at += chunk) {
		crc = crc32(new Uint8Array(await blob.slice(at, at + chunk).arrayBuffer()), crc);
	}
	return crc;
}

/** The archive of the entries, in their order */
export async function buildZip(entries: ZipEntry[]): Promise<Blob> {
	if (entries.length > MAX_ENTRIES) throw new Error('Too many files for one ZIP archive');
	const enc = new TextEncoder();
	const parts: BlobPart[] = [];
	const central: Uint8Array<ArrayBuffer>[] = [];
	let offset = 0;
	for (const e of entries) {
		const name = enc.encode(e.path);
		const data = e.data ?? new Blob([]);
		const size = data.size;
		const crc = e.data ? await blobCrc(data) : 0;
		const { time, date } = dosTime(e.modifiedTime ?? 0);
		const isDir = e.path.endsWith('/');

		const local = new Uint8Array(30 + name.length);
		const lv = new DataView(local.buffer);
		lv.setUint32(0, 0x04034b50, true);
		lv.setUint16(4, 20, true);
		lv.setUint16(6, 0x0800, true);
		lv.setUint16(8, 0, true);
		lv.setUint16(10, time, true);
		lv.setUint16(12, date, true);
		lv.setUint32(14, crc, true);
		lv.setUint32(18, size, true);
		lv.setUint32(22, size, true);
		lv.setUint16(26, name.length, true);
		lv.setUint16(28, 0, true);
		local.set(name, 30);

		const dir = new Uint8Array(46 + name.length);
		const dv = new DataView(dir.buffer);
		dv.setUint32(0, 0x02014b50, true);
		dv.setUint16(4, 20, true);
		dv.setUint16(6, 20, true);
		dv.setUint16(8, 0x0800, true);
		dv.setUint16(10, 0, true);
		dv.setUint16(12, time, true);
		dv.setUint16(14, date, true);
		dv.setUint32(16, crc, true);
		dv.setUint32(20, size, true);
		dv.setUint32(24, size, true);
		dv.setUint16(28, name.length, true);
		dv.setUint32(38, isDir ? 0x10 : 0, true);
		dv.setUint32(42, offset, true);
		dir.set(name, 46);

		parts.push(local, data);
		central.push(dir);
		offset += local.length + size;
		if (offset > MAX_SIZE) throw new Error('The folder is too large for one ZIP archive (4 GB)');
	}
	const centralSize = central.reduce((n, c) => n + c.length, 0);
	if (offset + centralSize > MAX_SIZE) throw new Error('The folder is too large for one ZIP archive (4 GB)');
	const end = new Uint8Array(22);
	const ev = new DataView(end.buffer);
	ev.setUint32(0, 0x06054b50, true);
	ev.setUint16(8, entries.length, true);
	ev.setUint16(10, entries.length, true);
	ev.setUint32(12, centralSize, true);
	ev.setUint32(16, offset, true);
	return new Blob([...parts, ...central, end], { type: 'application/zip' });
}
