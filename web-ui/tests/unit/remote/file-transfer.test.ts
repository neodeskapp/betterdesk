/**
 * RustDesk file transfer in the browser viewer (src/lib/remote/client/
 * filetransfer.ts): downloads, uploads and folder jobs against a fake
 * device that answers through handleFileResponse. Nothing goes on a wire;
 * what the client sends is caught as messages.
 */
import { afterEach, describe, expect, test, vi } from 'vitest';
import { RDFileTransfer } from '../../../src/lib/remote/client/filetransfer.ts';
import { FileResponse, type MessageInit } from '../../../src/lib/remote/client/generated/protos.ts';
import { LocalFiles } from '../../../src/lib/remote/client/local-files.ts';
import { RDProtocol } from '../../../src/lib/remote/client/protocol.ts';

const enc = (s: string) => new Uint8Array(new TextEncoder().encode(s));
const dec = (b: Uint8Array) => new TextDecoder().decode(b);
const tick = (ms = 0) => new Promise((r) => setTimeout(r, ms));

afterEach(() => {
	vi.useRealTimers();
	vi.restoreAllMocks();
});

async function setup() {
	const proto = new RDProtocol();
	await proto.load();
	const sent: MessageInit[] = [];
	const events: [string, Record<string, unknown>][] = [];
	const ft = new RDFileTransfer({
		proto,
		sendMessage: (msg) => sent.push(msg),
		emit: (event: string, ...args: unknown[]) =>
			events.push([event, (args[0] ?? {}) as Record<string, unknown>])
	});
	ft.enable();
	const saved: { name: string; text: string }[] = [];
	ft._saveDownload = async (name, blob) => {
		saved.push({ name, text: await blob.text() });
		return true;
	};
	/** A FileResponse as it comes off the wire */
	const respond = (resp: object) =>
		ft.handleFileResponse(FileResponse.decode(FileResponse.encode(FileResponse.fromObject(resp)).finish()));
	const named = (event: string) => events.filter(([e]) => e === event).map(([, a]) => a);
	return { proto, ft, sent, events, saved, respond, named };
}

/** Decompression that takes a while, as DecompressionStream does: the bytes come back as they went in */
function slowDecompress(ft: RDFileTransfer) {
	return vi.spyOn(ft, '_decompressBlock').mockImplementation(async (data: Uint8Array) => {
		await tick(5);
		return new Uint8Array(data);
	});
}

describe('a download with compressed blocks', () => {
	test('in the browser: the file holds every block in the order the device sent them', async () => {
		const t = await setup();
		slowDecompress(t.ft);
		const id = t.ft.downloadFile('C:\\data', { name: 'a.bin', size: 9 });
		t.respond({ digest: { id, fileNum: 0, fileSize: 9 } });
		t.respond({ block: { id, data: enc('AAA'), compressed: true } });
		t.respond({ block: { id, data: enc('BBB'), compressed: false } });
		t.respond({ block: { id, data: enc('CCC'), compressed: true } });
		t.respond({ done: { id } });
		await vi.waitFor(() => expect(t.saved).toHaveLength(1));
		expect(t.saved[0]).toEqual({ name: 'a.bin', text: 'AAABBBCCC' });
		expect(t.named('file_transfer_error')).toEqual([]);
	});

	test('on the desktop: every block is written in order before the file is closed', async () => {
		const t = await setup();
		slowDecompress(t.ft);
		vi.spyOn(t.ft, '_supportsStreamDownload').mockReturnValue(true);
		const disk: string[] = [];
		vi.spyOn(LocalFiles, 'beginDownload').mockResolvedValue({ started: true, handle: 7, path: '/tmp/a.bin' });
		vi.spyOn(LocalFiles, 'writeDownload').mockImplementation(async (_handle, data) => {
			disk.push(dec(data as Uint8Array));
		});
		vi.spyOn(LocalFiles, 'finishDownload').mockImplementation(async () => {
			disk.push('<closed>');
			return { saved: true };
		});
		const id = t.ft.downloadFile('C:\\data', { name: 'a.bin', size: 9 });
		await vi.waitFor(() => expect(t.sent.some((m) => m.fileAction?.send)).toBe(true));
		t.respond({ digest: { id, fileNum: 0, fileSize: 9 } });
		t.respond({ block: { id, data: enc('AAA'), compressed: true } });
		t.respond({ block: { id, data: enc('BBB'), compressed: false } });
		t.respond({ block: { id, data: enc('CCC'), compressed: true } });
		t.respond({ done: { id } });
		await vi.waitFor(() => expect(t.named('file_transfer_complete')).toHaveLength(1));
		expect(disk.join('')).toBe('AAABBBCCC<closed>');
	});
});

/** The device's folders: each lists its files (name and text) */
const TREE: Record<string, { name: string; text: string }[]> = {
	'/data/docs': [{ name: 'a.txt', text: 'abc' }],
	'/data/other': [{ name: 'b.txt', text: 'xyz' }]
};

/** A device that lists folders but never starts sending a file */
function silentSender(t: Awaited<ReturnType<typeof setup>>) {
	t.ft._sendMessage = (msg) => {
		t.sent.push(msg);
		const path = msg.fileAction?.readDir?.path;
		if (path != null) {
			const entries = (TREE[path] ?? []).map((f) => ({ name: f.name, entryType: 4, size: f.text.length }));
			queueMicrotask(() => t.respond({ dir: { id: 0, path, entries } }));
		}
	};
}

const readDirs = (t: Awaited<ReturnType<typeof setup>>) =>
	t.sent.map((m) => m.fileAction?.readDir?.path).filter((p) => p != null);
const sendIds = (t: Awaited<ReturnType<typeof setup>>) =>
	t.sent.map((m) => m.fileAction?.send?.id).filter((p) => p != null);

describe('a folder transfer whose file does not go through', () => {
	test('a file the device never starts fails the folder after the stall time, and the next folder runs', async () => {
		const t = await setup();
		silentSender(t);
		t.ft.TRANSFER_STALL_MS = 20;
		const first = t.ft.downloadFolderAsZip('/data', { name: 'docs', isDir: true });
		t.ft.downloadFolderAsZip('/data', { name: 'other', isDir: true });
		await vi.waitFor(() =>
			expect(t.named('file_transfer_error').filter((e) => e.id === first && e.isFolder)).toHaveLength(1)
		);
		await vi.waitFor(() => expect(readDirs(t)).toEqual(['/data/docs', '/data/other']));
	});

	test('cancelling the file of a folder ends that folder, and the next folder runs', async () => {
		const t = await setup();
		silentSender(t);
		const first = t.ft.downloadFolderAsZip('/data', { name: 'docs', isDir: true });
		t.ft.downloadFolderAsZip('/data', { name: 'other', isDir: true });
		await vi.waitFor(() => expect(sendIds(t)).toHaveLength(1));
		t.ft.cancelTransfer(Number(sendIds(t)[0]));
		await vi.waitFor(() =>
			expect(t.named('file_transfer_error').filter((e) => e.id === first && e.isFolder)).toHaveLength(1)
		);
		await vi.waitFor(() => expect(readDirs(t)).toEqual(['/data/docs', '/data/other']));
	});

	test('on the desktop, a download the device never starts closes and removes its file', async () => {
		const t = await setup();
		silentSender(t);
		t.ft.TRANSFER_STALL_MS = 20;
		vi.spyOn(t.ft, '_supportsStreamDownload').mockReturnValue(true);
		vi.spyOn(LocalFiles, 'beginDownload').mockResolvedValue({ started: true, handle: 7, path: '/tmp/a.txt' });
		const abort = vi.spyOn(LocalFiles, 'abortDownload').mockResolvedValue(undefined);
		const id = t.ft.downloadFile('/data/docs', { name: 'a.txt', size: 3 });
		await vi.waitFor(() => expect(t.named('file_transfer_error')).toHaveLength(1));
		expect(t.named('file_transfer_error')[0]).toMatchObject({ id, error: 'Remote did not start transfer' });
		expect(abort).toHaveBeenCalledWith(7, true);
	});
});
