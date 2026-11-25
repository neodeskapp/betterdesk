/**
 * Signed-in devices (A239) as one list with its actions, for the sheet in
 * the account menu and on the user's page (SessionsSheet) and for Your
 * account › Signed-in devices. Without `user`: the signed-in account's own
 * sessions; with it: that account's (user.edit).
 */

import {
	endAllUserSessions,
	endOtherSessions,
	endOwnSession,
	endUserSession,
	listOwnSessions,
	listUserSessions,
	type SignedInDevice,
	type SignedInDevices
} from '#lib/api/sessions.ts';
import { t } from '#lib/i18n/i18n.svelte.ts';
import { confirm } from '#lib/ui/dialogs.svelte.ts';
import { deviceLabel } from './sessions.ts';

export interface SessionsOwner {
	id: number | string;
	username: string;
}

export class SessionsList {
	list = $state.raw<SignedInDevices | null>(null);
	loadError = $state('');
	busy = $state(false);
	status = $state('');
	statusTone = $state<'neutral' | 'ok' | 'error'>('neutral');
	readonly others = $derived(this.list ? this.list.sessions.filter((s) => !s.current) : []);

	/** after: runs when an action is done (the sheet keeps focus in itself) */
	constructor(
		readonly user: SessionsOwner | null,
		private readonly after: () => void | Promise<void> = () => {}
	) {}

	async load(): Promise<void> {
		this.loadError = '';
		try {
			this.list = this.user ? await listUserSessions(this.user.id) : await listOwnSessions();
		} catch (err) {
			this.loadError = err instanceof Error && err.message ? err.message : t('sessions.load_failed');
		}
	}

	private async run(
		ask: { title: string; message: string; confirmLabel: string },
		work: () => Promise<string>
	): Promise<void> {
		if (this.busy) return;
		if (!(await confirm({ ...ask, danger: true }))) return;
		this.busy = true;
		this.status = '';
		try {
			this.status = await work();
			this.statusTone = 'ok';
			await this.load();
		} catch (err) {
			this.status = err instanceof Error && err.message ? err.message : t('errors.server_error');
			this.statusTone = 'error';
		} finally {
			this.busy = false;
		}
		await this.after();
	}

	signOut(d: SignedInDevice): void {
		const name = deviceLabel(d, t);
		const user = this.user;
		void this.run(
			{
				title: t('sessions.sign_out'),
				message: t('sessions.confirm_sign_out', { device: name }),
				confirmLabel: t('sessions.sign_out')
			},
			async () => {
				if (user) await endUserSession(user.id, d.id);
				else await endOwnSession(d.id);
				return t('sessions.signed_out_one', { device: name });
			}
		);
	}

	/** The label of the button that ends all the others (own) or all (another account) */
	get allLabel(): string {
		return this.user ? t('sessions.sign_out_all') : t('sessions.sign_out_everywhere_else');
	}

	signOutAll(): void {
		const user = this.user;
		void this.run(
			{
				title: this.allLabel,
				message: user
					? t('sessions.confirm_all_user', { username: user.username })
					: t('sessions.confirm_everywhere_else'),
				confirmLabel: this.allLabel
			},
			async () => {
				const ended = user ? await endAllUserSessions(user.id) : await endOtherSessions();
				return t('sessions.signed_out_count', { count: ended });
			}
		);
	}
}
