<!--
  The overview (/), the first page after sign-in: one or more dashboards
  of widgets (lib/dashboard). Without anything saved, the account sees the
  built-in dashboard: what needs someone, how the devices are doing, the
  last two weeks, the values a RustDesk client needs and the server's
  status. Customize opens the open dashboard for editing: add, move,
  resize, set up and remove widgets, then Done saves. The dashboard menu
  adds, renames, copies, deletes and hides dashboards, and lets people
  with server.config publish one for every account or for some roles.

  Data: lib/dashboard/data.svelte.ts loads the counts and the server status
  on load, every 30 s while the tab is visible (poll) and on Refresh, and
  the client values once a widget shows them. GET /api/dashboards gives the
  account's dashboards.

  URL: ?board=<id> (own) or ?board=shared:<id> opens that dashboard.
  ?activateAgent=1 opens "Pair with code" (enrollment.approve).
-->
<script lang="ts">
	import { onMount, untrack } from 'svelte';
	import type { PageData } from './$types';
	import { replaceState } from '$app/navigation';
	import { page } from '$app/state';
	import { listFolders, type Folder } from '#lib/api/devices.ts';
	import AddWidgetDialog from '#lib/dashboard/AddWidgetDialog.svelte';
	import BoardView from '#lib/dashboard/Board.svelte';
	import BoardDialog from '#lib/dashboard/BoardDialog.svelte';
	import { DashboardData } from '#lib/dashboard/data.svelte.ts';
	import { Dashboards } from '#lib/dashboard/dashboards.svelte.ts';
	import {
		addWidget,
		moveTo,
		moveWidget,
		removeWidget,
		updateWidget,
		type Sharing,
		type Tab,
		type WidgetKind
	} from '#lib/dashboard/model.ts';
	import WidgetDialog from '#lib/dashboard/WidgetDialog.svelte';
	import type { WidgetActions } from '#lib/dashboard/widgets/types.ts';
	import SetupGuide from '#lib/guide/SetupGuide.svelte';
	import { t, tf } from '#lib/i18n/i18n.svelte.ts';
	import { onRefresh, setPageTitle } from '#lib/shell/page.svelte.ts';
	import { poll } from '#lib/state/live.ts';
	import { can } from '#lib/state/session.svelte.ts';
	import Button from '#lib/ui/Button.svelte';
	import LiveStatus from '#lib/ui/LiveStatus.svelte';
	import MenuButton from '#lib/ui/MenuButton.svelte';
	import MenuDivider from '#lib/ui/MenuDivider.svelte';
	import MenuItem from '#lib/ui/MenuItem.svelte';
	import States from '#lib/ui/States.svelte';
	import Tabs from '#lib/ui/Tabs.svelte';
	import { confirm } from '#lib/ui/dialogs.svelte.ts';
	import { toast } from '#lib/ui/toasts.svelte.ts';
	import { ICONS } from '#lib/util/display.ts';
	import { lazy } from '#lib/util/lazy.ts';

	let { data: pageData }: { data: PageData } = $props();

	const canDevices = can('device.view');
	const canApprove = can('enrollment.approve');

	/* ── What the widgets show, loaded once for the page ── */

	const data = new DashboardData(
		// The help request list needs Support chat (DECISIONS.md P17, A242)
		{ devices: canDevices, approve: canApprove, help: canDevices && can('chat.support') },
		untrack(() => pageData.early)
	);

	// Now, every 30 s while the tab is visible, once when it is shown again
	$effect(() => poll(() => data.load(), { every: 30_000 }));
	// The top bar's Refresh: everything again
	$effect(() => onRefresh(() => data.refresh()));

	/* ── The dashboards ── */

	const boards = new Dashboards(() => t('nav.dashboard'));
	boards.asked = page.url.searchParams.get('board');
	$effect(() => {
		untrack(() => void boards.load(pageData.early.dashboards));
	});

	const editing = $derived(boards.editing);
	const current = $derived(boards.current);
	const board = $derived(boards.board);
	const tabItems = $derived(boards.tabs.map((x) => ({ id: x.key, label: x.board.name })));
	const otherBoards = $derived(
		boards.tabs.filter((x) => x.key !== current?.key).map((x) => ({ key: x.key, board: x.board }))
	);

	function showInUrl(key: string | null) {
		const url = new URL(location.href);
		if (key) url.searchParams.set('board', key);
		else url.searchParams.delete('board');
		// eslint-disable-next-line svelte/no-navigation-without-resolve -- this page's own URL with ?board=
		replaceState(url, page.state);
	}

	async function openBoard(key: string) {
		if (editing) return;
		showInUrl(key);
		await boards.setActive(key);
	}

	/** Runs a change that saves at once; a failure says so and leaves the page as it was */
	async function saving(work: () => Promise<unknown>, done?: string) {
		try {
			await work();
			if (done) toast.success(done);
		} catch (e) {
			toast.error(e instanceof Error && e.message ? e.message : t('errors.server_error'));
		}
	}

	/* ── Editing a dashboard ── */

	let adding = $state(false);
	let configuring = $state<string | null>(null);

	function change(fn: (b: NonNullable<typeof board>) => NonNullable<typeof board>) {
		if (editing) boards.change(fn(editing.draft));
	}

	function add(kind: WidgetKind) {
		adding = false;
		if (!editing) return;
		const next = addWidget(editing.draft, kind);
		boards.change(next);
		// A custom widget opens its settings right away: it shows nothing until they are filled in
		if (kind.configurable) configuring = next.widgets.at(-1)?.id ?? null;
	}

	async function done() {
		await saving(() => boards.saveEdit(), tf('dashboard.saved', 'Dashboard saved.'));
	}

	async function cancel() {
		if (editing && editing.draft !== current?.board) {
			const drop = await confirm({
				title: tf('dashboard.discard_title', 'Discard your changes?'),
				message: tf(
					'dashboard.discard_message',
					'The dashboard stays as it was before you started customizing.'
				),
				confirmLabel: tf('dashboard.discard', 'Discard'),
				danger: true
			});
			if (!drop) return;
		}
		boards.cancelEdit();
	}

	const configWidget = $derived(
		editing && configuring ? editing.draft.widgets.find((w) => w.id === configuring) : undefined
	);

	/* ── The dashboard menu ── */

	type BoardForm =
		{ kind: 'new' } | { kind: 'rename'; tab: Tab } | { kind: 'copy'; tab: Tab } | { kind: 'share'; tab: Tab };
	let boardForm = $state<BoardForm | null>(null);

	const formTitle = $derived.by(() => {
		switch (boardForm?.kind) {
			case 'new':
				return tf('dashboard.new_board', 'New dashboard');
			case 'rename':
				return boardForm.tab.shared
					? tf('dashboard.shared_settings', 'Sharing settings')
					: tf('dashboard.rename_board', 'Rename dashboard');
			case 'copy':
				return tf('dashboard.copy_board', 'Make a copy');
			case 'share':
				return tf('dashboard.share_board', 'Share with others');
			default:
				return '';
		}
	});

	async function submitBoard(name: string, sharing: Sharing | null) {
		const form = boardForm;
		boardForm = null;
		if (!form) return;
		if (form.kind === 'new') {
			await saving(async () => showInUrl(await boards.addBoard(name)));
		} else if (form.kind === 'rename') {
			await saving(() => boards.rename(form.tab, name, sharing));
		} else if (form.kind === 'copy') {
			await saving(async () => showInUrl(await boards.duplicate(form.tab, name)));
		} else if (form.kind === 'share' && sharing) {
			await saving(
				async () => showInUrl(await boards.share(form.tab, name, sharing)),
				tf('dashboard.shared_done', 'Dashboard shared.')
			);
		}
	}

	async function removeBoard(tab: Tab) {
		const shared = !!tab.shared;
		const ok = await confirm({
			title: shared
				? tf('dashboard.unshare_title', 'Stop sharing this dashboard?')
				: tf('dashboard.delete_title', 'Delete this dashboard?'),
			message: shared
				? tf('dashboard.unshare_message', 'Nobody sees it any more. Copies people made stay.')
				: tf('dashboard.delete_message', 'Its widgets and their settings are gone for good.'),
			confirmLabel: shared ? tf('dashboard.unshare', 'Stop sharing') : t('common.delete'),
			danger: true
		});
		if (!ok) return;
		await saving(async () => {
			await (shared ? boards.unshare(tab) : boards.remove(tab));
			showInUrl(null);
		});
	}

	async function resetAll() {
		const ok = await confirm({
			title: tf('dashboard.reset_title', 'Reset your dashboards?'),
			message: tf(
				'dashboard.reset_message',
				'Your own dashboards are deleted and the overview is back as it came, with every shared dashboard shown.'
			),
			confirmLabel: tf('dashboard.reset', 'Reset'),
			danger: true
		});
		if (!ok) return;
		await saving(async () => {
			await boards.reset();
			showInUrl(null);
		});
	}

	const isOwn = $derived(!!current && !current.shared);
	const ownCount = $derived(boards.tabs.filter((x) => !x.shared).length);

	/* ── Dialogs the widgets open (their code loads on first use) ── */

	const qrDialog = lazy(() => import('#lib/devices-area/ConfigQrDialog.svelte'));
	const scriptSheet = lazy(() => import('#lib/devices-area/InstallScriptSheet.svelte'));
	const pairDialog = lazy(() => import('#lib/devices-area/PairCodeDialog.svelte'));
	const addComputer = lazy(() => import('#lib/onboard/AddComputerDialog.svelte'));
	const activation = lazy(() => import('#lib/devices-area/AgentActivation.svelte'));

	let qrOpen = $state(false);
	let scriptOpen = $state(false);
	let pairOpen = $state(false);
	let activationOpen = $state(false);
	let addOpen = $state(false);
	let folders = $state.raw<Folder[]>([]);

	function closeAddComputer() {
		addOpen = false;
		void data.load();
	}

	const actions: WidgetActions = {
		openPair: () => (pairOpen = true),
		openQr: () => {
			if (!data.config?.config_uri || !data.config.public_key) return void toast.warning(t('keys.no_qr'));
			qrOpen = true;
		},
		openScript: () => {
			if (!data.config?.deploy_config_string)
				return void toast.warning(t('dashboard.deploy_string_not_ready'));
			scriptOpen = true;
		},
		openAddComputer: async () => {
			folders = await listFolders().catch(() => []);
			addOpen = true;
		},
		openBoard: (key) => void openBoard(key)
	};

	const activateParam = page.url.searchParams.get('activateAgent') === '1';
	// The tab title and the breadcrumb name the task, as the old server did; the page stays the overview
	if (activateParam) setPageTitle(t('nav.pair_agent'));
	onMount(() => {
		if (activateParam && canApprove) pairOpen = true;
	});

	/** The address agents type: the client address the values are for */
	const pairHost = $derived(data.config?.client_server_host || data.host.trim());
</script>

<div class="page page-wide dashboard-page">
	<div class="page-head">
		<h1>{t('nav.dashboard')}</h1>
		<p class="page-description">{t('dashboard.page_intro')}</p>
		<LiveStatus />
	</div>

	<!-- The first steps on a new server, while the guide is open for this account -->
	<SetupGuide />

	<div class={['board-bar', editing && 'is-editing']}>
		{#if editing}
			<p class="edit-hint" role="status">
				{tf(
					'dashboard.edit_hint',
					'Drag widgets by their bar or use the arrows. Changes are saved when you click Done.'
				)}
			</p>
			<div class="page-actions">
				<Button icon={ICONS.add} onclick={() => (adding = true)}
					>{tf('dashboard.add_widget', 'Add widget')}</Button
				>
				<Button onclick={() => void cancel()}>{t('common.cancel')}</Button>
				<Button variant="primary" icon="check" busy={boards.saving} onclick={() => void done()}
					>{tf('dashboard.done', 'Done')}</Button
				>
			</div>
		{:else}
			{#if tabItems.length > 1}
				<Tabs
					class="board-tabs"
					tabs={tabItems}
					value={current?.key}
					label={tf('dashboard.boards_label', 'Dashboards')}
					onchange={(key) => void openBoard(key)}
				/>
			{/if}
			<div class="page-actions board-actions">
				{#if boards.canEdit()}
					<Button icon={ICONS.edit} onclick={() => boards.startEdit()}
						>{tf('dashboard.customize', 'Customize')}</Button
					>
				{/if}
				{#if current}
					<MenuButton kebab label={tf('dashboard.board_menu', 'Dashboard options')} align="end">
						<MenuItem icon={ICONS.add} onselect={() => (boardForm = { kind: 'new' })}
							>{tf('dashboard.new_board', 'New dashboard')}</MenuItem
						>
						{#if isOwn || boards.canPublish}
							<MenuItem icon={ICONS.rename} onselect={() => (boardForm = { kind: 'rename', tab: current })}
								>{current.shared
									? tf('dashboard.shared_settings', 'Sharing settings')
									: tf('dashboard.rename_board', 'Rename dashboard')}</MenuItem
							>
						{/if}
						<MenuItem icon={ICONS.duplicate} onselect={() => (boardForm = { kind: 'copy', tab: current })}
							>{tf('dashboard.copy_board', 'Make a copy')}</MenuItem
						>
						{#if boards.canPublish && isOwn}
							<MenuItem icon={ICONS.guest_link} onselect={() => (boardForm = { kind: 'share', tab: current })}
								>{tf('dashboard.share_board', 'Share with others')}</MenuItem
							>
						{/if}
						{#if current.shared && !current.shared.locked}
							<MenuItem icon={ICONS.hide} onselect={() => void saving(() => boards.hide(current))}
								>{tf('dashboard.hide_board', 'Hide for me')}</MenuItem
							>
						{/if}
						{#if boards.hiddenShared.length}
							<MenuItem icon={ICONS.view} onselect={() => void saving(() => boards.unhideAll())}
								>{tf('dashboard.unhide_boards', 'Show hidden dashboards')}</MenuItem
							>
						{/if}
						{#if (isOwn && (ownCount > 1 || boards.own)) || (current.shared && boards.canPublish)}
							<MenuDivider />
							<MenuItem icon={ICONS.delete} onselect={() => void removeBoard(current)}
								>{current.shared
									? tf('dashboard.unshare', 'Stop sharing')
									: tf('dashboard.delete_board', 'Delete dashboard')}</MenuItem
							>
						{/if}
						{#if boards.own}
							<MenuItem icon={ICONS.reset} onselect={() => void resetAll()}
								>{tf('dashboard.reset_all', 'Reset my dashboards')}</MenuItem
							>
						{/if}
					</MenuButton>
				{/if}
			</div>
		{/if}
	</div>

	{#if editing?.shared}
		<p class="shared-note">
			{tf(
				'dashboard.editing_shared',
				'You are changing a shared dashboard. Everyone who sees it gets your changes when you click Done.'
			)}
		</p>
	{/if}

	<!-- Drawn once the account's dashboards are known (or failed, then the built-in one),
	     so the built-in dashboard doesn't flash before a saved one -->
	{#if !boards.loaded}
		<States kind="loading" message={t('status.checking')} />
	{:else if board}
		<BoardView
			{board}
			editing={!!editing}
			{data}
			{actions}
			boards={otherBoards}
			onmove={(id, steps) => change((b) => moveWidget(b, id, steps))}
			ondrop={(id, to) => change((b) => moveTo(b, id, to))}
			onresize={(id, size) => change((b) => updateWidget(b, id, { size }))}
			onconfigure={(id) => (configuring = id)}
			onremove={(id) => change((b) => removeWidget(b, id))}
		/>
	{/if}
</div>

{#if adding && editing}
	<AddWidgetDialog board={editing.draft} onadd={add} onclose={() => (adding = false)} />
{/if}

{#if configWidget}
	<WidgetDialog
		widget={configWidget}
		boards={otherBoards.map((b) => ({ key: b.key, name: b.board.name }))}
		onapply={(c) => {
			change((b) => updateWidget(b, configWidget.id, c));
			configuring = null;
		}}
		onclose={() => {
			// A custom widget added and closed without settings stays; its empty state says what to do
			configuring = null;
		}}
	/>
{/if}

{#if boardForm}
	<BoardDialog
		title={formTitle}
		name={boardForm.kind === 'new'
			? ''
			: boardForm.kind === 'copy'
				? tf('dashboard.copy_name', 'Copy of {name}', { name: boardForm.tab.board.name })
				: boardForm.tab.board.name}
		sharing={boardForm.kind === 'share'
			? { roles: [], locked: false, home: false }
			: boardForm.kind === 'rename' && boardForm.tab.shared
				? boardForm.tab.shared
				: null}
		confirmLabel={boardForm.kind === 'share'
			? tf('dashboard.share', 'Share')
			: boardForm.kind === 'new'
				? tf('dashboard.create', 'Create')
				: t('common.save')}
		onsave={(name, sharing) => void submitBoard(name, sharing)}
		onclose={() => (boardForm = null)}
	/>
{/if}

{#if qrOpen && data.config}
	{#await qrDialog.open(() => (qrOpen = false)) then { default: ConfigQrDialog }}
		<ConfigQrDialog config={data.config} onclose={() => (qrOpen = false)} />
	{/await}
{/if}

{#if scriptOpen && data.config}
	{#await scriptSheet.open(() => (scriptOpen = false)) then { default: InstallScriptSheet }}
		<InstallScriptSheet config={data.config} onclose={() => (scriptOpen = false)} />
	{/await}
{/if}

{#if pairOpen}
	{#await pairDialog.open(() => (pairOpen = false)) then { default: PairCodeDialog }}
		<PairCodeDialog
			host={pairHost}
			onclose={() => (pairOpen = false)}
			onscan={() => {
				pairOpen = false;
				activationOpen = true;
			}}
		/>
	{/await}
{/if}

{#if addOpen}
	{#await addComputer.open(closeAddComputer) then { default: AddComputerDialog }}
		<AddComputerDialog {folders} onclose={closeAddComputer} />
	{/await}
{/if}

{#if activationOpen}
	{#await activation.open(() => (activationOpen = false)) then { default: AgentActivation }}
		<AgentActivation onclose={() => (activationOpen = false)} />
	{/await}
{/if}

<style>
	.dashboard-page {
		display: flex;
		flex-direction: column;
		gap: var(--space-5);
	}

	/* Tabs on the left, Customize and the menu at the end of the same line */
	.board-bar {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-2) var(--space-4);
	}
	.board-bar > :global(.board-tabs) {
		flex: 1;
		min-width: 0;
	}
	.board-actions {
		margin-inline-start: auto;
	}
	.board-bar.is-editing {
		position: sticky;
		top: var(--topbar-h);
		z-index: var(--layer-sticky);
		padding-block: var(--space-2);
		background: var(--bg);
		border-bottom: 1px solid var(--rule);
	}
	.edit-hint {
		flex: 1;
		min-width: 0;
		margin: 0;
		color: var(--text-muted);
	}
	.shared-note {
		margin: 0;
		color: var(--warning);
	}
</style>
