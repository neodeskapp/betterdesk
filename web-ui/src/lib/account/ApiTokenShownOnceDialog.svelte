<!--
  A new API token, shown this once: the value with Copy, and the command
  that signs the command line in with it.
-->
<script lang="ts">
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import { loginCommand } from '#lib/people/apiTokens.ts';
	import Button from '#lib/ui/Button.svelte';
	import CodeBlock from '#lib/ui/CodeBlock.svelte';
	import Dialog from '#lib/ui/Dialog.svelte';
	import Notice from '#lib/ui/Notice.svelte';
	import ValueField from '#lib/ui/ValueField.svelte';

	let { token, name, origin, onclose }: { token: string; name: string; origin: string; onclose: () => void } =
		$props();
</script>

<Dialog title={t('api_tokens.made_title')} size="large" class="api-token-made" {onclose}>
	<Notice tone="warning" message={t('api_tokens.copy_warning')} />
	<dl class="facts made">
		<dt>{name}</dt>
		<dd><ValueField value={token} label={name} /></dd>
	</dl>
	<p class="next">{t('api_tokens.made_next')}</p>
	<CodeBlock title={t('api_tokens.cli_command')} code={loginCommand(origin)} />
	{#snippet footer()}
		<Button variant="primary" onclick={onclose}>{t('common.close')}</Button>
	{/snippet}
</Dialog>

<style>
	.made {
		grid-template-columns: minmax(0, 1fr);
		margin-top: var(--space-4);
	}
	.made > dt {
		min-height: 0;
		padding-bottom: 0;
		border-bottom: 0;
	}
	.made > dd {
		padding-top: var(--space-1);
	}
	.next {
		margin: var(--space-4) 0 var(--space-2);
		max-width: var(--measure);
	}
</style>
