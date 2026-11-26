<!--
  Settings, Sign-in: "Mail people when an admin views NeoDesk as them"
  (server_config impersonation_notice_mail, on by default). With it on, the
  account viewed through Sign in as gets a mail with who, when and why; its
  recent sign-ins on Your account show it either way.
-->
<script lang="ts">
	import { untrack } from 'svelte';
	import { getImpersonationSettings, saveImpersonationSettings } from '#lib/api/impersonation.ts';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import Checkbox from '#lib/ui/Checkbox.svelte';
	import SaveBar from '#lib/ui/SaveBar.svelte';
	import { createForm } from '#lib/ui/form.svelte.ts';
	import { trackDirty } from './dirty.ts';

	let loaded = $state(false);

	const form = createForm({
		id: 'impersonation-mail-form',
		initial: { mail: true },
		submit: async (v) => ({ mail: (await saveImpersonationSettings(v.mail)).notice_mail })
	});

	async function load() {
		try {
			form.reset({ mail: (await getImpersonationSettings()).notice_mail });
			loaded = true;
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
		id="impersonation-mail"
		label={t('settings.signin.impersonation_mail')}
		hint={t('settings.signin.impersonation_mail_hint')}
		disabled={!loaded}
		bind:checked={form.values.mail}
	/>
	<SaveBar {form} label={t('actions.save')} />
</form>

<style>
	/* The Sign-in tab's scoped layout doesn't reach into this component:
	   the same reading width and spacing as the tab's other forms */
	.form-column {
		max-width: var(--measure);
	}
	.settings-subgroup {
		display: flex;
		flex-direction: column;
		gap: var(--space-3);
		min-width: 0;
	}
	.settings-subgroup > :global(.form-group) {
		margin-bottom: 0;
	}
</style>
