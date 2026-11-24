/**
 * Remote sessions inside the console: one viewer for the whole tab, above
 * the routes. The console's shell renders it (ConsoleViewer.svelte) while
 * it has sessions and keeps it mounted on every page, hidden where the
 * session page is not shown, so the connections, their screens and their
 * state outlive the page that opened them. /remote/session/<id> shows the
 * viewer and opens the device's session, or picks it when it is open.
 *
 * Sessions the console can't hold go to the standalone viewer in a tab of
 * its own (/remote/<id>?standalone=1): guest links, RDP and VNC targets,
 * and transports other than the one the console's viewer runs.
 */

import { ApiError } from '#lib/api/client.ts';
import { getViewerData, type ViewerData } from '#lib/api/remote.ts';
import { tf } from '#lib/i18n/i18n.svelte.ts';
import { link } from '#lib/nav.ts';
import { isGuestMode, isRemoteTarget, transportOf } from './access.ts';
import { loadRdClient } from './rdclient.ts';
import { Viewer } from './viewer.svelte.ts';

/** The console's sidebar is folded away while a session shows (remembered in this browser) */
const COLLAPSED_KEY = 'nd_viewer_sidebar_collapsed';

export type OpenResult = 'opened' | 'standalone' | 'problem' | 'signin';

export interface SessionProblem {
	deviceId: string;
	title: string;
	message: string;
}

/** The address of a device's session in the console */
export function sessionPath(deviceId: string): string {
	return '/remote/session/' + encodeURIComponent(deviceId);
}

/** The standalone viewer's address, which stays a viewer of its own */
export function standalonePath(deviceId: string, search = ''): string {
	// eslint-disable-next-line svelte/prefer-svelte-reactivity -- a local value, nothing renders from it
	const q = new URLSearchParams(search);
	q.delete('standalone');
	q.set('standalone', '1');
	return '/remote/' + encodeURIComponent(deviceId) + '?' + q.toString();
}

function readCollapsed(): boolean {
	try {
		return localStorage.getItem(COLLAPSED_KEY) === '1';
	} catch {
		return false;
	}
}

export class ConsoleSessions {
	viewer = $state.raw<Viewer | null>(null);
	data = $state.raw<ViewerData | null>(null);
	/** The session page is on screen */
	shown = $state(false);
	/** A device being opened, and what went wrong opening one */
	opening = $state<string | null>(null);
	problem = $state.raw<SessionProblem | null>(null);
	/** The sidebar is folded away on the session page */
	collapsed = $state(readCollapsed());
	/** Full screen: the console's own bars step aside too */
	immersive = $state(false);
	/** Where Close goes when the last session ends */
	returnTo = '/devices';
	/** Leaves the session page (the shell sets it: a client-side navigation) */
	navigate: (path: string) => void = (path) => location.assign(link(path));

	get count(): number {
		return this.viewer?.sessions.length ?? 0;
	}

	/** Opens a device's session in the console or picks it; says where it went instead */
	async open(deviceId: string, search = ''): Promise<OpenResult> {
		this.problem = null;
		const v = this.viewer;
		if (v?.sessions.some((s) => s.deviceId === deviceId)) {
			v.switchTo(deviceId);
			return 'opened';
		}
		this.opening = deviceId;
		try {
			const data = await getViewerData(deviceId, search);
			const caps = data.capabilities;
			if (isGuestMode(caps) || isRemoteTarget(caps) || !data.user) return 'standalone';
			if (this.viewer && transportOf(caps) !== this.viewer.transport) return 'standalone';
			if (!this.viewer) {
				await loadRdClient({
					deviceId: data.deviceId,
					capabilities: caps,
					guestToken: data.guestToken,
					csrfToken: data.csrfToken,
					mock: data.mockRemote
				});
				this.data = data;
				this.viewer = new Viewer({ data, onleave: () => this.leave() });
				this.viewer.setParked(!this.shown);
			}
			// eslint-disable-next-line svelte/prefer-svelte-reactivity -- read once here
			const q = new URLSearchParams(search);
			this.viewer.open(data.deviceId, data.device.name, data.device.platform, {
				filesOnConnect: q.get('panel') === 'files'
			});
			return 'opened';
		} catch (err) {
			if (err instanceof ApiError && err.status === 401) return 'signin';
			this.problem = { deviceId, ...problemOf(err) };
			return 'problem';
		} finally {
			if (this.opening === deviceId) this.opening = null;
		}
	}

	/** The session page came on screen or went away */
	setShown(shown: boolean): void {
		this.shown = shown;
		this.viewer?.setParked(!shown);
		if (!shown && this.immersive && document.fullscreenElement)
			void document.exitFullscreen().catch(() => {});
	}

	setCollapsed(collapsed: boolean): void {
		this.collapsed = collapsed;
		try {
			localStorage.setItem(COLLAPSED_KEY, collapsed ? '1' : '0');
		} catch {
			/* storage blocked: kept for this page */
		}
	}

	/** Moves a session out to the standalone viewer in a new tab: this one ends here */
	openInNewTab(deviceId: string): void {
		const opened = window.open(link(standalonePath(deviceId)), '_blank');
		if (!opened) return;
		this.viewer?.close(deviceId);
	}

	close(deviceId: string): void {
		this.viewer?.close(deviceId);
	}

	/** The last session closed: back to where the person came from */
	private leave(): void {
		if (this.shown) this.navigate(this.returnTo || '/devices');
	}

	/** The console goes away (sign-out, the tab closes): every session ends */
	teardown(): void {
		this.viewer?.teardown();
		this.viewer = null;
		this.data = null;
		this.immersive = false;
	}
}

function problemOf(err: unknown): { title: string; message: string } {
	if (err instanceof ApiError && err.status === 403)
		return {
			title: tf('errors.forbidden_title', 'Access denied'),
			message: tf('errors.forbidden_message', 'You do not have permission to access this page.')
		};
	if (err instanceof ApiError && (err.status === 404 || err.status === 400))
		return {
			title: tf('errors.not_found_title', 'Page not found'),
			message: tf('errors.not_found_message', 'The page you are looking for does not exist.')
		};
	return {
		title: tf('remote_viewer.title_failed', 'Could not connect'),
		message:
			err instanceof Error && err.message
				? err.message
				: tf(
						'errors.server_error_message',
						'The server could not finish this request. Try again in a moment.'
					)
	};
}

export const consoleSessions = new ConsoleSessions();
