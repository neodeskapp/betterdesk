<!--
  Add or edit a console account: sign-in (a new account either gets an
  invite by email, where the person chooses the password, or a username
  and password now; a local one can get a new password), email, role, and
  which devices the account may see (user groups, folders, single devices,
  a client policy). A long form, so a side sheet. The role picker shows the roles
  the signed-in person may give, plus the account's current role (locked);
  LDAP accounts take their role from the directory.
-->
<script lang="ts">
	import { untrack } from 'svelte';
	import { listFolders, type Folder } from '#lib/api/devices.ts';
	import { listStrategies, type Strategy } from '#lib/api/strategies.ts';
	import {
		cancelEmailChange,
		createUser,
		inviteUser,
		resendEmailChange,
		updateUser,
		type ConsoleUser,
		type PendingEmail,
		type UserGroupRow
	} from '#lib/api/users.ts';
	import { formatDate } from '#lib/i18n/format.ts';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import PasswordField from '#lib/people/PasswordField.svelte';
	import {
		deviceIdsOk,
		passwordProblem,
		providerOf,
		roleChoices,
		splitIds,
		usernameFromEmail,
		usernameProblem
	} from '#lib/people/users.ts';
	import { can, session } from '#lib/state/session.svelte.ts';
	import Button from '#lib/ui/Button.svelte';
	import Checkbox from '#lib/ui/Checkbox.svelte';
	import ChoiceList from '#lib/ui/ChoiceList.svelte';
	import Facts from '#lib/ui/Facts.svelte';
	import Field from '#lib/ui/Field.svelte';
	import FormSection from '#lib/ui/FormSection.svelte';
	import Input from '#lib/ui/Input.svelte';
	import Notice from '#lib/ui/Notice.svelte';
	import Select from '#lib/ui/Select.svelte';
	import Sheet from '#lib/ui/Sheet.svelte';
	import Status from '#lib/ui/Status.svelte';
	import Tag from '#lib/ui/Tag.svelte';
	import User from '#lib/ui/User.svelte';
	import { createForm } from '#lib/ui/form.svelte.ts';
	import { toast } from '#lib/ui/toasts.svelte.ts';

	interface Props {
		/** null: a new account */
		user: ConsoleUser | null;
		groups: UserGroupRow[];
		roleLabel: (role: string) => string;
		providerLabel: (provider: string) => string;
		/** The console has email settings, so a new account can get an invite */
		mailReady?: boolean;
		onclose: () => void;
		onsaved: () => void;
	}

	let { user, groups, roleLabel, providerLabel, mailReady = false, onclose, onsaved }: Props = $props();

	const uid = $props.id();
	const editing = untrack(() => !!user);
	const provider = untrack(() => (user ? providerOf(user) : 'local'));
	const isLocal = provider === 'local';
	const choices = $derived(
		roleChoices(session.user?.role, user).map((c) => ({
			value: c.role,
			title: roleLabel(c.role),
			description: t(`users.role_desc_${c.role}`),
			disabled: c.disabled
		}))
	);

	// Folders and client policies, for the device access part (both need device.view)
	let folders = $state.raw<Folder[]>([]);
	let strategies = $state.raw<Strategy[]>([]);
	$effect(() => {
		if (!can('device.view')) return;
		const ctrl = new AbortController();
		listFolders(ctrl.signal).then(
			(f) => (folders = f),
			() => undefined
		);
		listStrategies(ctrl.signal).then(
			(s) => (strategies = s),
			() => undefined
		);
		return () => ctrl.abort();
	});

	const form = createForm({
		initial: untrack(() => ({
			/** invite: the person chooses the password from a mail; password: set here */
			mode: !user && mailReady ? 'invite' : 'password',
			username: '',
			password: '',
			email: user?.email ?? '',
			role: user?.role ?? 'viewer',
			groups: [...(user?.user_groups ?? [])],
			folders: [...(user?.folder_ids ?? [])],
			direct: (user?.peer_grants ?? []).join(', '),
			strategy: user?.strategy_guid ?? ''
		})),
		validate: (v) => {
			const inviting = !editing && v.mode === 'invite';
			const name = editing || (inviting && !v.username.trim()) ? null : usernameProblem(v.username.trim());
			const pw = inviting || (editing && !isLocal) ? null : passwordProblem(v.password, !editing);
			return {
				username: name ? t(name) : undefined,
				password: pw ? t(pw) : undefined,
				email: inviting && !v.email.trim() ? t('users.invite_email_required') : undefined,
				direct: deviceIdsOk(splitIds(v.direct)) ? undefined : t('users.invalid_device_id')
			};
		},
		submit: async (v) => {
			const common = {
				email: v.email.trim(),
				groupGuids: v.groups,
				folderIds: v.folders.map((f) => Number.parseInt(f, 10)).filter(Number.isFinite),
				peerIds: splitIds(v.direct),
				strategyGuid: v.strategy
			};
			if (user) {
				// The role only when it changed: an editor may keep a role they could not give.
				// A directory or single sign-on gives the address, so it is not sent
				const { email, ...rest } = common;
				await updateUser(user.id, {
					...(isLocal ? common : rest),
					...(provider !== 'ldap' && v.role !== user.role ? { role: v.role } : {}),
					...(isLocal && v.password ? { password: v.password } : {})
				});
				toast.success(t('users.user_updated_named', { username: user.username }));
				// A new address waits for its confirmation when mail goes out
				if (isLocal && mailReady && email && email !== user.email)
					toast.info(t('users.email_change_sent_named', { email }));
			} else if (v.mode === 'invite') {
				const username = v.username.trim();
				const made = await inviteUser({ ...common, ...(username ? { username } : {}), role: v.role });
				if (made.invite_sent) toast.success(t('users.invite_sent_named', { email: common.email }));
				else toast.warning(t('users.invite_not_sent_named', { username: made.username }));
			} else {
				const username = v.username.trim();
				await createUser({ ...common, username, password: v.password, role: v.role });
				toast.success(t('users.user_created_named', { username }));
			}
			onsaved();
		}
	});

	function toggle(list: 'groups' | 'folders', value: string, on: boolean) {
		const cur = form.values[list];
		form.values[list] = on ? [...cur.filter((x) => x !== value), value] : cur.filter((x) => x !== value);
	}

	// The page learns whether mail is set up after the sheet may have opened: a
	// new account nobody has typed into yet then starts with the invite
	$effect(() => {
		if (!mailReady || editing) return;
		untrack(() => {
			const v = form.values;
			if (v.mode === 'password' && !v.username && !v.password && !v.email) v.mode = 'invite';
		});
	});

	const inviting = $derived(!editing && form.values.mode === 'invite');
	const suggestion = $derived(inviting ? usernameFromEmail(form.values.email) : '');
	const modeChoices = $derived([
		{
			value: 'invite',
			title: t('users.add_mode_invite'),
			description: mailReady ? t('users.add_mode_invite_desc') : t('users.invite_needs_email'),
			disabled: !mailReady
		},
		{ value: 'password', title: t('users.add_mode_password'), description: t('users.add_mode_password_desc') }
	]);

	/** The account's new address that waits for its confirmation */
	let pending = $state.raw<PendingEmail | null>(untrack(() => user?.email_pending ?? null));
	let pendingBusy = $state(false);

	async function pendingAction(run: () => Promise<unknown>, done: string, gone: boolean) {
		pendingBusy = true;
		try {
			await run();
			toast.success(done);
			if (gone) pending = null;
		} catch (err) {
			toast.error(err instanceof Error ? err.message : t('errors.server_error'));
		} finally {
			pendingBusy = false;
		}
	}

	const emailHint = $derived(
		!editing
			? t('users.email_hint')
			: !isLocal
				? t('users.email_from_provider_hint', { provider: providerLabel(provider) })
				: mailReady
					? t('users.email_hint_confirm')
					: t('users.email_hint_no_mail')
	);

	const strategyOptions = $derived([
		{ value: '', label: t('users.pro_strategy_none') },
		...strategies.map((s) => ({ value: s.guid, label: s.name || s.guid }))
	]);
</script>

{#snippet who()}<User {user} avatar />{/snippet}
{#snippet signIn()}<Tag
		label={isLocal ? t('users.provider_local_account') : providerLabel(provider)}
	/>{/snippet}

<Sheet
	class="user-sheet"
	title={user ? t('users.edit_user_named', { username: user.username }) : t('users.add_user')}
	subtitle={editing ? undefined : t('users.sheet_add_desc')}
	status={form.status.message}
	statusTone={form.status.state === 'error' ? 'error' : 'neutral'}
	beforeclose={() => !form.busy}
	{onclose}
>
	<form id={form.id} class="user-form" onsubmit={form.onsubmit} novalidate>
		{#if user}
			<Facts
				items={[
					{ label: t('users.username'), value: who },
					{ label: t('users.sign_in_with'), value: signIn }
				]}
			/>
			{#if user.locked && user.locks.length}
				<Notice tone="warning" title={t('users.state_locked')}>
					<ul class="locks">
						{#each user.locks as lock (lock.ip)}
							<li>
								{t('users.lock_detail', {
									failures: lock.failures,
									ip: lock.ip,
									date: formatDate(lock.until, {}, '')
								})}
							</li>
						{/each}
					</ul>
					<p>{t('users.lock_hint')}</p>
				</Notice>
			{/if}
			{#if isLocal}
				<PasswordField
					label={t('users.new_password')}
					hint={t('users.password_leave_empty')}
					error={form.errors.password}
					bind:value={form.values.password}
				/>
			{:else}
				<p class="note">{t('users.provider_managed_hint')}</p>
			{/if}
		{:else}
			<FormSection title={t('settings.tab_auth')} id="{uid}-mode">
				<ChoiceList
					choices={modeChoices}
					labelledby="{uid}-mode-title"
					name="{uid}-mode-choice"
					bind:value={form.values.mode}
				/>
			</FormSection>
			{#if inviting}
				<Field label={t('users.email')} hint={t('users.invite_email_hint')} error={form.errors.email}>
					{#snippet children(f)}
						<Input
							{...f}
							type="email"
							width="host"
							autocomplete="off"
							spellcheck="false"
							data-autofocus
							bind:value={form.values.email}
						/>
					{/snippet}
				</Field>
				<Field
					label={t('users.username')}
					hint={suggestion
						? t('users.username_optional_hint', { suggestion })
						: t('users.username_optional_hint_empty')}
					error={form.errors.username}
				>
					{#snippet children(f)}
						<Input
							{...f}
							width="medium"
							maxlength={64}
							autocomplete="off"
							spellcheck="false"
							placeholder={suggestion}
							bind:value={form.values.username}
						/>
					{/snippet}
				</Field>
			{:else}
				<Field label={t('users.username')} hint={t('users.username_hint')} error={form.errors.username}>
					{#snippet children(f)}
						<Input
							{...f}
							width="medium"
							maxlength={64}
							autocomplete="off"
							spellcheck="false"
							data-autofocus
							bind:value={form.values.username}
						/>
					{/snippet}
				</Field>
				<PasswordField
					label={t('users.password')}
					hint={t('users.password_hint')}
					error={form.errors.password}
					bind:value={form.values.password}
				/>
			{/if}
		{/if}
		{#if !inviting}
			<Field label={t('users.email')} hint={emailHint}>
				{#snippet children(f)}
					<Input
						{...f}
						type="email"
						width="host"
						autocomplete="off"
						spellcheck="false"
						readonly={editing && !isLocal}
						bind:value={form.values.email}
					/>
				{/snippet}
			</Field>
			{#if user && pending}
				{@const p = pending}
				{@const id = user.id}
				<div class="pending-email">
					<Status
						tone={p.expired ? 'warning' : 'waiting'}
						label={t('users.email_pending', { email: p.email })}
					/>
					<p class="note">
						{p.expired
							? t('users.email_pending_expired')
							: t('users.email_pending_until', { date: formatDate(p.expires_at, {}, '') })}
					</p>
					<div class="pending-actions">
						{#if mailReady}
							<Button
								size="sm"
								disabled={pendingBusy}
								onclick={() =>
									void pendingAction(
										() => resendEmailChange(id),
										t('users.email_change_sent_named', { email: p.email }),
										false
									)}>{t('users.email_change_resend')}</Button
							>
						{/if}
						<Button
							size="sm"
							variant="danger-quiet"
							disabled={pendingBusy}
							onclick={() =>
								void pendingAction(() => cancelEmailChange(id), t('users.email_change_cancelled'), true)}
							>{t('users.email_change_cancel')}</Button
						>
					</div>
				</div>
			{/if}
		{/if}

		<FormSection title={t('users.role')} id="{uid}-role">
			<ChoiceList
				{choices}
				labelledby="{uid}-role-title"
				describedby="{uid}-role-note"
				name="{uid}-role-choice"
				bind:value={form.values.role}
			/>
			<p class="note" id="{uid}-role-note">{t('users.role_note')}</p>
		</FormSection>

		<FormSection title={t('users.device_access')}>
			<p class="note">{t('users.scope_hint')}</p>
			<fieldset class="group">
				<legend>{t('users.user_groups')}</legend>
				{#if groups.length}
					<ul class="pick">
						{#each groups as g (g.guid)}
							<li>
								<Checkbox
									label={g.name || g.guid}
									checked={form.values.groups.includes(g.guid)}
									onchange={(e) => toggle('groups', g.guid, (e.currentTarget as HTMLInputElement).checked)}
								/>
							</li>
						{/each}
					</ul>
				{:else}
					<p class="note">{t('users.no_user_groups')}</p>
				{/if}
			</fieldset>
			<fieldset class="group" aria-describedby="{uid}-folders-hint">
				<legend>{t('users.user_folders')}</legend>
				{#if folders.length}
					<ul class="pick">
						{#each folders as f (f.id)}
							<li>
								<Checkbox
									label={f.name || String(f.id)}
									checked={form.values.folders.includes(String(f.id))}
									onchange={(e) =>
										toggle('folders', String(f.id), (e.currentTarget as HTMLInputElement).checked)}
								/>
							</li>
						{/each}
					</ul>
				{:else}
					<p class="note">{t('users.no_folders')}</p>
				{/if}
				<p class="note" id="{uid}-folders-hint">{t('users.user_folders_hint')}</p>
			</fieldset>
			<Field
				label={t('users.user_direct_devices')}
				hint={t('users.user_direct_devices_hint')}
				error={form.errors.direct}
			>
				{#snippet children(f)}
					<Input {...f} width="host" spellcheck="false" autocomplete="off" bind:value={form.values.direct} />
				{/snippet}
			</Field>
			<Field label={t('users.pro_strategy_label')} hint={t('users.pro_strategy_hint')}>
				{#snippet children(f)}
					<Select {...f} width="medium" options={strategyOptions} bind:value={form.values.strategy} />
				{/snippet}
			</Field>
		</FormSection>
	</form>
	{#snippet footer()}
		<Button class="sheet-footer-start" onclick={onclose}>{t('actions.cancel')}</Button>
		<Button variant="primary" type="submit" form={form.id} busy={form.busy}
			>{user ? t('actions.save') : inviting ? t('users.send_invite') : t('users.create')}</Button
		>
	{/snippet}
</Sheet>

<style>
	.locks {
		margin: 0 0 var(--space-1);
		padding-inline-start: var(--space-4);
	}
	.locks + p {
		margin: 0;
	}
	.user-form :global(.facts) {
		grid-template-columns: minmax(0, 9rem) minmax(0, 1fr);
		/* rows hang their padding outside, so the labels line up with the field labels */
		margin: 0 calc(-1 * var(--row-pad-x)) var(--space-4);
	}
	.note {
		margin: 0 0 var(--space-4);
		color: var(--text-muted);
		font-size: var(--text-sm);
		max-width: var(--measure);
	}
	.pending-email {
		display: flex;
		flex-direction: column;
		gap: var(--space-2);
		max-width: var(--measure);
		margin: 0 0 var(--space-4);
	}
	.pending-email .note {
		margin: 0;
	}
	.pending-actions {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
	}
	.group {
		min-width: 0;
		margin: 0 0 var(--space-4);
		padding: 0;
		border: 0;
	}
	.group legend {
		margin-bottom: var(--space-1);
		padding: 0;
		font-weight: 600;
	}
	.group .note {
		margin: var(--space-1) 0 0;
	}
	.pick {
		max-height: 260px;
		margin: 0;
		overflow-y: auto;
		padding: 0;
		list-style: none;
		border-top: 1px solid var(--rule);
	}
	.pick > li {
		border-bottom: 1px solid var(--rule);
	}
	.pick > li > :global(.check-field) {
		padding-block: var(--row-pad-y);
	}
</style>
