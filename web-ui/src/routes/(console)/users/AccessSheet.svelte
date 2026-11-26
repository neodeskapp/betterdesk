<!--
  Panel access of a user or a user group: per catalog group a setting for
  all its sections, then each section with none, read or full access (or
  no setting, so it falls back), and the client features. Under each row,
  what applies now and where it comes from; for a user, a warning where a
  setting goes above what the role allows (the role's limit still applies
  on the server). Hard-locked features are always off and have no control.
  A note says which sessions the client features reach, and some features
  have a hint for what they do beyond their name (FEATURE_HINTS).

  user.edit edits and saves the whole policy at once; with only user.view
  the sheet shows what applies now, without controls. A super admin keeps
  full access in every section whatever is set (the server applies no
  section setting to them), so for one the sections give way to a note
  and only the client features show.

  For a user, the "Role or group default" choice names what that default
  gives right now (PATTERN.md rule 2): where the setting comes from the
  role or a group, the level in force; where the person has their own
  setting, what their groups' stored rows and the role would give without
  it (lib/people/access-inherited.ts, the server's rules).

  Each row says its value once. While a choice is as saved, the choice
  shows the value ("Default: Read, from the role", or "Read") and the line
  under the name only what the choice does not: "Set for this person" and
  the role's limit. A changed choice gets "Now: ..." there, what applies
  until Save. Without user.edit the line says what applies now.
-->
<script lang="ts">
	import { tick, untrack } from 'svelte';
	import {
		getAccessCatalog,
		getAccessPolicy,
		saveAccessPolicy,
		type AccessLevel,
		type AccessCatalog,
		type AccessPolicy,
		type AccessSubject
	} from '#lib/api/access.ts';
	import { has, t, tf } from '#lib/i18n/i18n.svelte.ts';
	import {
		aboveRole,
		catalogGroups,
		FEATURE_HINTS,
		LEVEL_KEYS,
		sectionLabelKeys,
		sourceKey,
		toDraft,
		toPayload,
		type Draft
	} from '#lib/people/access.ts';
	import {
		inheritedFeature,
		inheritedSection,
		type InheritedFeature,
		type InheritedLevel
	} from '#lib/people/access-inherited.ts';
	import { can, session } from '#lib/state/session.svelte.ts';
	import Button from '#lib/ui/Button.svelte';
	import Empty from '#lib/ui/Empty.svelte';
	import Facts from '#lib/ui/Facts.svelte';
	import FormSection from '#lib/ui/FormSection.svelte';
	import Notice from '#lib/ui/Notice.svelte';
	import Select from '#lib/ui/Select.svelte';
	import Sheet, { type SheetParts } from '#lib/ui/Sheet.svelte';
	import States from '#lib/ui/States.svelte';
	import Tag from '#lib/ui/Tag.svelte';
	import { createForm, warnOnLeave } from '#lib/ui/form.svelte.ts';

	interface Props {
		subject: AccessSubject;
		/** The user's or group's name for the title */
		name: string;
		/** The user's role (users only), shown with the groups */
		role?: string;
		roleLabel?: (role: string) => string;
		onclose: () => void;
	}

	let { subject, name, role, roleLabel, onclose }: Props = $props();

	const uid = $props.id();
	const editable = can('user.edit');
	const kind = untrack(() => subject.kind);
	const isMe = untrack(
		() => subject.kind === 'user' && String(subject.id) === String(session.user?.id ?? '')
	);
	/** super_admin and admin: section settings do not apply to them */
	const superAdmin = untrack(() => kind === 'user' && (role === 'super_admin' || role === 'admin'));

	let catalog = $state.raw<AccessCatalog | null>(null);
	let effective = $state.raw<AccessPolicy['effective'] | null>(null);
	let loadError = $state('');
	let formEl = $state<HTMLFormElement>();
	/** The choices as loaded or last saved: a row whose choice differs says what applies until Save */
	let savedChoices = $state.raw<Draft | null>(null);

	const form = createForm<Draft>({
		initial: { sections: {}, groups: {}, features: {} },
		submit: async (values) => {
			if (!catalog) return;
			const saved = await saveAccessPolicy(subject, toPayload(catalog, values));
			effective = saved.effective;
			savedChoices = toDraft(catalog, saved);
			return savedChoices;
		},
		savedMessage: tf('users.access_saved', 'Panel access saved.')
	});

	$effect(() => warnOnLeave(() => form.dirty));

	/** Closing the sheet stops a load that is still running */
	const aborter = new AbortController();
	$effect(() => () => aborter.abort());

	async function load() {
		loadError = '';
		try {
			const [c, p] = await Promise.all([
				getAccessCatalog(aborter.signal),
				getAccessPolicy(subject, aborter.signal)
			]);
			catalog = c;
			effective = p.effective;
			savedChoices = toDraft(c, p);
			form.reset(savedChoices);
			if (kind === 'user') void loadGroupRows(p.effective.groups.map((g) => g.guid));
			// The sheet focused a footer button while loading; the first field is where work starts
			await tick();
			const active = document.activeElement;
			if (!active || active === document.body || active.closest('.sheet-footer'))
				formEl?.querySelector<HTMLElement>('select')?.focus();
		} catch (err) {
			if (aborter.signal.aborted) return;
			loadError =
				err instanceof Error && err.message
					? err.message
					: tf('users.access_load_failed', 'Could not load the panel access.');
		}
	}

	$effect(() => {
		untrack(() => void load());
	});

	/** The stored rows of the person's groups, for what their default gives (users only) */
	let groupRows = $state.raw<{
		sections: { resource_key: string; access_level: string }[];
		features: { feature_key: string; enabled: boolean }[];
	} | null>(null);

	async function loadGroupRows(guids: string[]) {
		try {
			const policies = await Promise.all(
				guids.filter(Boolean).map((guid) => getAccessPolicy({ kind: 'group', guid }, aborter.signal))
			);
			groupRows = {
				sections: policies.flatMap((p) => p.sections),
				features: policies.flatMap((p) => p.features)
			};
		} catch {
			// Without them the choice keeps its plain name where the person has their own setting
			groupRows = null;
		}
	}

	/** The catalog group the server resolves a section under (its last entry) */
	const sectionGroup = $derived(new Map((catalog?.sections ?? []).map((s) => [s.key, s.group])));

	function sectionDefault(key: string): InheritedLevel | null {
		const eff = effective?.sections[key];
		if (!eff || kind !== 'user') return null;
		if (eff.source === 'role' || eff.source === 'group')
			return { level: eff.access as AccessLevel, source: eff.source };
		if (!groupRows) return null;
		return inheritedSection(key, sectionGroup.get(key) ?? '', groupRows.sections, eff.role_ceiling);
	}

	function featureDefault(key: string, hardLock: boolean): InheritedFeature | null {
		const eff = effective?.features[key];
		if (!eff || kind !== 'user') return null;
		if (eff.source === 'default' || eff.source === 'group')
			return { enabled: eff.enabled, source: eff.source };
		if (!groupRows) return null;
		return inheritedFeature(key, groupRows.features, hardLock);
	}

	const groups = $derived(catalog ? catalogGroups(catalog) : []);

	/** The first of the keys that has a string, else the catalog's label */
	function label(keys: string | string[], fallback: string): string {
		for (const key of Array.isArray(keys) ? keys : [keys]) if (has(key)) return t(key);
		return fallback || String(keys);
	}

	const inheritLabel = $derived(
		kind === 'user' ? tf('users.access_default_short', 'Default') : tf('users.access_not_set', 'Not set')
	);
	/** "Default: On" for the built-in default, else "Default: Read, from the role" */
	function defaultLabel(label: string, level: string, source: string): string {
		return source === 'default'
			? tf('users.access_default_plain', '{label}: {level}', { label, level })
			: tf('users.access_default_is', '{label}: {level}, {source}', {
					label,
					level,
					source: sourceText(source)
				});
	}
	/** The choices of a section; for a user the default names its level and where it comes from */
	function levelOptions(def: InheritedLevel | null) {
		return [
			{
				value: 'inherit',
				label: def ? defaultLabel(inheritLabel, levelText(def.level), def.source) : inheritLabel
			},
			{ value: 'none', label: t('users.access_none') },
			{ value: 'read', label: t('users.access_read') },
			{ value: 'write', label: t('users.access_write') }
		];
	}
	function featureOptions(def: InheritedFeature | null) {
		const base = tf('users.access_feature_inherit', 'Default');
		return [
			{
				value: 'inherit',
				label: def ? defaultLabel(base, def.enabled ? t('common.on') : t('common.off'), def.source) : base
			},
			{ value: 'on', label: t('common.on') },
			{ value: 'off', label: t('common.off') }
		];
	}

	/** English for the source words until the server has loaded the new strings */
	const SOURCE_FALLBACK: Record<string, string> = {
		'users.access_source_user': 'set for this person',
		'users.access_source_group': 'from a group',
		'users.access_source_group_own': 'set for this group',
		'users.access_source_role': 'from the role',
		'users.access_source_unset': 'not set',
		'users.access_source_default': 'default'
	};
	function sourceText(source: string): string {
		const key = sourceKey(source, kind);
		return tf(key, SOURCE_FALLBACK[key] ?? source);
	}

	const levelText = (level: string | undefined) =>
		t(LEVEL_KEYS[level as keyof typeof LEVEL_KEYS] ?? 'users.access_none');

	/** What applies now and where from; for a user's own or group setting also what the role allows at most */
	function nowLine(level: string, source: string, limit?: string): string {
		const params = { level: levelText(level), source: sourceText(source) };
		return limit
			? tf('users.access_now_role', 'Now: {level}, {source}. Role limit: {limit}', {
					...params,
					limit: levelText(limit)
				})
			: tf('users.access_now', 'Now: {level}, {source}', params);
	}

	const ownText = () =>
		kind === 'user'
			? tf('users.access_own_user', 'Set for this person')
			: tf('users.access_own_group', 'Set for this group');

	/**
	 * The line under a section's name while editing: nothing new beside the
	 * choice while it is as saved, "Now: ..." once it was changed
	 */
	function sectionLine(
		key: string,
		eff: { access: string; source: string; role_ceiling?: string },
		value: string,
		above: boolean
	): string {
		const limit = kind === 'user' && !above && eff.source !== 'role' ? eff.role_ceiling : undefined;
		const saved = savedChoices?.sections[key] ?? 'inherit';
		if (value !== saved) return nowLine(eff.access, eff.source, limit);
		if (value === 'inherit')
			return limit ? tf('users.access_limit_only', 'Role limit: {limit}', { limit: levelText(limit) }) : '';
		return limit
			? tf('users.access_own_user_limit', 'Set for this person. Role limit: {limit}', {
					limit: levelText(limit)
				})
			: ownText();
	}

	function featureLine(key: string, eff: { enabled: boolean; source: string }, value: string): string {
		const saved = savedChoices?.features[key] ?? 'inherit';
		if (value !== saved) return featureNow(eff.enabled, eff.source);
		return value === 'inherit' ? '' : ownText();
	}

	function featureNow(enabled: boolean, source: string): string {
		return tf('users.access_now', 'Now: {level}, {source}', {
			level: enabled ? t('common.on') : t('common.off'),
			source: sourceText(source)
		});
	}

	const memberOf = $derived(effective?.groups.map((g) => g.name || g.guid).join(', ') ?? '');
</script>

{#snippet roleValue()}{#if role}<Tag label={roleLabel ? roleLabel(role) : role} />{:else}<Empty
		/>{/if}{/snippet}

{#snippet sectionRow(key: string, text: string, isGroup: boolean)}
	{@const eff = isGroup ? null : effective?.sections[key]}
	{@const value = isGroup ? form.values.groups[key] : form.values.sections[key]}
	{@const above = !!eff && kind === 'user' && !!value && aboveRole(value, eff.role_ceiling)}
	{@const line = !eff
		? ''
		: editable
			? sectionLine(key, eff, value ?? 'inherit', above)
			: nowLine(
					eff.access,
					eff.source,
					kind === 'user' && eff.source !== 'role' ? eff.role_ceiling : undefined
				)}
	<li class={['row', isGroup && 'is-group']} data-access={isGroup ? `group:${key}` : key}>
		<span class="text">
			{#if editable}
				<label class="name" for="{uid}-{isGroup ? 'g' : 's'}-{key}">{text}</label>
			{:else}
				<span class="name">{text}</span>
			{/if}
			{#if line}
				<span class="now" id="{uid}-s-{key}-now">{line}</span>
				{#if above}
					<span class="warn" id="{uid}-s-{key}-warn"
						>{tf('users.access_role_limit', 'The role allows {level} here; that limit still applies.', {
							level: levelText(eff.role_ceiling)
						})}</span
					>
				{/if}
			{/if}
		</span>
		{#if editable}
			{#if isGroup}
				<Select id="{uid}-g-{key}" small options={levelOptions(null)} bind:value={form.values.groups[key]} />
			{:else}
				<Select
					id="{uid}-s-{key}"
					small
					options={levelOptions(sectionDefault(key))}
					aria-describedby={[line && `${uid}-s-${key}-now`, above && `${uid}-s-${key}-warn`]
						.filter(Boolean)
						.join(' ') || undefined}
					bind:value={form.values.sections[key]}
				/>
			{/if}
		{/if}
	</li>
{/snippet}

{#snippet content()}
	{#if loadError}
		<States kind="error" message={loadError} onretry={() => void load()} />
	{:else if !catalog}
		<States kind="loading" message={t('users.loading_access_policy')} />
	{:else}
		<form id={form.id} class="access-form" bind:this={formEl} onsubmit={form.onsubmit} novalidate>
			<p class="note">
				{kind === 'user'
					? tf(
							'users.access_user_note',
							"A setting here wins over this person's groups. Without one, the highest setting of their groups applies, then their role."
						)
					: tf(
							'users.access_group_note',
							'Members get the highest level any of their groups gives, unless their own setting says otherwise.'
						)}
			</p>
			{#if isMe && editable && !superAdmin}
				<Notice
					tone="warning"
					message={tf(
						'users.access_self_warning',
						'These settings apply to your own account. If you take away your access to Users, you cannot change them back yourself.'
					)}
				/>
			{/if}
			{#if kind === 'user'}
				{#snippet groupsValue()}{#if memberOf}{memberOf}{:else}<Empty />{/if}{/snippet}
				<Facts
					items={[
						{ label: t('users.role'), value: roleValue },
						{ label: t('users.user_groups'), value: groupsValue }
					]}
				/>
			{/if}

			{#if superAdmin}
				<Notice
					message={tf(
						'users.access_super_admin_note',
						'Super admins always have full access to every section, so section settings do not apply to them. Client features still do.'
					)}
				/>
			{:else}
				{#each groups as g (g.key)}
					<FormSection title={label(`users.access_group_${g.key}`, g.label)}>
						<ul class="rows">
							{#if editable}
								{@render sectionRow(
									g.key,
									tf('users.access_whole_group', 'All sections in this group'),
									true
								)}
							{/if}
							{#each g.sections as s (s.key)}
								{@render sectionRow(s.key, label(sectionLabelKeys(s.key), s.label), false)}
							{/each}
						</ul>
					</FormSection>
				{/each}
			{/if}

			<FormSection title={t('users.client_features')}>
				<p class="note" data-features-note>
					{tf(
						'users.access_features_note',
						'These switches apply to remote sessions started while signed in. For RustDesk sessions, from the RustDesk app or the web client, the device must run the NeoDesk client or RustDesk 1.4.5 or later, not on Android. In the web client they also apply to CDAP, MeshCentral and RDP or VNC sessions. To keep people from connecting without signing in, turn on Only signed-in RustDesk apps can start sessions in Settings.'
					)}
				</p>
				<ul class="rows">
					{#each catalog.features as f (f.key)}
						{@const eff = effective?.features[f.key]}
						{@const text = label(`users.access_feature_${f.key}`, f.label)}
						{@const hint = FEATURE_HINTS[f.key]}
						{@const line = !eff
							? ''
							: editable
								? featureLine(f.key, eff, form.values.features[f.key] ?? 'inherit')
								: featureNow(eff.enabled, eff.source)}
						{@const described = [
							line && !f.hardLock && `${uid}-f-${f.key}-now`,
							hint && `${uid}-f-${f.key}-hint`
						]
							.filter(Boolean)
							.join(' ')}
						<li class="row" data-feature={f.key}>
							<span class="text">
								{#if editable && !f.hardLock}
									<label class="name" for="{uid}-f-{f.key}">{text}</label>
								{:else}
									<span class="name">{text}</span>
								{/if}
								{#if f.hardLock}
									<span class="now">{tf('users.access_locked', 'Always off')}</span>
								{:else if line}
									<span class="now" id="{uid}-f-{f.key}-now">{line}</span>
								{/if}
								{#if hint}
									<span class="hint" id="{uid}-f-{f.key}-hint">{tf(hint.key, hint.text)}</span>
								{/if}
							</span>
							{#if editable && !f.hardLock}
								<Select
									id="{uid}-f-{f.key}"
									small
									options={featureOptions(featureDefault(f.key, !!f.hardLock))}
									aria-describedby={described || undefined}
									bind:value={form.values.features[f.key]}
								/>
							{/if}
						</li>
					{/each}
				</ul>
			</FormSection>
		</form>
	{/if}
{/snippet}

<Sheet
	wide
	class="access-sheet"
	title={tf('users.access_sheet_title', 'Panel access for {name}', { name })}
	subtitle={editable ? t('users.access_policy_hint') : undefined}
	status={editable ? form.status.message : undefined}
	statusTone={form.status.state === 'error' ? 'error' : form.status.state === 'saved' ? 'ok' : 'neutral'}
	beforeclose={() => !form.busy}
	{onclose}
>
	{#snippet body(parts: SheetParts)}
		{#if editable}
			<div class="sheet-body">{@render content()}</div>
		{:else}
			<!-- Nothing in the list takes focus without controls, so the list does, to scroll it with the keyboard -->
			<!-- svelte-ignore a11y_no_noninteractive_tabindex -->
			<div class="sheet-body" tabindex="0" role="region" aria-labelledby={parts.titleId}>
				{@render content()}
			</div>
		{/if}
	{/snippet}
	{#snippet footer()}
		{#if editable}
			<Button class="sheet-footer-start" onclick={onclose}>{t('actions.cancel')}</Button>
			<Button variant="primary" type="submit" form={form.id} busy={form.busy} disabled={!catalog}
				>{t('actions.save')}</Button
			>
		{:else}
			<Button onclick={onclose}>{t('actions.close')}</Button>
		{/if}
	{/snippet}
</Sheet>

<style>
	.access-form {
		display: block;
	}
	.note {
		margin: 0 0 var(--space-4);
		max-width: var(--measure);
		color: var(--text-muted);
	}
	.access-form :global(.notice) {
		margin-bottom: var(--space-4);
	}
	.access-form :global(.facts) {
		margin-bottom: var(--space-4);
	}
	.rows {
		margin: 0 calc(-1 * var(--row-pad-x));
		padding: 0;
		list-style: none;
		border-top: 1px solid var(--rule);
	}
	.row {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-2) var(--space-4);
		min-height: var(--row-h);
		padding: var(--row-pad-y) var(--row-pad-x);
		border-bottom: 1px solid var(--rule);
	}
	.row.is-group {
		background: var(--bg-subtle);
	}
	.text {
		display: flex;
		flex: 1 1 14rem;
		flex-direction: column;
		gap: 2px;
		min-width: 0;
	}
	.name {
		font-weight: 600;
	}
	.row:not(.is-group) .name {
		font-weight: 400;
	}
	.now,
	.hint {
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
	.warn {
		color: var(--warning);
		font-size: var(--text-sm);
	}
	/* As wide as the longest choice, so none is cut off */
	.row :global(select) {
		flex: 0 0 auto;
		width: auto;
		min-width: 14rem;
		max-width: 100%;
	}
	@media (max-width: 480px) {
		.row :global(select) {
			width: 100%;
		}
	}
</style>
