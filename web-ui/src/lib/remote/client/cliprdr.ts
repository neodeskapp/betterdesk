/**
 * Cliprdr file clipboard sync for RdClient desktop (local ↔ remote Explorer paste).
 *
 * Outbound: CF_HDROP → FormatList → serve FormatData/FileContents.
 * Inbound: peer FormatList → request descriptor + file bytes → local CF_HDROP.
 */
import type {
	Cliprdr,
	CliprdrFileContentsRequest,
	CliprdrFileContentsResponse,
	CliprdrServerFormatDataRequest,
	CliprdrServerFormatDataResponse,
	CliprdrServerFormatList,
	MessageInit
} from './generated/protos.ts';
import { RDInput } from './input.ts';
import { LocalFiles } from './local-files.ts';
import { debugInfo, debugLog as consoleLog } from './log.ts';
import { page, tauriInvoke } from './page.ts';
import type { RDCliprdrFormatNames, RDProtocol } from './protocol.ts';

const CLIP_POLL_MS = 3000;
// Larger chunks = fewer IPC round-trips. Still leave room for other relay traffic.
const INBOUND_CHUNK = 256 * 1024;
const OUTBOUND_SUPPRESS_MS = 4000;
const CB_RESPONSE_OK = 0x1;
const CB_RESPONSE_FAIL = 0x2;
const FILECONTENTS_SIZE = 0x1;
const FILECONTENTS_RANGE = 0x2;

/** What RDCliprdr uses of the client's input (RDInput); the optional members are not on RDInput */
export interface RDCliprdrInput {
	canvas: HTMLCanvasElement;
	onPotentialFileDragOut?: () => void;
	_sendSyntheticMouseUp?: (button: number) => void;
	sendCtrlC?: () => void;
	sendCtrlV?: () => void;
	setMouseSuppressed?: (suppressed: boolean) => void;
	clickAtCanvas?: (x: number, y: number) => void;
}

/** A request to the peer waiting for its FormatDataResponse or FileContentsResponse */
export interface RDCliprdrPending {
	resolve(bytes: Uint8Array): void;
	reject(err: Error): void;
}

/**
 * What RDCliprdr uses of the client (RDClient). The _cliprdr fields are
 * RDCliprdr's own state, which it keeps on the client.
 */
export interface RDCliprdrClient {
	_state: string;
	readonly viewOnly: boolean;
	proto: Pick<
		RDProtocol,
		| 'buildCliprdrMonitorReady'
		| 'buildCliprdrFormatList'
		| 'buildCliprdrFormatListResponse'
		| 'buildCliprdrFormatDataRequest'
		| 'buildCliprdrFormatDataResponse'
		| 'buildCliprdrFileContentsRequest'
		| 'buildCliprdrFileContentsResponse'
	>;
	input: RDCliprdrInput;
	_sendPeerMessage(msg: MessageInit): void;
	_emit(event: string, ...args: unknown[]): void;
	_cliprdrPeerReady?: boolean;
	_cliprdrLocalSignature?: string;
	/** The desktop bridge's answer to desktop_clipboard_format_names, as it came */
	_cliprdrFormatNames?: unknown;
	_cliprdrStreamId?: number;
	_cliprdrPending?: Record<string, RDCliprdrPending>;
	_cliprdrReceiving?: boolean;
	_cliprdrSuppressOutboundUntil?: number;
	_cliprdrPeerFdId?: number;
	_cliprdrPeerFcId?: number;
	_cliprdrDragOutConverting?: boolean;
	_cliprdrOleDragIntent?: boolean;
	_cliprdrOleDragWhenReady?: boolean;
	_cliprdrOleDragStarting?: boolean;
	_cliprdrDragOutTimer?: ReturnType<typeof setTimeout> | null;
	_cliprdrPollTimer?: ReturnType<typeof setInterval> | null;
	_cliprdrSyncInFlight?: boolean;
	_cliprdrFormatListAckResolve?: (() => void) | null;
	_cliprdrFormatDataStreamId?: number;
	_cliprdrOleDragLeaveHandler?: (() => void) | null;
	_cliprdrOleDragPaths?: string[];
	_cliprdrOutboundQueue?: Promise<void>;
}

/** The desktop bridge's answer to desktop_clipboard_sync and desktop_clipboard_sync_paths */
export interface RDCliprdrSyncResult {
	hasFiles?: boolean;
	signature?: string;
	busy?: boolean;
	tooLarge?: boolean;
	too_large?: boolean;
	entryCount?: number;
	entry_count?: number;
	totalBytes?: number;
	total_bytes?: number;
}

/** A file or folder of desktop_clipboard_receive_begin's answer */
interface RDCliprdrReceiveFile {
	isDir?: boolean;
	is_dir?: boolean;
	index?: number;
	size?: number;
}

/** desktop_clipboard_receive_begin's answer */
interface RDCliprdrReceiveBegin {
	files?: RDCliprdrReceiveFile[];
}

/** desktop_clipboard_receive_commit's answer */
interface RDCliprdrReceiveCommit {
	signature?: string;
	paths?: string[];
}

/** A drop position as the desktop shell reports it (physical pixels of the webview) */
export interface RDCliprdrDropPosition {
	x?: number;
	y?: number;
}

function isDesktopBridge(): boolean {
	return page().__BETTERDESK_RDCLIENT_DESKTOP__ === true && tauriInvoke() !== null;
}

function desktopInvoke(cmd: string, args?: Record<string, unknown>): Promise<unknown> {
	const invoke = tauriInvoke();
	if (!invoke) throw new TypeError('No desktop bridge for ' + cmd);
	return invoke(cmd, args || {});
}

/** A desktop bridge answer that is an object (its fields are read as the bridge sent them), else null */
function bridgeObject(value: unknown): object | null {
	return typeof value === 'object' && value !== null ? value : null;
}

/** err.message as the JS read it (undefined when there is none) */
function errorMessage(err: unknown): unknown {
	return typeof err === 'object' && err !== null && 'message' in err ? err.message : undefined;
}

function toUint8Array(data: unknown): Uint8Array {
	// Shared LocalFiles helper (same base64↔bytes contract as File transfer).
	return LocalFiles.coerceBinaryPayload(data);
}

/** Avoid Array.from(u8) → JSON number[] which freezes WebView on large transfers. */
function bytesToBase64(u8: Uint8Array): string {
	const bytes = toUint8Array(u8);
	if (!bytes.length) return '';
	const CHUNK = 0x8000;
	let binary = '';
	for (let i = 0; i < bytes.length; i += CHUNK) {
		binary += String.fromCharCode(...bytes.subarray(i, Math.min(i + CHUNK, bytes.length)));
	}
	return btoa(binary);
}

/** Let video / ping / input run between Cliprdr chunks (RustDesk keeps this off the UI thread). */
function yieldToUi(): Promise<void> {
	return new Promise((resolve) => {
		setTimeout(resolve, 0);
	});
}

function debugLog(...args: unknown[]): void {
	if (typeof window !== 'undefined' && page().BetterDesk?.debugRelay) {
		consoleLog('[RDCliprdr]', ...args);
	}
}

function formatNameEquals(name: unknown, expected: string): boolean {
	if (!name) return false;
	return String(name).toLowerCase() === String(expected).toLowerCase();
}

function nextStreamId(client: RDCliprdrClient): number {
	const next = (client._cliprdrStreamId || 0) + 1;
	client._cliprdrStreamId = next;
	return next;
}

function pendingKey(streamId: number): string {
	return String(streamId);
}

function waitForPeerResponse(
	client: RDCliprdrClient,
	streamId: number,
	timeoutMs: number
): Promise<Uint8Array> {
	if (!client._cliprdrPending) client._cliprdrPending = Object.create(null);
	const key = pendingKey(streamId);
	return new Promise((resolve, reject) => {
		const timer = setTimeout(() => {
			if (client._cliprdrPending) Reflect.deleteProperty(client._cliprdrPending, key);
			reject(new Error('Cliprdr response timeout (stream ' + streamId + ')'));
		}, timeoutMs || 60000);
		const pending: RDCliprdrPending = {
			resolve: (bytes) => {
				clearTimeout(timer);
				resolve(bytes);
			},
			reject: (err) => {
				clearTimeout(timer);
				reject(err);
			}
		};
		if (client._cliprdrPending) client._cliprdrPending[key] = pending;
	});
}

function resolvePending(client: RDCliprdrClient, streamId: number, ok: boolean, bytes: Uint8Array): void {
	if (!client._cliprdrPending) return;
	const pending = client._cliprdrPending[pendingKey(streamId)];
	if (!pending) return;
	Reflect.deleteProperty(client._cliprdrPending, pendingKey(streamId));
	if (ok) pending.resolve(bytes || new Uint8Array(0));
	else pending.reject(new Error('peer reported Cliprdr failure'));
}

function suppressOutbound(client: RDCliprdrClient, ms: number): void {
	client._cliprdrSuppressOutboundUntil = Date.now() + (ms || OUTBOUND_SUPPRESS_MS);
}

function outboundSuppressed(client: RDCliprdrClient): boolean {
	return !!(
		client._cliprdrReceiving ||
		(client._cliprdrSuppressOutboundUntil && Date.now() < client._cliprdrSuppressOutboundUntil)
	);
}

/** Serialize FormatData / FileContents responses so IPC + cache stays consistent. */
function enqueueOutbound(client: RDCliprdrClient, work: () => Promise<void>): Promise<void> {
	const prev = client._cliprdrOutboundQueue || Promise.resolve();
	const next = prev
		.catch(() => {
			/* keep chain alive */
		})
		.then(work);
	client._cliprdrOutboundQueue = next;
	return next;
}

export const RDCliprdr = {
	FILEDESCRIPTOR_FORMAT_ID: 49334,
	FILECONTENTS_FORMAT_ID: 49267,
	FILEDESCRIPTOR_FORMAT_NAME: 'FileGroupDescriptorW',
	FILECONTENTS_FORMAT_NAME: 'FileContents',

	isSupported(): boolean {
		return isDesktopBridge();
	},

	async initClient(client: RDCliprdrClient | null | undefined): Promise<void> {
		if (!RDCliprdr.isSupported() || !client) return;
		client._cliprdrPeerReady = false;
		client._cliprdrLocalSignature = '';
		client._cliprdrFormatNames = null;
		client._cliprdrStreamId = 1;
		client._cliprdrPending = Object.create(null);
		client._cliprdrReceiving = false;
		client._cliprdrSuppressOutboundUntil = 0;
		client._cliprdrPeerFdId = RDCliprdr.FILEDESCRIPTOR_FORMAT_ID;
		client._cliprdrPeerFcId = RDCliprdr.FILECONTENTS_FORMAT_ID;
		client._cliprdrDragOutConverting = false;
		client._cliprdrOleDragIntent = false;
		if (client._cliprdrDragOutTimer) {
			clearTimeout(client._cliprdrDragOutTimer);
			client._cliprdrDragOutTimer = null;
		}
		try {
			client._cliprdrFormatNames = await desktopInvoke('desktop_clipboard_format_names');
		} catch {
			client._cliprdrFormatNames = null;
		}
		client._sendPeerMessage(client.proto.buildCliprdrMonitorReady());
		RDCliprdr.startPolling(client);

		// Remote Explorer drag never updates Cliprdr by itself — when the
		// operator drags toward the window edge we Ctrl+C on the remote so
		// FormatList arrives and we can start a local OLE drag-out.
		if (client.input) {
			client.input.onPotentialFileDragOut = () => {
				RDCliprdr._beginDragOutConversion(client);
			};
		}
	},

	/**
	 * Convert a stuck remote Explorer file-drag into Cliprdr + local OLE drag.
	 * RustDesk only advertises files via clipboard FormatList (Copy), not OLE.
	 */
	_beginDragOutConversion(client: RDCliprdrClient | null | undefined): void {
		if (!RDCliprdr.isSupported() || !client) return;
		if (client._state !== 'streaming' || client.viewOnly) return;
		if (client._cliprdrDragOutConverting || client._cliprdrReceiving) return;
		if (client._cliprdrOleDragStarting) return;

		client._cliprdrDragOutConverting = true;
		client._cliprdrOleDragIntent = true;
		client._cliprdrOleDragWhenReady = true;

		// End remote Explorer drag only — do not freeze local input for seconds.
		// Full mouse suppress happens once file FormatList arrives.
		if (client.input && typeof client.input._sendSyntheticMouseUp === 'function') {
			client.input._sendSyntheticMouseUp(RDInput.MOUSE_BUTTON_LEFT);
		}
		try {
			void desktopInvoke('desktop_clipboard_prepare_ole_drag');
		} catch {
			/* ignore */
		}

		debugInfo('[RDCliprdr] remote Explorer drag → Ctrl+C for local drop; keep mouse button held');

		if (client._cliprdrDragOutTimer) {
			clearTimeout(client._cliprdrDragOutTimer);
		}
		setTimeout(() => {
			if (!client._cliprdrDragOutConverting) return;
			if (client.input && typeof client.input.sendCtrlC === 'function') {
				client.input.sendCtrlC();
			}
		}, 60);

		client._cliprdrDragOutTimer = setTimeout(() => {
			client._cliprdrDragOutTimer = null;
			if (!client._cliprdrDragOutConverting) return;
			if (client._cliprdrReceiving || client._cliprdrOleDragStarting) return;
			client._cliprdrDragOutConverting = false;
			client._cliprdrOleDragIntent = false;
			client._cliprdrOleDragWhenReady = false;
			RDCliprdr._disarmOleDrag(client);
			if (client.input && typeof client.input.setMouseSuppressed === 'function') {
				client.input.setMouseSuppressed(false);
			}
			console.warn(
				'[RDCliprdr] no file clipboard after drag-out conversion — use Copy/Paste or File transfer'
			);
		}, 2000);
	},

	_clearDragOutConversion(client: RDCliprdrClient | null | undefined, restoreMouse: boolean): void {
		if (!client) return;
		client._cliprdrDragOutConverting = false;
		if (client._cliprdrDragOutTimer) {
			clearTimeout(client._cliprdrDragOutTimer);
			client._cliprdrDragOutTimer = null;
		}
		if (restoreMouse && client.input && typeof client.input.setMouseSuppressed === 'function') {
			client.input.setMouseSuppressed(false);
		}
	},

	stopPolling(client: RDCliprdrClient | null | undefined): void {
		if (!client || !client._cliprdrPollTimer) return;
		clearInterval(client._cliprdrPollTimer);
		client._cliprdrPollTimer = null;
	},

	startPolling(client: RDCliprdrClient | null | undefined): void {
		if (!RDCliprdr.isSupported() || !client) return;
		RDCliprdr.stopPolling(client);
		client._cliprdrPollTimer = setInterval(() => {
			if (client._state !== 'streaming' || client.viewOnly) return;
			if (client._cliprdrSyncInFlight || client._cliprdrReceiving) return;
			client._cliprdrSyncInFlight = true;
			Promise.resolve(RDCliprdr.syncLocalFiles(client)).finally(() => {
				client._cliprdrSyncInFlight = false;
			});
		}, CLIP_POLL_MS);
	},

	async syncLocalFiles(client: RDCliprdrClient | null | undefined): Promise<RDCliprdrSyncResult> {
		return RDCliprdr.syncPaths(client, null, null);
	},

	/**
	 * @param paths - files dropped on the viewer, or null for the local clipboard
	 * @param position - physical drop position (webview)
	 */
	async syncPaths(
		client: RDCliprdrClient | null | undefined,
		paths: string[] | null,
		position: RDCliprdrDropPosition | null
	): Promise<RDCliprdrSyncResult> {
		if (!RDCliprdr.isSupported() || !client) {
			debugLog('syncPaths skipped: isSupported=', RDCliprdr.isSupported(), 'client=', !!client);
			return { hasFiles: false, signature: '', busy: false };
		}
		if (client._state !== 'streaming' || client.viewOnly) {
			debugLog('syncPaths skipped: state=', client._state, 'viewOnly=', client.viewOnly);
			return { hasFiles: false, signature: '', busy: false };
		}
		if (!paths && outboundSuppressed(client)) {
			debugLog('syncPaths skipped: outbound suppressed (inbound receive)');
			return { hasFiles: false, signature: client._cliprdrLocalSignature || '', busy: false };
		}

		let answer: unknown;
		try {
			if (paths && paths.length) {
				answer = await desktopInvoke('desktop_clipboard_sync_paths', { paths: paths });
			} else {
				answer = await desktopInvoke('desktop_clipboard_sync');
			}
		} catch (err) {
			console.warn('[RDCliprdr] sync failed:', err);
			return { hasFiles: false, signature: '', busy: false };
		}

		debugLog('sync result:', answer, paths ? paths.length + ' path(s) supplied' : 'clipboard poll');
		const sync: RDCliprdrSyncResult | null = bridgeObject(answer);

		if (sync && sync.busy) {
			return sync;
		}

		if (!sync || !sync.hasFiles) {
			if (!paths) client._cliprdrLocalSignature = '';
			return sync || { hasFiles: false, signature: '', busy: false };
		}

		const signature = sync.signature || '';
		const tooLarge = !!(sync.tooLarge || sync.too_large);
		// DnD paths must always re-advertise + auto-paste even if signature matches
		// a prior clipboard copy of the same files.
		const forceAdvertise = !!(paths && paths.length);
		if (!forceAdvertise && signature && signature === client._cliprdrLocalSignature) {
			return sync;
		}
		client._cliprdrLocalSignature = signature;

		// Large folder trees freeze remote Explorer over the shared desktop
		// relay — refuse Cliprdr and steer to dedicated File transfer.
		if (tooLarge) {
			console.warn(
				'[RDCliprdr] selection too large for Cliprdr paste (' +
					(sync.entryCount || sync.entry_count || '?') +
					' entries / ' +
					(sync.totalBytes || sync.total_bytes || '?') +
					' bytes) — use File transfer'
			);
			try {
				client._emit('cliprdr_too_large', {
					signature: signature,
					entryCount: Number(sync.entryCount != null ? sync.entryCount : sync.entry_count || 0),
					totalBytes: Number(sync.totalBytes != null ? sync.totalBytes : sync.total_bytes || 0)
				});
			} catch {
				/* ignore */
			}
			return sync;
		}

		debugLog('FormatList', paths ? paths.length + ' path(s)' : 'clipboard');
		if (client._cliprdrFormatNames == null) {
			client._cliprdrFormatNames = await desktopInvoke('desktop_clipboard_format_names').catch(() => {
				return null;
			});
		}

		let formatListAck: Promise<boolean> | null = null;
		if (forceAdvertise) {
			formatListAck = RDCliprdr._waitFormatListAck(client, 2500);
		}
		const names: RDCliprdrFormatNames | null = bridgeObject(client._cliprdrFormatNames);
		client._sendPeerMessage(client.proto.buildCliprdrFormatList(names || undefined));

		// Explicit paths = OS drag-drop onto the viewer — focus the drop
		// point on the remote, wait briefly for FormatList ack, then Ctrl+V.
		if (forceAdvertise && client.input) {
			RDCliprdr._autoPasteAfterDrop(client, position, formatListAck);
		}
		return sync;
	},

	_waitFormatListAck(client: RDCliprdrClient, timeoutMs: number): Promise<boolean> {
		return new Promise((resolve) => {
			let settled = false;
			const timer = setTimeout(() => {
				if (settled) return;
				settled = true;
				if (client._cliprdrFormatListAckResolve === resolve) {
					client._cliprdrFormatListAckResolve = null;
				}
				resolve(false);
			}, timeoutMs || 2500);
			client._cliprdrFormatListAckResolve = () => {
				if (settled) return;
				settled = true;
				clearTimeout(timer);
				client._cliprdrFormatListAckResolve = null;
				resolve(true);
			};
		});
	},

	_autoPasteAfterDrop(
		client: RDCliprdrClient,
		position: RDCliprdrDropPosition | null,
		formatListAck: Promise<boolean> | null
	): void {
		void (async () => {
			try {
				if (formatListAck) await formatListAck;
				else
					await new Promise((r) => {
						setTimeout(r, 400);
					});
				if (client._state !== 'streaming' || client.viewOnly) return;

				if (position && typeof client.input.clickAtCanvas === 'function') {
					const mapped = RDCliprdr._mapDropPositionToCanvas(client, position);
					if (mapped) {
						debugLog('auto-paste: click at canvas', mapped.x, mapped.y);
						client.input.clickAtCanvas(mapped.x, mapped.y);
						await new Promise((r) => {
							setTimeout(r, 80);
						});
					}
				}
				if (typeof client.input.sendCtrlV === 'function') {
					debugLog('auto-paste: sending synthetic Ctrl+V after drop');
					client.input.sendCtrlV();
				}
			} catch (err) {
				console.warn('[RDCliprdr] auto-paste after drop failed:', err);
			}
		})();
	},

	_mapDropPositionToCanvas(
		client: RDCliprdrClient | null | undefined,
		position: RDCliprdrDropPosition | null | undefined
	): { x: number; y: number } | null {
		if (!position || !client || !client.input || !client.input.canvas) return null;
		const canvas = client.input.canvas;
		const dpr = window.devicePixelRatio || 1;
		const x = Number(position.x);
		const y = Number(position.y);
		if (!isFinite(x) || !isFinite(y)) return null;
		// Tauri/wry positions are physical pixels relative to the webview client area.
		const cssX = x / dpr;
		const cssY = y / dpr;
		const rect = canvas.getBoundingClientRect();
		return {
			x: cssX - rect.left,
			y: cssY - rect.top
		};
	},

	async handleMessage(
		client: RDCliprdrClient | null | undefined,
		cliprdr: Cliprdr | null | undefined
	): Promise<void> {
		if (!cliprdr || !client) return;

		if (cliprdr.ready) {
			client._cliprdrPeerReady = true;
			client._sendPeerMessage(client.proto.buildCliprdrMonitorReady());
			await RDCliprdr.syncLocalFiles(client);
			return;
		}

		if (cliprdr.formatListResponse) {
			if (typeof client._cliprdrFormatListAckResolve === 'function') {
				client._cliprdrFormatListAckResolve();
			}
			return;
		}

		const formatList = cliprdr.formatList;
		if (formatList) {
			await RDCliprdr._handleInboundFormatList(client, formatList);
			return;
		}

		const formatDataReq = cliprdr.formatDataRequest;
		if (formatDataReq) {
			await enqueueOutbound(client, () => {
				return RDCliprdr._respondFormatData(client, formatDataReq);
			});
			return;
		}

		const formatDataResp = cliprdr.formatDataResponse;
		if (formatDataResp) {
			RDCliprdr._handleFormatDataResponse(client, formatDataResp);
			return;
		}

		const fileContentsReq = cliprdr.fileContentsRequest;
		if (fileContentsReq) {
			await enqueueOutbound(client, () => {
				return RDCliprdr._respondFileContents(client, fileContentsReq);
			});
			return;
		}

		const fileContentsResp = cliprdr.fileContentsResponse;
		if (fileContentsResp) {
			RDCliprdr._handleFileContentsResponse(client, fileContentsResp);
		}
	},

	_handleFormatDataResponse(client: RDCliprdrClient, resp: CliprdrServerFormatDataResponse): void {
		const flags = Number(resp.msgFlags);
		const data = toUint8Array(resp.formatData);
		// FormatDataResponse has no stream id — use the reserved pending key.
		resolvePending(client, client._cliprdrFormatDataStreamId || 0, flags === CB_RESPONSE_OK, data);
	},

	_handleFileContentsResponse(client: RDCliprdrClient, resp: CliprdrFileContentsResponse): void {
		const flags = Number(resp.msgFlags);
		const streamId = Number(resp.streamId);
		const data = toUint8Array(resp.requestedData);
		resolvePending(client, streamId, flags === CB_RESPONSE_OK, data);
	},

	async _handleInboundFormatList(
		client: RDCliprdrClient,
		formatList: CliprdrServerFormatList
	): Promise<void> {
		if (client._state !== 'streaming' || client.viewOnly) return;
		if (client._cliprdrReceiving) {
			debugLog('inbound FormatList ignored: already receiving');
			return;
		}

		const formats = formatList.formats || [];
		let fdId: number | null = null;
		let fcId: number | null = null;
		for (let i = 0; i < formats.length; i++) {
			const fmt: { id?: number; format?: string } = formats[i] || {};
			const id = Number(fmt.id);
			const name = fmt.format || '';
			if (
				formatNameEquals(name, RDCliprdr.FILEDESCRIPTOR_FORMAT_NAME) ||
				id === RDCliprdr.FILEDESCRIPTOR_FORMAT_ID
			) {
				fdId = id;
			}
			if (
				formatNameEquals(name, RDCliprdr.FILECONTENTS_FORMAT_NAME) ||
				id === RDCliprdr.FILECONTENTS_FORMAT_ID
			) {
				fcId = id;
			}
		}
		if (fdId == null || fcId == null) {
			debugLog('inbound FormatList has no file formats');
			if (client._cliprdrDragOutConverting) {
				RDCliprdr._clearDragOutConversion(client, true);
				client._cliprdrOleDragIntent = false;
				client._cliprdrOleDragWhenReady = false;
			}
			return;
		}

		client._cliprdrPeerFdId = fdId;
		client._cliprdrPeerFcId = fcId;
		client._sendPeerMessage(client.proto.buildCliprdrFormatListResponse(CB_RESPONSE_OK));

		// Drag-out: either we just converted Explorer-drag→Ctrl+C, or FormatList
		// arrived while local LBUTTON is still down.
		let dragIntent = !!client._cliprdrDragOutConverting;
		if (!dragIntent) {
			try {
				dragIntent = !!(await desktopInvoke('desktop_clipboard_lbutton_down'));
			} catch {
				dragIntent = false;
			}
		}
		RDCliprdr._clearDragOutConversion(client, false);
		client._cliprdrOleDragIntent = dragIntent;
		client._cliprdrOleDragWhenReady = false;
		RDCliprdr._disarmOleDrag(client);

		try {
			client._cliprdrReceiving = true;
			suppressOutbound(client, OUTBOUND_SUPPRESS_MS);
			if (dragIntent) {
				if (client.input && typeof client.input.setMouseSuppressed === 'function') {
					client.input.setMouseSuppressed(true);
				}
				try {
					await desktopInvoke('desktop_clipboard_prepare_ole_drag');
				} catch (prepErr) {
					debugLog('prepare OLE drag:', prepErr);
				}
				debugInfo('[RDCliprdr] remote→local drag: downloading; keep mouse button held');
				RDCliprdr._armOleDragOnLeave(client);
			}
			await RDCliprdr._pullRemoteFiles(client, fdId);
		} catch (err) {
			console.warn('[RDCliprdr] inbound file pull failed:', err);
			try {
				await desktopInvoke('desktop_clipboard_receive_abort');
			} catch {
				/* ignore */
			}
			RDCliprdr._disarmOleDrag(client);
			client._cliprdrOleDragIntent = false;
			if (client.input && typeof client.input.setMouseSuppressed === 'function') {
				client.input.setMouseSuppressed(false);
			}
		} finally {
			client._cliprdrReceiving = false;
			suppressOutbound(client, OUTBOUND_SUPPRESS_MS);
		}
	},

	_disarmOleDrag(client: RDCliprdrClient | null | undefined): void {
		if (!client) return;
		if (client._cliprdrOleDragLeaveHandler) {
			document.removeEventListener('mouseleave', client._cliprdrOleDragLeaveHandler, true);
			document.documentElement.removeEventListener('pointerleave', client._cliprdrOleDragLeaveHandler, true);
			window.removeEventListener('blur', client._cliprdrOleDragLeaveHandler);
			client._cliprdrOleDragLeaveHandler = null;
		}
	},

	_armOleDragOnLeave(client: RDCliprdrClient): void {
		RDCliprdr._disarmOleDrag(client);
		const handler = () => {
			if (!client._cliprdrOleDragIntent) return;
			if (client._cliprdrOleDragPaths && client._cliprdrOleDragPaths.length) {
				RDCliprdr._startOleDrag(client, client._cliprdrOleDragPaths);
			} else {
				// Files still downloading — start drag as soon as commit finishes.
				client._cliprdrOleDragWhenReady = true;
			}
		};
		client._cliprdrOleDragLeaveHandler = handler;
		document.addEventListener('mouseleave', handler, true);
		document.documentElement.addEventListener('pointerleave', handler, true);
		window.addEventListener('blur', handler);
	},

	async _startOleDrag(client: RDCliprdrClient, paths: string[] | null | undefined): Promise<void> {
		if (!paths || !paths.length) return;
		if (!client._cliprdrOleDragIntent && !client._cliprdrOleDragWhenReady) return;
		if (client._cliprdrOleDragStarting) return;
		client._cliprdrOleDragStarting = true;
		client._cliprdrOleDragIntent = false;
		client._cliprdrOleDragWhenReady = false;
		RDCliprdr._disarmOleDrag(client);
		try {
			const stillDown = await desktopInvoke('desktop_clipboard_lbutton_down');
			if (!stillDown) {
				debugLog('OLE drag skipped: LBUTTON up');
				return;
			}
			// Stop forwarding mouse to remote so the cursor can leave the window.
			if (client.input && typeof client.input.setMouseSuppressed === 'function') {
				client.input.setMouseSuppressed(true);
			}
			debugInfo('[RDCliprdr] starting OLE drag-out with', paths.length, 'path(s)');
			const result = await desktopInvoke('desktop_clipboard_start_drag', { paths: paths });
			debugLog('OLE drag result:', result);
		} catch (err) {
			console.warn('[RDCliprdr] OLE drag-out failed:', err);
		} finally {
			client._cliprdrOleDragStarting = false;
			if (client.input && typeof client.input.setMouseSuppressed === 'function') {
				client.input.setMouseSuppressed(false);
			}
		}
	},

	async _pullRemoteFiles(client: RDCliprdrClient, fdId: number): Promise<void> {
		const formatStreamId = 0;
		client._cliprdrFormatDataStreamId = formatStreamId;
		const formatWait = waitForPeerResponse(client, formatStreamId, 30000);
		client._sendPeerMessage(client.proto.buildCliprdrFormatDataRequest(fdId));
		const pdu = await formatWait;
		if (!pdu || !pdu.length) {
			throw new Error('empty FormatDataResponse');
		}

		const begin: RDCliprdrReceiveBegin | null = bridgeObject(
			await desktopInvoke('desktop_clipboard_receive_begin', {
				formatDataBase64: bytesToBase64(pdu)
			})
		);
		const files = (begin && begin.files) || [];
		debugLog('inbound receive begin:', files.length, 'entries');

		for (const file of files) {
			if (file.isDir || file.is_dir) continue;
			const listIndex = Number(file.index);
			const expectedSize = Number(file.size || 0);

			// Prefer descriptor size; fall back to FILECONTENTS_SIZE probe.
			let size = expectedSize;
			if (!size) {
				size = await RDCliprdr._requestFileSize(client, listIndex);
			}
			if (!size) {
				debugLog('skip empty file index', listIndex);
				continue;
			}

			let offset = 0;
			let chunkCount = 0;
			while (offset < size) {
				const chunkLen = Math.min(INBOUND_CHUNK, size - offset);
				const chunk = await RDCliprdr._requestFileRange(client, listIndex, offset, chunkLen);
				if (!chunk || !chunk.length) {
					throw new Error('empty FileContents chunk at offset ' + offset);
				}
				await desktopInvoke('desktop_clipboard_receive_write', {
					listIndex: listIndex,
					offset: offset,
					dataBase64: bytesToBase64(chunk)
				});
				offset += chunk.length;
				chunkCount++;
				// Keep the shared relay + WebView responsive (video/heartbeat).
				if ((chunkCount & 1) === 0) await yieldToUi();
				if (chunk.length < chunkLen) break;
			}
			await yieldToUi();
		}

		const commit: RDCliprdrReceiveCommit | null = bridgeObject(
			await desktopInvoke('desktop_clipboard_receive_commit')
		);
		if (commit && commit.signature) {
			client._cliprdrLocalSignature = commit.signature;
		}
		const paths = (commit && commit.paths) || [];
		client._cliprdrOleDragPaths = paths;
		debugLog('inbound receive committed; local CF_HDROP ready', paths.length, 'top path(s)');

		// Start OLE drag immediately while LBUTTON is still down — do not wait
		// for mouseleave. The remote/WebView often traps the cursor at the
		// window edge, so leave never fires.
		if (paths.length && (client._cliprdrOleDragIntent || client._cliprdrOleDragWhenReady)) {
			await RDCliprdr._startOleDrag(client, paths);
		} else if (client._cliprdrOleDragIntent) {
			// Download finished but nothing to drag — restore local mouse.
			client._cliprdrOleDragIntent = false;
			RDCliprdr._disarmOleDrag(client);
			if (client.input && typeof client.input.setMouseSuppressed === 'function') {
				client.input.setMouseSuppressed(false);
			}
		}
	},

	async _requestFileSize(client: RDCliprdrClient, listIndex: number): Promise<number> {
		const streamId = nextStreamId(client);
		const wait = waitForPeerResponse(client, streamId, 30000);
		client._sendPeerMessage(
			client.proto.buildCliprdrFileContentsRequest({
				streamId: streamId,
				listIndex: listIndex,
				dwFlags: FILECONTENTS_SIZE,
				nPositionLow: 0,
				nPositionHigh: 0,
				cbRequested: 8
			})
		);
		const data = await wait;
		if (!data || data.length < 8) return 0;
		const view = new DataView(data.buffer, data.byteOffset, data.byteLength);
		// RustDesk / our outbound encode size as u64 LE (not two u32s with high first).
		const lo = view.getUint32(0, true);
		const hi = view.getUint32(4, true);
		return hi * 0x100000000 + lo;
	},

	async _requestFileRange(
		client: RDCliprdrClient,
		listIndex: number,
		offset: number,
		length: number
	): Promise<Uint8Array> {
		const streamId = nextStreamId(client);
		const wait = waitForPeerResponse(client, streamId, 60000);
		const nPositionLow = offset >>> 0;
		const nPositionHigh = Math.floor(offset / 0x100000000) >>> 0;
		client._sendPeerMessage(
			client.proto.buildCliprdrFileContentsRequest({
				streamId: streamId,
				listIndex: listIndex,
				dwFlags: FILECONTENTS_RANGE,
				nPositionLow: nPositionLow,
				nPositionHigh: nPositionHigh,
				cbRequested: length
			})
		);
		return wait;
	},

	async _respondFormatData(client: RDCliprdrClient, req: CliprdrServerFormatDataRequest): Promise<void> {
		const formatId = Number(req.requestedFormatId);
		const names: RDCliprdrFormatNames | null = bridgeObject(client._cliprdrFormatNames);
		const fdId = names
			? Number(
					names.fileDescriptorFormatId ||
						names.file_descriptor_format_id ||
						RDCliprdr.FILEDESCRIPTOR_FORMAT_ID
				)
			: RDCliprdr.FILEDESCRIPTOR_FORMAT_ID;

		if (formatId !== fdId) {
			client._sendPeerMessage(
				client.proto.buildCliprdrFormatDataResponse(CB_RESPONSE_FAIL, new Uint8Array(0))
			);
			return;
		}

		try {
			const data = await desktopInvoke('desktop_clipboard_format_data');
			const bytes = toUint8Array(data);
			if (!bytes.length) {
				client._sendPeerMessage(
					client.proto.buildCliprdrFormatDataResponse(CB_RESPONSE_FAIL, new Uint8Array(0))
				);
				return;
			}
			debugLog('FormatDataResponse', bytes.length, 'bytes');
			client._sendPeerMessage(client.proto.buildCliprdrFormatDataResponse(CB_RESPONSE_OK, bytes));
			await yieldToUi();
		} catch (err) {
			const message = errorMessage(err);
			const msg = err && message ? message : String(err || '');
			console.warn('[RDCliprdr] format data failed:', err);
			if (/cliprdr_too_large/i.test(String(msg))) {
				try {
					client._emit('cliprdr_too_large', { signature: client._cliprdrLocalSignature || '' });
				} catch {
					/* ignore */
				}
			}
			// Fail fast so remote Explorer Paste unsticks instead of hanging.
			client._sendPeerMessage(
				client.proto.buildCliprdrFormatDataResponse(CB_RESPONSE_FAIL, new Uint8Array(0))
			);
		}
	},

	async _respondFileContents(client: RDCliprdrClient, req: CliprdrFileContentsRequest): Promise<void> {
		const streamId = Number(req.streamId);
		const listIndex = Number(req.listIndex);
		const dwFlags = Number(req.dwFlags);
		const nPositionLow = Number(req.nPositionLow);
		const nPositionHigh = Number(req.nPositionHigh);
		const cbRequested = Number(req.cbRequested);

		try {
			if (!cbRequested && dwFlags & FILECONTENTS_RANGE) {
				throw new Error('FILECONTENTS_RANGE with cbRequested=0');
			}
			// Pass both camelCase and snake_case — Tauri 2 normally converts,
			// but a missed rename leaves dw_flags=0 → unsupported → empty shells.
			const data = await desktopInvoke('desktop_clipboard_file_contents', {
				listIndex: listIndex,
				list_index: listIndex,
				dwFlags: dwFlags,
				dw_flags: dwFlags,
				nPositionLow: nPositionLow,
				n_position_low: nPositionLow,
				nPositionHigh: nPositionHigh,
				n_position_high: nPositionHigh,
				cbRequested: cbRequested,
				cb_requested: cbRequested
			});
			const bytes = toUint8Array(data);
			// FILECONTENTS_SIZE must be exactly 8 LE bytes. Empty here means the
			// desktop returned base64 that was decoded as `new Uint8Array(string)`
			// (length 0) — which produces 0 KB shells on remote Paste.
			if (dwFlags & FILECONTENTS_SIZE && !(dwFlags & FILECONTENTS_RANGE) && bytes.length !== 8) {
				throw new Error('invalid FILECONTENTS_SIZE payload length ' + bytes.length + ' (expected 8)');
			}
			// Never ACK an empty RANGE — remote Explorer would leave a 0 KB shell.
			if (dwFlags & FILECONTENTS_RANGE && cbRequested > 0 && !bytes.length) {
				throw new Error('empty FILECONTENTS_RANGE payload at listIndex ' + listIndex);
			}
			debugLog(
				'FileContentsResponse',
				'stream=' + streamId,
				'flags=' + dwFlags,
				'bytes=' + bytes.length,
				'cb=' + cbRequested
			);
			client._sendPeerMessage(client.proto.buildCliprdrFileContentsResponse(CB_RESPONSE_OK, streamId, bytes));
			await yieldToUi();
		} catch (err) {
			console.warn('[RDCliprdr] file contents failed:', err);
			client._sendPeerMessage(
				client.proto.buildCliprdrFileContentsResponse(CB_RESPONSE_FAIL, streamId, new Uint8Array(0))
			);
		}
	},

	clearLocalCache(): Promise<unknown> {
		if (!RDCliprdr.isSupported()) return Promise.resolve();
		return desktopInvoke('desktop_clipboard_clear').catch(() => {
			/* ignore */
		});
	}
};
