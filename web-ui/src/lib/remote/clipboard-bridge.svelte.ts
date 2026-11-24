/**
 * The clipboard between this computer and the remote one, for the browser
 * viewer: text and images both ways, without a button.
 *
 * This computer to the remote one:
 * - Ctrl+V (Cmd+V on a Mac) in the viewer: the browser's paste event
 *   carries the clipboard in every browser, also over plain HTTP. The
 *   viewer holds the key presses back (pasteGate), sends the clipboard,
 *   then lets the keys go, so the remote pastes what was just sent.
 * - Where the browser lets the page read the clipboard without asking
 *   (Chromium once the permission is granted), the clipboard also goes
 *   over when the viewer gets the focus.
 *
 * The remote computer to this one:
 * - Each change the remote sends is written at once where the browser
 *   allows it: Chromium while the page has the focus, Firefox within
 *   5 seconds of a key press or click in the page (its user activation),
 *   so a Ctrl+C or a click on Copy in the remote usually lands at once.
 * - Otherwise the newest content waits (pending) and is written on the next
 *   key press or click in the viewer, which is a user gesture everywhere.
 * - Safari allows a write only inside the gesture itself; Ctrl+C there
 *   hands the browser a clipboard item whose text follows when the remote
 *   answers.
 *
 * Plain HTTP pages have no clipboard API: text from the remote is written
 * with the copy command inside a gesture, images can't be written there.
 */

/** Size caps (bytes): text and images sent from here, and what the remote may send */
export const CLIPBOARD_LIMITS = Object.freeze({
	text: 4 * 1024 * 1024,
	image: 8 * 1024 * 1024,
	fromRemote: 40 * 1024 * 1024
});

/** How long a copy key in the viewer waits for the remote's clipboard (and Firefox's user activation lasts) */
export const COPY_WAIT_MS = 5000;

/** How long the held paste keys wait for the browser's paste event */
export const PASTE_WAIT_MS = 400;

export interface ClipEntry {
	text?: string;
	pngBlob?: Blob | null;
}

/** What the toolbar shows */
export type ClipboardState =
	/** Clipboard sharing works both ways */
	| 'on'
	/** Turned off: the toggle, the device, view only, or no session */
	| 'off'
	/** Chromium: a click grants the browser permission to read the clipboard */
	| 'permission'
	/** Content copied on the remote waits for a key press or click here */
	| 'pending';

/** Why the clipboard is off (the toolbar's tooltip) */
export type ClipboardOffReason = 'toggle' | 'device' | 'view_only' | 'not_streaming' | '';

/** Something the person should hear about */
export type ClipboardNotice =
	| { kind: 'too_large'; what: 'text' | 'image'; bytes: number; limit: number }
	| { kind: 'from_remote_too_large' }
	| { kind: 'image_unsupported' }
	| { kind: 'send_failed' };

/** The browser parts the bridge uses; tests pass their own */
export interface ClipboardEnv {
	/** navigator.clipboard (missing on plain HTTP) */
	clipboard: Clipboard | undefined;
	/** navigator.permissions, to see whether reading is granted (Chromium) */
	permissions: Permissions | undefined;
	ClipboardItem: typeof ClipboardItem | undefined;
	/** The copy command with a text, inside a gesture (plain HTTP); true when it ran */
	execCopy(text: string): boolean;
	/** Safari and other WebKit browsers: writes only inside the gesture */
	webkit: boolean;
	now(): number;
}

export interface ClipboardHooks {
	/** The session takes the clipboard: streaming, not view only, clipboard allowed */
	allowed(): ClipboardOffReason;
	/** Send to the remote; resolves when it went out */
	send(entry: ClipEntry): Promise<void>;
	notify(notice: ClipboardNotice): void;
}

export function browserClipboardEnv(): ClipboardEnv {
	const nav = typeof navigator !== 'undefined' ? navigator : undefined;
	const ua = nav?.userAgent ?? '';
	return {
		clipboard: nav?.clipboard,
		permissions: nav?.permissions,
		ClipboardItem: typeof ClipboardItem !== 'undefined' ? ClipboardItem : undefined,
		execCopy,
		webkit: /AppleWebKit/.test(ua) && !/Chrome|Chromium|Edg\//.test(ua),
		now: () => Date.now()
	};
}

/** The copy command: a one-off copy listener puts the text on the clipboard */
function execCopy(text: string): boolean {
	if (typeof document === 'undefined' || typeof document.execCommand !== 'function') return false;
	let done = false;
	const onCopy = (e: ClipboardEvent) => {
		if (!e.clipboardData) return;
		e.clipboardData.setData('text/plain', text);
		e.preventDefault();
		done = true;
	};
	document.addEventListener('copy', onCopy, true);
	try {
		document.execCommand('copy');
	} catch {
		/* refused */
	} finally {
		document.removeEventListener('copy', onCopy, true);
	}
	return done;
}

/** A short key for a clipboard content: the same content is not sent twice in a row */
export function entryKey(entry: ClipEntry): string {
	let h = 0x811c9dc5;
	const text = entry.text ?? '';
	for (let i = 0; i < text.length; i++) {
		h ^= text.charCodeAt(i);
		h = Math.imul(h, 0x01000193) >>> 0;
	}
	return `${text.length}:${h.toString(16)}:${entry.pngBlob ? entry.pngBlob.size : '-'}`;
}

function utf8Length(text: string): number {
	return new TextEncoder().encode(text).length;
}

/** An image of another type as PNG (the format the remote takes), or null when the browser can't */
async function asPng(blob: Blob): Promise<Blob | null> {
	if (blob.type === 'image/png') return blob;
	if (typeof createImageBitmap !== 'function') return null;
	try {
		const bitmap = await createImageBitmap(blob);
		const canvas =
			typeof OffscreenCanvas !== 'undefined'
				? new OffscreenCanvas(bitmap.width, bitmap.height)
				: Object.assign(document.createElement('canvas'), { width: bitmap.width, height: bitmap.height });
		const ctx = canvas.getContext('2d') as
			CanvasRenderingContext2D | OffscreenCanvasRenderingContext2D | null;
		if (!ctx) return null;
		ctx.drawImage(bitmap, 0, 0);
		bitmap.close();
		if ('convertToBlob' in canvas) return await canvas.convertToBlob({ type: 'image/png' });
		return await new Promise((resolve) => canvas.toBlob((b) => resolve(b), 'image/png'));
	} catch {
		return null;
	}
}

/** The text and the first image of a paste event's data */
export function fromDataTransfer(data: DataTransfer | null): { text: string; image: Blob | null } {
	if (!data) return { text: '', image: null };
	const text = data.getData('text/plain') || '';
	let image: Blob | null = null;
	for (const item of Array.from(data.items ?? [])) {
		if (item.kind === 'file' && item.type.startsWith('image/')) {
			image = item.getAsFile();
			if (image) break;
		}
	}
	if (!image) image = Array.from(data.files ?? []).find((f) => f.type.startsWith('image/')) ?? null;
	return { text, image };
}

export type SendResult = 'sent' | 'same' | 'empty' | 'off' | 'failed';

export class ClipboardBridge {
	/** What the toolbar shows (permission comes from the browser, pending from the remote) */
	readPermission = $state<'granted' | 'prompt' | 'denied' | 'unsupported'>('unsupported');
	/** Content from the remote that waits for a gesture here */
	pending = $state.raw<ClipEntry | null>(null);
	/** The page has no clipboard API (plain HTTP) */
	readonly insecure: boolean;

	private env: ClipboardEnv;
	private hooks: ClipboardHooks;
	/** What was last sent, or last written here from the remote: both are on the remote already */
	private lastKey = '';
	private copyArmedAt = -Infinity;
	/** Safari's copy key: the item's text waits for the remote */
	private copyWaiter: ((entry: ClipEntry | null) => void) | null = null;
	private writing = false;
	private permissionWatched = false;

	constructor(hooks: ClipboardHooks, env: ClipboardEnv = browserClipboardEnv()) {
		this.hooks = hooks;
		this.env = env;
		this.insecure = !env.clipboard;
	}

	/** The state for the toolbar */
	state(): ClipboardState {
		if (this.hooks.allowed()) return 'off';
		if (this.pending) return 'pending';
		if (this.readPermission === 'prompt') return 'permission';
		return 'on';
	}

	/** Asks the browser once whether this page may read the clipboard without a prompt (Chromium) */
	async watchPermission(): Promise<void> {
		if (this.permissionWatched || !this.env.permissions?.query || !this.env.clipboard?.readText) return;
		this.permissionWatched = true;
		try {
			const status = await this.env.permissions.query({ name: 'clipboard-read' as PermissionName });
			this.readPermission = status.state;
			status.onchange = () => {
				this.readPermission = status.state;
			};
		} catch {
			// Firefox and Safari know no such permission: they ask on each read, so the paste key is the way
			this.readPermission = 'unsupported';
		}
	}

	/* ── This computer to the remote ── */

	/** Text and image of a paste in the viewer (Ctrl+V): to the remote, once */
	async fromPaste(data: DataTransfer | null): Promise<SendResult> {
		const { text, image } = fromDataTransfer(data);
		return this.sendLocal(text, image);
	}

	/**
	 * Reads the clipboard without a prompt where the browser allows it and
	 * sends what changed: Chromium with the permission granted. Called when
	 * the viewer gets the focus.
	 */
	async readSilently(): Promise<SendResult> {
		if (this.readPermission !== 'granted' || !this.env.clipboard) return 'off';
		if (this.hooks.allowed()) return 'off';
		const read = await this.readClipboard();
		if (!read) return 'failed';
		return this.sendLocal(read.text, read.image);
	}

	/**
	 * One click on the toolbar (Chromium): the browser asks for the
	 * permission and, once granted, the clipboard goes over. true when granted.
	 */
	async requestPermission(): Promise<boolean> {
		if (!this.env.clipboard?.readText) return false;
		const read = await this.readClipboard();
		if (!read) {
			if (this.readPermission === 'prompt') this.readPermission = 'denied';
			return false;
		}
		this.readPermission = 'granted';
		if (!this.hooks.allowed()) await this.sendLocal(read.text, read.image);
		return true;
	}

	private async readClipboard(): Promise<{ text: string; image: Blob | null } | null> {
		const cb = this.env.clipboard;
		if (!cb) return null;
		try {
			if (typeof cb.read === 'function') {
				let text = '';
				let image: Blob | null = null;
				for (const item of await cb.read()) {
					if (!text && item.types.includes('text/plain'))
						text = await (await item.getType('text/plain')).text();
					const type = item.types.find((t) => t.startsWith('image/'));
					if (!image && type) image = await item.getType(type);
				}
				return { text, image };
			}
			return { text: await cb.readText(), image: null };
		} catch {
			return null;
		}
	}

	private async sendLocal(text: string, image: Blob | null): Promise<SendResult> {
		if (this.hooks.allowed()) return 'off';
		const entry: ClipEntry = {};
		if (text) {
			const bytes = utf8Length(text);
			if (bytes > CLIPBOARD_LIMITS.text)
				this.hooks.notify({ kind: 'too_large', what: 'text', bytes, limit: CLIPBOARD_LIMITS.text });
			else entry.text = text;
		}
		if (image) {
			const png = await asPng(image);
			if (!png) this.hooks.notify({ kind: 'image_unsupported' });
			else if (png.size > CLIPBOARD_LIMITS.image)
				this.hooks.notify({
					kind: 'too_large',
					what: 'image',
					bytes: png.size,
					limit: CLIPBOARD_LIMITS.image
				});
			else entry.pngBlob = png;
		}
		if (!entry.text && !entry.pngBlob) return 'empty';
		const key = entryKey(entry);
		if (key === this.lastKey) return 'same';
		try {
			await this.hooks.send(entry);
		} catch {
			this.hooks.notify({ kind: 'send_failed' });
			return 'failed';
		}
		this.lastKey = key;
		return 'sent';
	}

	/* ── The remote computer to this one ── */

	/** The remote's clipboard changed: write it here now if the browser lets us, else hold it */
	fromRemote(entry: ClipEntry): void {
		if (this.hooks.allowed()) return;
		if (!entry.text && !entry.pngBlob) return;
		// What the remote sends back is often what was just sent from here
		const key = entryKey(entry);
		if (key === this.lastKey && !this.pending) return;
		if (this.copyWaiter) {
			const resolve = this.copyWaiter;
			this.copyWaiter = null;
			resolve(entry);
			this.lastKey = key;
			this.pending = null;
			return;
		}
		this.pending = entry;
		void this.writePending();
	}

	/**
	 * A key press or click in the viewer: a user gesture, in which every
	 * browser lets the page write. Call it inside the event handler.
	 */
	gesture(): void {
		if (this.pending && !this.writing) void this.writePending();
	}

	/**
	 * Ctrl+C (Cmd+C) in the viewer: the copy goes to the remote as a key; its
	 * answer is written when it comes (within COPY_WAIT_MS it is still in the
	 * gesture's time in Firefox). Safari gets a clipboard item now whose text
	 * follows. Call it inside the key handler.
	 */
	copyKey(): void {
		if (this.hooks.allowed()) return;
		this.copyArmedAt = this.env.now();
		const { clipboard, ClipboardItem: Item, webkit } = this.env;
		if (!webkit || !clipboard?.write || !Item) return;
		const text = new Promise<Blob>((resolve, reject) => {
			const timer = setTimeout(() => {
				this.copyWaiter = null;
				reject(new Error('no copy from the remote'));
			}, COPY_WAIT_MS);
			this.copyWaiter = (entry) => {
				clearTimeout(timer);
				if (entry?.text) resolve(new Blob([entry.text], { type: 'text/plain' }));
				else reject(new Error('no text'));
			};
		});
		clipboard.write([new Item({ 'text/plain': text })]).catch(() => {});
	}

	/** A copy key was pressed in the viewer within the wait */
	copyArmed(): boolean {
		return this.env.now() - this.copyArmedAt < COPY_WAIT_MS;
	}

	/** Writes the pending content; it stays pending when the browser refuses */
	private async writePending(): Promise<void> {
		const entry = this.pending;
		if (!entry || this.writing) return;
		this.writing = true;
		const ok = await this.write(entry).finally(() => {
			this.writing = false;
		});
		if (ok && this.pending === entry) {
			this.pending = null;
			this.lastKey = entryKey(entry);
		}
	}

	/** One write to this computer's clipboard; false when the browser refused */
	private async write(entry: ClipEntry): Promise<boolean> {
		const { clipboard, ClipboardItem: Item } = this.env;
		if (!clipboard) {
			// Plain HTTP: text with the copy command, only inside a gesture
			return !!entry.text && this.env.execCopy(entry.text);
		}
		try {
			if (entry.pngBlob && clipboard.write && Item) {
				const data: Record<string, Blob> = { 'image/png': entry.pngBlob };
				if (entry.text) data['text/plain'] = new Blob([entry.text], { type: 'text/plain' });
				await clipboard.write([new Item(data)]);
				return true;
			}
			if (entry.text && clipboard.writeText) {
				await clipboard.writeText(entry.text);
				return true;
			}
		} catch {
			return false;
		}
		return false;
	}
}

/* ── The keys around a paste ── */

/** Ctrl+V, or Cmd+V on a Mac; Shift+Insert */
export function isPasteKey(e: KeyboardEvent, mac: boolean): boolean {
	if (e.altKey) return false;
	if (e.code === 'Insert' || e.key === 'Insert') return e.shiftKey && !e.ctrlKey && !e.metaKey;
	const v = e.code === 'KeyV' || e.key === 'v' || e.key === 'V';
	return v && (mac ? e.metaKey && !e.ctrlKey : e.ctrlKey && !e.metaKey);
}

/** Ctrl+C, or Cmd+C on a Mac; Ctrl+X cuts and changes the clipboard the same way */
export function isCopyKey(e: KeyboardEvent, mac: boolean): boolean {
	if (e.altKey || e.shiftKey) return false;
	const c = e.code === 'KeyC' || e.code === 'KeyX' || /^[cxCX]$/.test(e.key);
	return c && (mac ? e.metaKey && !e.ctrlKey : e.ctrlKey && !e.metaKey);
}

/**
 * Holds the key presses of a paste until the clipboard went out, then
 * lets them go in order: the remote must not see the paste key before
 * the content, nor the Ctrl key's release before the V.
 */
export class PasteGate {
	private queue: KeyboardEvent[] = [];
	private timer: ReturnType<typeof setTimeout> | null = null;
	private replay: (e: KeyboardEvent) => void;
	private waitMs: number;
	held = false;

	constructor(replay: (e: KeyboardEvent) => void, waitMs = PASTE_WAIT_MS) {
		this.replay = replay;
		this.waitMs = waitMs;
	}

	/** The paste key: hold it and what follows until release() or the wait runs out */
	hold(e: KeyboardEvent): void {
		this.held = true;
		this.queue.push(e);
		if (this.timer) clearTimeout(this.timer);
		this.timer = setTimeout(() => this.release(), this.waitMs);
	}

	/** A key event while held: queued (true), or not held (false) */
	take(e: KeyboardEvent): boolean {
		if (!this.held) return false;
		this.queue.push(e);
		return true;
	}

	/** The paste event came: keep holding until the clipboard went out */
	extend(ms: number): void {
		if (!this.held) return;
		if (this.timer) clearTimeout(this.timer);
		this.timer = setTimeout(() => this.release(), ms);
	}

	release(): void {
		if (this.timer) clearTimeout(this.timer);
		this.timer = null;
		this.held = false;
		const keys = this.queue;
		this.queue = [];
		for (const e of keys) this.replay(e);
	}

	/** Drop what is held (the session ended or lost the focus) */
	clear(): void {
		if (this.timer) clearTimeout(this.timer);
		this.timer = null;
		this.held = false;
		this.queue = [];
	}
}
