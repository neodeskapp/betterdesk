/**
 * The file sheet of the remote viewer (src/lib/remote/client/file-modal.ts)
 * with fake sessions: what it listens to when it moves between devices, and
 * the actions it offers for the file transfer of each kind of session.
 */
import { describe, expect, test, vi } from 'vitest';
import { CDAPFileTransfer } from '../../../src/lib/remote/client/cdap-filetransfer.ts';
import { FileTransferModal, type FileSheetTransfer } from '../../../src/lib/remote/client/file-modal.ts';
import { MeshFileTransfer } from '../../../src/lib/remote/client/mesh-files.ts';

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

describe('the actions the sheet offers', () => {
	const entries = [
		{ name: 'docs', isDir: true, size: 0, entryType: 0 },
		{ name: 'a.txt', isDir: false, size: 3, entryType: 4 }
	];

	/** The sheet on a session, the remote folder listed; what each menu and the toolbar offer */
	function offers(transfer: FileSheetTransfer) {
		const s = session('900100001', transfer);
		s.client.fileTransfer = transfer;
		const sheet = new FileTransferModal();
		sheet.open(s);
		s.emit('file_dir', { path: '/home', entries });
		const menu = (open: () => void) => {
			open();
			const items = [...(sheet._contextMenu?.querySelectorAll('.ft-context-item') ?? [])].map(
				(b) => b.textContent
			);
			sheet._hideContextMenu();
			return items;
		};
		const rows = sheet._el!.querySelectorAll<HTMLElement>('.ft-remote-list .ft-row');
		const rightClick = (row: HTMLElement) => () =>
			row.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true }));
		const toolbar = (sel: string) => sheet._el!.querySelector<HTMLElement>(sel)!.style.display !== 'none';
		const result = {
			folder: menu(rightClick(rows[0]!)),
			file: menu(rightClick(rows[1]!)),
			localFolder: menu(() =>
				sheet._showLocalContextMenu(new MouseEvent('contextmenu'), {
					name: 'pics',
					isDir: true,
					path: '/pics'
				} as never)
			),
			newFolder: toolbar('.ft-remote-new-folder'),
			showHidden: toolbar('.ft-remote-hidden')
		};
		sheet.close();
		return result;
	}

	const base = (): FileSheetTransfer => ({
		browseDir: vi.fn(),
		browseParent: vi.fn(),
		uploadFile: vi.fn(),
		downloadFile: vi.fn(),
		cancelTransfer: vi.fn(),
		enabled: true,
		currentPath: '/home'
	});

	test('a RustDesk session: everything', () => {
		expect(
			offers({
				...base(),
				createDir: vi.fn(),
				rename: vi.fn(),
				removeDir: vi.fn(),
				removeFile: vi.fn(),
				setShowHidden: vi.fn(),
				uploadFolder: vi.fn(),
				downloadFolder: vi.fn(),
				downloadFolderAsZip: vi.fn()
			})
		).toEqual({
			folder: [
				'folder_openOpen',
				'folder_zipDownload folder',
				'drive_file_rename_outlineRename',
				'delete_outlineDelete'
			],
			file: ['downloadDownload', 'drive_file_rename_outlineRename', 'delete_outlineDelete'],
			localFolder: ['folder_openOpen', 'drive_folder_uploadUpload folder'],
			newFolder: true,
			showHidden: true
		});
	});

	test('a CDAP session: no new folder, rename, delete or folder transfers', () => {
		const cdap = new CDAPFileTransfer({ deviceId: 'cdap-1', emit: () => {} });
		vi.spyOn(cdap, 'browseDir').mockImplementation(() => {});
		expect(offers(cdap)).toEqual({
			folder: ['folder_openOpen'],
			file: ['downloadDownload'],
			localFolder: ['folder_openOpen'],
			newFolder: false,
			showHidden: true
		});
	});

	test('a mesh session: no hidden files, rename or folder transfers', () => {
		const mesh = new MeshFileTransfer('mesh01', () => {});
		vi.spyOn(mesh, 'browseDir').mockImplementation(() => {});
		expect(offers(mesh)).toEqual({
			folder: ['folder_openOpen', 'delete_outlineDelete'],
			file: ['downloadDownload', 'delete_outlineDelete'],
			localFolder: ['folder_openOpen'],
			newFolder: true,
			showHidden: false
		});
	});
});
