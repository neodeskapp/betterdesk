/**
 * The remote desktop client: the protocol, video, audio, input, clipboard
 * and file transfer code in ./client/ (RDClient for RustDesk devices,
 * CDAPSession, MeshSession and RemoteTargetSession for the other
 * transports, the file transfer sheet). This module is the edge the viewer
 * uses: it sets the page globals the client reads, loads the client once
 * per page (a chunk of its own, so other pages never fetch it) and hands
 * out clients.
 *
 * The dev-only mock device (./client/mock-session.ts, a chunk of its own)
 * loads only when the viewer's data says the server has it on
 * (ND_DEV_MOCK_REMOTE=1, then ?mock=1 or a seeded demo device); without
 * the flag the viewer data has no mock and the chunk is never fetched.
 */

import { setCsrfToken } from '#lib/api/client.ts';
import { t } from '#lib/i18n/i18n.svelte.ts';
import { installCapabilities } from './capabilities.ts';
import type { ViewerCapabilities } from './access.ts';
import type { LoginAnswer } from './client/crypto.ts';
import type { DisplayZoom } from './client/display-layout.ts';
import type { BackgroundMode, RDDisplayTraffic } from './client/display-streams.ts';
import type { RDDisplay } from './client/displays.ts';
import type { MessageInit } from './client/generated/protos.ts';
import type { RDInputRenderer } from './client/input.ts';
import type { Transport } from './states.ts';

/* ── The client's surface (RDClient; CDAPSession, MeshSession, RemoteTargetSession and the mock copy it) ── */

export interface Monitor {
	idx: number;
	name: string;
	width: number;
	height: number;
	primary: boolean;
	/** Not reported by CDAP sessions */
	current?: boolean;
}

export type { DisplayLayout, DisplayZoom } from './client/display-layout.ts';
export type { BackgroundMode, RDDisplayTraffic } from './client/display-streams.ts';
export type { RDDisplay } from './client/displays.ts';

/** How a display shown on its own is drawn (RDShowDisplayOptions in client.ts) */
export interface ShowDisplayOptions {
	zoom?: DisplayZoom;
	/** The window the canvas is in, when it is another one than the viewer's */
	win?: Window;
	/** Its input takes its window's keyboard (a window of its own) */
	keyboard?: boolean;
}

export interface VirtualDisplaySupport {
	supported: boolean;
	impl?: string;
	rustdeskDisplays?: number[];
	amyuniCount?: number;
}

export interface WindowsSessions {
	sessions: { sid: number; name: string }[];
	currentSid: number;
}

export interface ClientStats {
	/** RDClient */
	video?: { codec?: string | null; currentCodec?: string | null };
	renderer?: { fps?: number; remoteWidth?: number; remoteHeight?: number };
	/** CDAPSession */
	frames?: number;
	bytes?: number;
	fps?: number;
	kbps?: number;
	transport?: string;
}

export interface RemoteClient {
	readonly state?: string;
	readonly _state?: string;
	/** RDClient: the device proved its identity and encrypts (the other clients go through the server) */
	readonly encrypted?: boolean;
	viewOnly?: boolean;
	/** Not on mesh sessions */
	renderer?: { resize(): void };
	video?: { onAutoplayBlocked?: (() => void) | null; retryPlay?(): void };
	audio?: { audioCtx?: AudioContext | null };
	input?: {
		sendMessage?(msg: MessageInit): void;
		_handleKeyDown?(e: KeyboardEvent): void;
		_handleKeyUp?(e: KeyboardEvent): void;
		resetKeyboard?(): void;
	};
	fileTransfer?: unknown;
	/* Every client has these */
	on(event: string, fn: (...args: never[]) => void): unknown;
	connect(): Promise<unknown>;
	disconnect(): void;
	authenticate(password: string): unknown;
	/*
	 * The rest depends on the transport: RDClient has all of them, CDAP most,
	 * the RDP/VNC gateway a few and a mesh session almost none. Call them
	 * with ?. so a missing one does nothing.
	 */
	submit2FA?(code: string): void;
	sendChat?(text: string): void;
	sendCtrlAltDel?(): void;
	/** RDClient: sign in with the server's answer to the device's question; the page holds no password (L454) */
	authenticateWith?(answer: LoginAnswer): Promise<boolean>;
	sendLockScreen?(): void;
	sendRestartRemoteDevice?(): void;
	sendRefreshScreen?(): void;
	sendClipboard?(text: string): void;
	sendText?(text: string): boolean | undefined;
	resetKeyboard?(): void;
	setBlockInput?(on: boolean): void;
	setQualityPreset?(preset: string): void;
	setFpsMode?(mode: string): void;
	setKeyboardMode?(mode: string): void;
	setScaleMode?(mode: string): void;
	setCodec?(codec: string): void;
	setShowRemoteCursor?(on: boolean): void;
	setLockAfterSession?(on: boolean): void;
	setPrivacyMode?(on: boolean): void;
	/** Keeps the device's clipboard out of the operator's (the device is asked too) */
	setDisableClipboard?(on: boolean): void;
	setViewOnly?(on: boolean): void;
	setAudioMuted?(muted: boolean): void;
	setBackgroundFps?(fps: number): void;
	setSessionActive?(active: boolean): void;
	getMonitors?(): Monitor[];
	switchMonitor?(idx: number): void;
	/*
	 * Several displays at once (RDClient with RustDesk 1.2.4 or later, and the
	 * mock): a display shown on its own canvas has its own decoder, screen and
	 * mouse; the device streams exactly the displays shown (display-streams.ts)
	 */
	getDisplays?(): RDDisplay[];
	canShowDisplaysTogether?(): boolean;
	showDisplay?(idx: number, canvas: HTMLCanvasElement, opts?: ShowDisplayOptions): void;
	hideDisplay?(idx: number): void;
	setDisplayZoom?(idx: number, zoom: DisplayZoom): void;
	setDisplayVisible?(idx: number, visible: boolean): void;
	setDisplayBackground?(idx: number, background: boolean): void;
	setBackgroundDisplays?(mode: BackgroundMode): void;
	setMainDisplay?(idx: number | null): void;
	setMainVisible?(visible: boolean): void;
	resizeDisplays?(): void;
	getDisplayTraffic?(): RDDisplayTraffic[];
	displayCanvas?(idx: number): HTMLCanvasElement | null;
	getVirtualDisplaySupport?(): VirtualDisplaySupport;
	toggleVirtualDisplay?(index: number, on: boolean): void;
	getWindowsSessions?(): WindowsSessions;
	selectWindowsSession?(sid: number): void;
	getStats?(): ClientStats;
}

export interface ClientOptions {
	deviceId: string;
	serverPubKey: string;
	connection: Record<string, unknown> | null;
	myName: string;
	scaleMode: string;
	fps: number;
	fpsMode: string;
	imageQuality: string;
	qualityPreset: string;
	adaptiveQuality: boolean;
	preferCodec: string;
	disableAudio: boolean;
	serverRecord: boolean;
	/** A watch-only guest link: the client sends no input and the guest cannot turn view only off */
	viewOnly: boolean;
}

type ClientCtor = new (canvas: HTMLCanvasElement, opts: ClientOptions) => RemoteClient;

export interface FileSheet {
	open(session: unknown): void;
	close(): void;
	isOpen(): boolean;
	_session?: { deviceId?: string } | null;
	_el?: HTMLElement | null;
}

export interface TouchInput {
	setMode(mode: string): void;
	start(): void;
	stop(): void;
}
export type TouchRenderer = RDInputRenderer;
type TouchCtor = new (
	canvas: HTMLCanvasElement,
	renderer: TouchRenderer,
	send: (msg: MessageInit) => void
) => TouchInput;

/** What the viewer takes from the loaded client */
interface ClientModules {
	clients: Record<'rd' | 'cdap' | 'mesh' | 'target', ClientCtor>;
	RDTouch: TouchCtor;
	videoSupported(): boolean;
	fileSheet(): FileSheet | null;
}

interface RdWindow {
	NeoDesk?: Record<string, unknown>;
	t?: (key: string) => string;
	__capabilities?: ViewerCapabilities;
	__guestToken?: string;
	__initialDeviceId?: string;
	__mockRemote?: { scenario: string } | null;
}

const w = () => window as unknown as RdWindow;

export interface LoadOptions {
	deviceId: string;
	capabilities: ViewerCapabilities;
	guestToken: string;
	csrfToken: string;
	/** Set only when the server has the dev mock on for this request */
	mock: { scenario: string } | null;
}

/** The globals the client reads before and while it runs */
export function setGlobals(opts: LoadOptions): void {
	installCapabilities();
	// The viewer's own console calls (the quick support password) go through the API client
	setCsrfToken(opts.csrfToken);
	const win = w();
	win.NeoDesk = { ...(win.NeoDesk ?? {}), csrfToken: opts.csrfToken, t };
	// The file sheet, CDAP and mesh code translate through window.t (key back when missing)
	win.t = (key: string) => t(key);
	win.__capabilities = opts.capabilities;
	win.__guestToken = opts.guestToken;
	win.__initialDeviceId = opts.deviceId;
	win.__mockRemote = opts.mock;
}

/** The client's modules: one chunk, fetched by the first viewer on the page */
async function importClient(): Promise<ClientModules> {
	const [client, cdap, mesh, target, touch, video, sheet] = await Promise.all([
		import('./client/client.ts'),
		import('./client/cdap-adapter.ts'),
		import('./client/mesh-adapter.ts'),
		import('./client/remote-target.ts'),
		import('./client/touch.ts'),
		import('./client/video.ts'),
		import('./client/file-modal.ts')
	]);
	return {
		clients: {
			rd: client.RDClient,
			cdap: cdap.CDAPSession,
			mesh: mesh.MeshSession,
			target: target.RemoteTargetSession
		},
		RDTouch: touch.RDTouch,
		videoSupported: () => video.RDVideo.isSupported(),
		fileSheet: sheet.fileTransferModal
	};
}

let modules: ClientModules | null = null;
let loading: Promise<void> | null = null;
/** The dev mock device, once loaded */
let mockClient: ClientCtor | null = null;

/** Load the client once per page; the mock only when `mock` is set */
export function loadRdClient(opts: LoadOptions): Promise<void> {
	setGlobals(opts);
	loading ??= importClient()
		.then(async (m) => {
			if (opts.mock) mockClient = (await import('./client/mock-session.ts')).MockRemoteSession;
			modules = m;
		})
		.catch((err: unknown) => {
			loading = null;
			throw err;
		});
	return loading;
}

/** The client for a transport (RDClient for RustDesk peers; the mock replaces all of them) */
export function createClient(
	canvas: HTMLCanvasElement,
	opts: ClientOptions,
	transport: Transport
): RemoteClient {
	const win = w();
	const clients = modules?.clients;
	const Ctor: ClientCtor | undefined =
		win.__mockRemote && mockClient
			? mockClient
			: transport === 'mesh'
				? clients?.mesh
				: transport === 'cdap'
					? clients?.cdap
					: transport === 'rdp' || transport === 'vnc'
						? clients?.target
						: clients?.rd;
	if (!Ctor) throw new Error('Remote client not loaded');
	return new Ctor(canvas, opts);
}

/** WebCodecs decoding (needs a secure context); without it the client falls back to slower H.264 */
export function videoDecodingSupported(): boolean {
	return modules?.videoSupported() ?? true;
}

/** The file transfer sheet of the client (client/file-modal.ts builds its own DOM) */
export function fileSheet(): FileSheet | null {
	return modules?.fileSheet() ?? null;
}

/** The touch input of the RustDesk client */
export function touchInput(): TouchCtor | null {
	return modules?.RDTouch ?? null;
}
