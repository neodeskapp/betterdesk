<!--
  A file from the file browser in a wide sheet: its path, size and kind,
  and the content in a read-only monospace text area. Files are only read
  here; files over 256 KB show their start.
-->
<script lang="ts">
	import type { FileContent } from '#lib/api/serverManagement.ts';
	import { formatBytes } from '#lib/i18n/format.ts';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import Button from '#lib/ui/Button.svelte';
	import Sheet from '#lib/ui/Sheet.svelte';
	import Textarea from '#lib/ui/Textarea.svelte';

	let {
		file,
		requested,
		onclose
	}: {
		file: FileContent;
		requested: string;
		onclose: () => void;
	} = $props();

	const uid = $props.id();
	const path = $derived(file.path || requested);
	const binary = $derived(file.isBinary || file.binary);
	const meta = $derived(
		t(binary ? 'server_mgmt.file_meta_binary' : 'server_mgmt.file_meta_text', {
			size: formatBytes(file.size ?? 0) || '0 B'
		}) + (!binary && file.truncated ? ' ' + t('server_mgmt.file_meta_truncated') : '')
	);
</script>

<Sheet wide title={path} subtitle={meta} {onclose}>
	<div class="viewer">
		<label class="sr-only" for="{uid}-content">{t('server_mgmt.file_content')}</label>
		<Textarea
			id="{uid}-content"
			class="content"
			spellcheck="false"
			autocomplete="off"
			readonly
			value={file.content}
		/>
	</div>
	{#snippet footer()}
		<Button class="sheet-footer-start" onclick={onclose}>{t('actions.close')}</Button>
	{/snippet}
</Sheet>

<style>
	.viewer {
		display: flex;
		flex-direction: column;
		height: 100%;
		min-height: 360px;
	}
	.viewer :global(.content) {
		flex: 1;
		width: 100%;
		min-height: 360px;
		resize: vertical;
		font-family: var(--mono);
		font-size: var(--text-sm);
		line-height: 1.5;
		white-space: pre;
		overflow: auto;
		background: var(--bg-subtle);
	}
</style>
