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
	import Field from '#lib/ui/Field.svelte';
	import IconButton from '#lib/ui/IconButton.svelte';
	import Input from '#lib/ui/Input.svelte';
	import ListRow from '#lib/ui/ListRow.svelte';
	import RowList from '#lib/ui/RowList.svelte';
	import States from '#lib/ui/States.svelte';
	import Tag from '#lib/ui/Tag.svelte';
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
		{@const reply = editing}
		<Field id="cr-title" label={t('chat.canned_title')}>
			{#snippet children(f)}<Input {...f} maxlength={80} bind:value={reply.title} />{/snippet}
		</Field>
		<Field id="cr-body" label={t('chat.canned_body')}>
			{#snippet children(f)}<Textarea {...f} rows={5} maxlength={4096} bind:value={reply.body} />{/snippet}
		</Field>
		{#if editing.id === null && mayShared}
			<Checkbox bind:checked={editing.shared} label={t('chat.canned_shared')} />
		{/if}
	{:else if replies === null}
		<States kind="loading" />
	{:else if !replies.length}
		<States message={t('chat.no_canned_long')} />
	{:else}
		<RowList>
			{#each replies as c (c.id)}
				<ListRow title={c.title}>
					{#snippet sub()}<span class="cr-body">{c.body}</span>{/snippet}
					{#snippet end()}
						<Tag label={c.owner_id === 0 ? t('chat.canned_shared_label') : t('chat.canned_mine_label')} />
					{/snippet}
					{#snippet actions()}
						{#if mayChange(c)}
							<IconButton
								icon="edit"
								label={t('chat.canned_edit', { title: c.title })}
								onclick={() =>
									(editing = { id: c.id, title: c.title, body: c.body, shared: c.owner_id === 0 })}
							/>
							<IconButton
								icon="delete"
								label={t('chat.canned_delete', { title: c.title })}
								onclick={() => void remove(c)}
							/>
						{/if}
					{/snippet}
				</ListRow>
			{/each}
		</RowList>
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
	.cr-body {
		white-space: pre-wrap;
		overflow-wrap: anywhere;
	}
</style>
