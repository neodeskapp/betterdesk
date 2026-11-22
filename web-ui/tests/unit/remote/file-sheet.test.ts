/**
 * The file sheet of the remote viewer (src/lib/remote/client/file-modal.ts)
 * with fake sessions: what it listens to when it moves between devices.
 */
import { describe, expect, test, vi } from 'vitest';
import { FileTransferModal, type FileSheetTransfer } from '../../../src/lib/remote/client/file-modal.ts';

type Listener = (...a: never[]) => void;

/** A session's client: its listeners per event, and a file transfer with every method the sheet may call */
function session(deviceId: string, transfer: Partial<FileSheetTransfer> = {}) {
	const listeners = new Map<string, Listener[]>();
	const fileTransfer: FileSheetTransfer = {
		browseDir: vi.fn(),
		browseParent: vi.fn(),
		uploadFile: vi.fn(),
		downloadFile: vi.fn(),
		cancelTransfer: vi.fn(),
		enabled: true,
		currentPath: '',
		...transfer
	};
	const client = {
		on(ev: string, fn: Listener) {
			listeners.set(ev, [...(listeners.get(ev) ?? []), fn]);
		},
		fileTransfer
	};
	const emit = (ev: string, ...a: unknown[]) =>
		(listeners.get(ev) ?? []).forEach((fn) => fn(...(a as never[])));
	return { deviceId, client, listeners, emit };
}

const remotePath = (sheet: FileTransferModal) => sheet._el?.querySelector('.ft-remote-path')?.textContent;

describe('the file sheet moving between sessions', () => {
	test('listens to each session once, and only the session it shows repaints it', () => {
		const a = session('900100001');
		const b = session('900100002');
		const sheet = new FileTransferModal();
		sheet.open(a);
		sheet.open(b);
		sheet.open(a);
		expect(a.listeners.get('file_dir')).toHaveLength(1);
		a.emit('file_dir', { path: '/a', entries: [] });
		expect(remotePath(sheet)).toBe('/a');
		b.emit('file_dir', { path: '/b', entries: [] });
		expect(remotePath(sheet)).toBe('/a');
		sheet.close();
	});

	test('a new client for the same device gets the sheet too', () => {
		const first = session('900100001');
		const again = session('900100001');
		const sheet = new FileTransferModal();
		sheet.open(first);
		sheet.open(again);
		again.emit('file_dir', { path: '/again', entries: [] });
		expect(remotePath(sheet)).toBe('/again');
		sheet.close();
	});
});
