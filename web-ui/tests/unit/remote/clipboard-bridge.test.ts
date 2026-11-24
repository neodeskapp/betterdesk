/**
 * The viewer's clipboard bridge with a fake browser: what goes to the
 * remote on a paste and when the viewer gets the focus, what is written
 * here when the remote's clipboard changes (at once, or held until the next
 * gesture when the browser refuses), the size caps, the plain HTTP way and
 * the keys held around a paste.
 */
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import {
	CLIPBOARD_LIMITS,
	ClipboardBridge,
	PasteGate,
	isCopyKey,
	isPasteKey,
	type ClipEntry,
	type ClipboardEnv,
	type ClipboardNotice,
	type ClipboardOffReason
} from '../../../src/lib/remote/clipboard-bridge.svelte.ts';

class FakeItem {
	constructor(public data: Record<string, Blob | Promise<Blob>>) {}
	get types() {
		return Object.keys(this.data);
	}
	async getType(t: string) {
		return this.data[t]!;
	}
}

function dataTransfer(text: string, files: File[] = []): DataTransfer {
	return {
		getData: (t: string) => (t === 'text/plain' ? text : ''),
		items: files.map((f) => ({ kind: 'file', type: f.type, getAsFile: () => f })),
		files
	} as unknown as DataTransfer;
}

interface Fake {
	env: ClipboardEnv;
	written: unknown[];
	refuse: { on: boolean };
	readPerm: { state: PermissionState };
	local: { text: string; image: Blob | null };
	copies: string[];
}

function fakeEnv(
	opts: { secure?: boolean; webkit?: boolean; permission?: PermissionState | null } = {}
): Fake {
	const written: unknown[] = [];
	const refuse = { on: false };
	const readPerm = { state: (opts.permission ?? 'prompt') as PermissionState };
	const local = { text: '', image: null as Blob | null };
	const copies: string[] = [];
	const clipboard = {
		writeText: vi.fn(async (t: string) => {
			if (refuse.on) throw new DOMException('no', 'NotAllowedError');
			written.push(t);
		}),
		write: vi.fn(async (items: FakeItem[]) => {
			if (refuse.on) throw new DOMException('no', 'NotAllowedError');
			const data: Record<string, unknown> = {};
			for (const [k, v] of Object.entries(items[0]!.data)) data[k] = await v;
			written.push(data);
		}),
		readText: vi.fn(async () => local.text),
		read: vi.fn(async () => {
			if (readPerm.state === 'denied') throw new DOMException('no', 'NotAllowedError');
			const data: Record<string, Blob> = {};
			if (local.text) data['text/plain'] = new Blob([local.text], { type: 'text/plain' });
			if (local.image) data['image/png'] = local.image;
			return [new FakeItem(data)];
		})
	};
	const env: ClipboardEnv = {
		clipboard: opts.secure === false ? undefined : (clipboard as unknown as Clipboard),
		permissions:
			opts.permission === null
				? undefined
				: ({
						query: async () => {
							const status = { state: readPerm.state, onchange: null };
							return status;
						}
					} as unknown as Permissions),
		ClipboardItem: FakeItem as unknown as typeof ClipboardItem,
		execCopy: (t) => {
			if (refuse.on) return false;
			copies.push(t);
			return true;
		},
		webkit: opts.webkit ?? false,
		now: () => Date.now()
	};
	return { env, written, refuse, readPerm, local, copies };
}

function bridge(fake: Fake, off: { reason: ClipboardOffReason } = { reason: '' }) {
	const sent: ClipEntry[] = [];
	const notices: ClipboardNotice[] = [];
	const b = new ClipboardBridge(
		{
			allowed: () => off.reason,
			send: async (e) => void sent.push(e),
			notify: (n) => void notices.push(n)
		},
		fake.env
	);
	return { b, sent, notices };
}

const png = (size = 64) => new Blob([new Uint8Array(size)], { type: 'image/png' });

beforeEach(() => vi.useFakeTimers());
afterEach(() => vi.useRealTimers());

describe('this computer to the remote', () => {
	test('a paste sends its text and its PNG image, and the same content only once', async () => {
		const { b, sent } = bridge(fakeEnv());
		const image = new File([new Uint8Array(32)], 'shot.png', { type: 'image/png' });
		expect(await b.fromPaste(dataTransfer('hello there', [image]))).toBe('sent');
		expect(sent).toHaveLength(1);
		expect(sent[0]!.text).toBe('hello there');
		expect(sent[0]!.pngBlob?.size).toBe(32);
		expect(await b.fromPaste(dataTransfer('hello there', [image]))).toBe('same');
		expect(sent).toHaveLength(1);
		expect(await b.fromPaste(dataTransfer('something else'))).toBe('sent');
		expect(sent).toHaveLength(2);
	});

	test('an empty paste sends nothing', async () => {
		const { b, sent } = bridge(fakeEnv());
		expect(await b.fromPaste(dataTransfer(''))).toBe('empty');
		expect(await b.fromPaste(null)).toBe('empty');
		expect(sent).toHaveLength(0);
	});

	test('nothing goes while the clipboard is off, the session view only or not streaming', async () => {
		const off = { reason: 'toggle' as ClipboardOffReason };
		const { b, sent } = bridge(fakeEnv({ permission: 'granted' }), off);
		await b.watchPermission();
		for (const reason of ['toggle', 'device', 'view_only', 'not_streaming'] as const) {
			off.reason = reason;
			expect(await b.fromPaste(dataTransfer('secret'))).toBe('off');
			expect(await b.readSilently()).toBe('off');
		}
		expect(sent).toHaveLength(0);
		expect(b.state()).toBe('off');
	});

	test('text and images over the caps are left out and the person is told', async () => {
		const { b, sent, notices } = bridge(fakeEnv());
		const big = 'x'.repeat(CLIPBOARD_LIMITS.text + 1);
		const image = new File([new Uint8Array(CLIPBOARD_LIMITS.image + 1)], 'big.png', { type: 'image/png' });
		expect(await b.fromPaste(dataTransfer(big, [image]))).toBe('empty');
		expect(sent).toHaveLength(0);
		expect(notices.map((n) => n.kind === 'too_large' && n.what)).toEqual(['text', 'image']);
		// The text still goes when only the image is too large
		expect(await b.fromPaste(dataTransfer('small', [image]))).toBe('sent');
		expect(sent[0]).toEqual({ text: 'small' });
	});

	test('the focus reads the clipboard only once the browser granted it (Chromium)', async () => {
		const fake = fakeEnv({ permission: 'prompt' });
		fake.local.text = 'copied here';
		const { b, sent } = bridge(fake);
		await b.watchPermission();
		expect(b.state()).toBe('permission');
		expect(await b.readSilently()).toBe('off');
		expect(sent).toHaveLength(0);
		// One click on the toolbar: the browser asks, then the clipboard goes over
		expect(await b.requestPermission()).toBe(true);
		expect(b.state()).toBe('on');
		expect(sent.map((e) => e.text)).toEqual(['copied here']);
		fake.local.image = png(10);
		expect(await b.readSilently()).toBe('sent');
		expect(sent[1]!.pngBlob?.size).toBe(10);
		expect(await b.readSilently()).toBe('same');
	});

	test('a browser without the permission (Firefox, Safari) shows the clipboard as on: the paste key works', async () => {
		const { b } = bridge(fakeEnv({ permission: null }));
		await b.watchPermission();
		expect(b.state()).toBe('on');
		expect(await b.readSilently()).toBe('off');
	});
});

describe('the remote computer to this one', () => {
	test('a change is written at once where the browser allows it', async () => {
		const fake = fakeEnv();
		const { b } = bridge(fake);
		b.fromRemote({ text: 'from the remote' });
		await vi.runAllTimersAsync();
		expect(fake.written).toEqual(['from the remote']);
		expect(b.pending).toBeNull();
	});

	test('an image is written as PNG together with its text', async () => {
		const fake = fakeEnv();
		const { b } = bridge(fake);
		const image = png(20);
		b.fromRemote({ text: 'caption', pngBlob: image });
		await vi.runAllTimersAsync();
		const item = fake.written[0] as Record<string, Blob>;
		expect(item['image/png']).toBe(image);
		expect(await item['text/plain']!.text()).toBe('caption');
	});

	test('refused without a gesture: held, then written on the next key press or click', async () => {
		const fake = fakeEnv();
		const { b } = bridge(fake);
		fake.refuse.on = true;
		b.fromRemote({ text: 'first' });
		b.fromRemote({ text: 'newest' });
		await vi.runAllTimersAsync();
		expect(fake.written).toEqual([]);
		expect(b.pending).toEqual({ text: 'newest' });
		expect(b.state()).toBe('pending');
		fake.refuse.on = false;
		b.gesture();
		await vi.runAllTimersAsync();
		expect(fake.written).toEqual(['newest']);
		expect(b.pending).toBeNull();
		expect(b.state()).toBe('on');
		// A later gesture writes nothing again
		b.gesture();
		await vi.runAllTimersAsync();
		expect(fake.written).toEqual(['newest']);
	});

	test('what the remote echoes back after a paste from here is not written again', async () => {
		const fake = fakeEnv();
		const { b } = bridge(fake);
		await b.fromPaste(dataTransfer('sent from here'));
		b.fromRemote({ text: 'sent from here' });
		await vi.runAllTimersAsync();
		expect(fake.written).toEqual([]);
	});

	test('with the clipboard off nothing from the remote is kept', async () => {
		const fake = fakeEnv();
		const off = { reason: 'device' as ClipboardOffReason };
		const { b } = bridge(fake, off);
		b.fromRemote({ text: 'leak' });
		await vi.runAllTimersAsync();
		expect(fake.written).toEqual([]);
		expect(b.pending).toBeNull();
	});

	test('plain HTTP: text goes with the copy command inside a gesture, images wait', async () => {
		const fake = fakeEnv({ secure: false });
		const { b } = bridge(fake);
		expect(b.insecure).toBe(true);
		fake.refuse.on = true;
		b.fromRemote({ text: 'over http' });
		await vi.runAllTimersAsync();
		expect(b.pending).toEqual({ text: 'over http' });
		fake.refuse.on = false;
		b.gesture();
		await vi.runAllTimersAsync();
		expect(fake.copies).toEqual(['over http']);
		expect(b.pending).toBeNull();
		b.fromRemote({ pngBlob: png() });
		b.gesture();
		await vi.runAllTimersAsync();
		expect(b.pending).not.toBeNull();
	});

	test('Safari: the copy key hands over an item whose text follows from the remote', async () => {
		const fake = fakeEnv({ webkit: true });
		const { b } = bridge(fake);
		b.copyKey();
		expect(b.copyArmed()).toBe(true);
		b.fromRemote({ text: 'copied over there' });
		await vi.runAllTimersAsync();
		const item = fake.written[0] as Record<string, Blob>;
		expect(await item['text/plain']!.text()).toBe('copied over there');
		expect(b.pending).toBeNull();
		vi.advanceTimersByTime(6000);
		expect(b.copyArmed()).toBe(false);
	});
});

describe('keys', () => {
	const key = (init: KeyboardEventInit) => new KeyboardEvent('keydown', init);

	test('paste and copy keys per system', () => {
		expect(isPasteKey(key({ code: 'KeyV', key: 'v', ctrlKey: true }), false)).toBe(true);
		expect(isPasteKey(key({ code: 'KeyV', key: 'v', metaKey: true }), false)).toBe(false);
		expect(isPasteKey(key({ code: 'KeyV', key: 'v', metaKey: true }), true)).toBe(true);
		expect(isPasteKey(key({ code: 'KeyV', key: 'v', ctrlKey: true, altKey: true }), false)).toBe(false);
		expect(isPasteKey(key({ code: 'Insert', key: 'Insert', shiftKey: true }), false)).toBe(true);
		expect(isPasteKey(key({ code: 'KeyV', key: 'v' }), false)).toBe(false);
		expect(isCopyKey(key({ code: 'KeyC', key: 'c', ctrlKey: true }), false)).toBe(true);
		expect(isCopyKey(key({ code: 'KeyX', key: 'x', ctrlKey: true }), false)).toBe(true);
		expect(isCopyKey(key({ code: 'KeyC', key: 'C', ctrlKey: true, shiftKey: true }), false)).toBe(false);
	});

	test('the paste gate holds the keys in order until released, or until its wait runs out', () => {
		const out: string[] = [];
		const gate = new PasteGate((e) => out.push(`${e.type}:${e.code}`), 400);
		expect(gate.take(new KeyboardEvent('keyup', { code: 'KeyA' }))).toBe(false);
		gate.hold(new KeyboardEvent('keydown', { code: 'KeyV' }));
		expect(gate.take(new KeyboardEvent('keyup', { code: 'KeyV' }))).toBe(true);
		expect(gate.take(new KeyboardEvent('keyup', { code: 'ControlLeft' }))).toBe(true);
		expect(out).toEqual([]);
		gate.release();
		expect(out).toEqual(['keydown:KeyV', 'keyup:KeyV', 'keyup:ControlLeft']);
		gate.hold(new KeyboardEvent('keydown', { code: 'KeyV' }));
		vi.advanceTimersByTime(399);
		expect(out).toHaveLength(3);
		vi.advanceTimersByTime(1);
		expect(out).toHaveLength(4);
	});
});
