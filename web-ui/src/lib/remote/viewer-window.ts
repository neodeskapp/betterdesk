/**
 * The remote dashboard (/remote) and the standalone viewer tab share one
 * browser tab for sessions. The viewer names its tab, and Connect on the
 * dashboard opens that name inside the click: the browser brings an open
 * viewer to the front (a tab can't focus itself from a BroadcastChannel
 * message, so the session used to land in a tab nobody saw), or opens a new
 * tab that then loads the viewer. An open viewer gets the session over
 * BroadcastChannel "neodesk-remote"; one that doesn't answer in time is
 * loaded with the device instead.
 */

export const VIEWER_WINDOW = 'neodesk-viewer';
export const VIEWER_CHANNEL = 'neodesk-remote';
const ANSWER_MS = 300;

/** The viewer tab takes the name, so Connect finds it */
export function claimViewerWindow(): void {
	try {
		window.name = VIEWER_WINDOW;
	} catch {
		/* not ours to name */
	}
}

/** The viewer left this tab: whatever loads next is not a viewer */
export function releaseViewerWindow(): void {
	try {
		if (window.name === VIEWER_WINDOW) window.name = '';
	} catch {
		/* nothing to release */
	}
}

/** Something already loaded in the tab: a fresh one sits on about:blank */
function hasPage(win: Window): boolean {
	try {
		return win.location.href !== 'about:blank';
	} catch {
		return true;
	}
}

/**
 * Opens the device in the viewer tab; call it inside the click. False when
 * the browser blocked the tab.
 */
export function openInViewerTab(viewerUrl: string, deviceId: string, deviceName = ''): boolean {
	let win: Window | null;
	try {
		win = window.open('', VIEWER_WINDOW);
	} catch {
		win = null;
	}
	if (!win) return false;
	if (!hasPage(win) || typeof BroadcastChannel === 'undefined') {
		win.location.href = viewerUrl;
		return true;
	}
	const target = win;
	const bc = new BroadcastChannel(VIEWER_CHANNEL);
	let answered = false;
	bc.onmessage = (ev: MessageEvent<{ type?: string; deviceId?: string } | null>) => {
		if (ev.data?.type === 'session-added' && ev.data.deviceId === deviceId) {
			answered = true;
			bc.close();
		}
	};
	bc.postMessage({ type: 'add-session', deviceId, deviceName });
	// The tab exists, so loading the viewer in it later is no pop-up
	setTimeout(() => {
		if (answered) return;
		bc.close();
		try {
			target.location.href = viewerUrl;
		} catch {
			/* the tab closed meanwhile */
		}
	}, ANSWER_MS);
	return true;
}
