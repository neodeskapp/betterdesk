<!--
  Where a message is written (A242). Enter or Send sends, Shift+Enter adds
  a line; the field says who reads it and grows with its text.
  Pictures and files come by the clip button, by pasting (a screenshot
  from the clipboard) or by dropping them on the conversation; each
  uploads at once and shows as a chip until it is sent. Above the field
  the message being answered or edited, with a way out (Escape). The
  emoji button inserts an emoji (not on touch screens, whose keyboard has
  them), the canned replies button a reply's text. After a new message
  went out (not after an edit) it calls onsend. A draft stays with its
  conversation while another one is open.
-->
<script lang="ts" module>
	/** Drafts by conversation, for this tab */
	// eslint-disable-next-line svelte/prefer-svelte-reactivity -- kept for later, nothing renders from it
	const drafts = new Map<string, string>();
</script>

<script lang="ts">
	import { tick } from 'svelte';
	import { formatBytes } from '#lib/i18n/format.ts';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import Button from '#lib/ui/Button.svelte';
	import Icon from '#lib/ui/Icon.svelte';
	import IconButton from '#lib/ui/IconButton.svelte';
	import Menu from '#lib/ui/Menu.svelte';
	import MenuDivider from '#lib/ui/MenuDivider.svelte';
	import MenuItem from '#lib/ui/MenuItem.svelte';
	import Textarea from '#lib/ui/Textarea.svelte';
	import { toast } from '#lib/ui/toasts.svelte.ts';
	import { loadCanned, MAX_FILE, uploadFile } from './api.ts';
	import ChatEmoji from './ChatEmoji.svelte';
	import { chat } from './client.svelte.ts';
	import { EMOJI_GROUPS } from './emoji.ts';
	import type { CannedReply, Message } from './types.ts';

	interface Props {
		conv: string;
		replyTo: Message | null;
		editing: Message | null;
		/** Who will read it, for the field's label */
		target: string;
		oncancel: () => void;
		onmanagecanned: () => void;
		/** A new message went out (not an edit) */
		onsend?: () => void;
		/** Files dropped on the conversation, to add */
		dropped?: File[];
		element?: HTMLTextAreaElement | null;
	}

	let {
		conv,
		replyTo,
		editing,
		target,
		oncancel,
		onmanagecanned,
		onsend,
		dropped = [],
		element = $bindable(null)
	}: Props = $props();

	interface Pending {
		key: number;
		file: File;
		id: string;
		state: 'uploading' | 'done' | 'failed';
		preview: string;
	}
	let files = $state<Pending[]>([]);
	let fileInput = $state<HTMLInputElement | null>(null);
	let seq = 0;

	let draft = $state('');
	let lastConv = '';
	$effect.pre(() => {
		// Another conversation: keep this draft, take that one's
		if (conv !== lastConv) {
			if (lastConv) drafts.set(lastConv, draft);
			draft = drafts.get(conv) ?? '';
			files = [];
			lastConv = conv;
		}
	});
	$effect(() => {
		if (editing) {
			draft = editing.text;
			void tick().then(() => element?.focus());
		}
	});

	function addFiles(list: File[]) {
		for (const file of list) {
			if (file.size > MAX_FILE) {
				toast.error(t('chat.file_too_large', { name: file.name }));
				continue;
			}
			const entry: Pending = {
				key: ++seq,
				file,
				id: '',
				state: 'uploading',
				preview: file.type.startsWith('image/') ? URL.createObjectURL(file) : ''
			};
			files = [...files, entry];
			uploadFile(file).then(
				(up) =>
					(files = files.map((f) => (f.key === entry.key ? { ...f, id: up.file_id, state: 'done' } : f))),
				() => (files = files.map((f) => (f.key === entry.key ? { ...f, state: 'failed' } : f)))
			);
		}
	}

	$effect(() => {
		if (dropped.length) addFiles(dropped);
	});

	function removeFile(key: number) {
		const f = files.find((x) => x.key === key);
		if (f?.preview) URL.revokeObjectURL(f.preview);
		files = files.filter((x) => x.key !== key);
	}

	function onpaste(e: ClipboardEvent) {
		const list = Array.from(e.clipboardData?.files ?? []);
		if (!list.length) return;
		e.preventDefault();
		addFiles(
			list.map((f) =>
				f.name && f.name !== 'image.png'
					? f
					: new File(
							[f],
							`${t('chat.screenshot_name')}-${new Date().toISOString().slice(0, 19).replace(/[:T]/g, '-')}.png`,
							{
								type: f.type
							}
						)
			)
		);
	}

	const uploading = $derived(files.some((f) => f.state === 'uploading'));
	const canSend = $derived(!uploading && (draft.trim() !== '' || files.some((f) => f.state === 'done')));

	function send() {
		if (editing) {
			const text = draft.trim();
			if (text && text !== editing.text) chat.edit(editing.id, text);
			draft = '';
			oncancel();
			return;
		}
		if (!canSend) return;
		const ok = chat.sendMessage(conv, draft.trim(), {
			replyTo: replyTo?.id,
			attachments: files.filter((f) => f.state === 'done').map((f) => f.id)
		});
		if (!ok) toast.error(t('chat.not_connected'));
		for (const f of files) if (f.preview) URL.revokeObjectURL(f.preview);
		files = [];
		draft = '';
		drafts.delete(conv);
		if (replyTo) oncancel();
		if (ok) onsend?.();
	}

	function onkeydown(e: KeyboardEvent) {
		if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) {
			e.preventDefault();
			send();
			return;
		}
		if (e.key === 'Escape' && (replyTo || editing)) {
			e.preventDefault();
			if (editing) draft = '';
			oncancel();
			return;
		}
		chat.typingIn(conv);
	}

	// The field grows with its text (and its border) up to 160 px
	$effect(() => {
		void draft;
		const el = element;
		if (!el) return;
		el.style.height = 'auto';
		el.style.height = `${Math.min(el.scrollHeight + el.offsetHeight - el.clientHeight, 160)}px`;
	});

	/* ── Emoji and canned replies ── */

	let emojiBtn = $state<HTMLButtonElement | null>(null);
	let cannedBtn = $state<HTMLButtonElement | null>(null);
	let emojiOpen = $state(false);
	let cannedOpen = $state(false);
	let canned = $state.raw<CannedReply[] | null>(null);

	function insert(text: string) {
		const el = element;
		if (!el) {
			draft += text;
			return;
		}
		const start = el.selectionStart ?? draft.length;
		const end = el.selectionEnd ?? draft.length;
		draft = draft.slice(0, start) + text + draft.slice(end);
		void tick().then(() => {
			el.focus();
			el.setSelectionRange(start + text.length, start + text.length);
		});
	}

	async function openCanned() {
		cannedOpen = true;
		try {
			canned = (await loadCanned()).replies;
		} catch {
			canned = [];
		}
	}
</script>

<div class="composer">
	{#if replyTo || editing}
		<div class="composer-context">
			<Icon name={editing ? 'edit' : 'reply'} />
			<span class="composer-context-text">
				{#if editing}
					{t('chat.editing')}
				{:else if replyTo}
					<strong>{t('chat.replying_to', { name: replyTo.from.name })}</strong>
					<span class="composer-quote">{replyTo.text || replyTo.attachments[0]?.name || ''}</span>
				{/if}
			</span>
			<IconButton
				icon="close"
				label={t('chat.cancel_reply')}
				onclick={() => {
					if (editing) draft = '';
					oncancel();
				}}
			/>
		</div>
	{/if}
	{#if files.length}
		<ul class="composer-files" aria-label={t('chat.attachments')}>
			{#each files as f (f.key)}
				<li class={['composer-file', f.state === 'failed' && 'is-failed']}>
					{#if f.preview}<img src={f.preview} alt="" width="40" height="40" />{:else}<Icon
							name="description"
						/>{/if}
					<span class="composer-file-text">
						<span class="composer-file-name">{f.file.name}</span>
						<span class="composer-file-size"
							>{f.state === 'uploading'
								? t('chat.uploading')
								: f.state === 'failed'
									? t('chat.upload_failed')
									: formatBytes(f.file.size)}</span
						>
					</span>
					<IconButton
						icon="close"
						label={t('chat.remove_file_named', { name: f.file.name })}
						onclick={() => removeFile(f.key)}
					/>
				</li>
			{/each}
		</ul>
	{/if}
	<div class="composer-row">
		<IconButton
			icon="attach_file"
			label={t('chat.attach_file')}
			disabled={!!editing}
			onclick={() => fileInput?.click()}
		/>
		<label class="sr-only" for={`composer-${conv}`}>{t('chat.message_to', { name: target })}</label>
		<Textarea
			bind:element
			bind:value={draft}
			id={`composer-${conv}`}
			class="composer-field"
			rows={1}
			maxlength={4096}
			placeholder={t('chat.message_to', { name: target })}
			{onkeydown}
			{onpaste}
		/>
		<IconButton
			bind:element={emojiBtn}
			class="composer-emoji"
			icon="mood"
			label={t('chat.emoji')}
			aria-haspopup="menu"
			aria-expanded={emojiOpen}
			onclick={() => (emojiOpen = !emojiOpen)}
		/>
		<IconButton
			bind:element={cannedBtn}
			icon="quickreply"
			label={t('chat.canned_replies')}
			aria-haspopup="menu"
			aria-expanded={cannedOpen}
			disabled={!!editing}
			onclick={() => (cannedOpen ? (cannedOpen = false) : void openCanned())}
		/>
		<Button
			class="composer-send"
			icon={editing ? 'check' : 'send'}
			disabled={!editing && !canSend}
			onclick={send}
			><span class="composer-send-word">{editing ? t('chat.save_edit') : t('chat.send')}</span></Button
		>
	</div>
	<input
		bind:this={fileInput}
		type="file"
		multiple
		class="sr-only"
		tabindex="-1"
		aria-hidden="true"
		onchange={() => {
			addFiles(Array.from(fileInput?.files ?? []));
			if (fileInput) fileInput.value = '';
		}}
	/>
</div>

{#if emojiOpen}
	<Menu
		open
		anchor={emojiBtn}
		align="end"
		label={t('chat.emoji')}
		class="chat-emoji-grid"
		onclose={(back) => {
			emojiOpen = false;
			if (back) emojiBtn?.focus();
		}}
	>
		{#each EMOJI_GROUPS as g (g.id)}
			{#each g.emoji as e (e)}
				<MenuItem
					onselect={() => {
						emojiOpen = false;
						insert(e);
					}}><ChatEmoji emoji={e} size="large" /></MenuItem
				>
			{/each}
		{/each}
	</Menu>
{/if}

{#if cannedOpen}
	<Menu
		open
		anchor={cannedBtn}
		align="end"
		label={t('chat.canned_replies')}
		class="chat-canned-menu"
		onclose={(back) => {
			cannedOpen = false;
			if (back) cannedBtn?.focus();
		}}
	>
		{#if canned === null}
			<MenuItem disabled>{t('common.loading')}</MenuItem>
		{:else if !canned.length}
			<MenuItem disabled>{t('chat.no_canned')}</MenuItem>
		{:else}
			{#each canned as c (c.id)}
				<MenuItem
					onselect={() => {
						cannedOpen = false;
						insert(c.body);
					}}>{c.title}</MenuItem
				>
			{/each}
		{/if}
		<MenuDivider />
		<MenuItem
			icon="settings"
			onselect={() => {
				cannedOpen = false;
				onmanagecanned();
			}}>{t('chat.manage_canned')}</MenuItem
		>
	</Menu>
{/if}

<style>
	.composer {
		display: flex;
		flex: none;
		flex-direction: column;
		gap: var(--space-2);
		padding: var(--space-2) var(--space-4) var(--space-3);
		border-top: 1px solid var(--rule);
		background: var(--bg);
	}
	.composer-context {
		display: flex;
		align-items: center;
		gap: var(--space-2);
		padding: var(--space-1) var(--space-2);
		border-inline-start: 3px solid var(--accent);
		border-radius: var(--radius);
		background: var(--hover);
		font-size: var(--text-sm);
	}
	.composer-context-text {
		display: flex;
		flex: 1;
		flex-direction: column;
		min-width: 0;
	}
	.composer-quote {
		overflow: hidden;
		color: var(--text-muted);
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.composer-files {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.composer-file {
		display: flex;
		align-items: center;
		gap: var(--space-2);
		max-width: 260px;
		padding: var(--space-1);
		border: 1px solid var(--rule);
		border-radius: var(--radius);
	}
	.composer-file.is-failed {
		border-color: var(--danger);
	}
	.composer-file img {
		width: 40px;
		height: 40px;
		border-radius: var(--radius);
		object-fit: cover;
	}
	.composer-file-text {
		display: flex;
		flex-direction: column;
		min-width: 0;
		font-size: var(--text-sm);
	}
	.composer-file-name {
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.composer-file-size {
		color: var(--text-muted);
	}
	.is-failed .composer-file-size {
		color: var(--danger);
	}
	.composer-row {
		display: flex;
		align-items: flex-end;
		gap: var(--space-1);
	}
	/* The kit's text area, one line high to start, growing with its text */
	.composer-row :global(textarea.composer-field) {
		flex: 1;
		width: auto;
		min-width: 0;
		min-height: var(--control-h);
		max-height: 160px;
		resize: none;
	}
	/* "Message to <name>" stays on the one line the field starts with */
	.composer-row :global(textarea.composer-field::placeholder) {
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	/* A phone: Send keeps its word for screen readers, the field gets the room */
	@media (max-width: 767px) {
		.composer-row :global(.composer-send .composer-send-word) {
			position: absolute;
			width: 1px;
			height: 1px;
			overflow: hidden;
			clip-path: inset(50%);
			white-space: nowrap;
		}
		.composer-row :global(.composer-send) {
			min-width: var(--control-h);
			padding-inline: 0;
			justify-content: center;
		}
	}
	:global(.menu.chat-emoji-grid) {
		display: grid;
		grid-template-columns: repeat(8, 36px);
		gap: 2px;
		padding: var(--space-1);
	}
	:global(.menu.chat-emoji-grid .menu-item) {
		justify-content: center;
		min-height: 36px;
		padding: 0;
	}
	:global(.menu.chat-canned-menu) {
		max-width: min(360px, 90vw);
	}
	@media (pointer: coarse) {
		/* A phone's keyboard has its own emoji */
		.composer-row :global(.icon-btn.composer-emoji) {
			display: none;
		}
		:global(.menu.chat-emoji-grid) {
			grid-template-columns: repeat(6, 44px);
		}
		:global(.menu.chat-emoji-grid .menu-item) {
			min-height: 44px;
		}
	}
</style>
