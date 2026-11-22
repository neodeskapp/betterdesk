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

describe('resuming a download on the desktop', () => {
	test('starts again where the file ends, not after the blocks that were not written yet', async () => {
		const t = await setup();
		vi.spyOn(t.ft, '_supportsStreamDownload').mockReturnValue(true);
		// Blocks of 3 bytes; the client writes to the file in batches of 6
		t.ft.BLOCK_SIZE = 3;
		t.ft.DOWNLOAD_WRITE_BATCH = 6;
		const disk: string[] = [];
		const begin = vi
			.spyOn(LocalFiles, 'beginDownload')
			.mockResolvedValue({ started: true, handle: 7, path: '/tmp/a.bin' });
		vi.spyOn(LocalFiles, 'writeDownload').mockImplementation(async (_handle, data) => {
			disk.push(dec(data as Uint8Array));
		});
		vi.spyOn(LocalFiles, 'abortDownload').mockResolvedValue(undefined);
		vi.spyOn(LocalFiles, 'finishDownload').mockResolvedValue({ saved: true });
		const confirms = () =>
			t.sent.filter((m) => m.fileAction?.sendConfirm).map((m) => m.fileAction!.sendConfirm!);
		const sends = () => t.sent.filter((m) => m.fileAction?.send);

		const id = t.ft.downloadFile('/data', { name: 'a.bin', size: 12 });
		await vi.waitFor(() => expect(sends()).toHaveLength(1));
		t.respond({ digest: { id, fileNum: 0, fileSize: 12 } });
		for (const part of ['AAA', 'BBB', 'CCC']) t.respond({ block: { id, data: enc(part) } });
		await vi.waitFor(() => expect(disk).toEqual(['AAABBB']));
		t.respond({ error: { id, error: 'connection reset' } });
		expect(t.named('file_transfer_error').at(-1)).toMatchObject({ id, resumable: true });

		t.ft.resumeTransfer(id);
		await vi.waitFor(() => expect(sends()).toHaveLength(2));
		expect(begin.mock.calls.at(-1)?.[0]).toMatchObject({ absolutePath: '/tmp/a.bin', appendOffset: 6 });
		t.respond({ digest: { id, fileNum: 0, fileSize: 12 } });
		expect(confirms().at(-1)?.offsetBlk).toBe(2);
		for (const part of ['CCC', 'DDD']) t.respond({ block: { id, data: enc(part) } });
		t.respond({ done: { id } });
		await vi.waitFor(() => expect(t.named('file_transfer_complete')).toHaveLength(1));
		expect(disk.join('')).toBe('AAABBBCCCDDD');
	});
});

describe('a file for another device (downloadFileToBlob)', () => {
	test('fails at once when file transfer is off for the session', async () => {
		const t = await setup();
		t.ft.disable();
		await expect(t.ft.downloadFileToBlob('/data', { name: 'a.txt', size: 3 })).rejects.toThrow(
			'File transfer not enabled'
		);
		expect(t.sent).toEqual([]);
	});

	test('gives the file once the device sent it', async () => {
		const t = await setup();
		const blob = t.ft.downloadFileToBlob('/data', { name: 'a.txt', size: 3 });
		const id = Number(t.sent[0]?.fileAction?.send?.id);
		t.respond({ digest: { id, fileNum: 0, fileSize: 3 } });
		t.respond({ block: { id, data: enc('abc') } });
		t.respond({ done: { id } });
		expect(await (await blob).text()).toBe('abc');
	});
});

describe('saving a download into the chosen local folder', () => {
	async function downloaded(save: () => Promise<boolean>) {
		const t = await setup();
		t.ft._saveDownload = save;
		const id = t.ft.downloadFile('/data', { name: 'a.txt', size: 3 });
		t.respond({ digest: { id, fileNum: 0, fileSize: 3 } });
		t.respond({ block: { id, data: enc('abc') } });
		t.respond({ done: { id } });
		const outcome = () =>
			t.events.filter(([e, a]) => a.id === id && /complete|error/.test(e)).map(([e]) => e);
		return { t, id, outcome };
	}

	test('reports the download complete only once the file is saved', async () => {
		let finish: (ok: boolean) => void = () => {};
		const d = await downloaded(() => new Promise<boolean>((r) => (finish = r)));
		await tick(10);
		expect(d.outcome()).toEqual([]);
		finish(true);
		await vi.waitFor(() => expect(d.outcome()).toEqual(['file_transfer_complete']));
	});

	test('a save that fails is an error of the download, not a completion followed by an error', async () => {
		const d = await downloaded(async () => {
			throw new Error('disk full');
		});
		await vi.waitFor(() => expect(d.outcome()).toContain('file_transfer_error'));
		await tick(10);
		expect(d.outcome()).toEqual(['file_transfer_error']);
		expect(d.t.named('file_transfer_error')[0]?.error).toBe('Failed to save file: disk full');
	});
});

describe('a folder walk and the sheet listing at the same time', () => {
	test("each listing goes where it was asked for: the sheet's to the sheet, the walk's to the walk", async () => {
		const t = await setup();
		const entries = (name: string) => [{ name, entryType: 4, size: 3 }];
		let walked: string[] | null = null;
		void t.ft.readDirAsync('/data/docs').then((list) => (walked = list.map((e) => e.name)));
		t.ft.browseDir('/data/other');
		t.respond({ dir: { id: 0, path: '/data/other', entries: entries('b.txt') } });
		await tick();
		expect(t.named('file_dir')).toEqual([
			{ path: '/data/other', entries: [expect.objectContaining({ name: 'b.txt' })] }
		]);
		expect(walked).toBeNull();
		t.respond({ dir: { id: 0, path: '/data/docs', entries: entries('a.txt') } });
		await vi.waitFor(() => expect(walked).toEqual(['a.txt']));
		expect(t.named('file_dir')).toHaveLength(1);
	});

	test('a device that names the folder a little differently still answers the walk', async () => {
		const t = await setup();
		let walked: string[] | null = null;
		void t.ft.readDirAsync('C:/data/docs').then((list) => (walked = list.map((e) => e.name)));
		t.respond({ dir: { id: 0, path: 'C:/Data/Docs', entries: [{ name: 'a.txt', entryType: 4 }] } });
		await vi.waitFor(() => expect(walked).toEqual(['a.txt']));
		expect(t.named('file_dir')).toEqual([]);
	});
});

describe('a download and the sheet listing', () => {
	test("the device's file list of a download job leaves the sheet in its folder", async () => {
		const t = await setup();
		t.ft.browseDir('/home/user');
		t.respond({ dir: { id: 0, path: '/home/user', entries: [{ name: 'a.txt', entryType: 4, size: 3 }] } });
		const id = t.ft.downloadFile('/home/user', { name: 'a.txt', size: 3 });
		// RustDesk answers a send request with the job's files under the job's id: the file itself, no name
		t.respond({ dir: { id, path: '/home/user/a.txt', entries: [{ name: '', entryType: 4, size: 3 }] } });
		await tick();
		expect(t.ft.currentPath).toBe('/home/user');
		expect(t.named('file_dir')).toEqual([
			{ path: '/home/user', entries: [expect.objectContaining({ name: 'a.txt' })] }
		]);
	});
});
