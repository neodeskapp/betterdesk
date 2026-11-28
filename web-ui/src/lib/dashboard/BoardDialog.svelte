<!--
  A dashboard's name, and for people who publish shared dashboards
  (server.config) who sees it: every account or some roles, whether people
  can hide it, and whether it opens first for them.
-->
<script lang="ts">
	import { untrack } from 'svelte';
	import { listRoles } from '#lib/api/roles.ts';
	import { t, tf } from '#lib/i18n/i18n.svelte.ts';
	import Button from '#lib/ui/Button.svelte';
	import Checkbox from '#lib/ui/Checkbox.svelte';
	import ChoiceList from '#lib/ui/ChoiceList.svelte';
	import Dialog from '#lib/ui/Dialog.svelte';
	import Field from '#lib/ui/Field.svelte';
	import Input from '#lib/ui/Input.svelte';
	import { roleLabel } from '#lib/ui/settings/pickers.ts';
	import { LIMITS, type Sharing } from './model.ts';

	interface Props {
		title: string;
		name: string;
		/** Who sees it; null for a dashboard only this account sees */
		sharing?: Sharing | null;
		confirmLabel: string;
		onsave: (name: string, sharing: Sharing | null) => void;
		onclose: () => void;
	}

	let {
		title,
		name: startName,
		sharing: startSharing = null,
		confirmLabel,
		onsave,
		onclose
	}: Props = $props();

	let name = $state(untrack(() => startName));
	const shared = untrack(() => startSharing);
	let who = $state<'all' | 'roles'>(shared?.roles.length ? 'roles' : 'all');
	let roles = $state<string[]>(untrack(() => [...(shared?.roles ?? [])]));
	let locked = $state(shared?.locked ?? false);
	let home = $state(shared?.home ?? false);
	let error = $state('');

	let known = $state<string[]>([]);
	$effect(() => {
		if (!shared) return;
		untrack(() => {
			listRoles()
				.then((r) => (known = r.roles.map((x) => x.name)))
				// Without the role list the roles already chosen stay choosable
				.catch(() => (known = [...roles]));
		});
	});
	const roleChoices = $derived(
		[...new Set([...known, ...roles])].map((r) => ({ value: r, title: roleLabel(r, t) }))
	);

	function save(e: SubmitEvent) {
		e.preventDefault();
		const n = name.trim();
		if (!n) {
			error = tf('common.name_required', 'Enter a name.');
			return;
		}
		if (shared && who === 'roles' && !roles.length) {
			error = tf('dashboard.share_pick_role', 'Choose at least one role, or share it with everyone.');
			return;
		}
		onsave(n, shared ? { roles: who === 'roles' ? roles : [], locked, home } : null);
	}
</script>

<Dialog {title} {onclose}>
	<form id="board-settings" onsubmit={save} novalidate>
		<Field
			label={tf('dashboard.board_name', 'Name')}
			error={error && !name.trim() ? error : undefined}
			required
		>
			{#snippet children(f)}
				<Input {...f} maxlength={LIMITS.name} autocomplete="off" bind:value={name} />
			{/snippet}
		</Field>
		{#if shared}
			<fieldset class="share">
				<legend>{tf('dashboard.share_who', 'Who sees it')}</legend>
				<ChoiceList
					label={tf('dashboard.share_who', 'Who sees it')}
					choices={[
						{ value: 'all', title: tf('dashboard.share_everyone', 'Every account') },
						{ value: 'roles', title: tf('dashboard.share_roles', 'Accounts with these roles') }
					]}
					bind:value={who}
				/>
				{#if who === 'roles'}
					<div class="roles">
						<ChoiceList
							type="checkbox"
							label={tf('dashboard.share_roles', 'Accounts with these roles')}
							choices={roleChoices}
							bind:selected={roles}
						/>
					</div>
					{#if error && name.trim()}<p class="text-danger" role="alert">{error}</p>{/if}
				{/if}
				<Checkbox
					label={tf('dashboard.share_home', 'Open it first')}
					hint={tf(
						'dashboard.share_home_hint',
						'It is the first tab and opens on sign-in, unless someone picked another dashboard.'
					)}
					bind:checked={home}
				/>
				<Checkbox
					label={tf('dashboard.share_locked', 'People can’t hide it')}
					hint={tf('dashboard.share_locked_hint', 'They can still make their own copy and change that.')}
					bind:checked={locked}
				/>
			</fieldset>
		{/if}
	</form>
	{#snippet footer()}
		<Button onclick={onclose}>{tf('common.cancel', 'Cancel')}</Button>
		<Button variant="primary" type="submit" form="board-settings">{confirmLabel}</Button>
	{/snippet}
</Dialog>

<style>
	.share {
		margin: var(--space-4) 0 0;
		padding: 0;
		border: 0;
		display: flex;
		flex-direction: column;
		gap: var(--space-3);
	}
	legend {
		padding: 0;
		margin-bottom: var(--space-2);
		font-weight: 600;
	}
	.roles {
		padding-inline-start: var(--space-5);
	}
</style>
