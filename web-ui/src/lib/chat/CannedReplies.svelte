<!--
  Canned replies (A242): the shared ones, which accounts with
  chat.canned_manage keep, and one's own. Each has a title (what the
  composer's list shows) and the text it inserts.
-->
<script lang="ts">
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import Button from '#lib/ui/Button.svelte';
	import Checkbox from '#lib/ui/Checkbox.svelte';
	import Dialog from '#lib/ui/Dialog.svelte';
	import IconButton from '#lib/ui/IconButton.svelte';
	import Input from '#lib/ui/Input.svelte';
	import Textarea from '#lib/ui/Textarea.svelte';
	import { confirm } from '#lib/ui/dialogs.svelte.ts';
	import { toast } from '#lib/ui/toasts.svelte.ts';
	import * as api from './api.ts';
	import type { CannedReply } from './types.ts';

	let { onclose }: { onclose: () => void } = $props();

	let replies = $state.raw<CannedReply[] | null>(null);
	let mayShared = $state(false);
	let editing = $state<{ id: number | null; title: string; body: string; shared: boolean } | null>(null);
	let busy = $state(false);

	async function load() {
		try {
			const r = await api.loadCanned();
			replies = r.replies;
			mayShared = r.mayManageShared;
		} catch {
			replies = [];
		}
	}
	void load();

	async function save() {
		if (!editing) return;
		busy = true;
		try {
			if (editing.id === null) await api.addCanned(editing.title, editing.body, editing.shared);
			else await api.updateCanned(editing.id, editing.title, editing.body);
			editing = null;
			await load();
		} catch (err) {
			toast.error(err instanceof Error ? err.message : t('chat.action_failed'));
		} finally {
			busy = false;
		}
	}

	async function remove(c: CannedReply) {
		if (
			!(await confirm({
				title: t('chat.canned_delete_title', { title: c.title }),
				danger: true,
				confirmLabel: t('actions.delete')
			}))
		)
			return;
		try {
			await api.deleteCanned(c.id);
			await load();
		} catch (err) {
			toast.error(err instanceof Error ? err.message : t('chat.action_failed'));
		}
	}

	const mayChange = (c: CannedReply) => c.owner_id !== 0 || mayShared;
</script>

<Dialog open title={t('chat.manage_canned')} size="large" {onclose}>
	{#if editing}
		<label class="cr-label" for="cr-title">{t('chat.canned_title')}</label>
		<Input id="cr-title" maxlength={80} bind:value={editing.title} />
		<label class="cr-label" for="cr-body">{t('chat.canned_body')}</label>
		<Textarea id="cr-body" rows={5} maxlength={4096} bind:value={editing.body} />
		{#if editing.id === null && mayShared}
			<Checkbox bind:checked={editing.shared} label={t('chat.canned_shared')} />
		{/if}
	{:else if replies === null}
		<p>{t('common.loading')}</p>
	{:else if !replies.length}
		<p class="cr-empty">{t('chat.no_canned_long')}</p>
	{:else}
		<ul class="cr-list">
			{#each replies as c (c.id)}
				<li class="cr-item">
					<div class="cr-text">
						<strong>{c.title}</strong>
						<span class="cr-kind"
							>{c.owner_id === 0 ? t('chat.canned_shared_label') : t('chat.canned_mine_label')}</span
						>
						<p class="cr-body">{c.body}</p>
					</div>
					{#if mayChange(c)}
						<IconButton
							icon="edit"
							label={t('chat.canned_edit', { title: c.title })}
							onclick={() => (editing = { id: c.id, title: c.title, body: c.body, shared: c.owner_id === 0 })}
						/>
						<IconButton
							icon="delete"
							label={t('chat.canned_delete', { title: c.title })}
							onclick={() => void remove(c)}
						/>
					{/if}
				</li>
			{/each}
		</ul>
	{/if}
	{#snippet footer()}
		{#if editing}
			<Button onclick={() => (editing = null)}>{t('actions.cancel')}</Button>
			<Button
				variant="primary"
				{busy}
				disabled={!editing.title.trim() || !editing.body.trim()}
				onclick={() => void save()}>{t('actions.save')}</Button
			>
		{:else}
			<Button onclick={onclose}>{t('actions.close')}</Button>
			<Button
				variant="primary"
				icon="add"
				onclick={() => (editing = { id: null, title: '', body: '', shared: false })}
				>{t('chat.canned_add')}</Button
			>
		{/if}
	{/snippet}
</Dialog>

<style>
	.cr-label {
		display: block;
		margin: var(--space-3) 0 var(--space-1);
		font-weight: 600;
	}
	.cr-empty {
		color: var(--text-muted);
	}
	.cr-list {
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.cr-item {
		display: flex;
		align-items: flex-start;
		gap: var(--space-2);
		padding: var(--space-2) 0;
		border-bottom: 1px solid var(--rule);
	}
	.cr-text {
		flex: 1;
		min-width: 0;
	}
	.cr-kind {
		margin-inline-start: var(--space-2);
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
	.cr-body {
		margin: var(--space-1) 0 0;
		color: var(--text-muted);
		white-space: pre-wrap;
		overflow-wrap: anywhere;
	}
</style>
