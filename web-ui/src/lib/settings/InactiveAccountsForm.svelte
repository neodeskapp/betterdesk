<!--
  Settings, Sign-in: "Disable inactive accounts" (off by default). On, an
  account that hasn't signed in for the chosen number of days is
  disabled; the person gets a notice by email a week before. Accounts
  with the exception in Edit user, open invites, pro accounts and the last
  Super admin are left alone. The server keeps the number of days.
-->
<script lang="ts">
	import { untrack } from 'svelte';
	import { getInactiveAccounts, saveInactiveAccounts, type InactiveAccounts } from '#lib/api/settings.ts';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import Checkbox from '#lib/ui/Checkbox.svelte';
	import Field from '#lib/ui/Field.svelte';
	import Input from '#lib/ui/Input.svelte';
	import SaveBar from '#lib/ui/SaveBar.svelte';
	import ShowWhen, { showWhenControl } from '#lib/ui/ShowWhen.svelte';
	import { createForm } from '#lib/ui/form.svelte.ts';
	import { trackDirty } from './dirty.ts';

	let limits = $state.raw<InactiveAccounts>({ days: 0, notice_days: 7, min_days: 14, max_days: 3650 });

	const form = createForm({
		id: 'inactive-accounts-form',
		initial: { on: false, days: '90' },
		validate: (v) => {
			if (!v.on) return null;
			const n = Number(v.days);
			return {
				days:
					Number.isInteger(n) && n >= limits.min_days && n <= limits.max_days
						? undefined
						: t('settings.inactive_days_invalid', { min: limits.min_days, max: limits.max_days })
			};
		},
		submit: async (v) => {
			await saveInactiveAccounts(v.on ? Number(v.days) : 0);
			return v;
		}
	});

	async function load() {
		try {
			limits = await getInactiveAccounts();
			form.reset({ on: limits.days > 0, days: String(limits.days > 0 ? limits.days : 90) });
		} catch (err) {
			form.status = {
				state: 'error',
				message: err instanceof Error ? err.message : t('errors.server_error')
			};
		}
	}

	$effect(() =>
		trackDirty(
			() => form.dirty,
			() => form.busy
		)
	);
	$effect(() => {
		untrack(() => void load());
	});
</script>

<form id={form.id} class="form-column settings-subgroup" onsubmit={form.onsubmit} novalidate>
	<Checkbox
		id="inactive-accounts"
		label={t('settings.inactive_label')}
		hint={t('settings.inactive_hint', { notice: limits.notice_days })}
		bind:checked={form.values.on}
		{...showWhenControl('inactive-accounts-days-group', form.values.on)}
	/>
	<ShowWhen id="inactive-accounts-days-group" when={form.values.on}>
		<Field
			id="inactive-accounts-days"
			label={t('settings.inactive_days')}
			hint={t('settings.inactive_days_hint', { min: limits.min_days, max: limits.max_days })}
			width="short"
			error={form.errors.days}
		>
			{#snippet children(f)}
				<Input
					{...f}
					type="number"
					min={limits.min_days}
					max={limits.max_days}
					bind:value={form.values.days}
				/>
			{/snippet}
		</Field>
	</ShowWhen>
	<SaveBar {form} label={t('actions.save')} />
</form>
