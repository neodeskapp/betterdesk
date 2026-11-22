/**
 * BetterDesk Web Remote Client - inbound clipboard decode/apply
 * Handles RustDesk Clipboard / MultiClipboards (zstd, text/html/image).
 */
import { RDCompress } from './compress.ts';
import type { IClipboard } from './generated/protos.ts';

/** A clipboard entry from the device, decoded (decodeEntry) */
export interface RDClipboardDecoded {
	format: 'text' | 'html' | 'rtf' | 'image/png' | 'image/svg+xml';
	text?: string;
	html?: string;
	pngBlob?: Blob;
	svgText?: string;
}

/** A clipboard entry of this browser: what readLocalRich reads and writeLocalRich writes */
export interface RDClipboardLocalEntry {
	text?: string | null;
	html?: string | null;
	pngBlob?: Blob | null;
}

/** writeLocalRich's answer: whether the entry is on the clipboard, and why not */
export interface RDClipboardWriteStatus {
	ok: boolean;
	reason?: string;
}

/**
 * The bytes as Blob takes them: on an ArrayBuffer.
 * Decoded and inflated bytes always are; bytes on other memory go as a copy.
 */
function blobBytes(data: Uint8Array): Uint8Array<ArrayBuffer> {
	return isOnArrayBuffer(data) ? data : new Uint8Array(data);
}

function isOnArrayBuffer(data: Uint8Array): data is Uint8Array<ArrayBuffer> {
	return data.buffer instanceof ArrayBuffer;
}

/** err.message as the JS read it (undefined when there is none) */
function errorMessage(err: object): unknown {
	return 'message' in err ? err.message : undefined;
}

export const RDClipboard = {
	OWNER_FORMAT: 'dyn.com.rustdesk.owner',
	/** Largest clipboard entry taken from the device, after decompression */
	MAX_BYTES: 16 * 1024 * 1024,

	FORMAT: {
		Text: 0,
		Rtf: 1,
		Html: 2,
		ImageRgba: 21,
		ImagePng: 22,
		ImageSvg: 23,
		Special: 31
	},

	formatId(format: number | string | null | undefined): number {
		if (format == null) return RDClipboard.FORMAT.Text;
		if (typeof format === 'number') return format;
		const map: Record<string, number> = {
			Text: RDClipboard.FORMAT.Text,
			Rtf: RDClipboard.FORMAT.Rtf,
			Html: RDClipboard.FORMAT.Html,
			ImageRgba: RDClipboard.FORMAT.ImageRgba,
			ImagePng: RDClipboard.FORMAT.ImagePng,
			ImageSvg: RDClipboard.FORMAT.ImageSvg,
			Special: RDClipboard.FORMAT.Special
		};
		const id = map[format];
		return id != null ? id : RDClipboard.FORMAT.Text;
	},

	shouldSkipEntry(clipboard: IClipboard | null | undefined): boolean {
		if (!clipboard) return true;
		const fmt = RDClipboard.formatId(clipboard.format);
		if (fmt !== RDClipboard.FORMAT.Special) return false;
		const name = clipboard.specialName || '';
		return name === RDClipboard.OWNER_FORMAT;
	},

	normalizeContent(
		content: Uint8Array | ArrayBuffer | ArrayLike<number> | null | undefined
	): Uint8Array | null {
		if (!content) return null;
		if (content instanceof Uint8Array) return content;
		if (content instanceof ArrayBuffer) return new Uint8Array(content);
		if (content.length != null) return new Uint8Array(content);
		return null;
	},

	stripHtml(html: string | null | undefined): string {
		if (!html) return '';
		if (typeof DOMParser === 'undefined') return '';
		try {
			const doc = new DOMParser().parseFromString(html, 'text/html');
			return doc.body && doc.body.textContent ? doc.body.textContent : '';
		} catch {
			return '';
		}
	},

	extractRtfPlain(rtf: string): string {
		if (!rtf) return '';
		const plainMatch = rtf.match(/\\plain[\s\S]*?(?=\\par|\\cell|\\row|\\}|$)/i);
		if (plainMatch) {
			return plainMatch[0]
				.replace(/^\\plain\s*/i, '')
				.replace(/\\[a-z]+\d* ?/gi, '')
				.replace(/[{}]/g, '')
				.replace(/\s+/g, ' ')
				.trim();
		}
		return rtf
			.replace(/\\[a-z]+\d* ?/gi, '')
			.replace(/[{}]/g, '')
			.replace(/\s+/g, ' ')
			.trim();
	},

	rgbaToPngBlob(
		width: number | null | undefined,
		height: number | null | undefined,
		rgbaBytes: Uint8Array
	): Promise<Blob | null> {
		const w = Number(width) || 0;
		const h = Number(height) || 0;
		const expected = w * h * 4;
		if (!w || !h || rgbaBytes.length < expected) {
			return Promise.resolve(null);
		}

		return new Promise((resolve) => {
			try {
				const canvas =
					typeof OffscreenCanvas !== 'undefined'
						? new OffscreenCanvas(w, h)
						: document.createElement('canvas');
				canvas.width = w;
				canvas.height = h;
				// The two canvas kinds' getContext overloads do not combine: one call per kind
				const ctx = 'convertToBlob' in canvas ? canvas.getContext('2d') : canvas.getContext('2d');
				if (!ctx) {
					resolve(null);
					return;
				}
				const slice = rgbaBytes.subarray(0, expected);
				const imgData = new ImageData(new Uint8ClampedArray(slice), w, h);
				ctx.putImageData(imgData, 0, 0);

				if ('convertToBlob' in canvas) {
					canvas
						.convertToBlob({ type: 'image/png' })
						.then(resolve)
						.catch(() => resolve(null));
					return;
				}
				canvas.toBlob((blob) => resolve(blob), 'image/png');
			} catch {
				resolve(null);
			}
		});
	},

	/**
	 * @param clipboard - protobuf Clipboard
	 */
	async decodeEntry(clipboard: IClipboard | null | undefined): Promise<RDClipboardDecoded | null> {
		if (!clipboard || RDClipboard.shouldSkipEntry(clipboard)) return null;

		let bytes = RDClipboard.normalizeContent(clipboard.content);
		if (!bytes || !bytes.length) return null;

		const needsDecompress = !!clipboard.compress || RDCompress.isZstdMagic(bytes);
		if (needsDecompress) {
			try {
				bytes = await RDCompress.decompressZstd(bytes, { force: true, maxBytes: RDClipboard.MAX_BYTES });
			} catch (err) {
				if (typeof err === 'object' && err !== null && 'tooLarge' in err && err.tooLarge)
					console.warn('[RDClipboard] Skipped a clipboard entry from the device:', errorMessage(err));
				else RDCompress.reportZstdUnavailable(err);
				return null;
			}
		}

		const fmt = RDClipboard.formatId(clipboard.format);
		const decoder = new TextDecoder('utf-8', { fatal: false });

		switch (fmt) {
			case RDClipboard.FORMAT.Text:
				return { format: 'text', text: decoder.decode(bytes) };
			case RDClipboard.FORMAT.Html: {
				const html = decoder.decode(bytes);
				return { format: 'html', html, text: RDClipboard.stripHtml(html) };
			}
			case RDClipboard.FORMAT.Rtf: {
				const rtf = decoder.decode(bytes);
				return { format: 'rtf', text: RDClipboard.extractRtfPlain(rtf) };
			}
			case RDClipboard.FORMAT.ImagePng:
				return { format: 'image/png', pngBlob: new Blob([blobBytes(bytes)], { type: 'image/png' }) };
			case RDClipboard.FORMAT.ImageRgba: {
				const blob = await RDClipboard.rgbaToPngBlob(clipboard.width, clipboard.height, bytes);
				if (!blob) return null;
				return { format: 'image/png', pngBlob: blob };
			}
			case RDClipboard.FORMAT.ImageSvg: {
				const svg = decoder.decode(bytes);
				return { format: 'image/svg+xml', svgText: svg, text: svg };
			}
			default:
				return null;
		}
	},

	pickBestText(entries: (RDClipboardDecoded | null | undefined)[]): string {
		for (let i = 0; i < entries.length; i++) {
			const e = entries[i];
			if (e && e.format === 'text' && e.text) return e.text;
		}
		for (let i = 0; i < entries.length; i++) {
			const e = entries[i];
			if (e && e.format === 'html' && e.text) return e.text;
		}
		for (let i = 0; i < entries.length; i++) {
			const e = entries[i];
			if (e && e.format === 'rtf' && e.text) return e.text;
		}
		for (let i = 0; i < entries.length; i++) {
			const e = entries[i];
			if (e && e.format === 'image/svg+xml' && e.text) return e.text;
		}
		return '';
	},

	async applyToLocal(
		decodedEntries: (RDClipboardDecoded | null | undefined)[] | null | undefined,
		opts?: { enabled?: boolean } | null
	): Promise<void> {
		const options = opts || {};
		if (!options.enabled) return;
		const entries = (decodedEntries || []).filter((e): e is RDClipboardDecoded => Boolean(e));
		if (!entries.length) return;
		await RDClipboard.writeLocalRich({
			text: RDClipboard.pickBestText(entries),
			html: entries.find((e) => e.format === 'html' && e.html)?.html || '',
			pngBlob: entries.find((e) => e.pngBlob)?.pngBlob || null
		});
	},

	/**
	 * Read the richest clipboard representation supported by this browser.
	 */
	async readLocalRich(): Promise<RDClipboardLocalEntry> {
		if (!navigator.clipboard) throw new Error('clipboard_unavailable');
		if (navigator.clipboard.read) {
			const items = await navigator.clipboard.read();
			const entry: RDClipboardLocalEntry = {};
			for (const item of items || []) {
				if (item.types.includes('text/plain')) {
					entry.text = await (await item.getType('text/plain')).text();
				}
				if (item.types.includes('text/html')) {
					entry.html = await (await item.getType('text/html')).text();
				}
				const imageType = item.types.find((type) => type === 'image/png');
				if (imageType) entry.pngBlob = await item.getType(imageType);
			}
			if (entry.text || entry.html || entry.pngBlob) return entry;
		}
		if (navigator.clipboard.readText) {
			const text = await navigator.clipboard.readText();
			if (text) return { text };
		}
		return {};
	},

	/**
	 * Write a rich clipboard entry. The return value lets the UI distinguish
	 * browser permission failures from an empty clipboard.
	 */
	async writeLocalRich(entry: RDClipboardLocalEntry | null | undefined): Promise<RDClipboardWriteStatus> {
		if (!navigator.clipboard) return { ok: false, reason: 'clipboard_unavailable' };

		const value = entry || {};
		const text = value.text || '';

		try {
			if (
				navigator.clipboard.write &&
				typeof ClipboardItem !== 'undefined' &&
				(value.pngBlob || value.html)
			) {
				const data: Record<string, Blob> = {};
				if (value.pngBlob) data['image/png'] = value.pngBlob;
				if (value.html) {
					data['text/html'] = new Blob([value.html], { type: 'text/html' });
					data['text/plain'] = new Blob([text || RDClipboard.stripHtml(value.html)], {
						type: 'text/plain'
					});
				} else if (text) {
					data['text/plain'] = new Blob([text], { type: 'text/plain' });
				}
				await navigator.clipboard.write([new ClipboardItem(data)]);
				return { ok: true };
			}
			if (text && navigator.clipboard.writeText) {
				await navigator.clipboard.writeText(text);
				return { ok: true };
			}
			return { ok: false, reason: 'clipboard_write_unsupported' };
		} catch {
			if (text && navigator.clipboard.writeText) {
				try {
					await navigator.clipboard.writeText(text);
					return { ok: true, reason: 'rich_write_fallback' };
				} catch {
					return { ok: false, reason: 'clipboard_permission_denied' };
				}
			}
			return { ok: false, reason: 'clipboard_permission_denied' };
		}
	},

	async decodeEntries(
		clipboards: (IClipboard | null | undefined)[] | null | undefined
	): Promise<RDClipboardDecoded[]> {
		const list = clipboards || [];
		const out: RDClipboardDecoded[] = [];
		for (let i = 0; i < list.length; i++) {
			const decoded = await RDClipboard.decodeEntry(list[i]);
			if (decoded) out.push(decoded);
		}
		return out;
	}
};
