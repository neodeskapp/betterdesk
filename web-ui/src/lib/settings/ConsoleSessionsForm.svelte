<!--
  Settings, Sign-in: "One session per account" (A239). Off (the default):
  an account may be signed in on several browsers and devices, up to the
  server's cap. On: a new sign-in signs out the account's other browser, as
  the console did before. SESSION_MAX_PER_USER in the server's environment
  decides over the switch when it is set; the form says so and the switch
  can't be changed here.
-->
<script lang="ts">
	import { untrack } from 'svelte';
	import { getConsoleSessions, saveConsoleSessions, type ConsoleSessions } from '#lib/api/sessions.ts';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import Checkbox from '#lib/ui/Checkbox.svelte';
	import Notice from '#lib/ui/Notice.svelte';
	import SaveBar from '#lib/ui/SaveBar.svelte';
	import { createForm } from '#lib/ui/form.svelte.ts';
	import { trackDirty } from './dirty.ts';

	let current = $state.raw<ConsoleSessions | null>(null);

	const form = createForm({
		id: 'console-sessions-form',
		initial: { single: false },
		submit: async (v) => {
			await saveConsoleSessions(v.single);
			current = await getConsoleSessions();
			return { single: current.single_session };
		}
	});

	const fromEnv = $derived(current?.source === 'env');

	async function load() {
		try {
			current = await getConsoleSessions();
			form.reset({ single: current.single_session });
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
		id="single-session"
		label={t('settings.signin.single_session')}
		hint={t('settings.signin.single_session_hint', { max: 10 })}
		disabled={fromEnv || current === null}
		bind:checked={form.values.single}
	/>
	{#if fromEnv && current}
		<Notice
			tone="info"
			message={t('settings.signin.single_session_env', {
				value: current.env_value ?? '',
				limit: current.limit
			})}
		/>
	{/if}
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
