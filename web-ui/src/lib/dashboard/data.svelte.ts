/**
 * What the overview's widgets read, loaded once for the whole page: device
 * counts and server status (with the counts behind "Needs attention", each
 * only for the people allowed to read it), and the RustDesk client values.
 * Several widgets show the same numbers, so they share one copy instead of
 * each asking the server.
 *
 * The page polls load() every 30 s while the tab is visible and on Refresh.
 * The client values load only once a widget asks for them (wantConfig), so
 * a dashboard without Client setup doesn't request them.
 */

import {
	countOpenHelpRequests,
	countRegistrations,
	getClientConfig,
	getDeviceStats,
	getServerStatus,
	type ClientConfig,
	type DeviceStats,
	type ServerStatus
} from '#lib/api/dashboard.ts';
import { readClientHost, serverProblem, writeClientHost } from '#lib/devices-area/dashboard.ts';

export interface Early {
	stats: Promise<DeviceStats | null>;
	status: Promise<ServerStatus | null>;
	config: Promise<ClientConfig | null>;
}

const orNull = <T>(p: Promise<T>): Promise<T | null> => p.catch(() => null);

export interface Permissions {
	devices: boolean;
	approve: boolean;
	help: boolean;
}

export class DashboardData {
	stats = $state.raw<DeviceStats | null>(null);
	statsFailed = $state(false);
	status = $state.raw<ServerStatus | null>(null);
	/** false until the first status answer (or failure) */
	statusKnown = $state(false);
	registrations = $state(0);
	openHelp = $state(0);
	attentionKnown = $state(false);

	config = $state.raw<ClientConfig | null>(null);
	configFailed = $state(false);
	configLoaded = $state(false);
	/** The address typed into "Client server address", kept for this tab */
	host = $state(readClientHost());

	readonly problem = $derived(this.statusKnown ? serverProblem(this.status) : null);

	#early: Partial<Early>;
	#perms: Permissions;
	#configWanted = false;

	constructor(perms: Permissions, early?: Early) {
		this.#perms = perms;
		this.#early = { ...early };
	}

	/** An answer +page.ts asked for while the console started, used once; a failed one is asked again */
	#first<T>(name: keyof Early, fresh: () => Promise<T>): Promise<T> {
		const p = this.#early[name] as Promise<T | null> | undefined;
		this.#early = { ...this.#early, [name]: undefined };
		return p ? p.then((v) => v ?? fresh()) : fresh();
	}

	async load(): Promise<void> {
		const counts = Promise.all([
			this.#perms.approve ? orNull(countRegistrations()) : Promise.resolve(0),
			this.#perms.help ? orNull(countOpenHelpRequests()) : Promise.resolve(0)
		]);
		const [s, st] = await Promise.all([
			orNull(this.#first('stats', () => getDeviceStats())),
			orNull(this.#first('status', () => getServerStatus()))
		]);
		if (s) this.stats = s;
		this.statsFailed = !s && !this.stats;
		this.status = st;
		this.statusKnown = true;
		const [reg, help] = await counts;
		this.registrations = reg ?? 0;
		this.openHelp = help ?? 0;
		this.attentionKnown = true;
	}

	/** A widget that shows the client values asks for them; the first ask loads them */
	wantConfig(): void {
		if (this.#configWanted) return;
		this.#configWanted = true;
		void this.loadConfig();
	}

	async loadConfig(persist = false): Promise<void> {
		const typed = this.host.trim();
		try {
			const c = await this.#first('config', () => getClientConfig(typed));
			if (!c) throw new Error('no config');
			this.config = c;
			this.configFailed = false;
			// The field shows the address the values are for
			if (!typed && c.client_server_host) this.host = c.client_server_host;
			if (persist) writeClientHost(typed);
		} catch {
			this.config = null;
			this.configFailed = true;
		} finally {
			this.configLoaded = true;
		}
	}

	/** Refresh: everything that was loaded before */
	refresh(): void {
		void this.load();
		if (this.#configWanted) void this.loadConfig();
	}
}
