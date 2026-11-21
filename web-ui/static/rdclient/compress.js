/**
 * BetterDesk Web Remote Client - zstd compression helpers
 * Shared by file transfer and clipboard sync (RustDesk wire format).
 */

// eslint-disable-next-line no-unused-vars
class RDCompress {
    static _zstdWarningShown = false;

    /** Largest inflated payload without opts.maxBytes; a few KB of zstd can inflate to gigabytes */
    static MAX_DECOMPRESSED = 64 * 1024 * 1024;

    /**
     * @param {Uint8Array|ArrayBuffer|Array} data
     * @returns {Uint8Array}
     */
    static normalizeBytes(data) {
        if (data instanceof Uint8Array) return data;
        if (data instanceof ArrayBuffer) return new Uint8Array(data);
        // Strings are array-like but `new Uint8Array(string)` yields length 0
        // (ToIndex → NaN). Never treat a string as a byte source here.
        if (typeof data === 'string') return new Uint8Array(0);
        if (Array.isArray(data)) return new Uint8Array(data);
        return new Uint8Array(0);
    }

    /**
     * @param {Uint8Array} bytes
     * @returns {boolean}
     */
    static isZstdMagic(bytes) {
        return bytes.length >= 4
            && bytes[0] === 0x28
            && bytes[1] === 0xb5
            && bytes[2] === 0x2f
            && bytes[3] === 0xfd;
    }

    /**
     * Decompress RustDesk zstd payload.
     * @param {Uint8Array|ArrayBuffer|Array} data
     * @param {Object} [opts]
     * @param {boolean} [opts.force] - decompress even without magic bytes
     * @param {number} [opts.maxBytes] - give up when the output grows past this (default MAX_DECOMPRESSED)
     * @returns {Promise<Uint8Array>}
     */
    static async decompressZstd(data, opts) {
        const options = opts || {};
        const bytes = RDCompress.normalizeBytes(data);
        if (!bytes.length) return bytes;

        const force = !!options.force;
        if (!force && !RDCompress.isZstdMagic(bytes)) {
            return bytes;
        }
        const maxBytes = Number(options.maxBytes) > 0 ? Number(options.maxBytes) : RDCompress.MAX_DECOMPRESSED;

        if (typeof DecompressionStream === 'function') {
            let stream = null;
            try {
                const input = new ReadableStream({
                    start(ctl) { ctl.enqueue(bytes); ctl.close(); }
                });
                stream = input.pipeThrough(new DecompressionStream('zstd'));
            } catch (err) {
                // Chromium versions may expose DecompressionStream but not
                // implement zstd. Try the optional decoder before failing.
            }
            if (stream) return RDCompress._readCapped(stream, maxBytes);
        }

        const decoder = (typeof window !== 'undefined' && window._zstdDecoder)
            || (typeof globalThis !== 'undefined' && globalThis._zstdDecoder);
        if (decoder && typeof decoder.decode === 'function') {
            try {
                const max = Math.min(Math.max(bytes.length * 30, 1024 * 1024), maxBytes);
                return decoder.decode(bytes, max);
            } catch (err) {
                // Fall through to one controlled error below. Returning the
                // compressed bytes would silently corrupt clipboard/file data.
            }
        }

        throw new Error('Zstandard decompression is unavailable in this browser');
    }

    /**
     * Read an inflating stream chunk by chunk and stop as soon as it passes
     * maxBytes, so a small payload cannot fill the tab's memory.
     * @param {ReadableStream<Uint8Array>} stream
     * @param {number} maxBytes
     * @returns {Promise<Uint8Array>}
     */
    static async _readCapped(stream, maxBytes) {
        const reader = stream.getReader();
        const chunks = [];
        let total = 0;
        for (;;) {
            const { done, value } = await reader.read();
            if (done) break;
            total += value.length;
            if (total > maxBytes) {
                reader.cancel().catch(() => {});
                const err = new Error(`Decompressed data is larger than ${maxBytes} bytes`);
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
    }

    /**
     * Log an unsupported-runtime warning once per page, rather than once per
     * clipboard entry or file block.
     * @param {Error} [err]
     */
    static reportZstdUnavailable(err) {
        if (RDCompress._zstdWarningShown) return;
        RDCompress._zstdWarningShown = true;
        console.warn('[RDCompress] Zstandard decompression unavailable:',
            err && err.message ? err.message : err || 'no decoder');
    }

    /**
     * Compress bytes with zstd when beneficial (RustDesk clipboard/file parity).
     * @param {Uint8Array} bytes
     * @returns {Promise<{ content: Uint8Array, compress: boolean }>}
     */
    static async compressZstd(bytes) {
        const raw = RDCompress.normalizeBytes(bytes);
        if (!raw.length) {
            return { content: raw, compress: false };
        }
        if (typeof CompressionStream !== 'function' || raw.length <= 128) {
            return { content: raw, compress: false };
        }
        try {
            const stream = new Blob([raw]).stream().pipeThrough(new CompressionStream('zstd'));
            const compressed = new Uint8Array(await new Response(stream).arrayBuffer());
            if (compressed.length < raw.length) {
                return { content: compressed, compress: true };
            }
        } catch (_) {
            // fall back to raw payload
        }
        return { content: raw, compress: false };
    }
}

if (typeof window !== 'undefined') {
    window.RDCompress = RDCompress;
}
