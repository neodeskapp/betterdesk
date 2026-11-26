/**
 * Session watchdog (public/js/session-guard.js). The server enforces idle
 * and absolute expiry; this keeps the tabs in step and warns before an
 * idle sign-out.
 *
 * - Cross-tab: a tab whose session ended (401, or Sign out) tells the
 *   others through BroadcastChannel "neodesk-panel-session" and the
 *   storage key "neodesk.session.expired"; they go to the sign-in page.
 * - Idle: a warning `warnBeforeMs` before `idleTimeoutMs` without
 *   activity (mouse, keys, scroll, touch), with "Stay signed in". At the
 *   end the session is checked: a 401 is sign-in; a session another tab
 *   kept alive hides the warning and the idle clock follows the server; a
 *   check that failed otherwise (network, 502, 429) runs again later.
 * - Absolute end: a session ends `maxAgeMs` after sign-in whatever
 *   happens. When that is under 20 hours (WCAG 2.2.1 lets 20 hours and
 *   more pass), `guard.ending` is set END_WARN_MS before the end, so the
 *   page can say so while there is time to save. The end comes from the
 *   server's absoluteExpiresAt, moved to this clock by its serverNow.
 * - Every 5 minutes (while the tab is visible) GET /api/auth/verify brings
 *   the policy and the server's last activity.
 * - The server counts writes and page loads as activity, not reads or polls
 *   (the API client marks those as background). Activity here reaches the
 *   server as a ping at most once a minute; activity inside the minute is
 *   sent when it ends, so the server never thinks the person left earlier
 *   than this tab does.
 */

import { ApiError, setUnauthorizedHandler } from '#lib/api/client.ts';
import { poll } from '#lib/state/live.ts';
import { verifySession, type SessionInfo } from './api.ts';

export const CHANNEL = 'neodesk-panel-session';
export const EXPIRED_KEY = 'neodesk.session.expired';
export const SYNC_MS = 5 * 60_000;
/** How soon the check at the end of the idle time runs again when it could not decide */
export const RECHECK_MS = 30_000;
const ACTIVITY = ['mousemove', 'mousedown', 'keydown', 'scroll', 'touchstart', 'click'] as const;

/** How long before a fixed session end the page warns */
export const END_WARN_MS = 5 * 60_000;
/** Sessions that last this long or longer get no warning before their fixed end */
export const LONG_SESSION_MS = 20 * 3_600_000;

export interface Policy {
	idleTimeoutMs: number;
	warnBeforeMs: number;
	/** 0: not known */
	maxAgeMs: number;
}

const DEFAULT_POLICY: Policy = { idleTimeoutMs: 30 * 60_000, warnBeforeMs: 60_000, maxAgeMs: 0 };

class Guard {
	/** Seconds left when the warning was shown, or null while it is hidden */
	warning = $state<number | null>(null);
	/** When the idle sign-out happens (ms, this clock), while the warning shows */
	deadline = $state<number | null>(null);
	/** When the session ends for good (ms, this clock), once that is close; null otherwise */
	ending = $state<number | null>(null);
}

export const guard = new Guard();

let channel: BroadcastChannel | null = null;
let policy: Policy = DEFAULT_POLICY;
let lastActivity = Date.now();
let lastMark = 0;
let lastPing = 0;
let pingTimer: ReturnType<typeof setTimeout> | null = null;
let warnTimer: ReturnType<typeof setTimeout> | null = null;
let expireTimer: ReturnType<typeof setTimeout> | null = null;
let endTimer: ReturnType<typeof setTimeout> | null = null;
/** The fixed end on this clock, once the server said it */
let absoluteEnd: number | null = null;
let stopSync: (() => void) | null = null;
let started = false;
let leaving = false;

/** The sign-in page with a return path to here (the API client's format) */
export function loginUrl(code: string | null, expired = true): string {
	const parts: string[] = [];
	if (expired) parts.push('expired=1');
	parts.push('return=' + encodeURIComponent(location.pathname + location.search));
	if (code) parts.push('reason=' + encodeURIComponent(code));
	return '/login?' + parts.join('&');
}

/** Tell the other tabs that this session ended */
export function broadcastExpired(code = 'session_expired'): void {
	try {
		localStorage.setItem(EXPIRED_KEY, JSON.stringify({ at: Date.now(), code }));
	} catch {
		/* private mode: the channel still works */
	}
	try {
		channel?.postMessage({ type: 'expired', code });
	} catch {
		/* closed */
	}
}

function onSignInPage(): boolean {
	const p = location.pathname;
	return p.startsWith('/login') || p.startsWith('/remote/login');
}

/** Leave for the sign-in page (once), telling the other tabs first */
export function goLogin(code: string | null = 'session_expired'): void {
	if (leaving || onSignInPage()) return;
	leaving = true;
	broadcastExpired(code ?? 'session_expired');
	location.assign(loginUrl(code));
}

function clearTimers(): void {
	if (warnTimer) clearTimeout(warnTimer);
	if (expireTimer) clearTimeout(expireTimer);
	warnTimer = expireTimer = null;
}

function schedule(): void {
	clearTimers();
	const idle = policy.idleTimeoutMs;
	const untilWarn = Math.max(0, lastActivity + idle - policy.warnBeforeMs - Date.now());
	warnTimer = setTimeout(() => {
		guard.deadline = lastActivity + idle;
		guard.warning = Math.max(1, Math.ceil((lastActivity + idle - Date.now()) / 1000));
	}, untilWarn);
	expireTimer = setTimeout(checkExpiry, Math.max(0, lastActivity + idle - Date.now()) + 500);
}

function recheckLater(): void {
	if (expireTimer) clearTimeout(expireTimer);
	expireTimer = setTimeout(checkExpiry, RECHECK_MS);
}

/** The idle time is over here: the server decides */
function checkExpiry(): void {
	verifySession().then(
		(info) => {
			applyPolicy(info);
			// Still valid on the server without newer activity: ask again later, not in a loop
			if (lastActivity + policy.idleTimeoutMs <= Date.now()) recheckLater();
		},
		(err: unknown) => {
			// A 401 is sign-in (the API client sends it there too); nothing else means signed out
			if (err instanceof ApiError && err.status === 401) goLogin();
			else recheckLater();
		}
	);
}

function ping(): void {
	lastPing = Date.now();
	verifySession(true).catch(() => {});
}

/** Tell the server someone is here, once a minute at most (a quarter of a short idle time) */
function reportActivity(): void {
	const wait = lastPing + Math.min(60_000, policy.idleTimeoutMs / 4) - Date.now();
	if (wait <= 0) return ping();
	pingTimer ??= setTimeout(() => {
		pingTimer = null;
		ping();
	}, wait);
}

/** Someone used the page: hide the warning and start the idle clock again */
export function markActivity(): void {
	const now = Date.now();
	// Mouse moves come in bursts; once a second is enough
	if (now - lastMark < 1000 && guard.warning === null) return;
	lastMark = now;
	lastActivity = now;
	guard.warning = null;
	guard.deadline = null;
	schedule();
	reportActivity();
}

/** "Stay signed in": activity here and on the server */
export function staySignedIn(): void {
	lastMark = 0;
	lastPing = 0;
	markActivity();
}

/** Warn END_WARN_MS before the fixed end of a session shorter than 20 hours */
function scheduleEnd(): void {
	if (endTimer) clearTimeout(endTimer);
	endTimer = null;
	const end = absoluteEnd;
	if (end === null || !policy.maxAgeMs || policy.maxAgeMs >= LONG_SESSION_MS) return;
	endTimer = setTimeout(
		() => {
			guard.ending = end;
		},
		Math.max(0, end - END_WARN_MS - Date.now())
	);
}

function applyPolicy(info: SessionInfo): void {
	const s = info.session;
	if (!s) return;
	policy = {
		idleTimeoutMs: s.idleTimeoutMs && s.idleTimeoutMs > 0 ? s.idleTimeoutMs : policy.idleTimeoutMs,
		warnBeforeMs: s.warnBeforeMs && s.warnBeforeMs > 0 ? s.warnBeforeMs : policy.warnBeforeMs,
		maxAgeMs: s.maxAgeMs && s.maxAgeMs > 0 ? s.maxAgeMs : policy.maxAgeMs
	};
	if (s.lastActivityAt) lastActivity = Math.max(lastActivity, s.lastActivityAt);
	// Activity in another tab moved the end: the warning goes until it is due again
	if (guard.warning !== null && lastActivity + policy.idleTimeoutMs - policy.warnBeforeMs > Date.now()) {
		guard.warning = null;
		guard.deadline = null;
	}
	if (s.absoluteExpiresAt) {
		// The server's clock may differ from this one
		const skew = s.serverNow ? Date.now() - s.serverNow : 0;
		const end = s.absoluteExpiresAt + skew;
		// Request time moves the guess by a little each time; only a real change reschedules
		if (absoluteEnd === null || Math.abs(end - absoluteEnd) > 5000) {
			absoluteEnd = end;
			scheduleEnd();
		}
	}
	schedule();
}

function readPolicy(raw: Record<string, unknown> | null | undefined): Policy {
	const idle = Number(raw?.idleTimeoutMs);
	const warn = Number(raw?.warnBeforeMs);
	const max = Number(raw?.maxAgeMs);
	return {
		idleTimeoutMs: Number.isFinite(idle) && idle > 0 ? idle : DEFAULT_POLICY.idleTimeoutMs,
		warnBeforeMs: Number.isFinite(warn) && warn > 0 ? warn : DEFAULT_POLICY.warnBeforeMs,
		maxAgeMs: Number.isFinite(max) && max > 0 ? max : DEFAULT_POLICY.maxAgeMs
	};
}

/**
 * The reason another tab was given, passed on so this one says the same:
 * a session signed out from another device or by an admin (A239). Its own
 * sign-out and an unknown text count as a plain end.
 */
function codeOf(code: unknown): string {
	return typeof code === 'string' && /^session_[a-z_]{1,40}$/.test(code) ? code : 'session_expired';
}

const onActivity = () => markActivity();
const onStorage = (e: StorageEvent) => {
	if (e.key !== EXPIRED_KEY || !e.newValue) return;
	let code: unknown;
	try {
		code = (JSON.parse(e.newValue) as { code?: unknown } | null)?.code;
	} catch {
		code = undefined;
	}
	goLogin(codeOf(code));
};

/** Start watching (the shell calls it once the session is known) */
export function startGuard(sessionPolicy: Record<string, unknown> | null | undefined): void {
	if (started) return;
	started = true;
	leaving = false;
	policy = readPolicy(sessionPolicy);
	lastActivity = Date.now();
	// Loading the console was activity on the server
	lastPing = lastActivity;
	// A 401 anywhere also signs out the other tabs (Utils.api did the same)
	setUnauthorizedHandler((code) => goLogin(code));
	try {
		channel = typeof BroadcastChannel === 'function' ? new BroadcastChannel(CHANNEL) : null;
		if (channel) {
			channel.onmessage = (e: MessageEvent<unknown>) => {
				const msg = e.data as { type?: unknown } | null;
				if (msg && msg.type === 'expired') goLogin(codeOf((msg as { code?: unknown }).code));
			};
		}
	} catch {
		channel = null;
	}
	window.addEventListener('storage', onStorage);
	for (const evt of ACTIVITY) document.addEventListener(evt, onActivity, { passive: true });
	schedule();
	absoluteEnd = null;
	// A session with a fixed end under 20 hours: ask now when it ends, not in 5 minutes
	const short = policy.maxAgeMs > 0 && policy.maxAgeMs < LONG_SESSION_MS;
	stopSync = poll(() => verifySession().then(applyPolicy), { every: SYNC_MS, immediate: short });
}

/** The person saw the warning about the session's end */
export function dismissEnding(): void {
	guard.ending = null;
}

export function stopGuard(): void {
	if (!started) return;
	started = false;
	clearTimers();
	if (pingTimer) clearTimeout(pingTimer);
	pingTimer = null;
	if (endTimer) clearTimeout(endTimer);
	endTimer = null;
	absoluteEnd = null;
	stopSync?.();
	stopSync = null;
	channel?.close();
	channel = null;
	window.removeEventListener('storage', onStorage);
	for (const evt of ACTIVITY) document.removeEventListener(evt, onActivity);
	guard.warning = null;
	guard.deadline = null;
	guard.ending = null;
}
