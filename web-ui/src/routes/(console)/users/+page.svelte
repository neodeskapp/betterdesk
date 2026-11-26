<!--
  Console users (/users): the accounts that can sign in, their role, which
  devices and organizations each can reach, and the user groups that share
  device access. A click on a row opens the user's page (/users/<id>); the
  row menu (lib/people/user-actions.ts, the same groups as on that page)
  keeps the quick edits in sheets. Panel access (which console
  sections an account or a group may read or change, and its client
  features) opens from the row menus in a sheet: read-only with user.view,
  editable with user.edit.

  Data: GET /api/users (one list), then per account its device count
  (/effective-scope) and organizations, four at a time; GET
  /api/panel/user-groups and GET /api/users/invites (invites not used yet,
  and whether the console can send mail) beside it. Actions are offered by permission:
  user.create (and a role the signed-in person may give) to add or invite
  by email and to withdraw an invite (which deletes the account that never
  signed in), user.edit to edit, reset passwords, send an invite again, see and end an account's signed-in devices
  (A239), disable and enable accounts and manage groups,
  user.security (never for yourself) to give a temporary password, ask for
  a new password at the next sign-in, unlock, require, reset or turn off
  two-factor sign-in and make recovery codes unusable, user.delete to
  delete (never yourself),
  org.manage_users to change organizations, user.impersonate to Sign in
  as an account (read-only; the dialog asks the server who may be viewed).

  Many accounts: the filters (role, organization, state, two-factor
  sign-in, sign-in method, last sign-in), the search and the sort live in
  the URL (lib/people/userList.ts): a reload or a shared link shows the
  same list, and Back undoes a filter. Rows can be selected (all shown at
  once with the header's box) for an action on several accounts
  (BulkDialog: it shows what happens to each before it runs); Import reads
  accounts from a CSV file (ImportSheet), Export CSV saves the accounts
  shown.

  URL: ?search= ?role= ?org=<id or none> ?state= ?2fa= ?method=
  ?inactive=30|60|90|never ?sort=<column>&dir=asc|desc; #user-groups goes
  to the user groups.
-->
<script lang="ts">
	import { tick, untrack } from 'svelte';
	import { SvelteMap } from 'svelte/reactivity';
	import { page } from '$app/state';
	import { pushState, replaceState } from '$app/navigation';
	import { ApiError } from '#lib/api/client.ts';
	import { goto } from '$app/navigation';
	import {
		deleteUserGroup,
		getEffectiveScope,
		listConsoleUsers,
		listInvites,
		listUserGroupRows,
		listUserOrgs,
		type ConsoleUser,
		type EffectiveScope,
		type PendingInvite,
		type UserGroupRow,
		type UserOrg
	} from '#lib/api/users.ts';
	import type { AccessSubject } from '#lib/api/access.ts';
	import { listOrgs, type Organization } from '#lib/api/orgs.ts';
	import type { BulkKind } from '#lib/api/users.ts';
	import { formatDate } from '#lib/i18n/format.ts';
	import { plural, t, tf } from '#lib/i18n/i18n.svelte.ts';
	import { link } from '#lib/nav.ts';
	import SessionsSheet from '#lib/people/SessionsSheet.svelte';
	import UserActionMenuItems from '#lib/people/UserActionMenuItems.svelte';
	import { runUserAction } from '#lib/people/run-user-action.ts';
	import { userActionGroups, type UserActionId } from '#lib/people/user-actions.ts';
	import NoAccess from '#lib/shell/NoAccess.svelte';
	import { takeIntent } from '#lib/shell/intent.svelte.ts';
	import {
		assignableRoles,
		DESCRIBED_ROLES,
		eachLimited,
		groupNote,
		providerOf,
		ROLE_ORDER,
		scopeText,
		signInStates,
		twoFactorStatus
	} from '#lib/people/users.ts';
	import {
		accountStates,
		applyView,
		exportCsv,
		isFiltered,
		METHOD_FILTERS,
		NO_ORG,
		onlySearchDiffers,
		sameView,
		saveCsv,
		STATE_FILTERS,
		TWO_FACTOR_FILTERS,
		twoFactorOf,
		urlWithView,
		viewFromParams,
		withoutFilters,
		type UserListView
	} from '#lib/people/userList.ts';
	import { reloadWhileShown } from '#lib/state/live.ts';
	import { myPresence } from '#lib/state/presence.svelte.ts';
	import { can, sectionWritable, session } from '#lib/state/session.svelte.ts';
	import Button from '#lib/ui/Button.svelte';
	import CountLine from '#lib/ui/CountLine.svelte';
	import Empty from '#lib/ui/Empty.svelte';
	import ListRow from '#lib/ui/ListRow.svelte';
	import MenuButton from '#lib/ui/MenuButton.svelte';
	import MenuDivider from '#lib/ui/MenuDivider.svelte';
	import MenuItem from '#lib/ui/MenuItem.svelte';
	import ReadOnlyHint from '#lib/ui/ReadOnlyHint.svelte';
	import RowList from '#lib/ui/RowList.svelte';
	import SearchField from '#lib/ui/SearchField.svelte';
	import Select from '#lib/ui/Select.svelte';
	import States from '#lib/ui/States.svelte';
	import Status from '#lib/ui/Status.svelte';
	import Table from '#lib/ui/Table.svelte';
	import Tag from '#lib/ui/Tag.svelte';
	import Time from '#lib/ui/Time.svelte';
	import Toolbar from '#lib/ui/Toolbar.svelte';
	import User from '#lib/ui/User.svelte';
	import { confirm } from '#lib/ui/dialogs.svelte.ts';
	import type { Column, SortState } from '#lib/ui/table.ts';
	import { toast } from '#lib/ui/toasts.svelte.ts';
	import { displayName, ICONS } from '#lib/util/display.ts';
	import AccessSheet from './AccessSheet.svelte';
	import BulkDialog from './BulkDialog.svelte';
	import DeleteUserDialog from './DeleteUserDialog.svelte';
	import ImportSheet from './ImportSheet.svelte';
	import ResetPasswordDialog from './ResetPasswordDialog.svelte';
	import SignInAsDialog from './SignInAsDialog.svelte';
	import UserGroupDialog from './UserGroupDialog.svelte';
	import UserOrgsSheet from './UserOrgsSheet.svelte';
	import UserSheet from './UserSheet.svelte';

	const allowed = can('user.view');
	/* With the Users section at read every write is refused, also creating and deleting */
	const canCreate = can('user.create') && sectionWritable() && assignableRoles(session.user?.role).length > 0;
	const canEdit = can('user.edit');
	const canDelete = can('user.delete') && sectionWritable();
	/* Sign in as: the server decides per account and the dialog says why not */
	const canViewAs = can('user.impersonate') && !session.impersonation;
	/** Sign-in security of other accounts */
	const canSecurity = can('user.security') && sectionWritable();
	/** Withdrawing an invite undoes Add user, so it asks what adding does */
	const canWithdraw = can('user.create') && sectionWritable();
	/* Actions on several accounts: each needs the permission of its route for one */
	const canBulkEdit = canEdit && sectionWritable();
	const canBulkRole = canBulkEdit && assignableRoles(session.user?.role).length > 0;
	const canBulkOrgs = can('org.manage_users') && sectionWritable();
	const selectable = canBulkEdit || canBulkOrgs || canSecurity || canDelete;
	const meId = String(session.user?.id ?? '');
	/** What the row menu offers (the user's page offers the same) */
	const rights = {
		meId,
		myRole: session.user?.role,
		canEdit,
		canDelete,
		canSecurity,
		canWithdraw,
		canViewAs,
		canTeamChat: can('chat.team')
	};

	/* ── Labels ── */

	function roleLabel(role: string): string {
		const key = `users.role_${role}`;
		const text = t(key);
		return text === key ? role : text;
	}
	function providerLabel(provider: string): string {
		const key = `users.provider_${provider}`;
		const text = t(key);
		return text === key ? provider : text;
	}

	/* ── Data ── */

	let users = $state.raw<ConsoleUser[]>([]);
	let loaded = $state(false);
	let loadError = $state<string | null>(null);
	let groups = $state.raw<UserGroupRow[]>([]);
	let groupsState = $state<'loading' | 'ok' | 'error'>('loading');
	/** Per account id: its device count, or 'error' */
	const scopes = new SvelteMap<string, EffectiveScope | 'error'>();
	/** Per account id: its organizations, or 'error' */
	const userOrgs = new SvelteMap<string, UserOrg[] | 'error'>();
	/** Per account id: its invite that was not used yet */
	let invites = $state.raw(new Map<string, PendingInvite>());
	/** The console has email settings, so invites can go out */
	let mailReady = $state(false);
	let generation = 0;
	/** The per-account requests of the last load; stopped by a new load or by leaving the page */
	let rowRequests: AbortController | null = null;

	async function loadUsers() {
		const gen = ++generation;
		rowRequests?.abort();
		const ctrl = new AbortController();
		rowRequests = ctrl;
		try {
			const list = await listConsoleUsers();
			if (gen !== generation) return;
			users = list;
			loadError = null;
			loaded = true;
			scopes.clear();
			userOrgs.clear();
			// The two extra columns, a few requests at a time
			void eachLimited(list, 4, async (u) => {
				if (ctrl.signal.aborted) return;
				const id = String(u.id);
				const [scope, orgs] = await Promise.all([
					getEffectiveScope(u.id, ctrl.signal).catch(() => 'error' as const),
					listUserOrgs(u.id, ctrl.signal).catch(() => 'error' as const)
				]);
				if (gen !== generation || ctrl.signal.aborted) return;
				scopes.set(id, scope);
				userOrgs.set(id, orgs);
			});
		} catch (err) {
			if (gen !== generation) return;
			loaded = true;
			loadError = err instanceof ApiError && err.status === 403 ? err.message : t('users.list_failed');
		}
	}

	async function loadInvites() {
		try {
			const list = await listInvites();
			invites = new Map(list.invites.map((i) => [String(i.user_id), i]));
			mailReady = list.mail_ready;
		} catch {
			invites = new Map();
		}
	}

	async function loadGroups() {
		try {
			groups = await listUserGroupRows();
			groupsState = 'ok';
		} catch {
			groups = [];
			groupsState = 'error';
		}
	}

	$effect(() => {
		if (!allowed) return;
		untrack(() => {
			void loadUsers();
			void loadGroups();
			void loadInvites();
		});
		// Others add and change accounts: the list loads again every 30 s while on screen
		const stop = reloadWhileShown(() => Promise.all([loadUsers(), loadGroups(), loadInvites()]));
		return () => {
			stop();
			rowRequests?.abort();
		};
	});

	/* ── Organizations, for their filter and the actions ── */

	let orgList = $state.raw<Organization[]>([]);
	$effect(() => {
		if (!allowed) return;
		const ctrl = new AbortController();
		listOrgs(ctrl.signal).then(
			(list) => (orgList = list),
			() => (orgList = [])
		);
		return () => ctrl.abort();
	});

	/* ── The view: search, filters and sort, in the URL (lib/people/userList.ts) ── */

	const startView = untrack(() => viewFromParams(page.url.searchParams));
	let view = $state<UserListView>(startView);
	let searchInput = $state(startView.search);
	let sort = $state<SortState | null>(startView.sort);

	// Typing stays instant; the list follows a moment later
	$effect(() => {
		const v = searchInput.trim();
		if (v === untrack(() => view.search)) return;
		const timer = setTimeout(() => (view = { ...untrack(() => view), search: v }), 150);
		return () => clearTimeout(timer);
	});
	// A click on a column header sorts: the view follows the table
	$effect(() => {
		const s = sort;
		untrack(() => {
			if (s?.column !== view.sort?.column || s?.dir !== view.sort?.dir) view = { ...view, sort: s };
		});
	});

	// pushState and replaceState change the address bar and page.state but not
	// page.url, so the view is read from the address bar after each of them
	const urlView = $derived.by(() => {
		void page.url;
		void page.state;
		return viewFromParams(new URL(location.href).searchParams);
	});
	// The URL changed (Back, Forward, a link): the list follows
	$effect(() => {
		const u = urlView;
		untrack(() => {
			if (sameView(u, view)) return;
			view = u;
			searchInput = u.search;
			sort = u.sort;
		});
	});
	// The view changed: the URL follows, a history entry per filter; typing replaces it
	$effect(() => {
		const v = view;
		untrack(() => {
			const current = viewFromParams(new URL(location.href).searchParams);
			if (sameView(v, current)) return;
			const next = urlWithView(new URL(location.href), v);
			const state: { usersView?: boolean } = { usersView: true };
			// This page's own address with another query: resolve() is for route paths
			/* eslint-disable svelte/no-navigation-without-resolve */
			if (onlySearchDiffers(v, current)) replaceState(next, page.state);
			else pushState(next, state);
			/* eslint-enable svelte/no-navigation-without-resolve */
		});
	});

	function setFilter(change: Partial<UserListView>) {
		view = { ...view, ...change };
	}
	function clearFilters() {
		searchInput = '';
		view = withoutFilters(view);
	}

	const viewFacts = $derived({
		roleLabel,
		invites,
		orgsOf: (id: string) => userOrgs.get(id)
	});
	const shown = $derived(applyView(users, view, viewFacts));
	const filtered = $derived(isFiltered(view));

	/* Filter choices: roles and sign-in methods the list has (and the one chosen) */
	const roleOptions = $derived([
		{ value: '', label: t('users.filter_role_any') },
		...ROLE_ORDER.filter((r) => r === view.role || users.some((u) => u.role === r)).map((r) => ({
			value: r as string,
			label: roleLabel(r)
		}))
	]);
	const orgOptions = $derived([
		{ value: '', label: t('users.filter_org_any') },
		{ value: NO_ORG, label: t('users.filter_org_none') },
		...orgList.map((o) => ({ value: String(o.id), label: o.name || String(o.id) }))
	]);
	const stateOptions = $derived([
		{ value: '' as const, label: t('users.filter_state_any') },
		...STATE_FILTERS.map((s) => ({ value: s, label: t(`users.filter_state_${s}`) }))
	]);
	const twoFactorOptions = $derived([
		{ value: '' as const, label: t('users.filter_2fa_any') },
		...TWO_FACTOR_FILTERS.map((s) => ({ value: s, label: t(`users.filter_2fa_${s}`) }))
	]);
	const methodOptions = $derived([
		{ value: '' as const, label: t('users.filter_method_any') },
		...METHOD_FILTERS.map((s) => ({ value: s, label: t(`users.filter_method_${s}`) }))
	]);
	const showMethods = $derived(!!view.method || users.some((u) => providerOf(u) !== 'local'));
	const inactiveOptions = $derived([
		{ value: '' as const, label: t('users.filter_last_any') },
		{ value: '30' as const, label: t('users.inactive_days', { days: 30 }) },
		{ value: '60' as const, label: t('users.inactive_days', { days: 60 }) },
		{ value: '90' as const, label: t('users.inactive_days', { days: 90 }) },
		{ value: 'never' as const, label: t('users.inactive_never') }
	]);

	/* ── Selection and actions on several accounts ── */

	let selection = $state.raw(new Set<string>());
	/** The selected accounts the list shows: a filter that hides one leaves it out */
	const selected = $derived(selection.size ? shown.filter((u) => selection.has(String(u.id))) : []);
	let bulkFor = $state.raw<{ kind: BulkKind; users: ConsoleUser[] } | null>(null);

	function clearSelection() {
		selection = new Set();
	}
	function openBulk(kind: BulkKind) {
		bulkFor = { kind, users: selected };
	}

	/* ── Import and export ── */

	let importing = $state(false);

	/** The accounts shown, with the columns of the table as plain text */
	function exportShown() {
		const date = new Date().toISOString().slice(0, 10);
		const states = (u: ConsoleUser) =>
			accountStates(u, invites.get(String(u.id)))
				.map((s) => t(`users.filter_state_${s}`))
				.join('; ');
		const orgsText = (u: ConsoleUser) => {
			const orgs = userOrgs.get(String(u.id));
			return Array.isArray(orgs) ? orgs.map((o) => o.name).join('; ') : '';
		};
		const scope = (u: ConsoleUser) => {
			const s = scopeText(scopes.get(String(u.id)));
			return s.key ? t(s.key, s.params) : '';
		};
		saveCsv(
			`users-${date}.csv`,
			exportCsv(shown, [
				{ label: t('users.username'), value: (u) => u.username },
				{ label: t('users.email'), value: (u) => u.email },
				{ label: t('users.role'), value: (u) => roleLabel(u.role) },
				{ label: t('users.export_method'), value: (u) => providerLabel(providerOf(u)) },
				{ label: t('users.user_groups'), value: (u) => u.user_groups.map(groupName).join('; ') },
				{ label: t('users.column_devices'), value: scope },
				{ label: t('users.column_organizations'), value: orgsText },
				{ label: t('nav.account_2fa'), value: (u) => t(`users.filter_2fa_${twoFactorOf(u)}`) },
				{ label: t('users.export_state'), value: states },
				{ label: t('users.last_login'), value: (u) => u.last_login ?? '' },
				{ label: t('users.created'), value: (u) => u.created_at ?? '' }
			])
		);
	}

	/* ── #user-groups ── */

	// The section is drawn once the list and the groups are in: go there then, once
	let wentToGroups = false;
	$effect(() => {
		if (!allowed || !loaded || groupsState === 'loading' || wentToGroups) return;
		if (location.hash !== '#user-groups') return;
		wentToGroups = true;
		void tick().then(() =>
			requestAnimationFrame(() => {
				const section = document.getElementById('user-groups');
				section?.scrollIntoView({ block: 'start' });
				(
					section?.querySelector<HTMLElement>('.add-group') ?? section?.querySelector<HTMLElement>('h2')
				)?.focus();
			})
		);
	});

	/* ── Sheets and dialogs ── */

	let editing = $state<{ user: ConsoleUser | null } | null>(null);
	// Add user chosen in the search (Ctrl K)
	$effect(() => {
		if (takeIntent('add-user') && canCreate) editing = { user: null };
	});
	let resetFor = $state.raw<ConsoleUser | null>(null);
	let deleteFor = $state.raw<ConsoleUser | null>(null);
	let orgsFor = $state.raw<ConsoleUser | null>(null);
	let sessionsFor = $state.raw<ConsoleUser | null>(null);
	let viewAsFor = $state.raw<ConsoleUser | null>(null);
	let groupDialog = $state<{ group: UserGroupRow | null } | null>(null);
	let accessFor = $state.raw<{ subject: AccessSubject; name: string; role?: string } | null>(null);

	/** A click on a row: the user's page */
	function openRow(u: ConsoleUser) {
		// link() adds the base; resolve() is for route paths
		// eslint-disable-next-line svelte/no-navigation-without-resolve
		void goto(link(`/users/${encodeURIComponent(String(u.id))}`));
	}

	/** An item of the row menu: a sheet or dialog of this page, or runUserAction */
	async function act(id: UserActionId, u: ConsoleUser) {
		switch (id) {
			case 'open':
				openRow(u);
				return;
			case 'edit':
				editing = { user: u };
				return;
			case 'orgs':
				orgsFor = u;
				return;
			case 'access':
				accessFor = { subject: { kind: 'user', id: u.id }, name: displayName(u), role: u.role };
				return;
			case 'sign_in_as':
				viewAsFor = u;
				return;
			case 'reset_password':
				resetFor = u;
				return;
			case 'sessions':
				sessionsFor = u;
				return;
			case 'delete':
				deleteFor = u;
				return;
		}
		const changed = await runUserAction(id, u);
		if (changed === 'user') void loadUsers();
		if (changed === 'invites') void loadInvites();
		if (changed === 'gone') {
			void loadUsers();
			void loadInvites();
		}
	}

	async function removeGroup(g: UserGroupRow) {
		const name = g.name || g.guid;
		const ok = await confirm({
			title: t('users.delete_user_group'),
			message: t('users.delete_user_group_confirm', { name }),
			confirmLabel: t('users.delete_user_group'),
			confirmIcon: ICONS.delete,
			danger: true
		});
		if (!ok) return;
		try {
			await deleteUserGroup(g.guid);
			toast.success(t('users.group_deleted_named', { name }));
			await loadGroups();
			void loadUsers();
			// The row and its menu are gone; focus stays in the section
			document.querySelector<HTMLElement>('#user-groups .add-group')?.focus();
		} catch (err) {
			toast.error(err instanceof Error ? err.message : t('users.group_delete_failed', { name }));
		}
	}

	/* ── Columns ── */

	const hasTwoFactor = $derived(users.some((u) => u.totp_enabled !== undefined && u.totp_enabled !== null));

	const columns = $derived<Column<ConsoleUser>[]>([
		{
			id: 'username',
			label: t('users.username'),
			cell: nameCell,
			primary: true,
			sortValue: (u) => u.username.toLowerCase(),
			class: 'col-name'
		},
		{ id: 'email', label: t('users.email'), cell: emailCell, sortValue: (u) => u.email.toLowerCase() },
		{
			id: 'role',
			label: t('users.role'),
			cell: roleCell,
			sortValue: (u) => roleLabel(u.role),
			class: 'col-nowrap'
		},
		// These two fill in after the list, one request per account: their width is
		// kept from the start so the rows don't grow and move when the text arrives
		{
			id: 'devices',
			label: t('users.column_devices'),
			cell: scopeCell,
			class: 'col-nowrap col-devices',
			minWidth: '8rem'
		},
		{
			id: 'orgs',
			label: t('users.column_organizations'),
			cell: orgsCell,
			minWidth: '12rem',
			class: 'col-orgs'
		},
		{
			id: 'two_factor',
			label: t('nav.account_2fa'),
			cell: twoFactorCell,
			visible: hasTwoFactor,
			sortValue: (u) =>
				u.two_factor_state === 'on' || u.totp_enabled ? 2 : u.two_factor_state === 'required' ? 1 : 0,
			class: 'col-nowrap'
		},
		{
			id: 'last_login',
			label: t('users.last_login'),
			cell: lastCell,
			sortValue: (u) => u.last_login ?? '',
			class: 'col-nowrap'
		},
		{
			id: 'created',
			label: t('users.created'),
			cell: createdCell,
			sortValue: (u) => u.created_at ?? '',
			class: 'col-nowrap col-created'
		},
		{ id: 'actions', label: t('users.actions'), labelHidden: true, cell: actionsCell, align: 'actions' }
	]);

	const groupName = (guid: string) => groups.find((g) => g.guid === guid)?.name || guid;
</script>

{#snippet nameCell(u: ConsoleUser)}
	{@const self = String(u.id) === meId}
	<span class="user-name"
		><User
			user={u}
			avatar
			presence={self ? myPresence.status : undefined}
			presenceLabel={self ? t(`chat.presence_${myPresence.status}`) : undefined}
		/></span
	>
	{#if displayName(u) !== u.username}<span class="cell-sub username">{u.username}</span>{/if}
	{#if u.user_groups.length}<span class="cell-sub"
			>{t('users.groups_line', { groups: u.user_groups.map(groupName).join(', ') })}</span
		>{/if}
	{#if self}<span class="cell-sub">{t('users.this_is_you')}</span>{/if}
	{#if u.disabled}<span class="cell-sub"><Status tone="warning" label={t('users.account_disabled')} /></span
		>{/if}
	{#each signInStates(u) as state (state.key)}<span class="cell-sub"
			><Status
				tone={state.tone}
				label={t(state.key)}
				title={state.titleKey ? t(state.titleKey, { date: formatDate(state.date, {}, '') }) : undefined}
			/></span
		>{/each}
	{#if u.expires_at}<span class="cell-sub"
			>{#if u.expired}<Status
					tone="warning"
					label={t('users.expired_status')}
					title={t('users.expired_on', { date: formatDate(u.expires_at, {}, '') })}
				/>{:else}{t('users.expires_on', {
					date: formatDate(u.expires_at, { hour: undefined, minute: undefined }, '')
				})}{/if}</span
		>{/if}
	{@const invite = invites.get(String(u.id))}
	{#if invite && !u.last_login}<span class="cell-sub"
			><Status
				tone={invite.expired ? 'warning' : 'waiting'}
				label={invite.expired ? t('users.invite_expired') : t('users.invite_pending')}
			/></span
		><span class="cell-sub invite-until"
			>{t(invite.expired ? 'users.invite_expired_on' : 'users.invite_open_until', {
				date: formatDate(invite.expires_at, {}, '')
			})}</span
		>{/if}
{/snippet}

{#snippet emailCell(u: ConsoleUser)}{#if u.email}{u.email}{:else}<Empty />{/if}{#if u.email_pending}<span
			class="cell-sub email-pending"
			><Status
				tone={u.email_pending.expired ? 'warning' : 'waiting'}
				label={t('users.email_pending', { email: u.email_pending.email })}
			/></span
		>{/if}{/snippet}

{#snippet roleCell(u: ConsoleUser)}
	{@const provider = providerOf(u)}
	<span class="tag-set"
		><Tag label={roleLabel(u.role)} />{#if provider !== 'local'}<Tag
				label={providerLabel(provider)}
				title={t('users.provider_account', { provider: providerLabel(provider) })}
			/>{/if}</span
	>
{/snippet}

{#snippet scopeCell(u: ConsoleUser)}
	{@const s = scopeText(scopes.get(String(u.id)))}
	{#if !s.key}<span class="text-muted" aria-hidden="true">…</span><span class="sr-only"
			>{t('common.loading')}</span
		>{:else}<span class={[s.muted && 'text-muted']}>{t(s.key, s.params)}</span>{/if}
{/snippet}

{#snippet orgsCell(u: ConsoleUser)}
	{@const orgs = userOrgs.get(String(u.id))}
	{#if orgs === undefined}<span class="text-muted" aria-hidden="true">…</span><span class="sr-only"
			>{t('common.loading')}</span
		>{:else if orgs === 'error'}<Empty word={t('users.orgs_unknown')} />{:else if !orgs.length}<Empty
			word={t('users.orgs_none')}
		/>{:else}{orgs.map((o) => o.name).join(', ')}{/if}
{/snippet}

{#snippet twoFactorCell(u: ConsoleUser)}{@const s = twoFactorStatus(u)}<Status
		tone={s.tone}
		label={t(s.key)}
	/>{/snippet}

{#snippet lastCell(u: ConsoleUser)}<Time value={u.last_login} empty={t('users.never')} />{/snippet}

{#snippet createdCell(u: ConsoleUser)}<Time
		value={u.created_at}
		relative={false}
		format={{ hour: undefined, minute: undefined }}
	/>{/snippet}

{#snippet actionsCell(u: ConsoleUser)}
	{@const groups = userActionGroups(u, rights, {
		mailReady,
		invited: invites.has(String(u.id)),
		withOpen: true
	})}
	<!-- Grouped: the account, what it may reach, its sign-in security, and what ends it -->
	<MenuButton kebab label={t('users.actions_for', { username: displayName(u) })}>
		<UserActionMenuItems {groups} onselect={(id) => void act(id, u)} />
	</MenuButton>
{/snippet}

{#if !allowed}
	<NoAccess />
{:else}
	<div class="page page-wide users-page">
		<div class="page-head">
			<h1>{t('nav.users')}</h1>
			{#if canCreate}
				<div class="page-actions">
					<Button class="import-users" icon={ICONS.upload} onclick={() => (importing = true)}
						>{t('users.import_button')}</Button
					>
					<Button
						class="add-user"
						variant="primary"
						icon={ICONS.add}
						onclick={() => (editing = { user: null })}>{t('users.add_user')}</Button
					>
				</div>
			{/if}
			<p class="page-description">{t('users.page_description')}</p>
		</div>
		<ReadOnlyHint writable={canCreate || canEdit || canDelete} />

		<div class="page-cols">
			<div class="page-main">
				<section class="section" aria-labelledby="users-list-title">
					<h2 id="users-list-title" class="sr-only">{t('nav.users')}</h2>
					<Toolbar class="users-filters">
						{#snippet search()}<SearchField
								label={t('users.search_label')}
								bind:value={searchInput}
							/>{/snippet}
						<Select
							class="role-filter"
							aria-label={t('users.filter_role')}
							options={roleOptions}
							value={view.role}
							onchange={(e) => setFilter({ role: e.currentTarget.value })}
						/>
						{#if orgList.length}
							<Select
								class="org-filter"
								aria-label={t('users.filter_org')}
								options={orgOptions}
								value={view.org}
								onchange={(e) => setFilter({ org: e.currentTarget.value })}
							/>
						{/if}
						<Select
							class="state-filter"
							aria-label={t('users.filter_state')}
							options={stateOptions}
							value={view.state}
							onchange={(e) => setFilter({ state: e.currentTarget.value as UserListView['state'] })}
						/>
						{#if hasTwoFactor}
							<Select
								class="two-factor-filter"
								aria-label={t('users.filter_2fa')}
								options={twoFactorOptions}
								value={view.twoFactor}
								onchange={(e) => setFilter({ twoFactor: e.currentTarget.value as UserListView['twoFactor'] })}
							/>
						{/if}
						{#if showMethods}
							<Select
								class="method-filter"
								aria-label={t('users.filter_method')}
								options={methodOptions}
								value={view.method}
								onchange={(e) => setFilter({ method: e.currentTarget.value as UserListView['method'] })}
							/>
						{/if}
						<Select
							class="inactive-filter"
							aria-label={t('users.inactive_label')}
							options={inactiveOptions}
							value={view.inactive}
							onchange={(e) => setFilter({ inactive: e.currentTarget.value as UserListView['inactive'] })}
						/>
						{#if filtered}
							<Button class="clear-filters" size="sm" variant="ghost" onclick={clearFilters}
								>{t('users.filters_clear')}</Button
							>
						{/if}
					</Toolbar>
					<div class="list-head">
						{#if selected.length}
							<span aria-live="polite"
								><CountLine text={t('users.bulk_selected')} values={{ count: selected.length }} /></span
							>
							<div class="bulk">
								<MenuButton class="bulk-actions" size="sm" label={t('users.bulk_actions')}>
									{#if canBulkRole || canBulkEdit}
										<MenuGroup label={t('users.menu_group_account')}>
											{#if canBulkRole}
												<MenuItem class="bulk-role" icon={ICONS.edit} onselect={() => openBulk('role')}
													>{t('users.bulk_role_menu')}</MenuItem
												>
											{/if}
											{#if canBulkEdit}
												<MenuItem class="bulk-enable" icon={ICONS.enable} onselect={() => openBulk('enable')}
													>{t('users.bulk_enable_menu')}</MenuItem
												>
											{/if}
										</MenuGroup>
									{/if}
									{#if canBulkOrgs}
										<MenuGroup label={t('users.menu_group_access')}>
											<MenuItem
												class="bulk-org-add"
												icon={ICONS.organization}
												onselect={() => openBulk('organization-add')}>{t('users.bulk_org_add_menu')}</MenuItem
											>
											<MenuItem
												class="bulk-org-remove"
												icon={ICONS.unlink}
												onselect={() => openBulk('organization-remove')}
												>{t('users.bulk_org_remove_menu')}</MenuItem
											>
										</MenuGroup>
									{/if}
									{#if canSecurity || (canBulkEdit && mailReady)}
										<MenuGroup label={t('users.menu_group_security')}>
											{#if canSecurity}
												<MenuItem
													class="bulk-two-factor"
													icon={ICONS.password}
													onselect={() => openBulk('two-factor-required')}
													>{t('users.bulk_two_factor_menu')}</MenuItem
												>
												<MenuItem
													class="bulk-password-change"
													icon={ICONS.password}
													onselect={() => openBulk('require-password-change')}
													>{t('users.bulk_password_change_menu')}</MenuItem
												>
											{/if}
											{#if canBulkEdit && mailReady}
												<MenuItem
													class="bulk-reset-link"
													icon={ICONS.send}
													onselect={() => openBulk('password-reset-link')}
													>{t('users.bulk_reset_link_menu')}</MenuItem
												>
											{/if}
										</MenuGroup>
									{/if}
									{#if canBulkEdit || canDelete}
										<MenuGroup label={t('users.menu_group_danger')}>
											{#if canBulkEdit}
												<MenuItem
													class="bulk-disable"
													icon={ICONS.disable}
													onselect={() => openBulk('disable')}>{t('users.bulk_disable_menu')}</MenuItem
												>
											{/if}
											{#if canDelete}
												<MenuItem
													class="bulk-delete"
													icon={ICONS.delete}
													danger
													onselect={() => openBulk('delete')}>{t('users.bulk_delete_menu')}</MenuItem
												>
											{/if}
										</MenuGroup>
									{/if}
								</MenuButton>
								<Button class="bulk-clear" size="sm" variant="ghost" onclick={clearSelection}
									>{t('users.bulk_clear')}</Button
								>
							</div>
						{:else if loaded && !loadError}
							<span aria-live="polite">
								{#if filtered && shown.length !== users.length}
									<CountLine
										text={t('users.count_filtered')}
										values={{ shown: shown.length, count: users.length }}
									/>
								{:else}
									<CountLine
										text={plural(users.length, { one: t('users.count_one'), other: t('users.count_many') })}
									/>
								{/if}
							</span>
							{#if shown.length}
								<Button
									class="export-users"
									size="sm"
									variant="ghost"
									icon={ICONS.export}
									onclick={exportShown}>{t('users.export_csv')}</Button
								>
							{/if}
						{/if}
					</div>
					<div class="users-table">
						<Table
							rows={shown}
							{columns}
							key={(u) => String(u.id)}
							label={t('nav.users')}
							bind:sort
							{selectable}
							bind:selection
							selectLabel={(u) => t('table.select_row', { name: u.username })}
							onrowclick={openRow}
							loading={!loaded}
							loadingText={t('users.loading_list')}
							error={loadError}
							onretry={() => void loadUsers()}
							emptyText={!users.length ? t('users.empty_text') : t('users.filters_none_match')}
						/>
					</div>
					{#if loaded && !loadError && users.length && !shown.length}
						<div>
							<Button size="sm" onclick={clearFilters}>{t('users.filters_clear')}</Button>
						</div>
					{/if}
				</section>
			</div>

			<!-- Below the table on most screens: drawn once the list and the groups are
			     in, so it doesn't move down when they arrive -->
			{#if loaded && groupsState !== 'loading'}
				<aside class="page-side" aria-label={t('users.side_label')}>
					<section class="section" id="user-groups" aria-labelledby="user-groups-title">
						<div class="section-head">
							<h2 id="user-groups-title" tabindex="-1">{t('users.user_groups')}</h2>
							{#if canEdit}
								<Button
									class="add-group"
									size="sm"
									icon={ICONS.add}
									onclick={() => (groupDialog = { group: null })}>{t('users.create_user_group')}</Button
								>
							{/if}
						</div>
						<p class="section-note">{t('users.user_groups_note')}</p>
						{#if groupsState === 'error'}
							<States kind="error" message={t('users.groups_failed')} onretry={() => void loadGroups()} />
						{:else if !groups.length}
							<RowList><ListRow empty title={t('users.no_user_groups')} /></RowList>
						{:else}
							<RowList label={t('users.user_groups')}>
								{#each groups as g (g.guid)}
									{@const members = plural(g.member_count, {
										one: t('users.members_one'),
										other: t('users.members_many')
									})}
									{#snippet groupMenu()}<MenuButton
											kebab
											label={t('users.group_actions_for', { name: g.name || g.guid })}
										>
											<MenuItem
												class="act-access"
												icon={ICONS.access_policy}
												onselect={() =>
													(accessFor = { subject: { kind: 'group', guid: g.guid }, name: g.name || g.guid })}
												>{tf('users.access_menu', 'Panel access…')}</MenuItem
											>
											{#if canEdit}
												<MenuItem
													class="act-edit"
													icon={ICONS.edit}
													onselect={() => (groupDialog = { group: g })}>{t('users.edit')}</MenuItem
												>
												<MenuDivider />
												<MenuItem
													class="act-delete"
													icon={ICONS.delete}
													danger
													onselect={() => void removeGroup(g)}>{t('actions.delete')}</MenuItem
												>
											{/if}
										</MenuButton>{/snippet}
									<ListRow
										title={g.name || g.guid}
										sub={g.note ? `${members}, ${groupNote(g.note, t)}` : members}
										actions={groupMenu}
									/>
								{/each}
							</RowList>
						{/if}
					</section>

					<section class="section" aria-labelledby="users-roles-title">
						<div class="section-head"><h2 id="users-roles-title">{t('users.roles_title')}</h2></div>
						<dl class="roles">
							{#each DESCRIBED_ROLES as r (r)}
								<div>
									<dt>{roleLabel(r)}</dt>
									<dd>{t(`users.role_desc_${r}`)}</dd>
								</div>
							{/each}
						</dl>
						<p class="section-note">
							{t('users.role_note')}
							{#if session.isSuperAdmin}<a href={link('/permissions')}>{t('users.roles_change_link')}</a>{/if}
						</p>
					</section>
				</aside>
			{/if}
		</div>
	</div>

	{#if editing}
		<UserSheet
			user={editing.user}
			{groups}
			{roleLabel}
			{providerLabel}
			{mailReady}
			onclose={() => (editing = null)}
			onsaved={() => {
				editing = null;
				void loadUsers();
				void loadGroups();
				void loadInvites();
			}}
		/>
	{/if}

	{#if bulkFor}
		<BulkDialog
			kind={bulkFor.kind}
			users={bulkFor.users}
			roles={assignableRoles(session.user?.role)}
			{roleLabel}
			orgs={orgList}
			onclose={() => (bulkFor = null)}
			ondone={() => {
				bulkFor = null;
				clearSelection();
				void loadUsers();
				void loadGroups();
				void loadInvites();
			}}
		/>
	{/if}

	{#if importing}
		<ImportSheet
			{mailReady}
			onclose={() => (importing = false)}
			ondone={() => {
				importing = false;
				void loadUsers();
				void loadInvites();
			}}
		/>
	{/if}

	{#if deleteFor}
		<DeleteUserDialog
			user={deleteFor}
			{roleLabel}
			onclose={() => (deleteFor = null)}
			ondeleted={() => {
				deleteFor = null;
				void loadUsers();
				void loadGroups();
			}}
		/>
	{/if}

	{#if resetFor}
		<ResetPasswordDialog
			user={resetFor}
			{mailReady}
			security={canSecurity && String(resetFor.id) !== meId}
			{providerLabel}
			onclose={() => (resetFor = null)}
			onchanged={() => void loadUsers()}
		/>
	{/if}

	{#if sessionsFor}
		<SessionsSheet user={sessionsFor} onclose={() => (sessionsFor = null)} />
	{/if}

	{#if viewAsFor}
		<SignInAsDialog user={viewAsFor} onclose={() => (viewAsFor = null)} />
	{/if}

	{#if orgsFor}
		<UserOrgsSheet
			user={orgsFor}
			onclose={() => (orgsFor = null)}
			onchanged={(orgs) => orgsFor && userOrgs.set(String(orgsFor.id), orgs)}
		/>
	{/if}

	{#if accessFor}
		<AccessSheet
			subject={accessFor.subject}
			name={accessFor.name}
			role={accessFor.role}
			{roleLabel}
			onclose={() => (accessFor = null)}
		/>
	{/if}

	{#if groupDialog}
		<UserGroupDialog
			group={groupDialog.group}
			onclose={() => (groupDialog = null)}
			onsaved={() => {
				groupDialog = null;
				void loadGroups();
			}}
		/>
	{/if}
{/if}

<style>
	/* #user-groups scrolls here: the heading stays clear of the top bar */
	#user-groups {
		scroll-margin-top: calc(var(--topbar-h) + var(--space-4));
	}
	.list-head {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		justify-content: space-between;
		gap: var(--space-2) var(--space-4);
		min-height: var(--control-h-sm);
		margin: 0;
	}
	.bulk {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
	}
	.users-table :global(.user-name) {
		font-weight: 600;
	}
	/* Lines under a name start where the name does, after the avatar */
	.users-table :global(.cell-sub) {
		display: block;
		padding-inline-start: calc(24px + var(--space-2));
		color: var(--text-muted);
		font-size: var(--text-sm);
		font-weight: 400;
	}
	/* The email column has no avatar before it */
	.users-table :global(.cell-sub.email-pending) {
		padding-inline-start: 0;
	}
	.users-table :global(td.col-nowrap) {
		white-space: nowrap;
	}
	.tag-set {
		display: inline-flex;
		flex-wrap: wrap;
		gap: var(--space-1);
	}
	/* Narrower tables leave out the creation date and the device scope (set
	   in the user's sheet), then the organizations (their own sheet), so the
	   table fits without scrolling sideways under the sticky actions */
	.users-table {
		container: userstable / inline-size;
	}
	@container userstable (max-width: 1299px) {
		.users-table :global(:is(th, td):is(.col-created, .col-devices)) {
			display: none;
		}
	}
	@container userstable (max-width: 1099px) {
		.users-table :global(:is(th, td).col-orgs) {
			display: none;
		}
	}
	/* When the table is wider than the page, the row actions stay in view at the end */
	.users-table :global(:is(td, th).actions) {
		position: sticky;
		inset-inline-end: 0;
		background: var(--bg);
	}
	.roles {
		display: flex;
		flex-direction: column;
		gap: var(--space-3);
		margin: 0;
	}
	.roles dt {
		font-weight: 600;
	}
	.roles dd {
		margin: 0;
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
	h2[tabindex='-1']:focus {
		outline: none;
	}
	/* The table needs about 1,100 px for its columns: the side column goes
	   beside it only on wide screens; below that it follows the table, with
	   its sections next to each other where they fit */
	@media (max-width: 1679px) {
		.users-page .page-cols {
			grid-template-columns: minmax(0, 1fr);
		}
		.users-page .page-side {
			display: grid;
			grid-template-columns: repeat(auto-fit, minmax(min(100%, 340px), 1fr));
			gap: var(--space-6) var(--space-7);
			align-items: start;
		}
	}
	/* Touch: the name that opens the row gets a 44 px hit area (as on the device list) */
	@media (pointer: coarse) {
		.users-table :global(.row-open) {
			display: inline-flex;
			flex-direction: column;
			justify-content: center;
			min-height: 44px;
		}
	}
</style>
