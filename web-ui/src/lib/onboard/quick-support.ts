/**
 * The supporter's side of a quick support session: what its state says,
 * when it is over, and the link that opens it in the desktop app.
 *
 * waiting: the link is out. opened, asking: the app runs and asks the
 * person. allowed: Connect. declined, ended, expired: over.
 */

import type { SessionState, SupportSession } from '#lib/api/onboard.ts';
import { appSessionLink, type DesktopApp } from '#lib/util/app-links.ts';

/** How often the dialog asks for the session's state */
export const POLL_MS = 2000;

export function isOver(status: SessionState): boolean {
	return status === 'declined' || status === 'ended' || status === 'expired';
}

export function canConnect(s: Pick<SupportSession, 'status' | 'device_id'>): boolean {
	return s.status === 'allowed' && !!s.device_id;
}

/** The language key (below onboard.support) of the state's line */
export function stateKey(s: Pick<SupportSession, 'status' | 'ended_by' | 'password_ready'>): string {
	switch (s.status) {
		case 'waiting':
			return 'onboard.support.state_waiting';
		case 'opened':
		case 'asking':
			return 'onboard.support.state_asking';
		case 'allowed':
			return s.password_ready ? 'onboard.support.state_allowed' : 'onboard.support.state_allowed_wait';
		case 'declined':
			return 'onboard.support.state_declined';
		case 'expired':
			return 'onboard.support.state_expired';
		default:
			return s.ended_by === 'timeout'
				? 'onboard.support.state_timeout'
				: s.ended_by === 'customer'
					? 'onboard.support.state_ended_customer'
					: 'onboard.support.state_ended';
	}
}

/** The tone of the state for Status */
export function stateTone(status: SessionState): string {
	if (status === 'allowed') return 'good';
	if (status === 'declined') return 'danger';
	if (isOver(status)) return 'neutral';
	return 'waiting';
}

/**
 * The desktop app's link to a device, with the session password when
 * there is one: neodesk://connection/new/<id>?password=<password>, or the
 * same with rustdesk:// for stock RustDesk. The link goes to the app on
 * this computer, not over the network.
 */
export function desktopLink(deviceId: string, password: string, app: DesktopApp = 'neodesk'): string {
	return appSessionLink(deviceId, password, app);
}

/** The browser viewer's address; the viewer asks the server for the password itself */
export function viewerPath(deviceId: string): string {
	return '/remote/' + encodeURIComponent(deviceId);
}

/** Where Connect goes: a new tab for the viewer, this tab for the desktop app's link */
export const go = {
	tab(path: string): void {
		window.open(path, '_blank');
	},
	here(url: string): void {
		window.location.href = url;
	}
};
