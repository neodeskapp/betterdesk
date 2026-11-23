/**
 * The dev mock device: a stand-in for RDClient with the same events and
 * methods and no network. The viewer loads it only when its data says the
 * server has the mock on (ND_DEV_MOCK_REMOTE=1, then ?mock=1 or a seeded
 * demo device; ../rdclient.ts). It goes through the connecting steps with
 * short delays, then draws a made-up desktop into the viewer's own canvas
 * through the real RDRenderer, so scaling, full screen, recording and
 * every control of the viewer can be reviewed without a device.
 *
 * Scenarios (?mock=1&scenario=...): ok, slow, password (accepts "demo"),
 * 2fa (password "demo", code 123456), fail-relay, refused, offline,
 * timeout, ended, dropped. The scenario applies to the first device of the
 * page; tabs opened later with "New connection" connect normally.
 */
import type {
	ClientOptions,
	ClientStats,
	Monitor,
	RemoteClient,
	VirtualDisplaySupport,
	WindowsSessions
} from '../rdclient.ts';
import {
	fileTransferModal,
	type FileSheetEntry,
	type FileSheetTransfer,
	type FileSheetUpload
} from './file-modal.ts';
import { LocalFiles, type LocalEntry, type LocalWalk, type NativeUploadFile } from './local-files.ts';
import { page } from './page.ts';
import { RDRenderer, type RDRendererStats } from './renderer.ts';

/** The page's mock settings and first device, read once when the mock loads */
const settings = page().__mockRemote ?? null;
const FIRST_ID = page().__initialDeviceId ?? '';

function scenarioFor(deviceId: string): string {
	return deviceId === FIRST_ID ? settings?.scenario || 'ok' : 'ok';
}

/** An event listener; each event has its own arguments (see the _emit calls) */
type Listener = (...args: never[]) => void;

/* ── A small fake file system on the "device" ── */

const KB = 1024;
const MB = 1024 * 1024;

interface MockEntry {
	name: string;
	isDir: boolean;
	size?: number;
	hidden?: boolean;
}

type MockTree = Record<string, MockEntry[]>;

function remoteTree(): MockTree {
	return {
		'/home/demo': [
			{ name: 'Documents', isDir: true },
			{ name: 'Downloads', isDir: true },
			{ name: 'Pictures', isDir: true },
			{ name: '.bashrc', isDir: false, size: 3 * KB, hidden: true },
			{ name: 'build.log', isDir: false, size: 248 * KB },
			{ name: 'notes.txt', isDir: false, size: 2 * KB },
			{ name: 'release-1.4.2.tar.gz', isDir: false, size: 86 * MB }
		],
		'/home/demo/Documents': [
			{ name: 'handover.pdf', isDir: false, size: 1.2 * MB },
			{ name: 'inventory.csv', isDir: false, size: 40 * KB },
			{ name: 'setup-guide.odt', isDir: false, size: 380 * KB }
		],
		'/home/demo/Downloads': [
			{ name: 'driver-update.zip', isDir: false, size: 24 * MB },
			{ name: 'printer-manual.pdf', isDir: false, size: 3.4 * MB }
		],
		'/home/demo/Pictures': [
			{ name: 'office-plan.png', isDir: false, size: 920 * KB },
			{ name: 'screenshot-0412.png', isDir: false, size: 610 * KB }
		]
	};
}

const LOCAL_ROOT = 'Review files';

function localEntry(e: MockEntry): LocalEntry {
	return e.isDir
		? { name: e.name, isDir: true, size: 0, modifiedTime: 0 }
		: { name: e.name, isDir: false, size: e.size || 0, modifiedTime: 0 };
}

/** Files on "this computer" for the left pane (no real disk access in the mock) */
export class MockLocalFiles extends LocalFiles {
	_stack: string[] = [];
	_tree: MockTree = {
		[LOCAL_ROOT]: [
			{ name: 'Scripts', isDir: true },
			{ name: 'checklist.txt', isDir: false, size: 4 * KB },
			{ name: 'config-backup.json', isDir: false, size: 18 * KB },
			{ name: 'support-tool.exe', isDir: false, size: 12 * MB }
		],
		[LOCAL_ROOT + '/Scripts']: [
			{ name: 'cleanup.ps1', isDir: false, size: 6 * KB },
			{ name: 'collect-logs.sh', isDir: false, size: 3 * KB }
		]
	};

	constructor() {
		super();
		this._rootPath = LOCAL_ROOT;
		this._currentPath = LOCAL_ROOT;
	}

	override get hasRoot(): boolean {
		return true;
	}

	/** What the list shows for the current folder */
	entries(): LocalEntry[] {
		return (this._tree[this._currentPath] || []).map(localEntry);
	}

	override async pickRoot(): Promise<string> {
		return this.goHome();
	}

	override async listCurrent(): Promise<LocalEntry[]> {
		return this.entries();
	}

	override async enterDir(entry: LocalEntry | null | undefined): Promise<string | undefined> {
		if (!entry) return undefined;
		this._stack.push(this._currentPath);
		this._currentPath = this._currentPath + '/' + entry.name;
		return this._currentPath;
	}

	override async goUp(): Promise<string> {
		const up = this._stack.pop();
		if (up !== undefined) this._currentPath = up;
		return this._currentPath;
	}

	override async goHome(): Promise<string> {
		this._currentPath = LOCAL_ROOT;
		this._stack = [];
		return this._currentPath;
	}

	override async readFile(entry: LocalEntry | null | undefined): Promise<NativeUploadFile | null> {
		if (!entry) return null;
		return {
			name: entry.name,
			size: entry.size || 0,
			lastModified: 0,
			__rdNativeHandle: null,
			__rdNativePath: null,
			slice: () => ({ arrayBuffer: async () => new ArrayBuffer(0) })
		};
	}

	override async walkFolder(entry: LocalEntry | null | undefined): Promise<LocalWalk> {
		if (!entry || !entry.isDir) return { rootName: '', dirs: [], files: [] };
		const files = (this._tree[this._currentPath + '/' + entry.name] || [])
			.filter((e) => !e.isDir)
			.map((e) => ({
				name: e.name,
				relativePath: entry.name + '/' + e.name,
				size: e.size || 0,
				modifiedTime: 0,
				isDir: false as const
			}));
		return { rootName: entry.name, dirs: [], files };
	}

	override async saveDownload(): Promise<boolean> {
		return true;
	}
}

/* ── File transfer with fake progress ── */

interface MockJob {
	id: number;
	done: number;
	total: number;
	timer: ReturnType<typeof setInterval> | null;
}

class MockFileTransfer implements FileSheetTransfer {
	client: MockRemoteSession;
	enabled = false;
	showHidden = false;
	currentPath = '';
	_tree: MockTree = remoteTree();
	_nextId = 1;
	_jobs = new Map<number, MockJob>();
	_saveDownload: ((fileName: string, blob: Blob) => unknown) | null = null;

	constructor(client: MockRemoteSession) {
		this.client = client;
	}

	_emit(event: string, ...args: unknown[]): void {
		this.client._emit(event, ...args);
	}

	enable(): void {
		this.enabled = true;
	}

	setShowHidden(on: boolean): void {
		this.showHidden = !!on;
		this.browseDir(this.currentPath);
	}

	browseDir(path: string): void {
		const target = path && this._tree[path] ? path : '/home/demo';
		this._emit('file_connecting');
		this._emit('file_browsing');
		setTimeout(() => {
			this.currentPath = target;
			const entries = (this._tree[target] || []).filter((e) => this.showHidden || !e.hidden);
			this._emit('file_dir', {
				path: target,
				entries: entries.map((e) => ({
					name: e.name,
					isDir: e.isDir,
					size: Math.round(e.size || 0),
					modified: Date.now() - 86400000
				}))
			});
		}, 350);
	}

	browseParent(): void {
		const p = this.currentPath || '/home/demo';
		const up = p.replace(/\/[^/]+$/, '') || '/home/demo';
		this.browseDir(this._tree[up] ? up : '/home/demo');
	}

	createDir(path: string): void {
		const name = String(path).split('/').pop() ?? '';
		(this._tree[this.currentPath] || []).push({ name, isDir: true });
		this._tree[this.currentPath + '/' + name] = [];
		this._emit('file_action');
	}

	_removeEntry(path: string): void {
		const dir = path.replace(/\/[^/]+$/, '');
		const name = path.split('/').pop();
		this._tree[dir] = (this._tree[dir] || []).filter((e) => e.name !== name);
		this._emit('file_action');
	}

	removeFile(path: string): void {
		this._removeEntry(path);
	}

	removeDir(path: string): void {
		this._removeEntry(path);
	}

	rename(path: string, newName: string): void {
		const dir = path.replace(/\/[^/]+$/, '');
		const name = path.split('/').pop();
		for (const e of this._tree[dir] || []) if (e.name === name) e.name = newName;
		this._emit('file_action');
	}

	/** One job: start, progress in steps, done. Big files take a few seconds. */
	_run(type: string, fileName: string, size: number, onDone: (() => void) | null, isFolder = false): number {
		const id = this._nextId++;
		const total = Math.max(1, Math.round(size || 1));
		const job: MockJob = { id, done: 0, total, timer: null };
		this._jobs.set(id, job);
		this._emit('file_transfer_start', { id, fileName, fileSize: total, type, isFolder });
		const steps = total > 20 * MB ? 40 : 14;
		let n = 0;
		job.timer = setInterval(() => {
			n++;
			job.done = Math.min(total, Math.round((total * n) / steps));
			this._emit('file_transfer_progress', {
				id,
				percent: Math.round((100 * job.done) / total),
				transferred: job.done,
				fileSize: total,
				isFolder
			});
			if (n >= steps) {
				if (job.timer) clearInterval(job.timer);
				this._jobs.delete(id);
				this._emit('file_transfer_complete', { id, fileName, isFolder });
				onDone?.();
			}
		}, 150);
		return id;
	}

	_listAt(dir: string): MockEntry[] {
		return (this._tree[dir] ??= []);
	}

	uploadFile(file: FileSheetUpload, dir: string): void {
		const name = file?.name || 'file';
		const size = file?.size || 64 * KB;
		const target = dir || this.currentPath || '/home/demo';
		this._run('upload', name, size, () => {
			const list = this._listAt(target);
			if (!list.some((e) => e.name === name)) list.push({ name, isDir: false, size });
			this._emit('file_action');
		});
	}

	uploadFolder(walk: LocalWalk, dir: string): void {
		const target = dir || this.currentPath || '/home/demo';
		const size = walk.files.reduce((a, f) => a + (f.size || 0), 0) || 64 * KB;
		this._run(
			'upload',
			walk.rootName || 'folder',
			size,
			() => {
				const list = this._listAt(target);
				if (!list.some((e) => e.name === walk.rootName)) list.push({ name: walk.rootName, isDir: true });
				this._tree[target + '/' + walk.rootName] = walk.files.map((f) => ({
					name: f.name,
					isDir: false,
					size: f.size
				}));
				this._emit('file_action');
			},
			true
		);
	}

	downloadFile(_dir: string, entry: FileSheetEntry): void {
		this._run('download', entry.name, entry.size || 64 * KB, null);
	}

	downloadFolder(_dir: string, entry: FileSheetEntry): void {
		this._run('download', entry.name, 5 * MB, null, true);
	}

	cancelTransfer(id: number): void {
		const job = this._jobs.get(id);
		if (job) {
			if (job.timer) clearInterval(job.timer);
			this._jobs.delete(id);
		}
		this._emit('file_transfer_cancelled', { id });
	}

	/** Nothing fails in the mock */
	resumeTransfer(): void {}

	/** No overwrite prompts in the mock */
	confirmOverwrite(): void {}

	stopAll(): void {
		for (const job of this._jobs.values()) if (job.timer) clearInterval(job.timer);
		this._jobs.clear();
	}
}

/* ── The made-up desktop ── */

const FONT = '"IBM Plex Sans", system-ui, sans-serif';
const MONO = '"IBM Plex Mono", ui-monospace, Consolas, monospace';
const TITLE_H = 34;
const TASKBAR_H = 48;

function pad2(n: number): string {
	return String(n).padStart(2, '0');
}

interface MockMonitor {
	name: string;
	width: number;
	height: number;
	primary?: boolean;
	/** Two tones of the wallpaper */
	wall: [string, string];
	/** Virtual displays: the index RustDesk gives them */
	vIndex?: number;
}

interface MockWindow {
	id: string;
	title: string;
	kind: 'files' | 'terminal' | 'browser';
	monitor: number;
	x: number;
	y: number;
	w: number;
	h: number;
	/** Its button on the taskbar, set while the taskbar is drawn */
	_task?: { x: number; y: number; w: number; h: number };
}

/** What the tray shows: what the viewer asked for */
interface TrayInfo {
	codec: string;
	quality: string;
	fps: string;
}

class MockDesktop {
	monitors: MockMonitor[] = [
		{ name: 'Display 1', width: 1920, height: 1080, primary: true, wall: ['#123a46', '#0d2530'] },
		{ name: 'Display 2', width: 1280, height: 1024, primary: false, wall: ['#2d2a46', '#1b1930'] }
	];
	virtual: MockMonitor[] = [];
	current = 0;
	windows: MockWindow[] = [
		{ id: 'files', title: 'Files - /home/demo', kind: 'files', monitor: 0, x: 120, y: 90, w: 760, h: 520 },
		{
			id: 'term',
			title: 'Terminal - demo@build-server',
			kind: 'terminal',
			monitor: 0,
			x: 820,
			y: 300,
			w: 900,
			h: 560
		},
		{
			id: 'web',
			title: 'Web browser - Build status',
			kind: 'browser',
			monitor: 1,
			x: 80,
			y: 70,
			w: 1080,
			h: 780
		}
	];
	z = ['files', 'term', 'web'];
	focusId = 'term';
	term = [
		'Welcome to the NeoDesk review desktop. Nothing here is real.',
		'Type "help" for the few commands this terminal knows.',
		'',
		'demo@build-server:~$ uptime',
		' 14:02:11 up 12 days,  3:41,  1 user,  load average: 0.21, 0.30, 0.28'
	];
	input = '';
	notes: { text: string; until: number }[] = [];
	overlay: 'cad' | 'lock' | null = null;
	banners: Record<string, string | null> = {};
	drag: { win: MockWindow; dx: number; dy: number } | null = null;

	allMonitors(): MockMonitor[] {
		return this.monitors.concat(this.virtual);
	}

	monitor(): MockMonitor {
		return this.allMonitors()[this.current] ?? (this.monitors[0] as MockMonitor);
	}

	note(text: string): void {
		this.notes.push({ text, until: Date.now() + 3500 });
		if (this.notes.length > 3) this.notes.shift();
	}

	windowsOn(m: number): MockWindow[] {
		return this.z
			.map((id) => this.windows.find((w) => w.id === id))
			.filter((w): w is MockWindow => !!w && w.monitor === m);
	}

	hit(x: number, y: number): { win: MockWindow; title: boolean } | null {
		const list = this.windowsOn(this.current);
		for (let i = list.length - 1; i >= 0; i--) {
			const w = list[i] as MockWindow;
			if (x >= w.x && x <= w.x + w.w && y >= w.y && y <= w.y + w.h) {
				return { win: w, title: y <= w.y + TITLE_H };
			}
		}
		return null;
	}

	raise(w: MockWindow): void {
		this.z = this.z.filter((id) => id !== w.id).concat([w.id]);
		this.focusId = w.id;
	}

	typeKey(key: string): void {
		if (this.overlay === 'lock') {
			this.overlay = null;
			this.note('Signed in again');
			return;
		}
		if (this.overlay === 'cad') {
			if (key === 'Escape') this.overlay = null;
			return;
		}
		const term = this.windows.find((w) => w.id === 'term');
		if (this.focusId !== 'term' && term) {
			this.raise(term);
			if (term.monitor !== this.current)
				this.note('Typing goes to the terminal on Display ' + (term.monitor + 1));
		}
		if (key === 'Enter') {
			this.run(this.input);
			this.input = '';
			return;
		}
		if (key === 'Backspace') {
			this.input = this.input.slice(0, -1);
			return;
		}
		if (key.length === 1 && this.input.length < 120) this.input += key;
	}

	run(cmd: string): void {
		const c = cmd.trim();
		this.term.push('demo@build-server:~$ ' + cmd);
		if (!c) return;
		let out: string[];
		switch (c) {
			case 'help':
				out = ['help, ls, whoami, date, uname -a, clear'];
				break;
			case 'ls':
				out = ['Documents  Downloads  Pictures  build.log  notes.txt  release-1.4.2.tar.gz'];
				break;
			case 'whoami':
				out = ['demo'];
				break;
			case 'date':
				out = [new Date().toString()];
				break;
			case 'uname -a':
				out = ['Linux build-server 6.8.0 #1 SMP x86_64 GNU/Linux (mock)'];
				break;
			case 'clear':
				this.term = [];
				return;
			default:
				out = [c.split(' ')[0] + ': command not found (this is the review desktop)'];
		}
		this.term.push(...out);
		if (this.term.length > 200) this.term.splice(0, this.term.length - 200);
	}

	draw(ctx: CanvasRenderingContext2D, w: number, h: number, tray: TrayInfo): void {
		const m = this.monitor();
		const now = new Date();
		// Wallpaper: two tones and a few soft shapes
		const g = ctx.createLinearGradient(0, 0, w, h);
		g.addColorStop(0, m.wall[0]);
		g.addColorStop(1, m.wall[1]);
		ctx.fillStyle = g;
		ctx.fillRect(0, 0, w, h);
		ctx.globalAlpha = 0.08;
		ctx.fillStyle = '#ffffff';
		ctx.beginPath();
		ctx.arc(w * 0.78, h * 0.22, h * 0.32, 0, Math.PI * 2);
		ctx.fill();
		ctx.beginPath();
		ctx.arc(w * 0.15, h * 0.85, h * 0.22, 0, Math.PI * 2);
		ctx.fill();
		ctx.globalAlpha = 1;
		ctx.fillStyle = 'rgba(255,255,255,0.35)';
		ctx.font = '600 22px ' + FONT;
		ctx.fillText(m.name + ', ' + m.width + ' × ' + m.height + ', review desktop', 28, 44);

		if (this.overlay === 'lock') {
			this.drawLock(ctx, w, h, now);
			return;
		}

		for (const win of this.windowsOn(this.current)) this.drawWindow(ctx, win, now);
		this.drawTaskbar(ctx, w, h, now, tray);
		this.drawBanners(ctx, w);
		this.drawNotes(ctx, w, h);
		if (this.overlay === 'cad') this.drawCad(ctx, w, h);
	}

	drawWindow(ctx: CanvasRenderingContext2D, win: MockWindow, now: Date): void {
		const focused = this.focusId === win.id;
		ctx.save();
		ctx.shadowColor = 'rgba(0,0,0,0.45)';
		ctx.shadowBlur = 24;
		ctx.shadowOffsetY = 6;
		ctx.fillStyle = '#f4f5f5';
		ctx.fillRect(win.x, win.y, win.w, win.h);
		ctx.restore();
		// Title bar
		ctx.fillStyle = focused ? '#2b3236' : '#4a5357';
		ctx.fillRect(win.x, win.y, win.w, TITLE_H);
		ctx.fillStyle = '#e8ecec';
		ctx.font = '500 15px ' + FONT;
		ctx.fillText(win.title, win.x + 14, win.y + 22);
		['#d9534f', '#e0a33a', '#5cb85c'].forEach((c, i) => {
			ctx.fillStyle = c;
			ctx.beginPath();
			ctx.arc(win.x + win.w - 22 - i * 22, win.y + TITLE_H / 2, 6, 0, Math.PI * 2);
			ctx.fill();
		});
		const bx = win.x;
		const by = win.y + TITLE_H;
		const bw = win.w;
		const bh = win.h - TITLE_H;
		ctx.save();
		ctx.beginPath();
		ctx.rect(bx, by, bw, bh);
		ctx.clip();
		if (win.kind === 'terminal') this.drawTerminal(ctx, bx, by, bw, bh, now, focused);
		else if (win.kind === 'files') this.drawFiles(ctx, bx, by, bw, bh);
		else this.drawBrowser(ctx, bx, by, bw, bh, now);
		ctx.restore();
	}

	drawTerminal(
		ctx: CanvasRenderingContext2D,
		x: number,
		y: number,
		w: number,
		h: number,
		now: Date,
		focused: boolean
	): void {
		ctx.fillStyle = '#101414';
		ctx.fillRect(x, y, w, h);
		ctx.font = '15px ' + MONO;
		const lineH = 22;
		const maxLines = Math.floor((h - 24) / lineH) - 1;
		const lines = this.term.slice(-maxLines);
		ctx.fillStyle = '#c8d3d3';
		lines.forEach((l, i) => ctx.fillText(l.slice(0, 110), x + 14, y + 26 + i * lineH));
		const py = y + 26 + lines.length * lineH;
		ctx.fillStyle = '#7fd4a8';
		const prompt = 'demo@build-server:~$ ';
		ctx.fillText(prompt, x + 14, py);
		const pw = ctx.measureText(prompt).width;
		ctx.fillStyle = '#e8ecec';
		ctx.fillText(this.input, x + 14 + pw, py);
		if (focused && Math.floor(now.getTime() / 530) % 2 === 0) {
			const iw = ctx.measureText(this.input).width;
			ctx.fillRect(x + 14 + pw + iw + 1, py - 15, 9, 19);
		}
	}

	drawFiles(ctx: CanvasRenderingContext2D, x: number, y: number, w: number, h: number): void {
		ctx.fillStyle = '#e6e9ea';
		ctx.fillRect(x, y, 190, h);
		ctx.font = '15px ' + FONT;
		ctx.fillStyle = '#2b3236';
		['Home', 'Documents', 'Downloads', 'Pictures', 'Trash'].forEach((s, i) => {
			if (i === 0) {
				ctx.fillStyle = '#cdd6d8';
				ctx.fillRect(x + 8, y + 12, 174, 30);
				ctx.fillStyle = '#2b3236';
			}
			ctx.fillText(s, x + 22, y + 33 + i * 38);
		});
		ctx.fillStyle = '#ffffff';
		ctx.fillRect(x + 190, y, w - 190, h);
		ctx.fillStyle = '#566064';
		ctx.font = '600 13px ' + FONT;
		ctx.fillText('Name', x + 210, y + 28);
		ctx.fillText('Size', x + w - 120, y + 28);
		const rows: [string, string][] = [
			['Documents', '-'],
			['Downloads', '-'],
			['Pictures', '-'],
			['build.log', '248 kB'],
			['notes.txt', '2 kB'],
			['release-1.4.2.tar.gz', '86 MB']
		];
		ctx.font = '15px ' + FONT;
		rows.forEach(([name, size], i) => {
			const ry = y + 44 + i * 36;
			ctx.fillStyle = i % 2 ? '#ffffff' : '#f5f7f7';
			ctx.fillRect(x + 190, ry, w - 190, 36);
			ctx.fillStyle = size === '-' ? '#2f6f8a' : '#8a96a0';
			ctx.fillRect(x + 210, ry + 10, 16, 14);
			ctx.fillStyle = '#1b1e1f';
			ctx.fillText(name, x + 238, ry + 23);
			ctx.fillStyle = '#566064';
			ctx.fillText(size, x + w - 120, ry + 23);
		});
	}

	drawBrowser(ctx: CanvasRenderingContext2D, x: number, y: number, w: number, h: number, now: Date): void {
		ctx.fillStyle = '#e6e9ea';
		ctx.fillRect(x, y, w, 46);
		ctx.fillStyle = '#ffffff';
		ctx.fillRect(x + 14, y + 9, w - 28, 28);
		ctx.fillStyle = '#566064';
		ctx.font = '14px ' + FONT;
		ctx.fillText('https://intranet.example/build-status', x + 26, y + 28);
		ctx.fillStyle = '#ffffff';
		ctx.fillRect(x, y + 46, w, h - 46);
		ctx.fillStyle = '#1b1e1f';
		ctx.font = '600 28px ' + FONT;
		ctx.fillText('Build status', x + 40, y + 100);
		ctx.font = '16px ' + FONT;
		ctx.fillStyle = '#566064';
		ctx.fillText(
			'Last refresh ' + pad2(now.getHours()) + ':' + pad2(now.getMinutes()) + ':' + pad2(now.getSeconds()),
			x + 40,
			y + 130
		);
		const jobs: [string, string, string, number][] = [
			['main', 'passed', '#1c7a3b', 1.0],
			['release-1.4', 'passed', '#1c7a3b', 0.86],
			['nightly', 'running', '#1b62cf', 0.4 + 0.3 * Math.abs(Math.sin(now.getTime() / 3000))],
			['docs', 'failed', '#b3261e', 0.62]
		];
		jobs.forEach(([name, state, color, share], i) => {
			const ry = y + 170 + i * 70;
			ctx.fillStyle = '#1b1e1f';
			ctx.font = '600 17px ' + FONT;
			ctx.fillText(name, x + 40, ry + 20);
			ctx.font = '15px ' + FONT;
			ctx.fillStyle = color;
			ctx.fillText(state, x + 200, ry + 20);
			ctx.fillStyle = '#e8ebeb';
			ctx.fillRect(x + 320, ry + 6, w - 380, 18);
			ctx.fillStyle = color;
			ctx.fillRect(x + 320, ry + 6, (w - 380) * share, 18);
		});
		ctx.fillStyle = '#8a96a0';
		for (let i = 0; i < 6; i++)
			ctx.fillRect(x + 40, y + 470 + i * 28, (w - 120) * (0.5 + ((i * 37) % 50) / 100), 12);
	}

	drawTaskbar(ctx: CanvasRenderingContext2D, w: number, h: number, now: Date, tray: TrayInfo): void {
		const y = h - TASKBAR_H;
		ctx.fillStyle = 'rgba(16,20,20,0.92)';
		ctx.fillRect(0, y, w, TASKBAR_H);
		ctx.fillStyle = '#e8ecec';
		ctx.font = '600 16px ' + FONT;
		ctx.fillText('Apps', 22, y + 30);
		let x = 100;
		for (const win of this.windows) {
			const on = win.monitor === this.current;
			ctx.fillStyle = this.focusId === win.id ? 'rgba(255,255,255,0.18)' : 'rgba(255,255,255,0.06)';
			ctx.fillRect(x, y + 8, 200, TASKBAR_H - 16);
			ctx.fillStyle = on ? '#e8ecec' : '#9aa4a8';
			ctx.font = '14px ' + FONT;
			ctx.fillText(win.title.split(' - ')[0] ?? win.title, x + 12, y + 29);
			win._task = { x, y: y + 8, w: 200, h: TASKBAR_H - 16 };
			x += 210;
		}
		// Tray: what the viewer asked for, then the clock (ticks every second)
		const info = (tray.codec || 'Auto') + ', ' + tray.quality + ', ' + tray.fps;
		ctx.font = '14px ' + FONT;
		ctx.fillStyle = '#9aa4a8';
		ctx.textAlign = 'right';
		ctx.fillText(info, w - 140, y + 29);
		ctx.fillStyle = '#e8ecec';
		ctx.font = '600 16px ' + MONO;
		ctx.fillText(
			pad2(now.getHours()) + ':' + pad2(now.getMinutes()) + ':' + pad2(now.getSeconds()),
			w - 22,
			y + 30
		);
		ctx.textAlign = 'left';
	}

	drawBanners(ctx: CanvasRenderingContext2D, w: number): void {
		const list = Object.values(this.banners).filter((text): text is string => !!text);
		list.forEach((text, i) => {
			ctx.fillStyle = 'rgba(135,91,0,0.92)';
			ctx.fillRect(w / 2 - 330, 16 + i * 44, 660, 36);
			ctx.fillStyle = '#ffffff';
			ctx.font = '600 15px ' + FONT;
			ctx.textAlign = 'center';
			ctx.fillText(text, w / 2, 39 + i * 44);
			ctx.textAlign = 'left';
		});
	}

	drawNotes(ctx: CanvasRenderingContext2D, w: number, h: number): void {
		const now = Date.now();
		this.notes = this.notes.filter((n) => n.until > now);
		this.notes.forEach((n, i) => {
			const y = h - TASKBAR_H - 90 - i * 70;
			ctx.fillStyle = 'rgba(31,35,35,0.95)';
			ctx.fillRect(w - 440, y, 410, 58);
			ctx.fillStyle = '#58a6ff';
			ctx.fillRect(w - 440, y, 4, 58);
			ctx.fillStyle = '#e8ecec';
			ctx.font = '15px ' + FONT;
			ctx.fillText(n.text.slice(0, 48), w - 422, y + 34);
		});
	}

	drawCad(ctx: CanvasRenderingContext2D, w: number, h: number): void {
		ctx.fillStyle = 'rgba(10,14,16,0.94)';
		ctx.fillRect(0, 0, w, h);
		ctx.fillStyle = '#e8ecec';
		ctx.font = '600 30px ' + FONT;
		ctx.fillText('Ctrl+Alt+Del received', w / 2 - 200, h / 2 - 120);
		ctx.font = '20px ' + FONT;
		['Lock', 'Switch user', 'Sign out', 'Task manager', 'Cancel (press Esc or click)'].forEach((s, i) => {
			ctx.fillText(s, w / 2 - 200, h / 2 - 60 + i * 42);
		});
	}

	drawLock(ctx: CanvasRenderingContext2D, w: number, h: number, now: Date): void {
		ctx.fillStyle = 'rgba(0,0,0,0.45)';
		ctx.fillRect(0, 0, w, h);
		ctx.fillStyle = '#ffffff';
		ctx.font = '300 120px ' + FONT;
		ctx.textAlign = 'center';
		ctx.fillText(pad2(now.getHours()) + ':' + pad2(now.getMinutes()), w / 2, h / 2 - 20);
		ctx.font = '22px ' + FONT;
		ctx.fillText('Locked. Click or press a key to sign in again.', w / 2, h / 2 + 40);
		ctx.textAlign = 'left';
	}
}

/* ── The transport stand-in ── */

const QUALITY_SCALE: Record<string, number> = { best: 1, balanced: 0.75, speed: 0.5 };
const QUALITY_NAME: Record<string, string> = { best: 'Best', balanced: 'Balanced', speed: 'Low' };

/** getStats: RDClient's fields the viewer reads, and the mock's own */
type MockStats = ClientStats & {
	state: string;
	renderer: RDRendererStats;
	audio: Record<string, never>;
	connection: string;
	diagnostics: Record<string, never>;
};

export class MockRemoteSession implements RemoteClient {
	deviceId: string;
	opts: ClientOptions;
	scenario: string;
	canvas: HTMLCanvasElement;
	renderer: RDRenderer;
	fileTransfer: MockFileTransfer;
	desktop: MockDesktop;
	_state = 'idle';
	_listeners = new Map<string, Listener[]>();
	_timers: ReturnType<typeof setTimeout>[] = [];
	_peerInfo: { platform: string; displays: Monitor[] } | null = null;
	_viewOnly = false;
	_active = true;
	_quality: string;
	_fpsMode: string;
	_backgroundFps = 1;
	_codec: string;
	_keyboardMode = 'Auto';
	_needsPassword: boolean;
	_muted = false;
	_sid = 1;
	_frameCanvas: HTMLCanvasElement;
	_frameCtx: CanvasRenderingContext2D | null;
	_lastFrame = 0;
	_raf = 0;
	_bound = false;
	_sessionStartedAt: number | null = null;
	_latencyTimer: ReturnType<typeof setInterval> | null = null;
	_onMove: ((e: MouseEvent) => void) | null = null;
	_onDown: ((e: MouseEvent) => void) | null = null;
	_onUp: (() => void) | null = null;
	_onKey: ((e: KeyboardEvent) => void) | null = null;
	_onContext: ((e: MouseEvent) => void) | null = null;

	constructor(canvas: HTMLCanvasElement, opts: ClientOptions) {
		this.deviceId = opts.deviceId;
		this.opts = opts;
		this.scenario = scenarioFor(this.deviceId);
		this.canvas = canvas;
		this.renderer = new RDRenderer(canvas);
		this.renderer.setScaleMode(opts.scaleMode || 'fit');
		this.fileTransfer = new MockFileTransfer(this);
		this.desktop = new MockDesktop();
		this._quality = opts.qualityPreset || 'best';
		this._fpsMode = opts.fpsMode || 'adaptive';
		this._codec = opts.preferCodec && opts.preferCodec !== 'Auto' ? opts.preferCodec : 'VP9';
		this._needsPassword = this.scenario === 'password' || this.scenario === '2fa';
		this._frameCanvas = document.createElement('canvas');
		this._frameCtx = this._frameCanvas.getContext('2d');
		this.renderer.onCursorReady = (ready) => {
			this.canvas.parentElement?.classList.toggle('has-remote-cursor', !!ready);
		};
		// The file sheet shows this computer's side from the mock too
		const modal = fileTransferModal();
		if (modal && !(modal._local instanceof MockLocalFiles)) {
			const local = new MockLocalFiles();
			modal._local = local;
			modal._localEntries = local.entries();
			const open = modal.open.bind(modal);
			modal.open = (session) => {
				open(session);
				const pathEl = modal._el?.querySelector('.ft-local-path');
				if (pathEl) pathEl.textContent = local.currentPath;
			};
		}
	}

	get state(): string {
		return this._state;
	}

	get peerInfo(): { platform: string; displays: Monitor[] } | null {
		return this._peerInfo;
	}

	get viewOnly(): boolean {
		return this._viewOnly;
	}

	on(event: string, fn: Listener): this {
		const list = this._listeners.get(event) ?? [];
		list.push(fn);
		this._listeners.set(event, list);
		return this;
	}

	off(event: string, fn: Listener): this {
		const list = this._listeners.get(event);
		if (list)
			this._listeners.set(
				event,
				list.filter((f) => f !== fn)
			);
		return this;
	}

	_emit(event: string, ...args: unknown[]): void {
		for (const fn of (this._listeners.get(event) ?? []).slice()) {
			try {
				Reflect.apply(fn, undefined, args);
			} catch (e) {
				console.error(e);
			}
		}
	}

	_setState(s: string): void {
		if (this._state === s) return;
		const prev = this._state;
		this._state = s;
		this._emit('state', s, prev);
	}

	_after(ms: number, fn: () => void): void {
		const id = setTimeout(() => {
			this._timers = this._timers.filter((t) => t !== id);
			fn();
		}, ms);
		this._timers.push(id);
	}

	_clearTimers(): void {
		for (const t of this._timers) clearTimeout(t);
		this._timers = [];
	}

	/** The same log lines RDClient writes, so the viewer maps them to its steps */
	async connect(): Promise<void> {
		const slow = this.scenario === 'slow';
		let t = 0;
		const at = (ms: number, fn: () => void) => {
			t += slow ? ms * 2.5 : ms;
			this._after(t, fn);
		};
		this._setState('connecting');
		this._emit('log', 'Loading protocol definitions...');
		at(350, () => this._emit('log', 'Connecting to rendezvous server...'));
		at(450, () => this._emit('log', 'Requesting connection to ' + this.deviceId + '...'));
		if (this.scenario === 'offline') {
			at(900, () => this._fail('Connection refused: Device offline'));
			return;
		}
		if (this.scenario === 'timeout') {
			at(5000, () => this._fail('Rendezvous response timeout (30s) - target device may be offline'));
			return;
		}
		at(500, () => this._emit('log', 'Requesting relay (uuid: 6f1c2a9e...)...'));
		at(450, () => this._emit('log', 'Connecting to relay server...'));
		at(400, () => {
			this._emit('log', 'Waiting for peer handshake...');
			this._setState('waiting_password');
		});
		if (this.scenario === 'fail-relay') {
			at(2600, () =>
				this._disconnect('Relay connection closed (WebSocket 1005)', {
					source: 'relay',
					closeCode: 1005,
					closeReason: ''
				})
			);
			return;
		}
		if (this.scenario === 'refused') {
			at(2200, () => this._fail('Relay refused: the remote user declined the connection'));
			return;
		}
		// Slow: the device answers only after the "someone has to accept" hint shows
		at(slow ? 5600 : 700, () => this._emit('log', 'Peer identified: ' + this.deviceId));
		at(300, () => this._emit('log', 'Key exchange prepared, waiting for peer to decide encryption mode...'));
		at(400, () => {
			if (this._needsPassword) {
				this._emit('log', 'Password required');
				this._setState('waiting_password');
				this._emit('password_required');
			} else {
				this._emit('log', 'No password required');
				this._startSession();
			}
		});
	}

	async authenticate(password: string): Promise<void> {
		this._setState('authenticating');
		this._emit('log', 'Authenticating...');
		this._after(700, () => {
			if (password !== 'demo') {
				this._emit('login_error', 'Wrong Password');
				this._setState('waiting_password');
				return;
			}
			if (this.scenario === '2fa') {
				this._setState('waiting_2fa');
				this._emit('2fa_required');
				return;
			}
			this._loginOk();
		});
	}

	async submit2FA(code: string): Promise<void> {
		this._setState('authenticating');
		this._emit('log', 'Verifying 2FA code...');
		this._after(600, () => {
			if (String(code) !== '123456') {
				this._setState('waiting_2fa');
				this._emit('2fa_error', 'Wrong 2FA Code');
				return;
			}
			this._loginOk();
		});
	}

	_loginOk(): void {
		this._emit('log', 'Login successful');
		this._emit('login_success', {});
		this._startSession();
	}

	_startSession(): void {
		this._peerInfo = { platform: '', displays: this.getMonitors() };
		this._emit('peer_info', this._peerInfo);
		this._after(this.scenario === 'slow' ? 1500 : 300, () => {
			this._sessionStartedAt = Date.now();
			this._setState('streaming');
			this._bindInput();
			this._installCursor();
			this.renderer.startRenderLoop();
			this._loop();
			this._emit('session_start');
			this._emit('log', 'Video codec: ' + this._codec);
			this._latencyTimer = setInterval(() => {
				this._emit('latency', 16 + Math.round(Math.random() * 14));
				this._emit('stats', this.getStats());
			}, 2000);
			if (this.scenario === 'ended') {
				this._after(7000, () =>
					this._disconnect('Remote: Closed manually by the peer', {
						source: 'peer',
						peerReason: 'Closed manually by the peer'
					})
				);
			}
			if (this.scenario === 'dropped') {
				this._after(7000, () =>
					this._disconnect('Relay connection closed (WebSocket 1006)', {
						source: 'relay',
						closeCode: 1006,
						closeReason: ''
					})
				);
			}
		});
	}

	_fail(msg: string): void {
		this._stop();
		this._emit('diagnostics', { type: 'error', message: msg, at: Date.now() });
		this._emit('error', msg, { cdapFallback: false });
		this._setState('error');
	}

	_disconnect(reason: string, meta: Record<string, unknown> = {}): void {
		this._stop();
		const info = { type: 'disconnect', reason, at: Date.now(), transport: 'mock', ...meta };
		this._emit('diagnostics', info);
		this._emit('log', 'Disconnected: ' + reason);
		this._setState('disconnected');
		this._emit('disconnected', reason, info);
	}

	_stop(): void {
		this._clearTimers();
		if (this._raf) cancelAnimationFrame(this._raf);
		this._raf = 0;
		if (this._latencyTimer) clearInterval(this._latencyTimer);
		this._latencyTimer = null;
		this.renderer.stopRenderLoop();
		this.fileTransfer.stopAll();
		this._unbindInput();
	}

	disconnect(): void {
		this._stop();
		this._setState('disconnected');
		this._emit('log', 'Disconnected');
	}

	/* ── Frames: the desktop drawn into a canvas, handed to the real renderer ── */

	_frameInterval(): number {
		if (!this._active) return 1000 / Math.max(1, this._backgroundFps);
		if (this._fpsMode === '60') return 1000 / 60;
		return 1000 / 30;
	}

	_loop(): void {
		const tick = (ts: number) => {
			if (this._state !== 'streaming') return;
			if (!this._lastFrame || ts - this._lastFrame >= this._frameInterval() - 2) {
				this._lastFrame = ts;
				this._pushFrame();
			}
			this._raf = requestAnimationFrame(tick);
		};
		this._raf = requestAnimationFrame(tick);
	}

	_pushFrame(): void {
		const m = this.desktop.monitor();
		const scale = QUALITY_SCALE[this._quality] || 1;
		const fw = Math.round(m.width * scale);
		const fh = Math.round(m.height * scale);
		const c = this._frameCanvas;
		if (c.width !== fw || c.height !== fh) {
			c.width = fw;
			c.height = fh;
		}
		const ctx = this._frameCtx;
		if (!ctx) return;
		ctx.setTransform(scale, 0, 0, scale, 0, 0);
		const quality = QUALITY_NAME[this._quality] || this._quality;
		const fps = this._fpsMode === 'adaptive' ? 'adaptive' : this._fpsMode + ' fps';
		this.desktop.draw(ctx, m.width, m.height, { codec: this._codec, quality, fps });
		ctx.setTransform(1, 0, 0, 1, 0, 0);
		this.renderer.pushFrame({
			displayWidth: m.width,
			displayHeight: m.height,
			_source: c,
			_isNew: true,
			close: () => {}
		});
	}

	/** A plain arrow, drawn once, sent as the remote cursor */
	_installCursor(): void {
		const cw = 18;
		const ch = 26;
		const cc = document.createElement('canvas');
		cc.width = cw;
		cc.height = ch;
		const x = cc.getContext('2d');
		if (!x) return;
		x.fillStyle = '#ffffff';
		x.strokeStyle = '#000000';
		x.lineWidth = 1.5;
		x.beginPath();
		x.moveTo(1, 1);
		x.lineTo(1, 21);
		x.lineTo(6, 16);
		x.lineTo(10, 25);
		x.lineTo(13, 24);
		x.lineTo(9, 15);
		x.lineTo(16, 15);
		x.closePath();
		x.fill();
		x.stroke();
		const data = x.getImageData(0, 0, cw, ch).data;
		void this.renderer.updateCursor({
			colors: new Uint8Array(data.buffer.slice(0)),
			width: cw,
			height: ch,
			hotx: 1,
			hoty: 1
		});
		const m = this.desktop.monitor();
		this.renderer.updateCursorPosition({ x: Math.round(m.width / 2), y: Math.round(m.height / 2) });
	}

	/* ── Input: mouse moves the remote cursor and drags windows, keys go to the terminal ── */

	_pos(e: MouseEvent): { x: number; y: number } {
		const rect = this.canvas.getBoundingClientRect();
		return this.renderer.canvasToRemote(e.clientX - rect.left, e.clientY - rect.top);
	}

	_inputAllowed(): boolean {
		return this._state === 'streaming' && this._active && !this._viewOnly;
	}

	_bindInput(): void {
		if (this._bound) return;
		const d = this.desktop;
		const onMove = (e: MouseEvent) => {
			if (!this._inputAllowed()) return;
			const p = this._pos(e);
			this.renderer.updateCursorPosition(p);
			if (d.drag) {
				const m = d.monitor();
				d.drag.win.x = Math.max(-d.drag.win.w + 80, Math.min(m.width - 80, p.x - d.drag.dx));
				d.drag.win.y = Math.max(0, Math.min(m.height - TASKBAR_H - TITLE_H, p.y - d.drag.dy));
			}
		};
		const onDown = (e: MouseEvent) => {
			if (!this._inputAllowed()) return;
			e.preventDefault();
			this.canvas.focus();
			if (e.button !== 0) return;
			const p = this._pos(e);
			if (d.overlay) {
				if (d.overlay === 'lock') d.note('Signed in again');
				d.overlay = null;
				return;
			}
			const m = d.monitor();
			if (p.y >= m.height - TASKBAR_H) {
				const task = d.windows.find((w) => w._task && p.x >= w._task.x && p.x <= w._task.x + w._task.w);
				if (task) {
					d.raise(task);
					if (task.monitor !== d.current) {
						d.note((task.title.split(' - ')[0] ?? task.title) + ' is on Display ' + (task.monitor + 1));
					}
				}
				return;
			}
			const hit = d.hit(p.x, p.y);
			if (!hit) return;
			d.raise(hit.win);
			if (hit.title) d.drag = { win: hit.win, dx: p.x - hit.win.x, dy: p.y - hit.win.y };
		};
		const onUp = () => {
			d.drag = null;
		};
		const onKey = (e: KeyboardEvent) => {
			if (!this._inputAllowed()) return;
			// F8 and F11 stay with the viewer
			if (/^F\d+$/.test(e.key)) return;
			if (e.ctrlKey || e.metaKey || e.altKey) return;
			e.preventDefault();
			d.typeKey(e.key);
		};
		const onContext = (e: MouseEvent) => e.preventDefault();
		this.canvas.addEventListener('mousemove', onMove);
		this.canvas.addEventListener('mousedown', onDown);
		window.addEventListener('mouseup', onUp);
		this.canvas.addEventListener('keydown', onKey);
		this.canvas.addEventListener('contextmenu', onContext);
		this._onMove = onMove;
		this._onDown = onDown;
		this._onUp = onUp;
		this._onKey = onKey;
		this._onContext = onContext;
		this._bound = true;
	}

	_unbindInput(): void {
		if (!this._bound) return;
		if (this._onMove) this.canvas.removeEventListener('mousemove', this._onMove);
		if (this._onDown) this.canvas.removeEventListener('mousedown', this._onDown);
		if (this._onUp) window.removeEventListener('mouseup', this._onUp);
		if (this._onKey) this.canvas.removeEventListener('keydown', this._onKey);
		if (this._onContext) this.canvas.removeEventListener('contextmenu', this._onContext);
		this._bound = false;
	}

	/* ── The controls the viewer calls ── */

	sendCtrlAltDel(): void {
		this.desktop.overlay = 'cad';
	}

	sendLockScreen(): void {
		this.desktop.overlay = 'lock';
	}

	sendRefreshScreen(): void {
		this.desktop.note('Screen refreshed');
	}

	sendRestartRemoteDevice(): void {
		this.desktop.note('Restarting...');
		this._after(1200, () =>
			this._disconnect('Remote: The device is restarting', { source: 'peer', peerReason: 'restart' })
		);
	}

	resetKeyboard(): void {
		this.desktop.note('Keyboard state reset');
	}

	setKeyboardMode(mode: string): void {
		if (mode !== this._keyboardMode) this.desktop.note('Keyboard mode: ' + mode);
		this._keyboardMode = mode;
	}

	sendClipboard(text: string): void {
		this.desktop.note('Clipboard: ' + String(text).replace(/\s+/g, ' ').slice(0, 36));
	}

	sendText(text: string): boolean {
		Array.from(String(text)).forEach((ch, i) => {
			this._after(30 * i, () => this.desktop.typeKey(ch === '\n' ? 'Enter' : ch));
		});
		return true;
	}

	sendChat(text: string): void {
		this.desktop.note('Message: ' + String(text).slice(0, 38));
		this._after(1400, () =>
			this._emit('chat', 'Seen on the device: "' + String(text).slice(0, 60) + '" (mock reply)')
		);
	}

	setBlockInput(on: boolean): void {
		this.desktop.banners.block = on ? 'Input on the device is blocked' : null;
	}

	setPrivacyMode(on: boolean): void {
		this.desktop.banners.privacy = on ? "Privacy mode: the device's own screen is dark" : null;
	}

	setLockAfterSession(on: boolean): void {
		this.desktop.note(on ? 'Will lock when the session ends' : 'Will not lock when the session ends');
	}

	setDisableClipboard(on: boolean): void {
		this.desktop.note(on ? 'Clipboard sharing off' : 'Clipboard sharing on');
	}

	setShowRemoteCursor(on: boolean): void {
		this.renderer.showCursor = !!on;
	}

	setViewOnly(on: boolean): void {
		this._viewOnly = !!on;
		this.desktop.banners.view = on ? 'View only: your mouse and keyboard are not sent' : null;
		this._emit('view_only', this._viewOnly);
	}

	setQualityPreset(preset: string): void {
		this._quality = QUALITY_SCALE[preset] ? preset : 'best';
		this._emit('quality_changed', preset);
	}

	setImageQuality(q: string): void {
		this.setQualityPreset(String(q).toLowerCase());
	}

	setFpsMode(mode: string): void {
		this._fpsMode = String(mode || 'adaptive');
	}

	/** The frame rate follows the mode */
	setCustomFps(): void {}

	setScaleMode(mode: string): void {
		this.renderer.setScaleMode(mode);
	}

	setCodec(codec: string): void {
		this._codec = codec === 'Auto' ? 'VP9' : codec;
		this._emit('codec_changed', this._codec);
	}

	setAudioMuted(muted: boolean): void {
		this._muted = !!muted;
	}

	setVolume(): void {}

	setDisableAudio(): void {}

	setBackgroundFps(fps: number): void {
		this._backgroundFps = Number(fps) || 1;
	}

	setSessionActive(active: boolean): void {
		this._active = !!active;
	}

	syncCliprdrFiles(): void {}

	syncCliprdrPaths(): void {}

	isFileConnectionReady(): boolean {
		return true;
	}

	async ensureFileConnection(): Promise<void> {}

	disconnectFileConnection(): void {}

	getMonitors(): Monitor[] {
		const cur = this.desktop.current;
		return this.desktop.allMonitors().map((m, i) => ({
			idx: i,
			name: m.name,
			width: m.width,
			height: m.height,
			primary: !!m.primary,
			current: i === cur
		}));
	}

	getCurrentDisplay(): number {
		return this.desktop.current;
	}

	switchMonitor(idx: number): void {
		this.desktop.current = Math.max(0, Math.min(this.desktop.allMonitors().length - 1, Number(idx) || 0));
		this.desktop.drag = null;
		this._emit('display_switched', this.desktop.current);
	}

	getVirtualDisplaySupport(): VirtualDisplaySupport {
		return {
			supported: true,
			impl: 'rustdesk_idd',
			rustdeskDisplays: this.desktop.virtual.map((v) => v.vIndex ?? 0)
		};
	}

	toggleVirtualDisplay(index: number, on: boolean): void {
		const d = this.desktop;
		if (index === -1) d.virtual = [];
		else if (on && !d.virtual.some((v) => v.vIndex === index)) {
			d.virtual.push({
				vIndex: index,
				name: 'Virtual display ' + index,
				width: 1600,
				height: 900,
				wall: ['#2a3a2a', '#182418']
			});
		} else if (!on) d.virtual = d.virtual.filter((v) => v.vIndex !== index);
		if (d.current >= d.allMonitors().length) d.current = 0;
		this._emit('virtual_display_toggled', { index, on: !!on });
	}

	getWindowsSessions(): WindowsSessions {
		return {
			sessions: [
				{ sid: 1, name: 'Console (demo)' },
				{ sid: 2, name: 'Remote session 2 (alex)' }
			],
			currentSid: this._sid
		};
	}

	selectWindowsSession(sid: number): void {
		this._sid = sid;
		this.desktop.note('Switched to session ' + sid);
		this._emit('windows_session_selected', sid);
	}

	getStats(): MockStats {
		return {
			state: this._state,
			video: { codec: this._codec },
			audio: {},
			renderer: this.renderer.getStats(),
			connection: 'mock',
			diagnostics: {}
		};
	}
}
