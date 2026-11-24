/**
 * Links that hand a device's ID to a remote desktop app on this computer.
 *
 * The NeoDesk client registers neodesk:// on every system. It takes
 * rustdesk:// only on Windows when stock RustDesk is not installed, and
 * never from RustDesk, so a rustdesk:// link opens stock RustDesk wherever
 * that is installed. The console offers NeoDesk first and keeps RustDesk as
 * its own choice. Phones have no NeoDesk app: links there go to the RustDesk
 * app.
 */

export type DesktopApp = 'neodesk' | 'rustdesk';

/** In the order the console offers them */
export const DESKTOP_APPS: readonly DesktopApp[] = ['neodesk', 'rustdesk'];

/** The app's name as people see it; the same in every language */
export const APP_NAMES: Record<DesktopApp, string> = { neodesk: 'NeoDesk', rustdesk: 'RustDesk' };

/** <scheme>://<id>: the app opens a session to the device */
export function appLink(deviceId: string, app: DesktopApp = 'neodesk'): string {
	return `${app}://${encodeURIComponent(deviceId)}`;
}

/**
 * <scheme>://connection/new/<id>?password=<password>: the same with the
 * session password, so the app doesn't ask for it. The link goes to the
 * app on this computer, not over the network.
 */
export function appSessionLink(deviceId: string, password: string, app: DesktopApp = 'neodesk'): string {
	const base = `${app}://connection/new/${encodeURIComponent(deviceId)}`;
	return password ? `${base}?password=${encodeURIComponent(password)}` : base;
}
