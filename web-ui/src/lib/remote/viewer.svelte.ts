/**
 * The remote viewer's sessions: one tab per device, each with its own
 * client (RDClient, CDAP, mesh, RDP/VNC gateway or the dev mock), its
 * connection state in plain words, its chat and its viewer settings. Port
 * of the session manager in public/js/remote.js; the page components
 * render this state and call these methods.
 *
 * The clients are the wrapped legacy scripts (rdclient.ts); nothing here
 * puts them into $state (they are large mutable objects with their own
 * timers), only what the page shows.
 */

import { tick } from 'svelte';
import { sessionChat, type SessionBridge } from '#lib/chat/session-bridge.ts';
import { formatDate } from '#lib/i18n/format.ts';
import { tf } from '#lib/i18n/i18n.svelte.ts';
import { toast } from '#lib/ui/toasts.svelte.ts';
import { confirm } from '#lib/ui/dialogs.svelte.ts';
import {
	acceptCertificate,
	getBillingCheck,
	getPendingBillingSession,
	submitWorkReport,
	type BillingCheck,
	type PendingBillingSession,
	type ViewerData
} from '#lib/api/remote.ts';
import { getDevice, getDeviceReach, unblockDevice } from '#lib/api/devices.ts';
import { link } from '#lib/nav.ts';
import { takeDevicePassword, type DeviceSupport } from '#lib/api/onboard.ts';
import { describeFailure, isReason, type FailAction, type Reason } from './failures.ts';
import { answerConnectPassword } from '#lib/api/connectAnswer.ts';
import {
	getSignin,
	refusalCode,
	useSealedSignin,
	useSignin,
	type SigninDetails,
	type SigninStatus
} from '#lib/api/savedSignin.ts';
import {
	logonStateFromMessage,
	signinAdditions,
	signinOffer,
	signinResultOf,
	signinSteps,
	typeSignin,
	type PeerFacts,
	type SigninOffer
} from './signin.ts';
import { isGuestMode, isRemoteTarget, isViewOnlyGuest, peerAllowed, transportOf } from './access.ts';
import { isMobileViewer } from './capabilities.ts';
import {
	CLIPBOARD_LIMITS,
	ClipboardBridge,
	type ClipEntry,
	type ClipboardNotice,
	type ClipboardOffReason
} from './clipboard-bridge.svelte.ts';
import {
	createClient,
	fileSheet,
	touchInput,
	videoDecodingSupported,
	type ClientOptions,
	type Monitor,
	type RemoteClient,
	type TouchInput,
	type BackgroundMode,
	type DisplayLayout,
	type DisplayZoom,
	type RDDisplay,
	type TouchRenderer,
	type VirtualDisplaySupport,
	type WindowsSessions
} from './rdclient.ts';
import { DISPLAY_LAYOUTS, planSpread, type Rect } from './client/display-layout.ts';
import {
	DEFAULT_DISPLAY_PREFS,
	loadDisplayPrefs,
	saveDisplayPrefs,
	type DisplayPrefs
} from './display-prefs.ts';
import { CODECS, fpsForMode, loadPrefs, presetForQuality, savePrefs, type ViewerPrefs } from './prefs.ts';
import { secureStore } from './secure-store.ts';
import {
	classifyEnd,
	detailRows,
	detailsText,
	endedOnDevice,
	endOutcome,
	HTTPS_DOCS_URL,
	rdEndReason,
	isStep,
	loginErrorText,
	SLOW_AFTER_MS,
	stepAdvances,
	stepFromLog,
	stepText,
	type CloseMeta,
	type EndInfo,
	type Outcome,
	type StepId,
	type Transport,
	type View
} from './states.ts';

/** Whether a client's renderer maps touches to the remote screen (the RustDesk renderer does) */
function isTouchRenderer(renderer: unknown): renderer is TouchRenderer {
	return (
		typeof renderer === 'object' &&
		renderer !== null &&
		'canvasToRemote' in renderer &&
		typeof renderer.canvasToRemote === 'function'
	);
}

/** How long the viewer waits for the person at the computer to accept */
export const CONSENT_WAIT_MS = 3 * 60 * 1000;

export type SheetName = 'chat' | 'files' | 'info';

/** The controls that open each sheet (focus goes back there when it closes; file-modal.js marks btn-file-transfer) */
export const SHEET_BUTTONS: Record<SheetName, string> = {
	chat: 'rd-btn-chat',
	files: 'btn-file-transfer',
	info: 'rd-btn-info'
};

export interface ChatLine {
	id: number;
	text: string;
	type: 'sent' | 'received';
}

export interface Toggles {
	showCursor: boolean;
	lockAfter: boolean;
	privacy: boolean;
	disableClipboard: boolean;
	blockInput: boolean;
}

export interface BillingPane {
	session: PendingBillingSession;
	summary: string;
	error: string;
	busy: boolean;
}

/** What the device lets this session use; it turns these off for the account's client features */
export interface DevicePermissions {
	keyboard: boolean;
	clipboard: boolean;
	file: boolean;
	restart: boolean;
}

const ALL_ALLOWED: DevicePermissions = { keyboard: true, clipboard: true, file: true, restart: true };

/** PermissionInfo.Permission values (message.proto) the viewer follows */
const DEVICE_PERMISSIONS: Record<number, keyof DevicePermissions> = {
	0: 'keyboard',
	2: 'clipboard',
	4: 'file',
	5: 'restart'
};

/** The wait before the viewer signs in by itself, so the first picture of the screen is there */
const SIGNIN_AUTO_DELAY_MS = 800;

/** What a refused use of the saved sign-in means, in the viewer's words */
function signinRefusalText(code: string): string {
	switch (code) {
		case 'already_used':
			return tf('remote.signin_already_used', 'Sign in was already used in this session.');
		case 'session_not_open':
			return tf(
				'remote.signin_session_not_open',
				'The server does not see this session as open. Reconnect and try again.'
			);
		case 'not_set':
			return tf('remote.signin_not_set', 'No sign-in details are saved for this computer any more.');
		case 'cannot_open':
			return tf(
				'remote.signin_cannot_open',
				"The saved details can't be read with this server's key. Save them again."
			);
		case 'vault_unavailable':
			return tf('remote.signin_unavailable', "This server can't use saved sign-in details right now.");
		case 'sealed_required':
			return tf(
				'remote.signin_sealed_required',
				'NeoDesk on this computer signs in by itself, but not in this session, so the server hands out no readable details. Update NeoDesk on the computer.'
			);
		case 'no_device_key':
			return tf(
				'remote.signin_no_device_key',
				"The server has no key of this computer, so it can't seal the sign-in for it."
			);
		default:
			return tf('remote.signin_failed', 'Sign in did not work. Sign in by hand.');
	}
}

/** How long the viewer waits for the computer's NeoDesk to answer a sealed sign-in */
const SEALED_ANSWER_MS = 15000;

/** What the computer's NeoDesk answered to a sealed sign-in, in the viewer's words */
function sealedResultText(result: string): string {
	switch (result) {
		case 'typed':
			return tf(
				'remote.signin_sealed_typed',
				'NeoDesk on the computer typed the saved details. If Windows shows an error, sign in by hand: they are not tried again in this session.'
			);
		case 'not_at_lock_screen':
			return tf(
				'remote.signin_not_at_lock_screen',
				"NeoDesk on the computer typed nothing: it isn't at the lock or sign-in screen."
			);
		case 'expired':
			return tf(
				'remote.signin_expired',
				"The sign-in request had expired when it reached the computer. Check the computer's clock."
			);
		case 'bad_blob':
			return tf('remote.signin_bad_blob', "NeoDesk on the computer couldn't open the sign-in request.");
		default:
			return tf('remote.signin_no_answer', "NeoDesk on the computer didn't answer the sign-in request.");
	}
}

/** Hooks a session panel gives the viewer to move focus inside it */
export interface PanelFocus {
	canvas(): void;
	password(): void;
	tfa(): void;
	reconnect(): void;
	summary(): void;
}

let chatSeq = 0;

export class ViewerSession {
	readonly deviceId: string;
	/** The session's chat as the device's support conversation (A242), null without chat */
	chatBridge: SessionBridge | null = null;
	deviceName = $state('');
	platform = $state('');
	/** What the panel shows */
	view = $state<View>('connecting');
	/** The transport's own state word */
	state = $state('idle');
	stepId = $state<StepId | null>(null);
	slowHint = $state('');
	statusText = $state('');
	/** Why it failed, as the server or the client knows it ('' while connecting) */
	whyText = $state('');
	tryText = $state('');
	outcomeKind = $state('');
	/** The buttons of a failure (failures.ts) */
	actions = $state.raw<FailAction[]>([]);
	/** This account may unblock the computer (the server's reach answer) */
	canUnblock = $state(false);
	unblocking = $state(false);
	/** Waiting for the person at the computer to accept: what they see */
	waitHint = $state('');
	awaitingConsent = false;
	/** The person at the computer (a quick support session's name), '' when unknown */
	consentPerson = '';
	consentTimer: ReturnType<typeof setTimeout> | null = null;
	/** A docs page the result points to, '' for none */
	helpUrl = $state('');
	endInfo = $state.raw<EndInfo | null>(null);
	endedBy = $state<'' | 'user' | 'cancel'>('');
	everStreamed = $state(false);
	connectedAt = $state<number | null>(null);
	lastError = $state('');
	/** The client confirmed end-to-end encryption with the device (RustDesk only) */
	encrypted = $state(false);
	loginError = $state('');
	tfaError = $state('');
	password = $state('');
	remember = $state(false);
	/** The password filled in from this browser's store, and the one last sent to the device */
	rememberedPassword = '';
	submittedPassword = '';
	/**
	 * The device password an organisation stored was tried on this
	 * connection: the server answered the device's question, the page never
	 * held the password (L454). Once per connection, never again after a refusal.
	 */
	orgAnswerTried = false;
	/** This connection's sign-in was that answer (a refusal says so) */
	usingOrgAnswer = false;
	/** Saved sign-in (L469): what the server says about it, loaded once per tab */
	signin = $state.raw<SigninStatus | null>(null);
	signinLoaded = false;
	/** What the device said about itself at this connection's sign-in */
	peerFacts = $state.raw<PeerFacts | null>(null);
	signinOffer = $state.raw<SigninOffer>({ kind: 'none' });
	/** This connection used its one attempt */
	signinTried = false;
	/** Due to sign in by itself while the viewer was off screen: it does once back */
	signinDeferred = false;
	signinBusy = $state(false);
	/** What the bar over the screen says after an attempt */
	signinNote = $state('');
	signinDismissed = $state(false);
	/** The nonce of the sealed sign-in sent to the computer, until it answers */
	signinPending = '';
	signinTimer: ReturnType<typeof setTimeout> | null = null;
	tfaCode = $state('');
	cdapFallback = $state(false);
	autoplayBlocked = $state(false);
	audioMuted = $state(false);
	recording = $state(false);
	meshServerRecord = $state(false);
	viewOnly = $state(false);
	/** What the device lets this session use (PermissionInfo messages) */
	allowed = $state<DevicePermissions>({ ...ALL_ALLOWED });
	/** The operator's own view only setting while the device has the keyboard off */
	viewOnlyBeforeKeyboardOff: boolean | null = null;
	chat = $state.raw<ChatLine[]>([]);
	latency = $state(0);
	monitors = $state.raw<Monitor[]>([]);
	/** The device's displays with their places, and whether it streams several at once */
	displays = $state.raw<RDDisplay[]>([]);
	together = $state(false);
	/** How the displays show: layout, zoom, background (display-prefs.ts, per device) */
	display = $state.raw<DisplayPrefs>({ ...DEFAULT_DISPLAY_PREFS });
	/** The display worked on in the arranged and grid layouts */
	activeDisplay = $state(0);
	/** Displays in windows of their own */
	popouts = $state.raw<number[]>([]);
	/** Their windows (not state: other windows of the page) */
	// eslint-disable-next-line svelte/prefer-svelte-reactivity -- windows, nothing renders from them
	windows = new Map<number, Window>();
	displayPrefsLoaded = false;
	virtualDisplays = $state.raw<VirtualDisplaySupport>({ supported: false });
	windowsSessions = $state.raw<WindowsSessions>({ sessions: [], currentSid: 0 });
	prefs = $state<ViewerPrefs>(loadPrefs(null));
	toggles = $state<Toggles>({
		showCursor: true,
		lockAfter: false,
		privacy: false,
		disableClipboard: false,
		blockInput: false
	});
	billing = $state.raw<BillingPane | null>(null);
	/** Bumped when the stats sheet should re-read the client */
	statsTick = $state(0);

	client: RemoteClient | null = null;
	canvas: HTMLCanvasElement | null = null;
	focus: PanelFocus | null = null;
	closed = false;
	started = false;
	slowTimer: ReturnType<typeof setTimeout> | null = null;
	endTimer: ReturnType<typeof setTimeout> | null = null;
	mediaRecorder: MediaRecorder | null = null;
	recordedChunks: Blob[] = [];
	touch: TouchInput | null = null;

	constructor(deviceId: string, name: string, platform: string, prefs: ViewerPrefs) {
		this.deviceId = deviceId;
		this.deviceName = name && name !== deviceId ? name : '';
		this.platform = platform;
		this.prefs = { ...prefs };
	}

	get label(): string {
		return this.deviceName || this.deviceId;
	}

	get streaming(): boolean {
		return this.view === 'streaming';
	}
}

/** Narrower windows and touch tablets show one display at a time */
export const TILES_MIN_WIDTH = 900;

/** Messages between the viewer and a display's own window (postMessage, same origin) */
export type DisplayWindowMessage =
	| { type: 'nd-display-ready'; deviceId: string; display: number }
	| { type: 'nd-display-attached'; label: string; device: string; zoom: DisplayZoom }
	| { type: 'nd-display-visible'; visible: boolean }
	| { type: 'nd-display-zoom'; zoom: DisplayZoom }
	| { type: 'nd-display-closed' }
	| { type: 'nd-display-bye' };

/** The display's own window: as large as fits most of the screen, in the display's shape */
function windowFeatures(d: RDDisplay | undefined, onScreen: Rect | null): string {
	if (onScreen) {
		return `popup=yes,left=${onScreen.x},top=${onScreen.y},width=${onScreen.w},height=${onScreen.h}`;
	}
	const maxW = Math.round((window.screen?.availWidth || 1280) * 0.6);
	const maxH = Math.round((window.screen?.availHeight || 800) * 0.6);
	const ratio = d && d.width && d.height ? d.width / d.height : 16 / 9;
	const w = Math.min(maxW, Math.round(maxH * ratio));
	const h = Math.round(w / ratio) + 48;
	return `popup=yes,width=${w},height=${h}`;
}

interface ScreenDetailsLike {
	screens: { availLeft: number; availTop: number; availWidth: number; availHeight: number }[];
	currentScreen: unknown;
}

export interface ViewerOptions {
	data: ViewerData;
	/** Leave the viewer (no sessions left, or Back) */
	onleave: () => void;
}

export class Viewer {
	sessions = $state.raw<ViewerSession[]>([]);
	activeId = $state<string | null>(null);
	sheet = $state<SheetName | null>(null);
	controlsOpen = $state(false);
	keepOpen = $state(false);
	pickerOpen = $state(false);
	fullscreen = $state(false);
	httpWarning = $state(false);
	/** The window is wide enough for the arranged and grid layouts (not on touch tablets) */
	wide = $state(true);
	billingCheck = $state.raw<BillingCheck | null>(null);
	mobileMode = $state<'direct' | 'touchpad'>('direct');
	/** Off screen with its sessions connected (the console's other pages) */
	parked = $state(false);

	readonly data: ViewerData;
	readonly transport: Transport;
	readonly guest: boolean;
	readonly viewOnlyGuest: boolean;
	/** The server opened an RDP or VNC target, not a device */
	readonly remoteTarget: boolean;
	readonly userId: string | number | null;
	private onleave: () => void;
	private openFilesOnConnect = false;
	/** Devices the viewer signed in by itself since the page opened: a reconnect does not again */
	// eslint-disable-next-line svelte/prefer-svelte-reactivity -- nothing renders from it
	private autoSignedIn = new Set<string>();
	/** The clipboard between this computer and the active session's (clipboard-bridge.svelte.ts) */
	readonly clipboard: ClipboardBridge;

	constructor(opts: ViewerOptions) {
		this.data = opts.data;
		this.transport = transportOf(opts.data.capabilities);
		this.guest = isGuestMode(opts.data.capabilities);
		this.viewOnlyGuest = isViewOnlyGuest(opts.data.capabilities);
		this.remoteTarget = isRemoteTarget(opts.data.capabilities);
		this.userId = opts.data.user?.id ?? null;
		this.onleave = opts.onleave;
		this.clipboard = new ClipboardBridge({
			allowed: () => this.clipboardOff(),
			send: (entry) => this.sendClipboardEntry(entry),
			notify: (n) => this.clipboardNotice(n)
		});
	}

	get active(): ViewerSession | null {
		return this.sessions.find((s) => s.deviceId === this.activeId) ?? null;
	}

	/** Controls and Full screen show only while the active tab shows a picture */
	get chromeVisible(): boolean {
		return this.active?.view === 'streaming';
	}

	get controlsShown(): boolean {
		return this.chromeVisible && (this.controlsOpen || this.keepOpen);
	}

	/**
	 * The codecs the Display menu offers: without WebCodecs (plain HTTP) a
	 * RustDesk session plays only H.264, so it offers no codec it cannot play
	 */
	get codecChoices(): readonly string[] {
		if (this.transport !== 'rd' || videoDecodingSupported()) return CODECS;
		return CODECS.filter((c) => c === 'Auto' || c === 'H264');
	}

	isActive(s: ViewerSession): boolean {
		return s.deviceId === this.activeId;
	}

	connectedIds(): string[] {
		return this.sessions.map((s) => s.deviceId);
	}

	/* ── Opening and closing tabs ── */

	/** A new tab (or the existing one) for a device */
	open(deviceId: string, name = '', platform = '', opts: { filesOnConnect?: boolean } = {}): void {
		if (!peerAllowed(this.data.capabilities, this.data.deviceId, deviceId)) {
			console.warn('Guest access: refused session outside allowlist', deviceId);
			return;
		}
		const existing = this.sessions.find((s) => s.deviceId === deviceId);
		if (existing) {
			this.switchTo(deviceId);
			return;
		}
		if (opts.filesOnConnect) this.openFilesOnConnect = true;
		const s = new ViewerSession(deviceId, name, platform, loadPrefs(this.userId));
		s.display = loadDisplayPrefs(this.userId, deviceId);
		if (!videoDecodingSupported()) {
			const insecure =
				location.protocol === 'http:' &&
				location.hostname !== 'localhost' &&
				location.hostname !== '127.0.0.1';
			if (insecure) this.httpWarning = true;
		}
		this.sessions = [...this.sessions, s];
		this.switchTo(deviceId);
		void this.loadDetails(s);
	}

	/** The panel's canvas is in the page: start connecting */
	attach(s: ViewerSession, canvas: HTMLCanvasElement): void {
		s.canvas = canvas;
		if (s.started || s.closed) return;
		s.started = true;
		this.startConnecting(s);
		this.connectClient(s);
	}

	private connectClient(s: ViewerSession): void {
		const c = s.client;
		if (!c) return;
		try {
			c.renderer?.resize();
		} catch {
			/* not ready */
		}
		c.connect().catch((err: unknown) => {
			if (s.client === c) this.fail(s, err instanceof Error ? err.message : '');
		});
	}

	/** Name and system for tabs opened from the picker or a link */
	private async loadDetails(s: ViewerSession): Promise<void> {
		if (this.guest || this.remoteTarget) return;
		if (s.platform && s.deviceName) return;
		try {
			const d = await getDevice(s.deviceId);
			if (s.closed) return;
			const r = d as Record<string, unknown>;
			const name = String(r.display_name || r.hostname || '');
			if (!s.deviceName && name) s.deviceName = name;
			if (!s.platform) s.platform = String(r.platform || r.os || '');
			if (s.view === 'connecting' && s.stepId) this.setStep(s, s.stepId, true);
		} catch {
			/* the ID is enough to connect */
		}
	}

	switchTo(deviceId: string): void {
		const s = this.sessions.find((x) => x.deviceId === deviceId);
		if (!s) return;
		const previous = this.activeId;
		this.activeId = deviceId;
		// Files belong to one session; chat and info follow the tab
		if (previous !== deviceId && this.sheet === 'files') this.closeSheet();
		if (s.view === 'streaming') {
			void tick().then(() => {
				s.focus?.canvas();
				this.resize();
			});
			s.client?.setAudioMuted?.(s.audioMuted);
			this.applyPrefs(s);
		}
		this.syncMediaCapture();
		this.syncTouch();
		void this.refreshBilling(deviceId);
	}

	close(deviceId: string, opts: { suppressLeave?: boolean } = {}): void {
		const s = this.sessions.find((x) => x.deviceId === deviceId);
		if (!s) return;
		s.closed = true;
		s.chatBridge?.dispose();
		s.chatBridge = null;
		this.clearStepTimer(s);
		this.clearConsent(s);
		if (s.endTimer) clearTimeout(s.endTimer);
		if (s.signinTimer) clearTimeout(s.signinTimer);
		try {
			s.client?.disconnect();
		} catch {
			/* gone already */
		}
		const files = fileSheet();
		if (files?.isOpen() && files._session && (files._session as { deviceId?: string }).deviceId === deviceId)
			files.close();
		if (s.mediaRecorder?.state === 'recording') {
			try {
				s.mediaRecorder.stop();
			} catch {
				/* stopped */
			}
		}
		s.touch?.stop();
		this.closeDisplayWindows(s);
		const index = this.sessions.indexOf(s);
		this.sessions = this.sessions.filter((x) => x !== s);
		if (this.activeId === deviceId) {
			this.activeId = null;
			const next = this.sessions[index] ?? this.sessions[index - 1] ?? this.sessions[0];
			if (next) this.switchTo(next.deviceId);
			else {
				this.closeSheet();
				if (!opts.suppressLeave) this.onleave();
			}
		} else this.syncMediaCapture();
	}

	closeAll(): void {
		for (const s of [...this.sessions]) this.close(s.deviceId, { suppressLeave: true });
	}

	/** Disconnect every session when the page goes away (not into the back-forward cache) */
	teardown(): void {
		for (const s of this.sessions) {
			this.closeDisplayWindows(s);
			try {
				s.client?.disconnect();
			} catch {
				/* gone */
			}
			if (s.mediaRecorder?.state === 'recording') {
				try {
					s.mediaRecorder.stop();
				} catch {
					/* stopped */
				}
			}
		}
	}

	reconnect(s: ViewerSession): void {
		const old = s.client;
		s.client = null;
		try {
			old?.disconnect();
		} catch {
			/* gone */
		}
		s.cdapFallback = false;
		s.billing = null;
		s.autoplayBlocked = false;
		this.startConnecting(s);
		this.connectClient(s);
	}

	cancel(s: ViewerSession): void {
		s.endedBy = 'cancel';
		try {
			s.client?.disconnect();
		} catch {
			/* gone */
		}
		// Transports that do not report the state change still end here
		if (s.view !== 'ended') this.finishEnd(s);
	}

	/** Disconnect (controls row): the session ends by you */
	disconnect(): void {
		const s = this.active;
		if (!s?.client) return;
		s.endedBy = 'user';
		s.client.disconnect();
	}

	/* ── Connecting ── */

	private clientOptions(s: ViewerSession): ClientOptions {
		const p = s.prefs;
		return {
			deviceId: s.deviceId,
			serverPubKey: this.data.serverPubKey,
			connection: this.data.connection,
			myName: this.data.user?.name || 'NeoDesk Web',
			scaleMode: p.scale,
			fps: fpsForMode(p.fpsMode, p.quality),
			fpsMode: p.fpsMode,
			imageQuality: p.quality,
			qualityPreset: presetForQuality(p.quality),
			adaptiveQuality: p.fpsMode === 'adaptive' && p.adaptiveQuality !== false,
			preferCodec: p.codec || 'Auto',
			disableAudio: false,
			serverRecord: s.meshServerRecord,
			viewOnly: this.viewOnlyGuest
		};
	}

	/** A fresh client and the connecting screen at step one */
	private startConnecting(s: ViewerSession): void {
		s.state = 'connecting';
		s.everStreamed = false;
		s.connectedAt = null;
		s.endedBy = '';
		s.endInfo = null;
		s.lastError = '';
		s.encrypted = false;
		s.loginError = '';
		s.tfaError = '';
		// The device of the new session says again what it turns off
		s.allowed = { ...ALL_ALLOWED };
		s.viewOnlyBeforeKeyboardOff = null;
		// A new connection: its own password question and its own one sign-in attempt
		s.orgAnswerTried = false;
		s.usingOrgAnswer = false;
		s.peerFacts = null;
		s.signinOffer = { kind: 'none' };
		s.signinTried = false;
		s.signinDeferred = false;
		s.signinBusy = false;
		s.signinNote = '';
		s.signinDismissed = false;
		s.signinPending = '';
		if (s.signinTimer) clearTimeout(s.signinTimer);
		s.signinTimer = null;
		this.clearConsent(s);
		s.consentPerson = '';
		s.canUnblock = false;
		if (!s.canvas) return;
		s.client = createClient(s.canvas, this.clientOptions(s), this.transport);
		s.client.setBackgroundFps?.(s.prefs.backgroundFps || 1);
		// A watch-only guest link: no keyboard or mouse input, also not after a reconnect
		if (this.viewOnlyGuest) s.client.setViewOnly?.(true);
		// Disable clipboard is also a local flag in the client: a new client starts with it
		if (s.toggles.disableClipboard) s.client.setDisableClipboard?.(true);
		s.viewOnly = this.viewOnlyGuest || !!s.client.viewOnly;
		this.wire(s, s.client);
		this.attachTouch(s);
		this.showView(s, 'connecting');
		this.setStep(s, 'find', true);
	}

	private wire(s: ViewerSession, c: RemoteClient): void {
		// Events from a client this session has replaced (reconnect) or closed are ignored
		const live = () => s.client === c && !s.closed;
		const on = <A extends unknown[]>(event: string, fn: (...args: A) => void) =>
			c.on(event, ((...args: A) => {
				if (live()) fn(...args);
			}) as unknown as (...args: never[]) => void);

		on('state', (state: string) => {
			s.state = String(state);
			// CDAP, mesh, RDP and VNC streams pass through the server: never end-to-end
			s.encrypted = this.transport === 'rd' && c.encrypted === true;
			this.handleState(s, s.state);
		});
		on('log', (msg: unknown) => {
			if (s.view !== 'connecting') return;
			const step = stepFromLog(msg);
			if (step) this.setStep(s, step);
		});
		on('step', (step: unknown) => {
			if (s.view === 'connecting' && isStep(step)) this.setStep(s, step);
		});
		// The device's clipboard changed: to this computer's, for the active tab only
		on('clipboard_remote', (entry: ClipEntry) => {
			if (this.isActive(s)) this.clipboard.fromRemote(entry);
		});
		on('clipboard_too_large', () => this.clipboardNotice({ kind: 'from_remote_too_large' }));
		// The device takes no password: someone there accepts, the password prompt is not the way
		on('approval_wait', () => {
			this.awaitConsent(s, c, s.consentPerson);
		});
		on('error', (msg: unknown, meta?: CloseMeta) => {
			const text =
				msg && typeof msg === 'object'
					? String((msg as { message?: string }).message ?? '')
					: String(msg ?? '');
			this.fail(s, text, meta);
			if (meta?.cdapFallback) s.cdapFallback = true;
		});
		on('cdap_fallback_available', () => {
			s.cdapFallback = true;
		});
		on('disconnected', (reason: unknown, info?: CloseMeta) => {
			// A failure already on screen keeps its own message
			if (s.view === 'failed') return;
			s.endInfo = {
				reason: typeof reason === 'string' ? reason : '',
				meta: info ?? null,
				at: Date.now(),
				step: s.stepId
			};
			this.finishEnd(s);
			s.autoplayBlocked = false;
		});
		on('password_required', () => {
			this.setStep(s, 'signin');
			s.loginError = '';
			s.password = '';
			const local = secureStore()
				.loadPeerPassword(s.deviceId)
				.catch(() => '');
			// Only a signed-in account can have a quick support session to take a password from
			const none: DeviceSupport = { password: '', session: null };
			const handed = c.mockServer
				? c.mockServer.support()
				: this.data.mockRemote || !this.data.user
					? Promise.resolve(none)
					: takeDevicePassword(s.deviceId);
			void Promise.all([local, handed]).then(async ([mine, support]) => {
				if (s.client !== c || s.closed) return;
				// A quick support session's password, which the person's app handed to this
				// supporter, is sent at once and never stored in this browser
				if (support.password) {
					s.password = support.password;
					s.remember = false;
					s.rememberedPassword = '';
					this.authenticate(s);
					return;
				}
				// A quick support session without its password (the server lost it or gave it
				// out already): the person accepts on their screen, there is nothing to type (L551)
				if (support.session) {
					this.awaitConsent(s, c, support.session.name);
					void c.askWithoutPassword?.();
					return;
				}
				this.showView(s, 'password');
				// The device password an organisation stored comes next, once per connection:
				// the server answers the device's question, the password never reaches this page
				if (await this.signInWithOrgPassword(s, c)) return;
				if (s.client !== c || s.closed) return;
				if (mine) {
					s.password = mine;
					s.remember = true;
					s.rememberedPassword = mine;
				} else {
					// Nothing to sign in with: ask the device without a password while the form shows,
					// as the desktop client does, so someone there can accept (L515)
					void c.askWithoutPassword?.();
				}
				if (this.isActive(s)) void tick().then(() => s.focus?.password());
			});
		});
		on('login_error', (error: unknown) => {
			if (s.state === 'waiting_2fa' || s.view === 'tfa') {
				s.tfaError = String(error ?? '');
				s.tfaCode = '';
				if (this.isActive(s)) void tick().then(() => s.focus?.tfa());
				return;
			}
			// The device refused the password remembered in this browser: forget it
			if (s.rememberedPassword && s.submittedPassword === s.rememberedPassword) {
				s.rememberedPassword = '';
				s.remember = false;
				void secureStore()
					.clearPeerPassword(s.deviceId)
					.catch(() => {});
			}
			this.showView(s, 'password');
			s.loginError = loginErrorText(error);
			// The organisation's stored password was refused: say so, and ask (no second try)
			if (s.usingOrgAnswer) {
				s.usingOrgAnswer = false;
				s.loginError = tf(
					'remote.org_password_refused',
					'The device password saved for your organisation was not accepted. Enter the password.'
				);
			}
			s.password = '';
			if (this.isActive(s)) void tick().then(() => s.focus?.password());
		});
		on('certificate_required', (details?: { fingerprint?: string }) => {
			const fingerprint = String(details?.fingerprint ?? '');
			if (fingerprint) void this.trustCertificate(s, c, fingerprint);
		});
		on('2fa_error', (error: unknown) => {
			this.showView(s, 'tfa');
			s.tfaError = String(error || tf('remote.2fa_invalid', 'Invalid code'));
			s.tfaCode = '';
			if (this.isActive(s)) void tick().then(() => s.focus?.tfa());
		});
		on('2fa_required', () => {
			this.setStep(s, 'signin');
			this.showView(s, 'tfa');
			s.tfaError = '';
			s.tfaCode = '';
			if (this.isActive(s)) void tick().then(() => s.focus?.tfa());
		});
		on('login_success', () => {
			this.clearConsent(s);
			if (s.view === 'password' || s.view === 'tfa') this.showView(s, 'connecting');
			this.setStep(s, 'screen');
			const pw = s.password;
			if (s.remember && pw)
				void secureStore()
					.savePeerPassword(s.deviceId, pw)
					.catch(() => {});
			else if (!s.remember)
				void secureStore()
					.clearPeerPassword(s.deviceId)
					.catch(() => {});
		});
		on('session_start', () => {
			this.showView(s, 'streaming');
			this.resize();
			if (this.isActive(s)) this.applyPrefs(s);
			this.syncMediaCapture();
			if (this.isActive(s)) void tick().then(() => s.focus?.canvas());
			this.refreshMonitors(s);
			if (c.video)
				c.video.onAutoplayBlocked = () => {
					if (this.isActive(s)) s.autoplayBlocked = true;
				};
			if (this.openFilesOnConnect && this.isActive(s)) {
				this.openFilesOnConnect = false;
				void tick().then(() => this.openSheet('files'));
			}
			void this.refreshSignin(s);
		});
		on('stats', () => {
			s.statsTick++;
		});
		on('diagnostics', () => {
			/* window.NeoDesk.debugRelay logs them in the client */
		});
		on('latency', (rtt: unknown) => {
			s.latency = Number(rtt) || 0;
		});
		s.chatBridge?.dispose();
		s.chatBridge = sessionChat(s.deviceId, (text) => {
			s.client?.sendChat?.(text);
			this.addChat(s, text, 'sent');
		});
		on('chat', (text: unknown) => {
			this.addChat(s, String(text ?? ''), 'received');
			s.chatBridge?.received(String(text ?? ''));
		});
		on('permission', (info: unknown) => this.applyPermission(s, c, info));
		// CDAP: the agent lists its monitors; RustDesk: peer_info after sign-in
		on('monitors', () => this.refreshMonitors(s));
		on(
			'peer_info',
			(info?: {
				platform?: string;
				os?: string;
				username?: string;
				sasEnabled?: boolean;
				platformAdditions?: string;
			}) => {
				if (info && !s.platform && (info.platform || info.os)) s.platform = String(info.platform || info.os);
				// The sign-in's PeerInfo says who is signed in; a later one (displays) leaves it as it was
				if (info && !s.peerFacts) {
					s.peerFacts = {
						platform: String(info.platform || info.os || ''),
						username: String(info.username ?? ''),
						sasEnabled: info.sasEnabled === true,
						...signinAdditions(info.platformAdditions)
					};
				}
				this.refreshMonitors(s);
			}
		);
		// NeoDesk's lock screen state as it changes, and its answer to a sealed sign-in
		on('plugin_request', (id: unknown, content: unknown) => {
			const bytes = ArrayBuffer.isView(content)
				? new Uint8Array(content.buffer, content.byteOffset, content.byteLength)
				: new Uint8Array(0);
			if (id === 'nd.logon_state' && s.peerFacts) {
				const logonState = logonStateFromMessage(bytes);
				if (logonState && logonState !== s.peerFacts.logonState) {
					s.peerFacts = { ...s.peerFacts, logonState };
					void this.refreshSignin(s, { atStart: false });
				}
			} else if (id === 'nd.signin_result') {
				const answer = signinResultOf(bytes);
				if (!answer || !s.signinPending || (answer.nonce && answer.nonce !== s.signinPending)) return;
				this.finishSealed(s, answer.result);
			}
		});
		on('display_switched', () => this.refreshMonitors(s));
		on('virtual_display_toggled', () => this.refreshMonitors(s));
		// The device's identity or the encryption failed: the session ends, nothing more goes to it
		const insecure = (msg: unknown) => {
			console.warn('[Remote] Connection not secure:', msg);
			this.fail(s, `Handshake failed: ${String(msg ?? '')}`);
			try {
				c.disconnect();
			} catch {
				/* gone */
			}
		};
		on('signature_warning', insecure);
		on('encryption_warning', insecure);
	}

	/**
	 * The device turned a permission of this session on or off
	 * (PermissionInfo): without the keyboard the session is view only and
	 * sends no input, without the clipboard the client keeps it off, without
	 * file transfer the file sheet closes, without restart the command goes.
	 */
	private applyPermission(s: ViewerSession, c: RemoteClient, info: unknown): void {
		if (!info || typeof info !== 'object') return;
		const key = DEVICE_PERMISSIONS[Number(Reflect.get(info, 'permission') ?? 0)];
		if (!key) return;
		const on = Reflect.get(info, 'enabled') === true;
		if (s.allowed[key] === on) return;
		s.allowed = { ...s.allowed, [key]: on };
		if (key === 'keyboard') {
			if (!on) {
				s.viewOnlyBeforeKeyboardOff = s.viewOnly;
				s.viewOnly = true;
				c.setViewOnly?.(true);
			} else {
				s.viewOnly = this.viewOnlyGuest || !!s.viewOnlyBeforeKeyboardOff;
				s.viewOnlyBeforeKeyboardOff = null;
				c.setViewOnly?.(s.viewOnly);
			}
			this.syncTouch();
		} else if (key === 'clipboard') {
			c.setDisableClipboard?.(!on || s.toggles.disableClipboard);
		} else if (key === 'file' && !on && this.sheet === 'files' && this.isActive(s)) {
			this.closeSheet();
		}
	}

	private async trustCertificate(s: ViewerSession, c: RemoteClient, fingerprint: string): Promise<void> {
		const ok = await confirm({
			title: tf('remote.certificate_title', 'Untrusted certificate'),
			message: tf(
				'remote.certificate_confirm',
				'The certificate of {name} is not trusted. Trust the certificate with the fingerprint {fingerprint}?',
				{ name: s.label, fingerprint }
			),
			confirmLabel: tf('remote.certificate_trust', 'Trust certificate')
		});
		if (!ok || s.client !== c) return;
		try {
			await acceptCertificate(s.deviceId, fingerprint);
			c.disconnect();
			void c.connect();
		} catch (err) {
			this.fail(s, err instanceof Error ? err.message : 'Certificate was not trusted');
		}
	}

	private handleState(s: ViewerSession, state: string): void {
		switch (state) {
			case 'connecting':
				if (s.view !== 'connecting') this.showView(s, 'connecting');
				break;
			case 'waiting_password':
				// The client waits for the device's answer (a password request, or someone accepting)
				if (s.view === 'connecting') this.setStep(s, 'accept');
				break;
			case 'authenticating':
			case 'waiting_2fa':
				if (s.view === 'connecting') this.setStep(s, 'signin');
				break;
			case 'streaming':
				s.everStreamed = true;
				if (!s.connectedAt) s.connectedAt = Date.now();
				this.showView(s, 'streaming');
				this.syncMediaCapture();
				this.attachTouch(s);
				this.syncTouch();
				break;
			case 'disconnected':
			case 'error':
				if (state === 'error') {
					// The error event came first and already said what happened
					if (s.view !== 'failed') this.fail(s, s.lastError || '');
					break;
				}
				if (s.view === 'failed' || s.view === 'ended') break;
				// The client sends the reason right after this state; wait one tick for it
				if (s.endTimer) clearTimeout(s.endTimer);
				s.endTimer = setTimeout(() => this.finishEnd(s), 0);
				break;
		}
	}

	setStep(s: ViewerSession, id: StepId, force = false): void {
		if (!force && !stepAdvances(s.stepId, id)) return;
		s.stepId = id;
		if (s.view !== 'connecting') return;
		s.statusText = stepText(id, s.label, this.transport).now;
		s.slowHint = '';
		this.clearStepTimer(s);
		s.slowTimer = setTimeout(() => {
			if (s.closed || s.view !== 'connecting' || s.stepId !== id || s.awaitingConsent) return;
			s.slowHint = stepText(id, s.label, this.transport).slow;
		}, SLOW_AFTER_MS);
	}

	private clearStepTimer(s: ViewerSession): void {
		if (s.slowTimer) clearTimeout(s.slowTimer);
		s.slowTimer = null;
	}

	private showView(s: ViewerSession, view: View): void {
		s.view = view;
		if (view !== 'connecting') {
			this.clearStepTimer(s);
			s.slowHint = '';
		}
		if (view === 'connecting') {
			s.tryText = '';
			s.whyText = '';
			s.actions = [];
			s.outcomeKind = '';
			s.helpUrl = '';
		}
		if (view !== 'streaming' && this.isActive(s)) {
			if (!this.keepOpen) this.controlsOpen = false;
		}
	}

	/** Failed before or while connecting: what happened, what to try, details */
	fail(s: ViewerSession, reason: string, meta?: CloseMeta | null): void {
		if (s.closed) return;
		s.lastError = reason || s.lastError || '';
		s.endInfo = { reason: s.lastError, meta: meta ?? null, at: Date.now(), step: s.stepId };
		if (s.endedBy) {
			this.finishEnd(s);
			return;
		}
		this.clearConsent(s);
		// The other transports give no reason yet: their messages are still read (L557)
		if (this.transport !== 'rd' && !isReason(meta?.reason)) {
			this.showResult(s, classifyEnd(s.lastError, meta, { name: s.label, everStreamed: s.everStreamed }));
			return;
		}
		this.showFailure(s, isReason(meta?.reason) ? meta.reason : 'unknown');
	}

	/** What happened, why and what to do, from the reason; the message stays for Details */
	private showFailure(s: ViewerSession, reason: Reason, checked = false): void {
		const f = describeFailure(reason, {
			name: s.label,
			person: s.consentPerson || undefined,
			canUnblock: s.canUnblock
		});
		this.showResult(s, {
			kind: reason,
			text: f.what,
			tryText: f.todo,
			helpUrl: reason === 'https_required' ? HTTPS_DOCS_URL : undefined
		});
		s.whyText = f.why;
		s.actions = f.actions;
		// Once per failure: the server's answer is shown with checked set
		if (!checked && (reason === 'device_offline' || reason === 'device_unknown' || reason === 'no_answer'))
			void this.checkReach(s, reason);
	}

	/**
	 * The signal server says only that it can't find the computer, or the
	 * computer did not answer: the server knows why (revoked, blocked,
	 * banned, deleted, a refused key), and whether this account may unblock
	 * it (L550)
	 */
	private async checkReach(s: ViewerSession, shown: Reason): Promise<void> {
		const c = s.client;
		try {
			const r = c?.mockServer ? await c.mockServer.reach() : await getDeviceReach(s.deviceId);
			if (s.client !== c || s.closed || s.view !== 'failed' || s.outcomeKind !== shown) return;
			s.canUnblock = r.can_unblock === true;
			// "online" or the same reason: the first reason stays. A computer that did not
			// answer changes only for a cause that keeps it from answering
			const known = isReason(r.reason) && r.reason !== shown ? r.reason : null;
			const keeps = ['device_revoked', 'device_blocked', 'key_changed', 'device_banned'];
			const next = shown === 'no_answer' ? (known && keeps.includes(known) ? known : null) : known;
			if (next) this.showFailure(s, next, true);
		} catch {
			/* The first reason stays */
		}
	}

	/** Unblock this computer: its ID leaves the server's blocklist, with an audit entry */
	async unblock(s: ViewerSession): Promise<void> {
		if (s.unblocking) return;
		s.unblocking = true;
		try {
			if (s.client?.mockServer) await s.client.mockServer.unblock();
			else await unblockDevice(s.deviceId);
			s.actions = s.actions.filter((a) => a !== 'unblock');
			s.tryText = tf(
				'remote_viewer.unblocked_try',
				'{name} is unblocked. It checks in with the server within a minute; then reconnect.',
				{ name: s.label }
			);
			toast.success(tf('remote_viewer.unblocked', '{name} is unblocked.', { name: s.label }));
		} catch {
			toast.error(
				tf('remote_viewer.unblock_failed', 'Could not unblock {name}. Try again.', { name: s.label })
			);
		} finally {
			s.unblocking = false;
		}
	}

	/**
	 * The computer waits for the person there to accept (a quick support
	 * session, or a computer that accepts by click): say whom it waits for
	 * and what they see, and give up after CONSENT_WAIT_MS
	 */
	private awaitConsent(s: ViewerSession, c: RemoteClient, person: string): void {
		s.consentPerson = person;
		s.awaitingConsent = true;
		if (s.view !== 'connecting') this.showView(s, 'connecting');
		this.setStep(s, 'accept', true);
		s.slowHint = '';
		s.statusText = person
			? tf('remote_viewer.wait_person', 'Waiting for {person} to accept on their screen.', { person })
			: tf('remote_viewer.wait_someone', 'Waiting for someone at {name} to accept on their screen.', {
					name: s.label
				});
		s.waitHint = person
			? tf(
					'remote_viewer.wait_person_hint',
					'{person} sees a window that asks to let you connect, with Accept and Decline.',
					{ person }
				)
			: tf(
					'remote_viewer.wait_someone_hint',
					'The person there sees a window that asks to let you connect, with Accept and Decline.'
				);
		if (s.consentTimer) clearTimeout(s.consentTimer);
		s.consentTimer = setTimeout(() => {
			s.consentTimer = null;
			if (s.client !== c || s.closed || !s.awaitingConsent) return;
			s.lastError = 'Nobody accepted the connection in time';
			s.endInfo = { reason: s.lastError, meta: null, at: Date.now(), step: s.stepId };
			s.awaitingConsent = false;
			this.showFailure(s, 'consent_timeout');
			try {
				c.disconnect();
			} catch {
				/* gone */
			}
		}, c.mockServer?.consentWait?.() ?? CONSENT_WAIT_MS);
	}

	private clearConsent(s: ViewerSession): void {
		if (s.consentTimer) clearTimeout(s.consentTimer);
		s.consentTimer = null;
		s.awaitingConsent = false;
		s.waitHint = '';
	}

	/** The connection closed: ended by you, by the device, or failed on the way */
	private finishEnd(s: ViewerSession): void {
		if (s.closed) return;
		if (s.endTimer) {
			clearTimeout(s.endTimer);
			s.endTimer = null;
		}
		const end = s.endInfo ?? { reason: '', meta: null, at: Date.now(), step: s.stepId };
		s.endInfo = end;
		const awaitingConsent = s.awaitingConsent;
		this.clearConsent(s);
		if (this.transport === 'rd' && !s.endedBy) {
			const r = rdEndReason(end.meta, { everStreamed: s.everStreamed, awaitingConsent });
			if (r === 'peer') this.showResult(s, endedOnDevice(s.label));
			else this.showFailure(s, r);
		} else
			this.showResult(
				s,
				endOutcome(s.endedBy, end.reason, end.meta, { name: s.label, everStreamed: s.everStreamed })
			);
		if (s.everStreamed && this.data.workReports) void this.promptWorkReport(s);
	}

	private showResult(s: ViewerSession, info: Outcome): void {
		this.clearStepTimer(s);
		this.showView(s, info.ended ? 'ended' : 'failed');
		// The windows of its displays have nothing more to show; a reconnect brings the layout back
		this.closeDisplayWindows(s);
		s.displayPrefsLoaded = false;
		s.statusText = info.text;
		s.tryText = info.tryText;
		s.outcomeKind = info.kind;
		s.helpUrl = info.helpUrl ?? '';
		this.syncMediaCapture();
		// Keyboard users land on Reconnect; focus elsewhere in the page stays put
		const focused = document.activeElement;
		if (this.isActive(s) && (!focused || focused === document.body || focused === s.canvas))
			void tick().then(() => s.focus?.reconnect());
	}

	details(s: ViewerSession): [string, string][] {
		return detailRows(s.endInfo, {
			deviceId: s.deviceId,
			name: s.label,
			transport: this.transport,
			lastError: s.lastError,
			formatTime: (d) => formatDate(d, { second: '2-digit' })
		});
	}

	detailsText(s: ViewerSession): string {
		return detailsText(s.label, s.deviceId, s.statusText, this.details(s));
	}

	authenticate(s: ViewerSession): void {
		if (!s.password) {
			s.focus?.password();
			return;
		}
		s.loginError = '';
		s.submittedPassword = s.password;
		void s.client?.authenticate(s.password);
	}

	/**
	 * Signs in with the device password an organisation stored, when there
	 * is one this account may use: the client asks the server for the answer
	 * to the device's question (L454). Once per connection. true when the
	 * answer went out (login_success or login_error follows).
	 */
	private async signInWithOrgPassword(s: ViewerSession, c: RemoteClient): Promise<boolean> {
		if (s.orgAnswerTried || this.data.mockRemote || this.guest || this.transport !== 'rd') return false;
		if (typeof c.authenticateWith !== 'function' || s.client !== c || s.closed) return false;
		s.orgAnswerTried = true;
		const sent = await c.authenticateWith((salt, challenge) =>
			answerConnectPassword(s.deviceId, salt, challenge)
		);
		if (!sent || s.client !== c) return false;
		s.usingOrgAnswer = true;
		s.remember = false;
		s.rememberedPassword = '';
		s.loginError = '';
		return true;
	}

	/* ── Saved sign-in (L469) ── */

	/**
	 * What the viewer offers for the session's saved sign-in, from the
	 * server's status (loaded once per tab) and what the device said at the
	 * sign-in; with the switch on and nobody signed in, it signs in by itself.
	 */
	private async refreshSignin(s: ViewerSession, opts: { atStart?: boolean } = {}): Promise<void> {
		if (this.transport !== 'rd' || this.guest || this.remoteTarget || this.data.mockRemote || !this.data.user)
			return;
		const c = s.client;
		// Only over a session encrypted end to end with the device: the relay never sees what is typed
		if (!c || !s.encrypted || !s.peerFacts || s.view !== 'streaming') return;
		// Also on a computer that isn't Windows: saved details there get the bar that says why nothing is typed
		if (!s.signinLoaded) {
			s.signinLoaded = true;
			try {
				s.signin = await getSignin(s.deviceId);
			} catch {
				s.signin = null;
			}
		}
		if (s.client !== c || s.closed) return;
		const offer = signinOffer(s.peerFacts, s.signin, {
			tried: s.signinTried,
			autoDone: this.autoSignedIn.has(s.deviceId),
			atStart: opts.atStart !== false
		});
		s.signinOffer = offer;
		s.signinDeferred = false;
		if (offer.kind === 'auto') {
			// Nobody sees a parked session: it signs in by itself only once it is on screen again
			if (this.parked) {
				s.signinDeferred = true;
				return;
			}
			this.autoSignedIn.add(s.deviceId);
			await new Promise((r) => setTimeout(r, SIGNIN_AUTO_DELAY_MS));
			if (s.client !== c || s.closed) return;
			if (this.parked) {
				this.autoSignedIn.delete(s.deviceId);
				s.signinDeferred = true;
				return;
			}
			void this.signIn(s, { auto: true });
		}
	}

	/**
	 * Types the saved sign-in at the device's lock or sign-in screen: one
	 * attempt per connection, also when it failed at the device, so a wrong
	 * password can't lock the account. The details come from the server
	 * for this session, go to the keys and are dropped; nothing keeps them.
	 */
	async signIn(s: ViewerSession, opts: { auto?: boolean } = {}): Promise<void> {
		const c = s.client;
		if (this.parked) return;
		if (!c || !s.encrypted || s.signinTried || s.view !== 'streaming' || s.viewOnly || !s.allowed.keyboard)
			return;
		const offer = s.signinOffer;
		if (offer.kind !== 'button' && offer.kind !== 'auto') return;
		if (!c.relayUUID) return;
		if (offer.path === 'sealed' ? !c.sendPluginRequest : !c.sendCtrlAltDel || !c.sendText || !c.sendKeyPress)
			return;
		if (!opts.auto && offer.kind === 'button' && offer.confirm) {
			const ok = await confirm({
				title: tf('remote.signin_confirm_title', 'Sign in at the lock screen?'),
				message: tf(
					'remote.signin_confirm',
					'Someone is signed in on {name}. Use Sign in only while the screen shows the lock screen: on an open desktop the password would be typed into whatever has the focus.',
					{ name: s.label }
				),
				confirmLabel: tf('remote.signin', 'Sign in')
			});
			if (!ok || s.client !== c || s.signinTried || s.view !== 'streaming') return;
		}
		s.signinTried = true;
		s.signinBusy = true;
		s.signinDismissed = false;
		s.signinOffer = { kind: 'none' };
		s.signinNote = tf('remote.signin_busy', 'Signing in with the saved details…');
		if (offer.path === 'sealed') {
			await this.signInSealed(s, c);
			return;
		}
		// The details live in this call only: typed, then dropped with it
		let details: SigninDetails;
		try {
			details = await useSignin(s.deviceId, c.relayUUID);
		} catch (err) {
			s.signinBusy = false;
			s.signinNote = signinRefusalText(refusalCode(err));
			return;
		}
		const live = () => s.client === c && !s.closed && s.view === 'streaming';
		const typed = await typeSignin(
			{
				sendCtrlAltDel: () => c.sendCtrlAltDel?.(),
				sendText: (text) => c.sendText?.(text),
				sendKeyPress: (key) => c.sendKeyPress?.(key) ?? false
			},
			signinSteps(details.form),
			details,
			{ alive: live }
		);
		s.signinBusy = false;
		s.signinNote = typed
			? tf(
					'remote.signin_typed',
					'The saved details were typed. If Windows shows an error, sign in by hand: they are not tried again in this session.'
				)
			: tf('remote.signin_stopped', 'The session ended before the saved details were typed.');
		if (this.isActive(s)) s.focus?.canvas();
	}

	/**
	 * The sealed way: the server seals the details for the computer's own
	 * NeoDesk, the page forwards the bytes (Misc.plugin_request "nd.signin")
	 * and shows the computer's answer. The password never reaches the page.
	 */
	private async signInSealed(s: ViewerSession, c: RemoteClient): Promise<void> {
		let nonce = '';
		try {
			const sealed = await useSealedSignin(s.deviceId, c.relayUUID ?? '');
			if (s.client !== c || s.closed || !c.sendPluginRequest?.('nd.signin', sealed.packet)) {
				s.signinBusy = false;
				s.signinNote = tf('remote.signin_stopped', 'The session ended before the saved details were typed.');
				return;
			}
			nonce = sealed.nonce || 'sent';
		} catch (err) {
			s.signinBusy = false;
			s.signinNote = signinRefusalText(refusalCode(err));
			return;
		}
		s.signinPending = nonce;
		s.signinNote = tf('remote.signin_sealed_sent', 'Sent to NeoDesk on the computer…');
		s.signinTimer = setTimeout(() => {
			if (s.signinPending === nonce) this.finishSealed(s, '');
		}, SEALED_ANSWER_MS);
	}

	private finishSealed(s: ViewerSession, result: string): void {
		if (s.signinTimer) clearTimeout(s.signinTimer);
		s.signinTimer = null;
		s.signinPending = '';
		s.signinBusy = false;
		s.signinDismissed = false;
		s.signinNote = sealedResultText(result);
		if (this.isActive(s)) s.focus?.canvas();
	}

	dismissSignin(s: ViewerSession): void {
		s.signinDismissed = true;
		if (this.isActive(s)) s.focus?.canvas();
	}

	verifyCode(s: ViewerSession): void {
		const code = s.tfaCode.replace(/\s/g, '');
		if (code.length !== 6) {
			s.tfaError = tf('remote.2fa_invalid', 'Enter a valid 6-digit code');
			s.focus?.tfa();
			return;
		}
		s.tfaError = '';
		s.client?.submit2FA?.(code);
	}

	/* ── Work report after a billed session ── */

	private async promptWorkReport(s: ViewerSession): Promise<void> {
		await new Promise((r) => setTimeout(r, 1200));
		let pending: PendingBillingSession | null = null;
		for (let i = 0; i < 5 && !s.closed; i++) {
			pending = await getPendingBillingSession(s.deviceId);
			if (pending) break;
			await new Promise((r) => setTimeout(r, 800));
		}
		if (!pending || s.closed || s.view === 'streaming' || s.view === 'connecting') return;
		s.billing = { session: pending, summary: '', error: '', busy: false };
		if (this.isActive(s)) void tick().then(() => s.focus?.summary());
	}

	skipWorkReport(s: ViewerSession): void {
		s.billing = null;
	}

	async submitWorkReport(s: ViewerSession): Promise<void> {
		const pane = s.billing;
		if (!pane || pane.busy) return;
		const summary = pane.summary.trim();
		if (!summary) {
			s.billing = { ...pane, error: tf('commercialization.report.summary_required', 'Summary is required') };
			return;
		}
		s.billing = { ...pane, busy: true, error: '' };
		try {
			await submitWorkReport(pane.session.id, summary);
			s.billing = null;
			toast.success(tf('commercialization.report.saved', 'Work report saved'));
		} catch (err) {
			s.billing = { ...pane, busy: false, error: err instanceof Error ? err.message : 'Error' };
		}
	}

	setReportSummary(s: ViewerSession, summary: string): void {
		if (s.billing) s.billing = { ...s.billing, summary };
	}

	async refreshBilling(deviceId: string): Promise<void> {
		if (this.guest) return;
		const check = await getBillingCheck(deviceId);
		if (this.activeId === deviceId) this.billingCheck = check;
	}

	/** Work report (controls row): the form opens when the session ends */
	markReportIntent(): void {
		const s = this.active;
		if (!s) return;
		try {
			sessionStorage.setItem(`nd-billing-report-intent:${s.deviceId}`, '1');
		} catch {
			/* storage blocked */
		}
		toast.info(tf('commercialization.report.intent_hint', 'Report form will open when the session ends'));
	}

	/* ── Controls ── */

	private withClient(fn: (c: RemoteClient, s: ViewerSession) => void): void {
		const s = this.active;
		if (s?.client) fn(s.client, s);
	}

	/** The client of the active tab for a control that sends input; none for a watch-only guest */
	private inputClient(): RemoteClient | null {
		if (this.viewOnlyGuest || this.active?.allowed.keyboard === false) return null;
		return this.active?.client ?? null;
	}

	focusScreen(): void {
		const s = this.active;
		if (s?.view === 'streaming') s.focus?.canvas();
	}

	ctrlAltDel(): void {
		this.inputClient()?.sendCtrlAltDel?.();
		this.focusScreen();
	}

	lockScreen(): void {
		this.inputClient()?.sendLockScreen?.();
		this.focusScreen();
	}

	async restartRemote(): Promise<void> {
		const s = this.active;
		if (!s?.client || !this.inputClient() || !s.allowed.restart) return;
		const ok = await confirm({
			title: tf('remote.restart_remote', 'Restart remote device'),
			message: tf('remote.confirm_restart', 'Restart the remote device?'),
			confirmLabel: tf('remote.restart_remote', 'Restart remote device'),
			danger: true
		});
		if (ok && s.client) s.client.sendRestartRemoteDevice?.();
	}

	refreshScreen(): void {
		this.withClient((c) => c.sendRefreshScreen?.());
		this.focusScreen();
	}

	resetKeyboard(): void {
		this.withClient((c) => {
			if (typeof c.resetKeyboard === 'function') c.resetKeyboard();
			else c.input?.resetKeyboard?.();
		});
		toast.success(tf('remote.reset_keyboard_done', 'Keyboard state reset on remote'));
		this.focusScreen();
	}

	/**
	 * Paste clipboard (Actions menu): reads this computer's clipboard (the
	 * browser may ask first) and sends it. Ctrl+V in the viewer does the same
	 * without the menu.
	 */
	async pasteClipboard(): Promise<void> {
		const s = this.active;
		if (!s?.client) return;
		const off = this.clipboardOff();
		if (off === 'view_only') {
			toast.warning(tf('remote.view_only', 'View only mode'));
			return;
		}
		if (off === 'device') {
			toast.warning(tf('remote.feature_off', 'This is turned off for your account on this server.'));
			return;
		}
		if (off) return;
		if (!(await this.clipboard.requestPermission()))
			toast.error(
				tf(
					'remote.clipboard_read_refused',
					"The browser didn't let the page read the clipboard. Press Ctrl+V in the viewer instead."
				)
			);
	}

	/** Type text (controls row): keystrokes for login screens and password fields; true when sent */
	typeText(text: string): boolean {
		const s = this.active;
		if (!s?.client) return false;
		if (s.client.viewOnly || this.viewOnlyGuest) {
			toast.warning(tf('remote.view_only', 'View only mode'));
			return false;
		}
		if (!text) {
			toast.warning(tf('remote.type_text_empty', 'Enter text to type on the remote'));
			return false;
		}
		const unavailable = tf('remote.type_text_unavailable', 'Type text is not available on this transport');
		if (typeof s.client.sendText !== 'function' || s.client.sendText(text) === false) {
			toast.warning(unavailable);
			return false;
		}
		toast.success(tf('remote.type_text_done', 'Text sent to remote keyboard'));
		return true;
	}

	setToggle(key: keyof Toggles, on: boolean): void {
		const s = this.active;
		if (!s) return;
		if (key === 'blockInput' && this.viewOnlyGuest) return;
		s.toggles = { ...s.toggles, [key]: on };
		const c = s.client;
		if (!c) return;
		if (key === 'blockInput') c.setBlockInput?.(on);
		else if (key === 'showCursor') c.setShowRemoteCursor?.(on);
		else if (key === 'lockAfter') c.setLockAfterSession?.(on);
		else if (key === 'privacy') c.setPrivacyMode?.(on);
		// The clipboard stays off while the device has it off
		else c.setDisableClipboard?.(on || !s.allowed.clipboard);
	}

	/** Image quality, frame rate, scaling, keyboard mode, codec: the session and the saved defaults */
	setPref<K extends keyof ViewerPrefs>(key: K, value: ViewerPrefs[K]): void {
		const s = this.active;
		if (!s) return;
		s.prefs = { ...s.prefs, [key]: value };
		savePrefs(this.userId, s.prefs);
		const c = s.client;
		if (!c) return;
		if (key === 'quality') c.setQualityPreset?.(presetForQuality(String(value)));
		else if (key === 'fpsMode') c.setFpsMode?.(String(value));
		else if (key === 'scale') c.setScaleMode?.(String(value));
		else if (key === 'codec') c.setCodec?.(String(value));
		else if (key === 'keyboardMode') c.setKeyboardMode?.(String(value));
	}

	private applyPrefs(s: ViewerSession): void {
		const c = s.client;
		if (!c) return;
		const p = s.prefs;
		c.setBackgroundFps?.(p.backgroundFps || 1);
		if (c._state !== 'streaming' && c.state !== 'streaming') return;
		c.setQualityPreset?.(presetForQuality(p.quality));
		c.setFpsMode?.(p.fpsMode || 'adaptive');
		c.setScaleMode?.(p.scale || 'fit');
		if (p.codec && p.codec !== 'Auto') c.setCodec?.(p.codec);
		if (p.keyboardMode) c.setKeyboardMode?.(p.keyboardMode);
	}

	toggleAudio(): void {
		const s = this.active;
		if (!s) return;
		s.audioMuted = !s.audioMuted;
		s.client?.setAudioMuted?.(s.audioMuted);
	}

	toggleViewOnly(): void {
		const s = this.active;
		if (!s?.client || this.viewOnlyGuest || !s.allowed.keyboard) return;
		s.viewOnly = !s.viewOnly;
		s.client.setViewOnly?.(s.viewOnly);
		this.syncTouch();
	}

	toggleRecording(): void {
		const s = this.active;
		if (!s) return;
		if (s.mediaRecorder?.state === 'recording') {
			s.mediaRecorder.stop();
			s.recording = false;
			return;
		}
		if (this.transport === 'mesh') {
			s.meshServerRecord = !s.meshServerRecord;
			toast.info(
				s.meshServerRecord
					? tf('mesh.recording_server_on', 'Server recording enabled — reconnecting…')
					: tf('mesh.recording_server_off', 'Server recording disabled — reconnecting…')
			);
			this.reconnect(s);
			return;
		}
		try {
			// In the arranged and grid layouts, the display worked on
			const canvas = (this.tilesShown(s) ? s.client?.displayCanvas?.(s.activeDisplay) : null) ?? s.canvas;
			if (!canvas) return;
			const stream = canvas.captureStream(30);
			s.recordedChunks = [];
			const mime = ['video/webm;codecs=vp9', 'video/webm;codecs=vp8', 'video/webm'].find((m) =>
				MediaRecorder.isTypeSupported(m)
			);
			const rec = new MediaRecorder(stream, mime ? { mimeType: mime } : {});
			rec.ondataavailable = (e) => {
				if (e.data.size > 0) s.recordedChunks.push(e.data);
			};
			rec.onstop = () => {
				const blob = new Blob(s.recordedChunks, { type: mime || 'video/webm' });
				const url = URL.createObjectURL(blob);
				const a = document.createElement('a');
				a.href = url;
				a.download = `neodesk-${s.deviceId}-${Date.now()}.webm`;
				a.click();
				URL.revokeObjectURL(url);
				s.mediaRecorder = null;
				s.recording = false;
			};
			rec.start(1000);
			s.mediaRecorder = rec;
			s.recording = true;
		} catch (err) {
			console.warn('[Remote] Recording not supported:', err);
		}
	}

	/* ── Screens ── */

	refreshMonitors(s: ViewerSession): void {
		const c = s.client;
		if (!c) return;
		try {
			s.monitors = c.getMonitors?.() ?? [];
		} catch {
			s.monitors = [];
		}
		try {
			s.virtualDisplays = c.getVirtualDisplaySupport?.() ?? { supported: false };
		} catch {
			s.virtualDisplays = { supported: false };
		}
		try {
			s.windowsSessions = c.getWindowsSessions?.() ?? { sessions: [], currentSid: 0 };
		} catch {
			s.windowsSessions = { sessions: [], currentSid: 0 };
		}
		s.displays = c.getDisplays?.() ?? [];
		s.together = !!c.canShowDisplaysTogether?.() && s.displays.length > 1;
		if (!s.displayPrefsLoaded && s.together && s.view === 'streaming') {
			s.displayPrefsLoaded = true;
			this.restoreDisplays(s);
		}
	}

	/** Show a display in the single layout (in the arranged and grid layouts: work on it) */
	switchMonitor(idx: number): void {
		const s = this.active;
		if (!s?.client) return;
		if (s.popouts.includes(idx)) {
			s.windows.get(idx)?.focus();
			return;
		}
		if (this.tilesShown(s)) {
			this.focusDisplay(s, idx);
			return;
		}
		s.client.switchMonitor?.(idx);
		this.refreshMonitors(s);
		s.monitors = s.monitors.map((m) => ({ ...m, current: m.idx === idx }));
		s.activeDisplay = idx;
		if (s.together) this.saveDisplays(s, { display: idx });
	}

	/* ── Several displays at once (RustDesk 1.2.4 and later, the mock) ── */

	/** The layout in use: the arranged and grid layouts need a device that streams several displays and a wide window */
	layoutOf(s: ViewerSession): DisplayLayout {
		return s.together && this.wide ? s.display.layout : 'single';
	}

	/** The displays show as tiles (arranged or grid) */
	tilesShown(s: ViewerSession): boolean {
		return s.view === 'streaming' && this.layoutOf(s) !== 'single';
	}

	/** The display the single layout shows */
	singleDisplay(s: ViewerSession): number {
		const shown = s.monitors.find((m) => m.current)?.idx ?? 0;
		const want = s.display.display ?? shown;
		const free = (i: number) => !s.popouts.includes(i) && i < Math.max(1, s.displays.length);
		if (free(want)) return want;
		return s.displays.find((d) => free(d.idx))?.idx ?? want;
	}

	private saveDisplays(s: ViewerSession, change: Partial<DisplayPrefs>): void {
		s.display = saveDisplayPrefs(this.userId, s.deviceId, { ...s.display, ...change });
	}

	/** The session streams: bring back the layout, the display and the background setting used last */
	private restoreDisplays(s: ViewerSession): void {
		s.client?.setBackgroundDisplays?.(s.display.background);
		const current = s.monitors.find((m) => m.current)?.idx ?? 0;
		s.activeDisplay = s.display.display ?? current;
		this.syncLayout(s);
	}

	/** Tell the client what the session's own canvas shows (nothing while the displays show as tiles) */
	private syncLayout(s: ViewerSession): void {
		const c = s.client;
		if (!c || !s.together) return;
		if (this.layoutOf(s) !== 'single') {
			c.setMainDisplay?.(null);
			if (!s.displays.some((d) => d.idx === s.activeDisplay && !s.popouts.includes(d.idx))) {
				s.activeDisplay = s.displays.find((d) => !s.popouts.includes(d.idx))?.idx ?? 0;
			}
			for (const d of s.displays) c.setDisplayBackground?.(d.idx, d.idx !== s.activeDisplay);
			return;
		}
		const idx = this.singleDisplay(s);
		const shown = s.monitors.find((m) => m.current)?.idx;
		c.setMainDisplay?.(idx);
		if (shown !== idx) {
			c.switchMonitor?.(idx);
			this.refreshMonitors(s);
			s.monitors = s.monitors.map((m) => ({ ...m, current: m.idx === idx }));
		}
		s.activeDisplay = idx;
	}

	setLayout(layout: DisplayLayout): void {
		const s = this.active;
		if (!s?.together || !DISPLAY_LAYOUTS.includes(layout)) return;
		this.saveDisplays(s, { layout });
		this.syncLayout(s);
		this.resize();
	}

	/** The next layout (Ctrl+Alt+Shift+L) */
	nextLayout(): void {
		const s = this.active;
		if (!s?.together || !this.wide) return;
		const i = DISPLAY_LAYOUTS.indexOf(s.display.layout);
		this.setLayout(DISPLAY_LAYOUTS[(i + 1) % DISPLAY_LAYOUTS.length] ?? 'single');
	}

	/** The window became wide enough for tiles, or too narrow */
	setWide(wide: boolean): void {
		if (this.wide === wide) return;
		this.wide = wide;
		for (const s of this.sessions) if (s.view === 'streaming') this.syncLayout(s);
	}

	/** A tile's canvas is in the page: the display shows there */
	attachTile(s: ViewerSession, idx: number, canvas: HTMLCanvasElement): void {
		const c = s.client;
		if (!c?.showDisplay) return;
		c.showDisplay(idx, canvas, { zoom: s.display.zoom[idx] ?? 'fit' });
		c.setDisplayBackground?.(idx, idx !== s.activeDisplay);
		c.setDisplayVisible?.(idx, document.visibilityState !== 'hidden');
	}

	detachTile(s: ViewerSession, idx: number): void {
		if (!s.popouts.includes(idx)) s.client?.hideDisplay?.(idx);
	}

	/** Work on a display: it streams live, the other tiles follow the background setting */
	focusDisplay(s: ViewerSession, idx: number): void {
		if (s.activeDisplay === idx) return;
		s.activeDisplay = idx;
		if (!this.tilesShown(s)) return;
		for (const d of s.displays) {
			if (!s.popouts.includes(d.idx)) s.client?.setDisplayBackground?.(d.idx, d.idx !== idx);
		}
	}

	setDisplayZoom(s: ViewerSession, idx: number, zoom: DisplayZoom): void {
		this.saveDisplays(s, { zoom: { ...s.display.zoom, [idx]: zoom } });
		s.client?.setDisplayZoom?.(idx, zoom);
		s.windows
			.get(idx)
			?.postMessage({ type: 'nd-display-zoom', zoom } satisfies DisplayWindowMessage, location.origin);
	}

	/** Fitted or at its own size, for the display worked on (Ctrl+Alt+Shift+Z) */
	toggleZoom(): void {
		const s = this.active;
		if (!s || !this.tilesShown(s)) return;
		const idx = s.activeDisplay;
		this.setDisplayZoom(s, idx, (s.display.zoom[idx] ?? 'fit') === 'fit' ? '1:1' : 'fit');
	}

	setBackgroundDisplays(mode: BackgroundMode): void {
		const s = this.active;
		if (!s) return;
		this.saveDisplays(s, { background: mode });
		s.client?.setBackgroundDisplays?.(mode);
	}

	/** The page was hidden (minimised, another tab) or shows again: its displays pause meanwhile */
	setPageVisible(visible: boolean): void {
		for (const s of this.sessions) {
			const c = s.client;
			if (!c || !s.together) continue;
			// A recording keeps its picture coming
			c.setMainVisible?.(visible || s.recording);
			for (const d of s.displays) {
				if (!s.popouts.includes(d.idx)) c.setDisplayVisible?.(d.idx, visible || s.recording);
			}
		}
	}

	/** Checks for display windows closed without a word, while there are any */
	private windowTimer: ReturnType<typeof setInterval> | null = null;

	/**
	 * Open a display in a window of its own. The window loads the viewer page
	 * in its display mode and says when its canvas is ready; this window keeps
	 * the connection and draws the display's pictures into that canvas.
	 * False when the browser blocked the window.
	 */
	popOut(s: ViewerSession, idx: number, onScreen: Rect | null = null): boolean {
		if (!s.together || !s.client?.showDisplay) return false;
		const open = s.windows.get(idx);
		if (open && !open.closed) {
			open.focus();
			return true;
		}
		const url = link(`/remote/${encodeURIComponent(s.deviceId)}?popout=${idx}`);
		const name = `nd-display-${s.deviceId}-${idx}`;
		const w = window.open(url, name, windowFeatures(s.displays[idx], onScreen));
		if (!w) return false;
		s.windows.set(idx, w);
		s.popouts = [...s.popouts, idx];
		s.client.hideDisplay?.(idx);
		this.syncLayout(s);
		this.windowTimer ??= setInterval(() => this.checkWindows(), 1000);
		return true;
	}

	/** Pop out the display worked on (Ctrl+Alt+Shift+P) */
	popOutActive(): void {
		const s = this.active;
		if (!s?.together) return;
		const idx = this.tilesShown(s) ? s.activeDisplay : this.singleDisplay(s);
		if (!this.popOut(s, idx)) this.windowBlocked();
	}

	windowBlocked(): void {
		toast.warning(
			tf(
				'remote_viewer.window_blocked',
				'The browser blocked the new window. Allow pop-ups for this site, then try again.'
			)
		);
	}

	/** A display's window closed: the display comes back to this window */
	returnDisplay(s: ViewerSession, idx: number): void {
		if (!s.popouts.includes(idx)) return;
		s.windows.delete(idx);
		s.popouts = s.popouts.filter((i) => i !== idx);
		s.client?.hideDisplay?.(idx);
		this.syncLayout(s);
		if (!this.sessions.some((x) => x.windows.size) && this.windowTimer) {
			clearInterval(this.windowTimer);
			this.windowTimer = null;
		}
	}

	/** Close every display window and bring the displays back */
	returnAllDisplays(s: ViewerSession): void {
		for (const [idx, w] of [...s.windows]) {
			try {
				w.close();
			} catch {
				/* gone */
			}
			this.returnDisplay(s, idx);
		}
	}

	private closeDisplayWindows(s: ViewerSession): void {
		for (const w of s.windows.values()) {
			try {
				w.postMessage({ type: 'nd-display-bye' } satisfies DisplayWindowMessage, location.origin);
				w.close();
			} catch {
				/* gone */
			}
		}
		s.windows.clear();
		s.popouts = [];
	}

	/** Windows closed without saying so (the browser ended them) */
	private checkWindows(): void {
		for (const s of this.sessions) {
			for (const [idx, w] of [...s.windows]) if (w.closed) this.returnDisplay(s, idx);
		}
	}

	/** A message from a display's window: ready, shown or hidden, zoom, closed */
	onWindowMessage(ev: MessageEvent<unknown>): void {
		if (ev.origin !== location.origin || !ev.data || typeof ev.data !== 'object') return;
		const msg = ev.data as DisplayWindowMessage;
		for (const s of this.sessions) {
			for (const [idx, w] of s.windows) {
				if (w !== ev.source) continue;
				if (msg.type === 'nd-display-ready') this.attachWindow(s, idx, w);
				else if (msg.type === 'nd-display-visible') s.client?.setDisplayVisible?.(idx, !!msg.visible);
				else if (msg.type === 'nd-display-zoom' && (msg.zoom === 'fit' || msg.zoom === '1:1')) {
					this.saveDisplays(s, { zoom: { ...s.display.zoom, [idx]: msg.zoom } });
					s.client?.setDisplayZoom?.(idx, msg.zoom);
				} else if (msg.type === 'nd-display-closed') this.returnDisplay(s, idx);
				return;
			}
		}
	}

	/** The display's window has its canvas: draw the display there */
	private attachWindow(s: ViewerSession, idx: number, w: Window): void {
		let canvas: HTMLCanvasElement | null;
		try {
			canvas = w.document.getElementById('nd-popout-canvas') as HTMLCanvasElement | null;
		} catch {
			// The window navigated away from this origin
			canvas = null;
		}
		if (!canvas || !s.client?.showDisplay) return;
		const zoom = s.display.zoom[idx] ?? 'fit';
		s.client.showDisplay(idx, canvas, { win: w, keyboard: true, zoom });
		s.client.setDisplayVisible?.(idx, w.document.visibilityState !== 'hidden');
		const d = s.displays[idx];
		const label = tf('remote_viewer.display_n', 'Display {n}', { n: idx + 1 });
		w.postMessage(
			{
				type: 'nd-display-attached',
				label: d?.name && d.name !== label ? `${label} (${d.name})` : label,
				device: s.label,
				zoom
			} satisfies DisplayWindowMessage,
			location.origin
		);
	}

	/** This browser has more than one screen (Window Management API) */
	get screensExtended(): boolean {
		return (window.screen as Screen & { isExtended?: boolean }).isExtended === true;
	}

	/**
	 * One remote display on each local screen (Window Management API): this
	 * window keeps the display of its own screen, each other screen gets a
	 * window of its own. Without the API, each display but one opens in a
	 * window the user can move.
	 */
	async spreadDisplays(): Promise<void> {
		const s = this.active;
		if (!s?.together) return;
		const wm = window as Window & { getScreenDetails?: () => Promise<ScreenDetailsLike> };
		let details: ScreenDetailsLike | null = null;
		if (wm.getScreenDetails) {
			try {
				details = await wm.getScreenDetails();
			} catch {
				details = null;
			}
		}
		const free = s.displays.filter((d) => !s.popouts.includes(d.idx));
		// Firefox and Safari can't place windows on other screens: the person moves them
		if (!wm.getScreenDetails)
			toast.info(
				tf(
					'remote_viewer.no_screen_placement',
					"This browser can't put windows on your other screens. Drag each display's window to the screen you want it on."
				)
			);
		if (!details || details.screens.length < 2) {
			// Each display but the one shown here in a window of its own
			const keep = this.tilesShown(s) ? s.activeDisplay : this.singleDisplay(s);
			for (const d of free) {
				if (d.idx !== keep && !this.popOut(s, d.idx)) {
					this.windowBlocked();
					return;
				}
			}
			return;
		}
		const screens = details.screens.map((sc) => ({
			x: sc.availLeft,
			y: sc.availTop,
			w: sc.availWidth,
			h: sc.availHeight
		}));
		const current = details.screens.findIndex((sc) => sc === details.currentScreen);
		const plan = planSpread(free, screens, current);
		for (const { display, screen } of plan.windows) {
			if (!this.popOut(s, display, screens[screen] ?? null)) {
				this.windowBlocked();
				return;
			}
		}
		if (plan.main >= 0) {
			this.saveDisplays(s, { display: plan.main, layout: plan.left.length ? s.display.layout : 'single' });
			this.syncLayout(s);
		}
	}

	toggleVirtualDisplay(index: number, on: boolean): void {
		const s = this.active;
		s?.client?.toggleVirtualDisplay?.(index, on);
		if (s) this.refreshMonitors(s);
	}

	selectWindowsSession(sid: number): void {
		const s = this.active;
		if (!s?.client) return;
		s.client.selectWindowsSession?.(sid);
		s.windowsSessions = { ...s.windowsSessions, currentSid: sid };
	}

	/* ── Side sheets: chat, file transfer, session info (one at a time, beside the screen) ── */

	openSheet(name: SheetName): void {
		const s = this.active;
		if (!s) return;
		const files = fileSheet();
		if (name !== 'files' && files?.isOpen()) files.close();
		if (name === 'files') {
			if (!s.allowed.file) {
				toast.warning(tf('remote.feature_off', 'This is turned off for your account on this server.'));
				return;
			}
			if (!s.client?.fileTransfer && this.transport !== 'cdap') {
				toast.warning(
					tf('remote.file_transfer_unavailable', 'File transfer is not available for this session.')
				);
				return;
			}
			if (!files) return;
			this.sheet = 'files';
			files.open(s);
			files._el?.querySelector<HTMLElement>('.ft-modal-close')?.focus();
		} else this.sheet = name;
		this.resize();
	}

	closeSheet(): void {
		const was = this.sheet;
		this.sheet = null;
		if (was === 'files') {
			const files = fileSheet();
			if (files?.isOpen()) files.close();
		}
		this.resize();
	}

	toggleSheet(name: SheetName): void {
		if (this.sheet === name) this.closeSheet();
		else this.openSheet(name);
	}

	/** The file sheet closed itself (its × or Escape) */
	onFileSheetClosed(): void {
		if (this.sheet === 'files') {
			this.sheet = null;
			this.resize();
		}
	}

	sendChat(s: ViewerSession, text: string): boolean {
		const msg = text.trim();
		if (!msg || !s.client?.sendChat) return false;
		// Stored in the device's conversation, which passes it into the session
		if (s.chatBridge?.send(msg)) return true;
		s.client.sendChat(msg);
		this.addChat(s, msg, 'sent');
		return true;
	}

	private addChat(s: ViewerSession, text: string, type: 'sent' | 'received'): void {
		s.chat = [...s.chat, { id: ++chatSeq, text, type }];
	}

	/* ── Screen size, input, clipboard ── */

	resize(): void {
		const s = this.active;
		if (!s?.client) return;
		requestAnimationFrame(() => {
			try {
				s.client?.renderer?.resize();
				s.client?.resizeDisplays?.();
			} catch {
				/* not ready */
			}
		});
	}

	resizeAll(): void {
		for (const s of this.sessions) {
			try {
				s.client?.renderer?.resize();
			} catch {
				/* not ready */
			}
		}
	}

	/** Keyboard, mouse, inbound clipboard and audio only for the active streaming tab, and not while parked */
	syncMediaCapture(): void {
		for (const s of this.sessions) {
			s.client?.setSessionActive?.(s.deviceId === this.activeId && s.state === 'streaming' && !this.parked);
		}
	}

	/**
	 * The viewer leaves the screen while its sessions stay connected (the
	 * console shows another page): no session takes the keyboard, the mouse,
	 * the clipboard or plays sound, and all stream at the background rate.
	 * Back on screen, the active one takes them again.
	 */
	setParked(parked: boolean): void {
		if (this.parked === parked) return;
		this.parked = parked;
		this.syncMediaCapture();
		this.syncTouch();
		if (!parked) {
			this.resize();
			void tick().then(() => this.focusScreen());
			for (const s of this.sessions) {
				if (!s.signinDeferred) continue;
				s.signinDeferred = false;
				void this.refreshSignin(s);
			}
		}
	}

	/** Tablets: touch input on the screen (RustDesk transport only) */
	private attachTouch(s: ViewerSession): void {
		if (!isMobileViewer() || !s.client || !s.canvas) return;
		if (typeof s.client.input?.sendMessage !== 'function') return;
		const Touch = touchInput();
		if (!Touch) return;
		const renderer = s.client.renderer;
		if (!s.touch && isTouchRenderer(renderer))
			s.touch = new Touch(s.canvas, renderer, (msg) => {
				s.client?.input?.sendMessage?.(msg);
			});
		this.syncTouch();
	}

	syncTouch(): void {
		for (const s of this.sessions) {
			if (!s.touch) continue;
			if (s.deviceId === this.activeId && s.state === 'streaming' && !s.client?.viewOnly && !this.parked) {
				s.touch.setMode(this.mobileMode);
				s.touch.start();
			} else s.touch.stop();
		}
	}

	setMobileMode(mode: 'direct' | 'touchpad'): void {
		this.mobileMode = mode;
		this.syncTouch();
	}

	/** Special keys of the tablet toolbar, as key presses on the remote */
	sendSpecialKey(key: 'Meta' | 'Alt+Tab' | 'PrintScreen' | 'Escape'): void {
		const input = this.inputClient()?.input;
		if (!input?._handleKeyDown || !input._handleKeyUp) return;
		const code = { Meta: 'MetaLeft', 'Alt+Tab': 'Tab', PrintScreen: 'PrintScreen', Escape: 'Escape' }[key];
		const alt = key === 'Alt+Tab';
		input._handleKeyDown(
			new KeyboardEvent('keydown', { code, altKey: alt, bubbles: true, cancelable: true })
		);
		input._handleKeyUp(new KeyboardEvent('keyup', { code, altKey: alt, bubbles: true, cancelable: true }));
	}

	keyFromBridge(e: KeyboardEvent): void {
		const input = this.inputClient()?.input;
		if (e.type === 'keydown') input?._handleKeyDown?.(e);
		else input?._handleKeyUp?.(e);
	}

	/**
	 * The local clipboard to the device when the viewer gets the focus, where
	 * the browser allows reading it without asking (Chromium once granted;
	 * Firefox and Safari ask on every read, there Ctrl+V sends it)
	 */
	async syncClipboard(): Promise<void> {
		await this.clipboard.readSilently();
	}

	/**
	 * Cmd as Ctrl for common shortcuts (cmd-as-ctrl.ts): the active
	 * session's remote isn't a Mac and the keyboard menu's switch is on
	 */
	cmdAsCtrlOn(): boolean {
		const s = this.active;
		if (!s || s.prefs.cmdAsCtrl === false) return false;
		return !/mac|darwin|os x/i.test(s.platform);
	}

	/** Why the active session takes no clipboard now, '' when it does */
	clipboardOff(): ClipboardOffReason {
		const s = this.active;
		if (!s || s.view !== 'streaming' || !s.client || this.parked) return 'not_streaming';
		if (this.viewOnlyGuest || s.viewOnly || !s.allowed.keyboard) return 'view_only';
		if (!s.allowed.clipboard) return 'device';
		if (s.toggles.disableClipboard) return 'toggle';
		return '';
	}

	/** To the active session's device: text and a PNG image (RustDesk), text alone on the other transports */
	private async sendClipboardEntry(entry: ClipEntry): Promise<void> {
		const c = this.active?.client;
		if (!c?.sendClipboard) throw new Error('no clipboard on this transport');
		const rich = this.transport === 'rd' || !!this.data.mockRemote;
		if (!rich && !entry.text) {
			this.clipboardNotice({ kind: 'image_unsupported' });
			return;
		}
		const sent = await c.sendClipboard(rich ? entry : (entry.text ?? ''));
		if (sent === false) throw new Error('not sent');
	}

	private clipboardNotice(n: ClipboardNotice): void {
		const mb = (bytes: number) => `${Math.round(bytes / (1024 * 1024))} MB`;
		switch (n.kind) {
			case 'too_large':
				toast.warning(
					n.what === 'text'
						? tf(
								'remote.clipboard_text_too_large',
								'The copied text is larger than {limit} and was not sent.',
								{
									limit: mb(n.limit)
								}
							)
						: tf(
								'remote.clipboard_image_too_large',
								'The copied image is larger than {limit} and was not sent.',
								{
									limit: mb(n.limit)
								}
							)
				);
				break;
			case 'from_remote_too_large':
				toast.warning(
					tf(
						'remote.clipboard_remote_too_large',
						'The remote computer copied more than {limit}. It was not copied to this computer.',
						{ limit: mb(CLIPBOARD_LIMITS.fromRemote) }
					)
				);
				break;
			case 'image_unsupported':
				toast.warning(
					tf('remote.clipboard_image_unsupported', "The copied image can't be sent. Only the text went over.")
				);
				break;
			case 'send_failed':
				toast.error(
					tf('remote.clipboard_send_failed', 'The clipboard could not be sent to the remote computer.')
				);
				break;
		}
	}

	/** Autoplay was blocked: the click on "Start playback" plays video and resumes audio */
	startPlayback(s: ViewerSession): void {
		s.autoplayBlocked = false;
		s.client?.video?.retryPlay?.();
		const ctx = s.client?.audio?.audioCtx;
		if (ctx?.state === 'suspended') void ctx.resume();
	}
}
