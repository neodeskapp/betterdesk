/**
 * "Download folder" in the browser viewer's file sheet (RustDesk file
 * transfer): the client walks the remote folder through the file transfer
 * protocol (read_dir), fetches every file, and saves one ZIP archive of the
 * folder. A fake device answers the file session's requests.
 */
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { FileTransferModal } from '../../../src/lib/remote/client/file-modal.ts';
import { RDFileTransfer } from '../../../src/lib/remote/client/filetransfer.ts';
import { FileResponse, type MessageInit } from '../../../src/lib/remote/client/generated/protos.ts';
import { RDProtocol } from '../../../src/lib/remote/client/protocol.ts';
import { buildZip, crc32 } from '../../../src/lib/remote/client/zip.ts';

/** The device's files: folders map to their entries, files to their text */
const TREE: Record<string, { name: string; dir?: boolean; text?: string }[]> = {
	'C:\\data': [{ name: 'docs', dir: true }],
	'C:\\data\\docs': [
		{ name: 'readme.txt', text: 'hello' },
		{ name: 'sub', dir: true },
		{ name: 'empty', dir: true }
	],
	'C:\\data\\docs\\sub': [{ name: 'notes.md', text: '# notes\nline two' }],
	'C:\\data\\docs\\empty': []
};
const FILES: Record<string, string> = {
	'C:\\data\\docs\\readme.txt': 'hello',
	'C:\\data\\docs\\sub\\notes.md': '# notes\nline two'
};

const enc = (s: string) => new Uint8Array(new TextEncoder().encode(s));

/** A device behind the file session: answers read_dir, send and send_confirm like RustDesk */
function fakeDevice(ft: () => RDFileTransfer) {
	const sends = new Map<number, string>();
	const answer = (resp: object) =>
		queueMicrotask(() =>
			ft().handleFileResponse(
				FileResponse.decode(FileResponse.encode(FileResponse.fromObject(resp)).finish())
			)
		);
	return (msg: MessageInit) => {
		const action = msg.fileAction;
		if (action?.readDir) {
			// An empty path is the device's home folder, as RustDesk answers it
			const path = action.readDir.path || 'C:\\data';
			const entries = (TREE[path] ?? []).map((e) => ({
				name: e.name,
				entryType: e.dir ? 0 : 4,
				size: e.text ? enc(e.text).length : 0,
				modifiedTime: 1_699_999_999
			}));
			answer({ dir: { id: 0, path, entries } });
		} else if (action?.send) {
			const id = Number(action.send.id);
			const path = action.send.path ?? '';
			sends.set(id, path);
			answer({
				digest: { id, fileNum: 0, fileSize: enc(FILES[path] ?? '').length, lastModified: 1_699_999_999 }
			});
		} else if (action?.sendConfirm) {
			const id = Number(action.sendConfirm.id);
			const data = enc(FILES[sends.get(id) ?? ''] ?? '');
			answer({ block: { id, fileNum: 0, data, compressed: false, blkId: 0 } });
			answer({ done: { id, fileNum: 0 } });
		}
	};
}

/** The files of a stored (uncompressed) ZIP archive */
function unzip(bytes: Uint8Array): Record<string, string> {
	const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
	const out: Record<string, string> = {};
	const dec = new TextDecoder();
	let at = 0;
	while (view.getUint32(at, true) === 0x04034b50) {
		const size = view.getUint32(at + 18, true);
		const nameLen = view.getUint16(at + 26, true);
		const extra = view.getUint16(at + 28, true);
		const name = dec.decode(bytes.subarray(at + 30, at + 30 + nameLen));
		const start = at + 30 + nameLen + extra;
		out[name] = dec.decode(bytes.subarray(start, start + size));
		at = start + size;
	}
	return out;
}

let saved: Blob[] = [];
beforeEach(() => {
	saved = [];
	vi.spyOn(URL, 'createObjectURL').mockImplementation((b: Blob | MediaSource) => {
		if (b instanceof Blob) saved.push(b);
		return 'blob:zip';
	});
	vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {});
	vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {});
});
afterEach(() => vi.restoreAllMocks());

describe('Download folder in the browser', () => {
	test('saves the remote folder as one ZIP archive, sub-folders and empty folders included', async () => {
		const alert = vi.spyOn(window, 'alert').mockImplementation(() => {});
		const proto = new RDProtocol();
		await proto.load();
		let device: (msg: MessageInit) => void = () => {};
		const events: [string, unknown][] = [];
		const ft = new RDFileTransfer({
			proto,
			sendMessage: (msg) => device(msg),
			emit: (event: string, ...args: unknown[]) => events.push([event, args[0]]),
			ensureConnected: async () => {},
			isConnected: () => true
		});
		device = fakeDevice(() => ft);
		ft.enable();

		const sheet = new FileTransferModal();
		sheet.open({ deviceId: '900100012', client: { fileTransfer: ft, on: () => {} } });
		// The sheet lists the remote folder when it opens; the operator picks from that list
		await vi.waitFor(() => expect(events.some(([ev]) => ev === 'file_dir')).toBe(true));
		await sheet._downloadRemoteFolder({ name: 'docs', isDir: true, size: 0, entryType: 0 });

		await vi.waitFor(() => expect(saved).toHaveLength(1), { timeout: 4000 });
		expect(alert).not.toHaveBeenCalled();
		const anchor = document.querySelector<HTMLAnchorElement>('a[download]');
		expect(anchor?.download).toBe('docs.zip');
		expect(unzip(new Uint8Array(await saved[0]!.arrayBuffer()))).toEqual({
			'docs/': '',
			'docs/sub/': '',
			'docs/empty/': '',
			'docs/readme.txt': 'hello',
			'docs/sub/notes.md': '# notes\nline two'
		});
		await vi.waitFor(() =>
			expect(
				events.some(
					([ev, data]) => ev === 'file_transfer_complete' && (data as { isFolder?: boolean }).isFolder
				)
			).toBe(true)
		);
		sheet.close();
	});
});

describe('the ZIP archive', () => {
	test('uses the standard CRC-32 and stores the entries a zip tool can list', async () => {
		expect(crc32(new TextEncoder().encode('123456789'))).toBe(0xcbf43926);
		const zip = await buildZip([
			{ path: 'a/' },
			{ path: 'a/b.txt', data: new Blob(['hi']), modifiedTime: 1_700_000_000 }
		]);
		const bytes = new Uint8Array(await zip.arrayBuffer());
		const view = new DataView(bytes.buffer);
		// End of central directory: two entries, the directory right after the files
		const end = bytes.length - 22;
		expect(view.getUint32(end, true)).toBe(0x06054b50);
		expect(view.getUint16(end + 10, true)).toBe(2);
		const dirAt = view.getUint32(end + 16, true);
		expect(view.getUint32(dirAt, true)).toBe(0x02014b50);
		expect(unzip(bytes)).toEqual({ 'a/': '', 'a/b.txt': 'hi' });
		// The file's CRC in its local header
		const second = 30 + 2;
		expect(view.getUint32(second + 14, true)).toBe(crc32(new TextEncoder().encode('hi')));
	});
});
