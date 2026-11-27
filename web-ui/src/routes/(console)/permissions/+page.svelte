<!--
  Roles and permissions (/permissions, super admins only, as the server
  requires since commit 024540ff), on the settings pattern (PATTERN.md):
  the role is the scope and comes first, a picker of role names with what
  the chosen role can do beside it (one sentence, live with the unsaved
  changes, and the counts). The permissions are grouped by what a person
  does, each one line of name and sentence with its box; the key is not
  shown (rule E): the filter finds it and it is the name's title. On wide
  screens the compare value and "Which roles have this?" sit in the right
  column. Edit, then save: the ChangesBar lists every change, Save sends
  one batch, and the toast names what changed and offers Undo (the batch's
  `previous` sent back). A permission another one needs says so with a
  button that turns it on (in the same draft); one others need says which
  stop working while it is off. Permissions the code fixes for the role
  show off and locked with the reason. Views: only what differs from the
  default, a second role to compare with, and per permission which roles
  have it. Super admin roles always have everything (a note says so).

  URL: ?role= opens that role and follows the picker; #perm-<key> (the
  settings search) opens a permission's row.
-->
<script lang="ts">
	import { tick, untrack } from 'svelte';
	import { SvelteMap } from 'svelte/reactivity';
	import { page } from '$app/state';
	import { replaceState } from '$app/navigation';
	import {
		listOverrides,
		listRoles,
		roleEffective,
		saveRoleChanges,
		type PermissionNeed,
		type RoleInfo,
		type RoleOverride
	} from '#lib/api/roles.ts';
	import { i18n, t, tf } from '#lib/i18n/i18n.svelte.ts';
	import {
		batchBody,
		brokenBy,
		compareWith,
		defaultOn,
		differsFromDefault,
		draftValues,
		groupDesc,
		groupTitle,
		liveEffective,
		missingNeeds,
		overrideMap,
		permHelp,
		permissionGroups,
		permLabel,
		permRowId,
		pickRole,
		roleSummary,
		rolesWith,
		toggleValue,
		undoBody,
		type Ovr
	} from '#lib/people/permissions.ts';
	import { onRefresh } from '#lib/shell/page.svelte.ts';
	import { session } from '#lib/state/session.svelte.ts';
	import ChoiceList from '#lib/ui/ChoiceList.svelte';
	import CountLine from '#lib/ui/CountLine.svelte';
	import DangerZone from '#lib/ui/DangerZone.svelte';
	import Notice from '#lib/ui/Notice.svelte';
	import Select from '#lib/ui/Select.svelte';
	import States from '#lib/ui/States.svelte';
	import Switch from '#lib/ui/Switch.svelte';
	import { confirm } from '#lib/ui/dialogs.svelte.ts';
	import { toast } from '#lib/ui/toasts.svelte.ts';
	import ChangesBar from '#lib/ui/settings/ChangesBar.svelte';
	import SettingRow, { type Dependency } from '#lib/ui/settings/SettingRow.svelte';
	import SettingsGroup from '#lib/ui/settings/SettingsGroup.svelte';
	import SettingsPage from '#lib/ui/settings/SettingsPage.svelte';
	import { SettingsDraft, inWords, saveAndConfirm } from '#lib/ui/settings/draft.svelte.ts';
	import { SettingsFilter } from '#lib/ui/settings/filter.svelte.ts';
	import { roleLabel, roleOptions } from '#lib/ui/settings/pickers.ts';
	import { ICONS } from '#lib/util/display.ts';

	const allowed = session.isSuperAdmin;

	let roles = $state.raw<RoleInfo[]>([]);
	let all = $state.raw<string[]>([]);
	let requires = $state.raw<Record<string, PermissionNeed[]>>({});
	let overrides = $state.raw<RoleOverride[]>([]);
	/** What each role has now, as the server says */
	const effective = new SvelteMap<string, Set<string>>();
	let phase = $state<'loading' | 'ok' | 'error'>('loading');
	let role = $state('');
	let compareRole = $state('');
	let diffOnly = $state(false);
	let resetting = $state(false);
	/** Draws the role picker again when switching was cancelled */
	let pickerKey = $state(0);

	const filter = new SettingsFilter();
	const draft = new SettingsDraft<Record<string, Ovr>>({});
	$effect(() => draft.track());

	const current = $derived(roles.find((r) => r.name === role) ?? null);
	const groups = $derived(permissionGroups(all));
	const live = $derived(
		current
			? liveEffective(current, effective.get(role) ?? new Set(), draft.loaded, draft.values, all)
			: new Set<string>()
	);
	const overridden = $derived(all.filter((p) => (draft.values[p] ?? null) !== null).length);
	const stored = $derived([...overrideMap(overrides, role).keys()]);
	const other = $derived(compareRole ? (effective.get(compareRole) ?? new Set<string>()) : null);
	const otherRole = $derived(roles.find((r) => r.name === compareRole) ?? null);

	const yesNo = (on: boolean) =>
		on ? tf('permissions.allowed', 'allowed') : tf('permissions.not_allowed', 'not allowed');

	function lister(type: 'conjunction' | 'disjunction'): (items: string[]) => string {
		try {
			const f = new Intl.ListFormat(i18n.locale ?? 'en', { style: 'long', type });
			return (items) => f.format(items);
		} catch {
			return (items) => items.join(', ');
		}
	}

	const sentence = $derived(
		current && !current.is_super_admin
			? roleSummary(live, all, t, lister('conjunction'), lister('disjunction'))
			: ''
	);

	/** The draft's words for each permission: its name, and "allowed" or "default (allowed)" */
	function describe(r: RoleInfo) {
		draft.describe(
			Object.fromEntries(
				all.map((p) => [
					p,
					{
						label: permLabel(p, t),
						format: (v: unknown) =>
							v === null || v === undefined
								? tf('permissions.val_default', 'default ({value})', { value: yesNo(defaultOn(p, r)) })
								: typeof v === 'boolean'
									? yesNo(v)
									: inWords(v)
					}
				])
			)
		);
	}

	function openRole(name: string) {
		const r = roles.find((x) => x.name === name);
		draft.load(draftValues(all, overrideMap(overrides, name)));
		if (r) describe(r);
	}

	async function readRoles() {
		const [r, ovr] = await Promise.all([listRoles(), listOverrides()]);
		const perms = await Promise.all(r.roles.map((x) => roleEffective(x.name).catch(() => [] as string[])));
		roles = r.roles;
		all = r.all;
		requires = r.requires;
		overrides = ovr;
		effective.clear();
		r.roles.forEach((x, i) => effective.set(x.name, new Set(perms[i])));
	}

	async function load() {
		phase = 'loading';
		try {
			await readRoles();
			role = pickRole(
				roles,
				untrack(() => page.url.searchParams.get('role'))
			);
			if (compareRole === role || !roles.some((r) => r.name === compareRole)) compareRole = '';
			openRole(role);
			phase = 'ok';
		} catch {
			phase = 'error';
		}
	}

	/** Read one role again after a save, an undo or a reset */
	async function reloadRole(name: string) {
		const [ovr, perms] = await Promise.all([listOverrides(), roleEffective(name)]);
		overrides = ovr;
		effective.set(name, new Set(perms));
		if (name === role) openRole(name);
	}

	$effect(() => {
		if (!allowed) return;
		untrack(() => void load());
		return onRefresh(() => {
			if (!draft.dirty) void load();
		});
	});

	async function selectRole(name: string) {
		if (name === role) return;
		if (draft.dirty) {
			const ok = await confirm({
				title: tf('settings.page.discard_title', 'Discard unsaved changes?'),
				message: tf('settings.page.discard_message', 'The changes on this page are not saved yet.'),
				confirmLabel: tf('settings.page.discard_confirm', 'Discard changes'),
				danger: true
			});
			if (!ok) {
				pickerKey++;
				return;
			}
		}
		role = name;
		if (compareRole === name) compareRole = '';
		openRole(name);
		const url = new URL(location.href);
		url.searchParams.set('role', name);
		// This page's own address with another ?role=
		// eslint-disable-next-line svelte/no-navigation-without-resolve
		replaceState(url, page.state);
	}

	function setPerm(perm: string, on: boolean) {
		if (!current) return;
		draft.set(perm, toggleValue(on, draft.loaded[perm] ?? null, defaultOn(perm, current)));
	}

	async function save() {
		const name = role;
		let previous: { permission: string; granted: boolean | null }[] = [];
		await saveAndConfirm(
			draft,
			async (changes) => {
				const out = await saveRoleChanges(name, batchBody(changes));
				previous = out.previous;
				effective.set(name, new Set(out.permissions));
			},
			{
				undo: () => saveRoleChanges(name, undoBody(previous)),
				reload: () => reloadRole(name).catch(() => undefined)
			}
		);
	}

	async function resetAll() {
		if (!current || !stored.length) return;
		const name = role;
		const count = stored.length;
		const label = roleLabel(name, t);
		const message =
			(count === 1
				? tf(
						'permissions.confirm_reset_one',
						'Put the one changed permission of {role} back to its default? People with this role get the default from the next page they open.',
						{ role: label }
					)
				: tf(
						'permissions.confirm_reset_count',
						'Put {count} changed permissions of {role} back to their defaults? People with this role get the defaults from the next page they open.',
						{ role: label, count }
					)) +
			(draft.dirty ? ` ${tf('permissions.reset_unsaved', 'Your unsaved changes are dropped too.')}` : '');
		const ok = await confirm({
			title: tf('permissions.reset_overrides', 'Reset all changes'),
			message,
			confirmLabel: tf('permissions.reset_overrides', 'Reset all changes'),
			confirmIcon: ICONS.reset,
			danger: true
		});
		if (!ok) return;
		resetting = true;
		try {
			const out = await saveRoleChanges(
				name,
				stored.map((p) => ({ permission: p, granted: null }))
			);
			toast.show({
				type: 'success',
				message: tf('permissions.reset_done', '{role}: {count} permissions are back to their defaults.', {
					role: label,
					count
				}),
				action: {
					label: tf('settings_ui.undo', 'Undo'),
					undo: true,
					onclick: () => {
						void saveRoleChanges(name, undoBody(out.previous))
							.then(async () => {
								await reloadRole(name);
								toast.success(tf('settings_ui.undone', 'The changes are undone.'));
							})
							.catch((err: unknown) =>
								toast.error(
									`${tf('settings_ui.undo_failed', 'Undoing did not work.')} ${err instanceof Error ? err.message : ''}`.trim()
								)
							);
					}
				}
			});
		} catch (err) {
			toast.error(
				`${tf('permissions.save_failed', 'Could not save.')} ${err instanceof Error ? err.message : ''}`.trim()
			);
		} finally {
			resetting = false;
		}
		try {
			await reloadRole(name);
		} catch {
			phase = 'error';
		}
	}

	/** The notes under a row: what it needs, what stops working without it, and what the role can't do with it */
	function notesOf(perm: string, r: RoleInfo): Dependency[] {
		const out: Dependency[] = [];
		for (const n of missingNeeds(perm, live, requires, r.fixed)) {
			const name = permLabel(n.permission, t);
			const key = `permissions.needs_${n.reason}`;
			const text =
				t(key, { name }) === key
					? tf('permissions.needs_other', 'Needs {name}.', { name })
					: t(key, { name });
			out.push(
				n.fixable
					? {
							text,
							fixLabel: tf('permissions.turn_on', 'Turn on {name}', { name }),
							onfix: () => void turnOn(n.permission)
						}
					: { text: `${text} ${tf('permissions.needs_fixed', "It can't be turned on for this role.")}` }
			);
		}
		const broken = brokenBy(perm, live, requires, all);
		if (broken.length)
			out.push({
				text: tf('permissions.breaks', 'While this is off, these do nothing: {list}.', {
					list: lister('conjunction')(broken.map((p) => permLabel(p, t)))
				})
			});
		// server.keys creates keys only for a role that can give a role (api/auth_handlers.go handleCreateAPIKey)
		if (perm === 'server.keys' && live.has(perm) && r.fixed['user.create'] === 'assigns_no_role')
			out.push({
				text: tf(
					'permissions.keys_no_create',
					'This role can list and delete API keys but not create them: it can give no role.'
				)
			});
		return out;
	}

	/** Turn on a needed permission and move focus to its switch */
	async function turnOn(perm: string) {
		setPerm(perm, true);
		await tick();
		document.getElementById(`${permRowId(perm)}-control`)?.focus();
	}

	function whoLabel(name: string): string {
		const r = roles.find((x) => x.name === name);
		const label = roleLabel(name, t);
		if (r?.is_super_admin) return tf('permissions.who_always', '{role} (always)', { role: label });
		if (name === role && draft.dirty)
			return tf('permissions.who_unsaved', '{role} (with your unsaved changes)', { role: label });
		return label;
	}

	const pickerChoices = $derived(
		// Names only: the chosen role's own sentence is beside the picker
		roleOptions(roles, t).map((o) => ({ value: o.value, title: o.label }))
	);
	const compareOptions = $derived([
		{ value: '', label: tf('permissions.compare_none', 'No other role') },
		...roles.filter((r) => r.name !== role).map((r) => ({ value: r.name, label: roleLabel(r.name, t) }))
	]);
	const sections = $derived(
		current && !current.is_super_admin
			? groups.map((g) => ({ id: `perm-group-${g.id}`, label: groupTitle(g.id, t) }))
			: []
	);
	const shownGroups = $derived(
		current && !current.is_super_admin
			? groups
					.map((g) => ({
						...g,
						shown: g.permissions.filter(
							(p) => !diffOnly || differsFromDefault(p, current, live, draft.values)
						)
					}))
					.filter((g) => g.shown.length)
			: []
	);
</script>

<SettingsPage
	id="permissions"
	title={t('nav.permissions')}
	description={tf(
		'permissions.page_description',
		'A role is a set of permissions that everyone with that role has. Changes apply from the next page those people open.'
	)}
	{allowed}
	filter={current && !current.is_super_admin ? filter : undefined}
	filterLabel={tf('permissions.filter_label', 'Filter permissions')}
	{sections}
>
	{#snippet scope()}
		{#if phase === 'ok' && roles.length}
			<div class="perm-scope">
				<div id="perm-role" class="perm-role">
					<h2 id="perm-role-title" class="perm-scope-title">
						{tf('permissions.pick_role', 'Role to change')}
					</h2>
					{#key pickerKey}
						<ChoiceList
							choices={pickerChoices}
							labelledby="perm-role-title"
							name="perm-role"
							value={role}
							onchange={(v, on) => {
								if (on) void selectRole(v);
							}}
						/>
					{/key}
				</div>
				{#if current}
					<!-- What the role can do now, beside the picker: the first thing to read after picking -->
					<div class="perm-summary" aria-live="polite">
						<h2 class="perm-scope-title">
							{tf('permissions.summary_title_named', 'What {role} can do', { role: roleLabel(role, t) })}
						</h2>
						{#if current.is_super_admin}
							<p>{t('permissions.locked_desc')}</p>
						{:else}
							<p class="perm-sentence">{sentence}</p>
							{#if draft.dirty}<p class="perm-muted">
									{tf('permissions.summary_unsaved', 'With your unsaved changes.')}
								</p>{/if}
							<CountLine
								text={t('permissions.role_summary')}
								values={{ granted: live.size, total: all.length, changed: overridden }}
							/>
						{/if}
					</div>
				{/if}
			</div>
		{/if}
	{/snippet}

	{#if phase === 'loading'}
		<States kind="loading" message={t('permissions.loading')} />
	{:else if phase === 'error'}
		<States kind="error" message={t('permissions.load_failed')} onretry={() => void load()} />
	{:else if !current}
		<States message={t('permissions.empty_desc')} />
	{:else if current.is_super_admin}
		<Notice message={t('permissions.locked_desc')} />
	{:else}
		<!-- The views in one line under the filter: what differs, and a second role to compare with -->
		<div id="perm-views" class="perm-views" role="group" aria-labelledby="perm-views-title">
			<h2 id="perm-views-title" class="sr-only">{tf('permissions.views_title', 'Views')}</h2>
			<Switch
				id="perm-diff-only"
				label={tf('permissions.diff_only', 'Only show what differs from the default')}
				bind:checked={diffOnly}
			/>
			<div class="perm-compare-pick">
				<label for="perm-compare">{tf('permissions.compare_label', 'Compare with')}</label>
				<Select
					id="perm-compare"
					small
					describedby="perm-compare-hint"
					options={compareOptions}
					bind:value={compareRole}
				/>
				<span id="perm-compare-hint" class="sr-only"
					>{tf(
						'permissions.compare_hint',
						"Shows the other role's value under each permission. It can't be changed from here."
					)}</span
				>
			</div>
		</div>

		{#if diffOnly && !shownGroups.length}
			<p class="perm-muted">
				{tf('permissions.diff_none', 'Nothing differs from the default for this role.')}
			</p>
		{/if}

		{#each shownGroups as g (g.id)}
			<SettingsGroup
				id={`perm-group-${g.id}`}
				title={groupTitle(g.id, t)}
				description={groupDesc(g.id, t) || undefined}
			>
				{#each g.shown as perm (perm)}
					{@const fixedBy = current.fixed[perm]}
					{@const v = draft.values[perm] ?? null}
					{@const cmp = other ? compareWith(perm, live, other) : null}
					{@const who = rolesWith(perm, roles, effective, role, live)}
					<SettingRow
						id={permRowId(perm)}
						label={permLabel(perm, t)}
						help={permHelp(perm, t) || undefined}
						keyName={perm}
						terms={[groupTitle(g.id, t)]}
						class="perm-row"
						checked={live.has(perm)}
						disabled={!!fixedBy}
						onchange={(on) => setPerm(perm, on)}
						changed={draft.isChanged(perm)}
						source={v !== null
							? {
									kind: 'here',
									text: defaultOn(perm, current)
										? tf('permissions.src_changed_on', 'Changed for this role. The default is allowed.')
										: tf('permissions.src_changed_off', 'Changed for this role. The default is not allowed.')
								}
							: null}
						onreset={v !== null ? () => draft.set(perm, null) : undefined}
						resetLabel={tf('permissions.back_default', 'Back to the default')}
						noEffect={fixedBy ? t(`permissions.fixed_${fixedBy}`) : undefined}
						needs={notesOf(perm, current)}
					>
						<div class="perm-extra">
							{#if cmp && otherRole}
								<p class={['perm-compare', cmp.differs && 'is-different']}>
									<span
										>{tf('permissions.compare_value', '{role}: {value}', {
											role: roleLabel(otherRole.name, t),
											value: yesNo(cmp.other)
										})}</span
									>
									<span class="perm-compare-mark"
										>{cmp.differs
											? tf('permissions.compare_differs', 'Differs')
											: tf('permissions.compare_same', 'Same')}</span
									>
								</p>
							{/if}
							<details class="perm-who">
								<summary>{tf('permissions.who_has', 'Which roles have this?')}</summary>
								{#if who.length}
									<ul>
										{#each who as name (name)}<li>{whoLabel(name)}</li>{/each}
									</ul>
								{:else}
									<p>{tf('permissions.who_none', 'No role has it.')}</p>
								{/if}
							</details>
						</div>
					</SettingRow>
				{/each}
			</SettingsGroup>
		{/each}

		<!-- Only while the role has a saved change: a reset with nothing to reset is a dead button (rule F) -->
		{#if stored.length}
			<DangerZone
				title={t('permissions.danger_title')}
				rows={[
					{
						title: tf('permissions.reset_overrides', 'Reset all changes'),
						consequence: t('permissions.reset_overrides_desc'),
						action: t('permissions.reset_overrides_button'),
						onclick: () => void resetAll(),
						busy: resetting
					}
				]}
			/>
		{/if}
	{/if}

	{#snippet bar()}<ChangesBar {draft} onsave={save} />{/snippet}
</SettingsPage>

<style>
	.perm-scope {
		display: grid;
		grid-template-columns: minmax(0, 1fr);
		gap: var(--space-4) var(--space-6);
	}
	.perm-scope-title {
		margin: 0 0 var(--space-2);
		font-size: var(--text-md);
		font-weight: 600;
	}
	/* Names only, in columns: the whole picker is two or three lines */
	.perm-role :global(.choice-list) {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(min(100%, 160px), 1fr));
		column-gap: var(--space-4);
	}
	.perm-role :global(.choice) {
		padding-block: var(--space-2);
	}
	.perm-muted {
		margin: var(--space-2) 0 0;
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
	.perm-summary p {
		margin: 0 0 var(--space-2);
	}
	.perm-sentence {
		max-width: var(--measure);
	}
	/* The picker and what the role can do side by side once both fit */
	@media (min-width: 1100px) {
		.perm-scope {
			grid-template-columns: minmax(0, 3fr) minmax(0, 2fr);
		}
	}
	.perm-views {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-2) var(--space-6);
	}
	.perm-compare-pick {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-1) var(--space-2);
	}
	.perm-compare-pick label {
		font-weight: 600;
	}
	.perm-compare-pick :global(select) {
		width: auto;
		min-width: 12rem;
	}
	.perm-extra {
		display: flex;
		flex-wrap: wrap;
		align-items: baseline;
		gap: var(--space-1) var(--space-4);
		padding-inline-start: calc(var(--check-size) + var(--check-gap));
		font-size: var(--text-sm);
	}
	/* Two columns: the compare line and "Which roles have this?" go to the
	   right column beside the name, Reset under them, so a permission is
	   one line of name and help */
	@container settings-rows (min-width: 900px) {
		:global(.setting-row.perm-row) {
			--setting-control-w: 24rem;
			grid-template-rows: auto 1fr;
		}
		:global(.perm-row > .setting-text) {
			grid-row: 1 / span 2;
		}
		:global(.perm-row > .setting-extra) {
			grid-column: 2;
			grid-row: 1;
		}
		:global(.perm-row > .setting-actions) {
			grid-column: 2;
			grid-row: 2;
		}
		.perm-extra {
			padding-inline-start: 0;
		}
	}
	.perm-compare {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
		margin: 0;
		color: var(--text-muted);
	}
	/* A difference is said with a word, not only a colour */
	.perm-compare.is-different .perm-compare-mark {
		color: var(--text);
		font-weight: 600;
	}
	.perm-who summary {
		display: inline-flex;
		align-items: center;
		min-height: 28px;
		color: var(--link, var(--accent));
		cursor: pointer;
	}
	.perm-who ul {
		margin: var(--space-1) 0 0;
		padding-inline-start: var(--space-5);
	}
	.perm-who p {
		margin: var(--space-1) 0 0;
	}
	@media (pointer: coarse) {
		.perm-who summary {
			min-height: 44px;
		}
	}
</style>
