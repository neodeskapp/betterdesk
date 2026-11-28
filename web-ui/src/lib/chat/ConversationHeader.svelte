<!--
  The head of an open conversation (A242).
  Support: the computer by name and system, online or not, its owner,
  folder and ID, who has the conversation, and the actions: take it, close
  it with a note (and resolve the computer's help request when it has
  one), open it again, connect while it is online; in the menu: hand it
  to a colleague who sees the computer, open the device, a ticket, the
  help request. Team: the colleague with role and status, the group with its
  members and a way to change them, or the team with its members and
  user groups (changed by people with Manage teams). On a phone a back
  button returns to the list.
-->
<script lang="ts">
	import { link } from '#lib/nav.ts';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import * as act from '#lib/devices/actions.svelte.ts';
	import { openDevice } from '#lib/state/devicePanel.svelte.ts';
	import { can } from '#lib/state/session.svelte.ts';
	import Avatar from '#lib/ui/Avatar.svelte';
	import Button from '#lib/ui/Button.svelte';
	import Checkbox from '#lib/ui/Checkbox.svelte';
	import Dialog from '#lib/ui/Dialog.svelte';
	import Field from '#lib/ui/Field.svelte';
	import Icon from '#lib/ui/Icon.svelte';
	import IconButton from '#lib/ui/IconButton.svelte';
	import MenuButton from '#lib/ui/MenuButton.svelte';
	import MenuDivider from '#lib/ui/MenuDivider.svelte';
	import MenuItem from '#lib/ui/MenuItem.svelte';
	import OsIcon from '#lib/ui/OsIcon.svelte';
	import Status from '#lib/ui/Status.svelte';
	import Textarea from '#lib/ui/Textarea.svelte';
	import { toast } from '#lib/ui/toasts.svelte.ts';
	import ValueCopy from '#lib/ui/ValueCopy.svelte';
	import * as api from './api.ts';
	import { goTo } from './nav.ts';
	import { chat } from './client.svelte.ts';
	import type { Conversation, Device, Person } from './types.ts';

	interface Props {
		conv: string;
		detail: api.ConversationDetail | null;
		onback?: () => void;
		/** Shown in the narrow panel: fewer words on the buttons */
		compact?: boolean;
		onchanged: (c: Conversation) => void;
		ongroup?: () => void;
		/** Change the team (Manage teams) */
		onteam?: () => void;
	}

	let { conv, detail, onback, compact = false, onchanged, ongroup, onteam }: Props = $props();

	const c = $derived(detail?.conversation);
	const device = $derived<Device | undefined>(detail?.device);
	const me = $derived(chat.me);
	const mine = $derived(!!c && !!me && c.assignee_id === me.id);
	const mayClose = $derived(!!c && (chat.may.assign || mine));

	// "Loading" only when the facts take a while, not on every quick switch
	let slow = $state(false);
	$effect(() => {
		if (c) return;
		slow = false;
		const timer = setTimeout(() => (slow = true), 400);
		return () => clearTimeout(timer);
	});

	function status(p: Person): Person['status'] {
		return (chat.presence[p.id] as Person['status']) || p.status;
	}

	async function assignTo(id: number) {
		try {
			onchanged(await api.assign(conv, id));
		} catch (err) {
			toast.error(err instanceof Error ? err.message : t('chat.action_failed'));
		}
	}

	let closing = $state(false);
	let note = $state('');
	let resolveHelp = $state(true);
	let busy = $state(false);

	async function close() {
		busy = true;
		try {
			// Only a conversation with a help request has one to resolve
			const r = await api.closeConversation(conv, note.trim(), !!c?.help_request_id && resolveHelp);
			onchanged(r.conversation);
			closing = false;
			note = '';
			toast.success(
				r.resolved ? t('chat.closed_resolved', { count: r.resolved }) : t('chat.closed_toast'),
				device?.name
			);
		} catch (err) {
			toast.error(err instanceof Error ? err.message : t('chat.action_failed'));
		} finally {
			busy = false;
		}
	}

	async function reopen() {
		try {
			onchanged(await api.reopenConversation(conv));
		} catch (err) {
			toast.error(err instanceof Error ? err.message : t('chat.action_failed'));
		}
	}

	function newTicket() {
		if (!device) return;
		goTo(
			link(
				`/tickets?new=1&device=${encodeURIComponent(device.id)}&title=${encodeURIComponent(t('chat.ticket_title', { name: device.name }))}`
			)
		);
	}
</script>

<header class={['conv-head', compact && 'is-compact']}>
	{#if onback}
		<IconButton class="conv-back" icon="arrow_back" label={t('chat.back_to_list')} onclick={onback} />
	{/if}
	{#if !c}
		<!-- Two lines like a loaded head, so it keeps its height -->
		<div class="conv-who">
			<p class="conv-name">{slow ? t('common.loading') : ' '}</p>
			<p class="conv-sub" aria-hidden="true">&nbsp;</p>
		</div>
	{:else if c.kind === 'support'}
		<span class="conv-icon" aria-hidden="true"
			>{#if device?.platform}<OsIcon platform={device.platform} />{:else}<Icon name="computer" />{/if}</span
		>
		<div class="conv-who">
			<div class="conv-title">
				<h2 class="conv-name">{device?.name ?? conv}</h2>
				{#if c.state === 'closed'}
					<Status tone="neutral" label={t('chat.closed_by', { name: c.closed_by || '?' })} />
				{:else if c.assignee_id}
					<span class="conv-assignee"
						><Avatar user={{ id: c.assignee_id, username: c.assignee_name }} size="sm" />{mine
							? t('chat.assigned_you')
							: t('chat.assigned_to', { name: c.assignee_name ?? '?' })}</span
					>
				{/if}
			</div>
			<p class="conv-sub">
				<Status
					tone={device?.online ? 'good' : 'neutral'}
					label={device?.online ? t('chat.online') : t('chat.offline')}
				/>
				{#if device?.owner}<span><Icon name="person" />{device.owner}</span>{/if}
				{#if device?.folder}<span><Icon name="folder" />{device.folder}</span>{/if}
				{#if device && device.hostname && device.hostname !== device.name}<span
						><Icon name="dns" />{device.hostname}</span
					>{/if}
				<span class="conv-id"><ValueCopy value={conv} /></span>
				{#if c.ticket_id}<a href={link(`/tickets?id=${c.ticket_id}`)}
						>{t('chat.ticket_linked', { id: c.ticket_id })}</a
					>{/if}
			</p>
		</div>
		<div class="conv-actions">
			{#if c.state === 'open' && chat.may.assign && !mine}
				<Button size="sm" variant="primary" icon="pan_tool" onclick={() => me && void assignTo(me.id)}
					>{c.assignee_id ? t('chat.take_over') : t('chat.take')}</Button
				>
			{/if}
			{#if c.state === 'open' && mayClose}
				<Button size="sm" icon="task_alt" onclick={() => (closing = true)}>{t('chat.close')}</Button>
			{:else if c.state === 'closed'}
				<Button size="sm" icon="replay" onclick={() => void reopen()}>{t('chat.reopen')}</Button>
			{/if}
			{#if device?.online && can('device.connect')}
				<Button
					size="sm"
					icon="desktop_windows"
					onclick={() => device && void act.connectInBrowser({ id: device.id, hostname: device.hostname })}
					>{t('chat.connect')}</Button
				>
			{/if}
			<MenuButton kebab label={t('chat.conversation_actions')} align="end">
				{#if chat.may.assign && detail?.assignable?.length}
					{#each detail.assignable.filter((p) => p.id !== c.assignee_id) as p (p.id)}
						<MenuItem icon="forward" onselect={() => void assignTo(p.id)}
							>{t('chat.hand_to', { name: p.name })}</MenuItem
						>
					{/each}
					{#if c.assignee_id}
						<MenuItem icon="person_remove" onselect={() => void assignTo(0)}>{t('chat.unassign')}</MenuItem>
					{/if}
					<MenuDivider />
				{/if}
				{#if can('device.view')}
					<MenuItem icon="info" onselect={() => openDevice(conv)}>{t('chat.open_device')}</MenuItem>
				{/if}
				{#if can('device.edit')}
					<MenuItem icon="confirmation_number" onselect={newTicket}>{t('chat.new_ticket')}</MenuItem>
				{/if}
				{#if c.help_request_id}
					<MenuItem icon="support" onselect={() => goTo(link('/help-requests'))}
						>{t('chat.help_request_open')}</MenuItem
					>
				{/if}
			</MenuButton>
		</div>
	{:else}
		{@const people = detail?.people ?? []}
		{@const other = c.kind === 'dm' ? people.find((p) => p.id !== me?.id) : undefined}
		{#if other}
			<Avatar
				user={{ id: other.id, username: other.username, name: other.name }}
				size="md"
				presence={status(other)}
			/>
		{:else}
			<span class="conv-icon" aria-hidden="true"
				><Icon name={c.kind === 'old' ? 'history' : c.kind === 'team' ? 'workspaces' : 'group'} /></span
			>
		{/if}
		<div class="conv-who">
			<h2 class="conv-name">
				{other
					? other.name
					: c.title ||
						t(c.kind === 'old' ? 'chat.old_messages' : c.kind === 'team' ? 'chat.team' : 'chat.group')}
			</h2>
			<p class="conv-sub">
				{#if other}
					<span>{t(`chat.presence_${status(other)}`)}</span>
					<span>{t(`users.role_${other.role}`)}</span>
				{:else if c.kind === 'old'}
					<span>{t('chat.old_messages_hint')}</span>
				{:else}
					<span>{people.map((p) => p.name).join(', ')}</span>
					{#if detail?.groups?.length}<span
							>{t('chat.team_groups_line', { names: detail.groups.join(', ') })}</span
						>{/if}
				{/if}
			</p>
		</div>
		{#if c.kind === 'group' && ongroup}
			<div class="conv-actions">
				<Button size="sm" icon="group_add" onclick={ongroup}>{t('chat.edit_group')}</Button>
			</div>
		{:else if c.kind === 'team' && onteam && can('chat.teams_manage')}
			<div class="conv-actions">
				<Button size="sm" icon="edit" onclick={onteam}>{t('chat.edit_team')}</Button>
			</div>
		{/if}
	{/if}
</header>

{#if closing}
	<Dialog
		open
		title={t('chat.close_title', { name: device?.name ?? conv })}
		onclose={() => (closing = false)}
	>
		<Field label={t('chat.close_note')} hint={t('chat.close_note_hint')}>
			{#snippet children(f)}<Textarea {...f} rows={3} maxlength={1000} bind:value={note} />{/snippet}
		</Field>
		{#if c?.help_request_id}
			<Checkbox bind:checked={resolveHelp} label={t('chat.close_resolve_help')} />
		{/if}
		{#snippet footer()}
			<Button onclick={() => (closing = false)}>{t('actions.cancel')}</Button>
			<Button variant="primary" icon="task_alt" {busy} onclick={() => void close()}>{t('chat.close')}</Button>
		{/snippet}
	</Dialog>
{/if}

<style>
	.conv-head {
		display: flex;
		flex: none;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-2) var(--space-3);
		padding: var(--space-3) var(--space-4);
		border-bottom: 1px solid var(--rule);
	}
	.conv-title {
		display: flex;
		flex-wrap: wrap;
		align-items: baseline;
		gap: var(--space-1) var(--space-3);
		min-width: 0;
	}
	.conv-head :global(.conv-back) {
		display: none;
	}
	/* The computer's system in a square box the size of a person's avatar */
	.conv-icon {
		display: inline-flex;
		flex: none;
		align-items: center;
		justify-content: center;
		width: 32px;
		height: 32px;
		border-radius: var(--radius);
		background: var(--tag-bg);
		color: var(--text-muted);
	}
	.conv-icon :global(.os-icon) {
		width: var(--icon-btn-glyph);
		height: var(--icon-btn-glyph);
	}
	.conv-icon :global(.material-icons) {
		font-size: var(--icon-btn-glyph);
	}
	.conv-who {
		flex: 1;
		min-width: 12rem;
	}
	.conv-name {
		margin: 0;
		font-size: var(--text-md);
		overflow-wrap: anywhere;
	}
	.conv-assignee {
		display: inline-flex;
		align-items: center;
		align-self: center;
		gap: var(--space-2);
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
	.conv-sub {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-1) var(--space-3);
		margin: var(--space-1) 0 0;
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
	.conv-sub > span {
		display: inline-flex;
		align-items: center;
		gap: var(--space-1);
		min-width: 0;
	}
	.conv-sub > span > :global(.material-icons) {
		font-size: 16px;
	}
	.conv-actions {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-2);
	}
	/* Narrow (a phone, the panel): the back button takes the icon's place,
	   and the actions line up under the name */
	@media (max-width: 767px) {
		.conv-head {
			display: grid;
			grid-template-columns: auto minmax(0, 1fr);
		}
		.conv-head :global(.conv-back) {
			display: inline-flex;
		}
		.conv-icon,
		.conv-head > :global(.avatar),
		.conv-head > :global(.avatar-presence) {
			display: none;
		}
		.conv-actions {
			grid-column: 2;
		}
		.conv-id {
			display: none !important;
		}
	}
	.is-compact {
		display: grid;
		grid-template-columns: auto minmax(0, 1fr);
		padding-inline: var(--space-3);
	}
	.is-compact :global(.conv-back) {
		display: inline-flex;
	}
	.is-compact .conv-icon,
	.is-compact > :global(.avatar),
	.is-compact > :global(.avatar-presence) {
		display: none;
	}
	.is-compact .conv-actions {
		grid-column: 2;
	}
</style>
