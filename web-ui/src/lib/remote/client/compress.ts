/**
 * NeoDesk Web Remote Client - zstd compression helpers
 * Shared by file transfer and clipboard sync (RustDesk wire format).
 */
import { page } from './page.ts';

/** What normalizeBytes turns into bytes (anything else is empty) */
export type RDBytesLike = Uint8Array | ArrayBuffer | number[] | string | null | undefined;

export interface RDDecompressOptions {
	/** decompress even without magic bytes */
	force?: boolean;
	/** give up when the output grows past this (default MAX_DECOMPRESSED) */
	maxBytes?: number;
}

/** compressZstd's answer: the bytes to send and whether they are zstd */
export interface RDCompressed {
	content: Uint8Array;
	compress: boolean;
}

/** The error of a payload that inflates past its limit */
export interface RDTooLargeError extends Error {
	tooLarge?: boolean;
}

/**
 * A zstd stream. TypeScript's CompressionFormat lists only deflate and gzip,
 * so the format goes in through Reflect.construct; an unsupported format
 * throws there as it does in the constructor.
 */
function zstdStream<T extends object>(Stream: { prototype: T; new (format: CompressionFormat): T }): T {
	const stream: unknown = Reflect.construct(Stream, ['zstd']);
	if (stream instanceof Stream) return stream;
	throw new TypeError('No zstd stream');
}

/**
 * The bytes as the stream and Blob types take them: on an ArrayBuffer.
 * Protobuf and TextEncoder bytes always are; bytes on other memory go as a copy.
 */
function bufferBytes(data: Uint8Array): Uint8Array<ArrayBuffer> {
	return isOnArrayBuffer(data) ? data : new Uint8Array(data);
}

function isOnArrayBuffer(data: Uint8Array): data is Uint8Array<ArrayBuffer> {
	return data.buffer instanceof ArrayBuffer;
}

function errorMessage(err: unknown): unknown {
	return typeof err === 'object' && err !== null && 'message' in err ? err.message : undefined;
}

export const RDCompress = {
	_zstdWarningShown: false,

	/** Largest inflated payload without opts.maxBytes; a few KB of zstd can inflate to gigabytes */
	MAX_DECOMPRESSED: 64 * 1024 * 1024,

	normalizeBytes(data: RDBytesLike): Uint8Array {
		if (data instanceof Uint8Array) return data;
		if (data instanceof ArrayBuffer) return new Uint8Array(data);
		// Strings are array-like but `new Uint8Array(string)` yields length 0
		// (ToIndex → NaN). Never treat a string as a byte source here.
		if (typeof data === 'string') return new Uint8Array(0);
		if (Array.isArray(data)) return new Uint8Array(data);
		return new Uint8Array(0);
	},

	isZstdMagic(bytes: Uint8Array): boolean {
		return (
			bytes.length >= 4 && bytes[0] === 0x28 && bytes[1] === 0xb5 && bytes[2] === 0x2f && bytes[3] === 0xfd
		);
	},

	/**
	 * Decompress RustDesk zstd payload.
	 */
	async decompressZstd(data: RDBytesLike, opts?: RDDecompressOptions | null): Promise<Uint8Array> {
		const options = opts || {};
		const bytes = RDCompress.normalizeBytes(data);
		if (!bytes.length) return bytes;

		const force = !!options.force;
		if (!force && !RDCompress.isZstdMagic(bytes)) {
			return bytes;
		}
		const maxBytes = Number(options.maxBytes) > 0 ? Number(options.maxBytes) : RDCompress.MAX_DECOMPRESSED;

		if (typeof DecompressionStream === 'function') {
			let stream: ReadableStream<Uint8Array> | null = null;
			try {
				const input = new ReadableStream<Uint8Array<ArrayBuffer>>({
					start(ctl) {
						ctl.enqueue(bufferBytes(bytes));
						ctl.close();
					}
				});
				stream = input.pipeThrough(zstdStream(DecompressionStream));
			} catch {
				// Chromium versions may expose DecompressionStream but not
				// implement zstd. Try the optional decoder before failing.
			}
			if (stream) return RDCompress._readCapped(stream, maxBytes);
		}

		const decoder = typeof window !== 'undefined' ? page()._zstdDecoder : undefined;
		if (decoder && typeof decoder.decode === 'function') {
			try {
				const max = Math.min(Math.max(bytes.length * 30, 1024 * 1024), maxBytes);
				return decoder.decode(bytes, max);
			} catch {
				// Fall through to one controlled error below. Returning the
				// compressed bytes would silently corrupt clipboard/file data.
			}
		}

		throw new Error('Zstandard decompression is unavailable in this browser');
	},

	/**
	 * Read an inflating stream chunk by chunk and stop as soon as it passes
	 * maxBytes, so a small payload cannot fill the tab's memory.
	 */
	async _readCapped(stream: ReadableStream<Uint8Array>, maxBytes: number): Promise<Uint8Array> {
		const reader = stream.getReader();
		const chunks: Uint8Array[] = [];
		let total = 0;
		for (;;) {
			const { done, value } = await reader.read();
			if (done) break;
			total += value.length;
			if (total > maxBytes) {
				reader.cancel().catch(() => {});
				const err: RDTooLargeError = new Error(`Decompressed data is larger than ${maxBytes} bytes`);
				err.tooLarge = true;
				throw err;
			}
			chunks.push(value);
		}
		const out = new Uint8Array(total);
		let offset = 0;
		for (const chunk of chunks) {
			out.set(chunk, offset);
			offset += chunk.length;
		}
		return out;
	},

	/**
	 * Log an unsupported-runtime warning once per page, rather than once per
	 * clipboard entry or file block.
	 */
	reportZstdUnavailable(err?: unknown): void {
		if (RDCompress._zstdWarningShown) return;
		RDCompress._zstdWarningShown = true;
		const message = err ? errorMessage(err) : undefined;
		console.warn(
			'[RDCompress] Zstandard decompression unavailable:',
			message ? message : err || 'no decoder'
		);
	},

	/**
	 * Compress bytes with zstd when beneficial (RustDesk clipboard/file parity).
	 */
	async compressZstd(bytes: RDBytesLike): Promise<RDCompressed> {
		const raw = RDCompress.normalizeBytes(bytes);
		if (!raw.length) {
			return { content: raw, compress: false };
		}
		if (typeof CompressionStream !== 'function' || raw.length <= 128) {
			return { content: raw, compress: false };
		}
		try {
			const source = new Blob([bufferBytes(raw)]).stream();
			const stream = source.pipeThrough(zstdStream(CompressionStream));
			const compressed = new Uint8Array(await new Response(stream).arrayBuffer());
			if (compressed.length < raw.length) {
				return { content: compressed, compress: true };
			}
		} catch {
			// fall back to raw payload
		}
		return { content: raw, compress: false };
	}
};
