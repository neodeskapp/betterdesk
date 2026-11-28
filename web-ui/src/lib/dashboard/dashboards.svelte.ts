/**
 * The overview's dashboards for the signed-in account: its own (saved on
 * the server, or the built-in one until it changes something), the shared
 * ones it sees, which tab is open, and the dashboard being edited.
 *
 * Editing works on a copy: Done saves it (an own dashboard with PUT
 * /api/dashboards, a shared one by publishing the shared list again),
 * Cancel drops it. Renaming, adding, removing, hiding and sharing save at
 * once.
 */

import {
	getDashboards,
	type DashboardsAnswer,
	publishSharedDashboards,
	resetOwnDashboards,
	saveOwnDashboards
} from '#lib/api/dashboards.ts';
import {
	copyBoard,
	defaultBoard,
	newId,
	openTab,
	ownKey,
	sharedKey,
	tabsFor,
	type Board,
	type OwnBoards,
	type SharedBoard,
	type Sharing,
	type Tab
} from './model.ts';

export interface Editing {
	key: string;
	draft: Board;
	/** The draft is a shared dashboard (only people who publish edit those) */
	shared: boolean;
}

export class Dashboards {
	own = $state.raw<OwnBoards | null>(null);
	shared = $state.raw<SharedBoard[]>([]);
	canPublish = $state(false);
	loaded = $state(false);
	failed = $state(false);
	/** The tab asked for in the address (?board=) */
	asked = $state<string | null>(null);
	editing = $state.raw<Editing | null>(null);
	saving = $state(false);

	#builtInName: () => string;

	constructor(builtInName: () => string) {
		this.#builtInName = builtInName;
	}

	readonly builtIn = $derived.by(() => defaultBoard(this.#builtInName()));
	readonly tabs: Tab[] = $derived(tabsFor(this.own, this.shared, this.builtIn));
	readonly current: Tab | undefined = $derived(openTab(this.tabs, this.asked, this.own?.active ?? ''));
	/** The board drawn: the draft while it is being edited */
	readonly board: Board | undefined = $derived(
		this.editing && this.editing.key === this.current?.key ? this.editing.draft : this.current?.board
	);
	/** Shared dashboards this account hid (a locked one can't be) */
	readonly hiddenShared = $derived(
		this.shared.filter((s) => !s.locked && (this.own?.hidden ?? []).includes(s.id))
	);

	/** first: the answer the page's load asked for while the console started (null when it failed) */
	async load(first?: Promise<DashboardsAnswer | null>): Promise<void> {
		try {
			const out = (await first) ?? (await getDashboards());
			this.own = out.own;
			this.shared = out.shared;
			this.canPublish = out.canPublish;
			this.failed = false;
		} catch {
			// The built-in dashboard still shows
			this.failed = true;
		} finally {
			this.loaded = true;
		}
	}

	/** Whether this account can change the open tab's widgets */
	canEdit(tab: Tab | undefined = this.current): boolean {
		return !!tab && (!tab.shared || this.canPublish);
	}

	/* ── Own dashboards ── */

	/** The account's own list as it is to be saved: the built-in one when it never saved */
	#ownNow(): OwnBoards {
		return (
			this.own ?? { boards: this.shared.some((s) => s.home) ? [] : [this.builtIn], active: '', hidden: [] }
		);
	}

	async #saveOwn(next: OwnBoards): Promise<void> {
		this.saving = true;
		try {
			this.own = await saveOwnDashboards(next);
		} finally {
			this.saving = false;
		}
	}

	#ownIds(): string[] {
		return this.#ownNow().boards.map((b) => b.id);
	}

	/** A new empty dashboard of this account, opened in edit mode; returns its tab key */
	async addBoard(name: string): Promise<string> {
		const own = this.#ownNow();
		const board: Board = { id: newId(own.boards.map((b) => b.id)), name, widgets: [] };
		await this.#saveOwn({ ...own, boards: [...own.boards, board] });
		const key = ownKey(board.id);
		this.asked = key;
		this.editing = { key, draft: board, shared: false };
		return key;
	}

	/** An own copy of any tab (an own or a shared one); returns its key */
	async duplicate(tab: Tab, name: string): Promise<string> {
		const own = this.#ownNow();
		const board = copyBoard(tab.board, name, this.#ownIds());
		await this.#saveOwn({ ...own, boards: [...own.boards, board] });
		return (this.asked = ownKey(board.id));
	}

	async rename(tab: Tab, name: string, sharing: Sharing | null = null): Promise<void> {
		const sharedId = tab.shared?.id;
		if (sharedId) {
			await this.#publish(
				this.shared.map((s) => (s.id === sharedId ? { ...s, name, ...(sharing ?? {}) } : s))
			);
			return;
		}
		const own = this.#ownNow();
		await this.#saveOwn({
			...own,
			boards: own.boards.map((b) => (b.id === tab.board.id ? { ...b, name } : b))
		});
	}

	/** Deletes an own dashboard; the last one gone brings back the built-in one */
	async remove(tab: Tab): Promise<void> {
		const own = this.#ownNow();
		const boards = own.boards.filter((b) => b.id !== tab.board.id);
		await this.#saveOwn({ ...own, boards, active: own.active === tab.key ? '' : own.active });
		if (this.asked === tab.key) this.asked = null;
	}

	/** The tab that opens next time */
	async setActive(key: string): Promise<void> {
		this.asked = key;
		const own = this.#ownNow();
		if (own.active === key) return;
		// Remembering a tab is not worth an error message
		await this.#saveOwn({ ...own, active: key }).catch(() => {});
	}

	async hide(tab: Tab): Promise<void> {
		if (!tab.shared || tab.shared.locked) return;
		const own = this.#ownNow();
		await this.#saveOwn({ ...own, hidden: [...own.hidden, tab.shared.id] });
		if (this.asked === tab.key) this.asked = null;
	}

	async unhideAll(): Promise<void> {
		const own = this.#ownNow();
		await this.#saveOwn({ ...own, hidden: [] });
	}

	/** Back to the built-in dashboard with every shared one shown */
	async reset(): Promise<void> {
		this.saving = true;
		try {
			await resetOwnDashboards();
			this.own = null;
			this.asked = null;
			this.editing = null;
		} finally {
			this.saving = false;
		}
	}

	/* ── Shared dashboards (server.config) ── */

	async #publish(boards: SharedBoard[]): Promise<void> {
		this.saving = true;
		try {
			this.shared = await publishSharedDashboards(boards);
		} finally {
			this.saving = false;
		}
	}

	/** Publishes a copy of a tab for the accounts sharing names; returns the shared tab's key */
	async share(tab: Tab, name: string, sharing: Sharing): Promise<string> {
		const board = copyBoard(
			tab.board,
			name,
			this.shared.map((s) => s.id)
		);
		// One home per audience is enough: a new home takes over from the others
		const others = sharing.home ? this.shared.map((s) => ({ ...s, home: false })) : this.shared;
		await this.#publish([...others, { ...board, ...sharing }]);
		return (this.asked = sharedKey(board.id));
	}

	async unshare(tab: Tab): Promise<void> {
		const sharedId = tab.shared?.id;
		if (!sharedId) return;
		await this.#publish(this.shared.filter((s) => s.id !== sharedId));
		if (this.asked === tab.key) this.asked = null;
	}

	/* ── Editing ── */

	startEdit(): void {
		const tab = this.current;
		if (!tab || !this.canEdit(tab)) return;
		this.editing = { key: tab.key, draft: tab.board, shared: !!tab.shared };
	}

	change(draft: Board): void {
		if (this.editing) this.editing = { ...this.editing, draft };
	}

	cancelEdit(): void {
		this.editing = null;
	}

	async saveEdit(): Promise<void> {
		const e = this.editing;
		if (!e) return;
		if (e.shared) {
			await this.#publish(
				this.shared.map((s) => (s.id === e.draft.id ? { ...s, widgets: e.draft.widgets } : s))
			);
		} else {
			const own = this.#ownNow();
			const has = own.boards.some((b) => b.id === e.draft.id);
			const boards = has
				? own.boards.map((b) => (b.id === e.draft.id ? e.draft : b))
				: [...own.boards, e.draft];
			await this.#saveOwn({ ...own, boards });
		}
		this.editing = null;
	}
}
