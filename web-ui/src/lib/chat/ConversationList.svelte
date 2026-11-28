<!--
  The list side of the chat (A242): Support and Team as two sections of
  equal weight, a search that finds computers by name, ID, host name,
  owner and folder (Support) or people and groups by name (Team), and the
  rows. Support has the inbox's three filters: Open (nobody's first),
  Mine, Closed. A computer's row says its name, folder, whether it is
  online, who has it and its newest line; a colleague's row the name,
  role, status and newest line. Team lists the teams one is in first,
  then the direct and group conversations, then the colleagues one has no
  conversation with yet, so it is plain who can be written to.
-->
<script lang="ts">
	import { untrack } from 'svelte';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import Avatar from '#lib/ui/Avatar.svelte';
	import Button from '#lib/ui/Button.svelte';
	import Icon from '#lib/ui/Icon.svelte';
	import SearchField from '#lib/ui/SearchField.svelte';
	import SegmentedFilter from '#lib/ui/SegmentedFilter.svelte';
	import Time from '#lib/ui/Time.svelte';
	import { loadInbox, loadServerSettings, loadTeam, searchMessages } from './api.ts';
	import { chat } from './client.svelte.ts';
	import { lastLine, snippet } from './logic.ts';
	import type {
		FoundConversation,
		InboxCounts,
		InboxFilter,
		InboxItem,
		Message,
		Person,
		Section,
		TeamItem
	} from './types.ts';

	interface Props {
		section: Section;
		filter: InboxFilter;
		active: string | null;
		onsection: (s: Section) => void;
		onfilter: (f: InboxFilter) => void;
		/** message: a found message to show in the conversation */
		onopen: (conv: string, row: HTMLElement | null, message?: string) => void;
		onnew: () => void;
		/** Open a direct conversation with a colleague */
		onperson: (p: Person, row: HTMLElement | null) => void;
		compact?: boolean;
	}

	let {
		section,
		filter,
		active,
		onsection,
		onfilter,
		onopen,
		onnew,
		onperson,
		compact = false
	}: Props = $props();

	let q = $state('');
	let support = $state.raw<InboxItem[] | null>(null);
	let counts = $state<InboxCounts>({ open: 0, mine: 0, closed: 0, unassigned: 0 });
	let team = $state.raw<TeamItem[] | null>(null);
	let people = $state.raw<Person[]>([]);
	let old = $state.raw<TeamItem[]>([]);
	let failed = $state(false);
	/** Messages with the search text, and their conversations */
	let found = $state.raw<Message[]>([]);
	let foundIn = $state.raw<Record<string, FoundConversation>>({});
	let searching: AbortController | null = null;
	/** "Chat with computers" is off (A259): computers can't open the chat */
	let computersOff = $state(false);

	async function findMessages(text: string) {
		searching?.abort();
		if (text.length < 2) {
			found = [];
			return;
		}
		const ctl = new AbortController();
		searching = ctl;
		try {
			const r = await searchMessages(text, section, ctl.signal);
			if (searching === ctl) {
				found = r.messages;
				foundIn = r.conversations;
			}
		} catch {
			if (searching === ctl) found = [];
		}
	}

	async function load() {
		failed = false;
		void findMessages(q.trim());
		try {
			if (section === 'support' && chat.may.support) {
				void loadServerSettings().then(
					(s) => (computersOff = !s.computers),
					() => undefined
				);
				const r = await loadInbox(filter, q.trim());
				support = r.conversations;
				counts = r.counts;
			} else if (section === 'team' && chat.may.team) {
				const r = await loadTeam();
				team = r.conversations;
				people = r.people;
				old = r.old;
			}
		} catch {
			failed = true;
		}
	}

	// Loads when the section, filter or search changes, and when messages or conversations change
	$effect(() => {
		void section;
		void filter;
		void chat.listVersion;
		void chat.may.support;
		void chat.may.team;
		const text = q;
		const timer = setTimeout(() => void untrack(load), text ? 250 : 0);
		return () => clearTimeout(timer);
	});

	const tq = $derived(q.trim().toLowerCase());
	const matching = $derived(
		(team ?? []).filter(
			(c) =>
				!tq ||
				(c.title ?? '').toLowerCase().includes(tq) ||
				c.people.some((p) => p.name.toLowerCase().includes(tq) || p.username.toLowerCase().includes(tq))
		)
	);
	const teamsShown = $derived(matching.filter((c) => c.kind === 'team'));
	const teamShown = $derived(matching.filter((c) => c.kind !== 'team'));
	const others = $derived.by(() => {
		const withDirect = new Set(
			(team ?? []).filter((c) => c.kind === 'dm').flatMap((c) => c.people.map((p) => p.id))
		);
		return people
			.filter((p) => !withDirect.has(p.id))
			.filter((p) => !tq || p.name.toLowerCase().includes(tq) || p.username.toLowerCase().includes(tq));
	});
	const oldShown = $derived(old.filter((c) => !tq || (c.title ?? '').toLowerCase().includes(tq)));

	function status(p: Person): Person['status'] {
		const live = chat.presence[p.id];
		return (live as Person['status']) || p.status;
	}

	function teamName(c: TeamItem): string {
		if (c.kind === 'group') return c.title || t('chat.group');
		if (c.kind === 'team') return c.title || t('chat.team');
		return c.people[0]?.name ?? t('chat.unknown_person');
	}

	const sections = $derived(
		[
			chat.may.support && {
				value: 'support' as const,
				label: chat.supportUnread
					? `${t('chat.section_support')} (${chat.supportUnread})`
					: t('chat.section_support')
			},
			chat.may.team && {
				value: 'team' as const,
				label: chat.teamUnread ? `${t('chat.section_team')} (${chat.teamUnread})` : t('chat.section_team')
			}
		].filter((x) => !!x)
	);

	const filters = $derived([
		{ value: 'open' as const, label: `${t('chat.filter_open')} (${counts.open})` },
		{ value: 'mine' as const, label: `${t('chat.filter_mine')} (${counts.mine})` },
		{ value: 'closed' as const, label: t('chat.filter_closed') }
	]);

	/**
	 * A row the pointer rests on, or that has focus or is pressed, loads its
	 * conversation ahead of the click, so it opens without a "Loading" line
	 */
	let resting: ReturnType<typeof setTimeout> | null = null;
	function ahead(conv: string) {
		const leave = () => {
			if (resting) clearTimeout(resting);
			resting = null;
		};
		return {
			onpointerenter: () => {
				leave();
				resting = setTimeout(() => chat.preload(conv), 120);
			},
			onpointerleave: leave,
			onpointerdown: () => chat.preload(conv),
			onfocus: () => chat.preload(conv)
		};
	}
	$effect(() => () => {
		if (resting) clearTimeout(resting);
	});

	function waiting(ms: number | undefined): boolean {
		return !!ms && Date.now() - ms > 60_000;
	}
</script>

{#snippet foundRows()}
	{#if found.length}
		<h3 class="rows-title">{t('chat.found_messages')}</h3>
		<ul class="rows" aria-label={t('chat.found_messages')}>
			{#each found as m (m.id)}
				{@const part = snippet(m.text, q.trim())}
				<li>
					<button
						type="button"
						class="row is-found"
						onclick={(e) => onopen(m.conversation_id, e.currentTarget, m.id)}
						{...ahead(m.conversation_id)}
					>
						<span class="row-avatar"
							><span class="row-icon" aria-hidden="true"
								><Icon name={foundIn[m.conversation_id]?.kind === 'support' ? 'computer' : 'forum'} /></span
							></span
						>
						<span class="row-main">
							<span class="row-top">
								<span class="row-name">{foundIn[m.conversation_id]?.name || m.conversation_id}</span>
								<span class="row-time"><Time value={m.created_at} /></span>
							</span>
							<span class="row-found"
								>{#if m.from.name}<span class="row-from">{m.from.name}:</span>{/if}{part.before}<mark
									>{part.match}</mark
								>{part.after}</span
							>
						</span>
					</button>
				</li>
			{/each}
		</ul>
	{/if}
{/snippet}

<div class={['chat-list', compact && 'is-compact']}>
	<div class="chat-list-top">
		{#if sections.length > 1}
			<SegmentedFilter
				class="chat-sections"
				label={t('chat.sections')}
				options={sections}
				value={section}
				onchange={onsection}
			/>
		{/if}
		<div class="chat-find" role="search">
			<SearchField
				label={section === 'support' ? t('chat.search_support') : t('chat.search_team')}
				autocomplete="off"
				bind:value={q}
			/>
			<Button icon="edit_square" onclick={onnew}>{t('chat.new')}</Button>
		</div>
		{#if section === 'support'}
			<SegmentedFilter
				class="chat-filters"
				label={t('chat.inbox_filters')}
				options={filters}
				value={filter}
				onchange={onfilter}
			/>
		{/if}
	</div>

	<div class="chat-rows">
		{#if section === 'support' && computersOff}
			<p class="chat-list-note" role="status">{t('chat.computers_off')}</p>
		{/if}
		{#if failed}
			<p class="chat-list-state is-error">
				{t('chat.list_failed')}
				<Button size="sm" onclick={() => void load()}>{t('actions.retry')}</Button>
			</p>
		{:else if section === 'support'}
			{#if support === null}
				<p class="chat-list-state">{t('common.loading')}</p>
			{:else if !support.length}
				{#if !found.length}
					<div class="chat-list-empty">
						<Icon name="support_agent" />
						<p>
							{q.trim()
								? t('chat.no_match')
								: filter === 'mine'
									? t('chat.empty_mine')
									: filter === 'closed'
										? t('chat.empty_closed')
										: t('chat.empty_support')}
						</p>
					</div>
				{/if}
				{@render foundRows()}
			{:else}
				<ul class="rows" aria-label={t('chat.section_support')}>
					{#each support as c (c.id)}
						{@const unread = c.unread}
						<li>
							<button
								type="button"
								class={['row', active === c.id && 'is-current', unread > 0 && 'is-unread']}
								aria-current={active === c.id ? 'true' : undefined}
								onclick={(e) => onopen(c.id, e.currentTarget)}
								{...ahead(c.id)}
							>
								<span class="row-icon" aria-hidden="true">
									<Icon name="computer" />
									<span class={['row-dot', c.device.online && 'is-on']}></span>
								</span>
								<span class="row-main">
									<span class="row-top">
										<span class="row-name">{c.device.name}</span>
										{#if c.last_message}<span class="row-time"><Time value={c.last_message.at} /></span>{/if}
									</span>
									<span class="row-bottom">
										<span class="row-last">{lastLine(c.last_message, t)}</span>
										{#if unread > 0}<span class="row-badge"
												><span class="sr-only">{t('chat.unread_count', { count: unread })}</span><span
													aria-hidden="true">{unread > 99 ? '99+' : unread}</span
												></span
											>{/if}
									</span>
									<span class="row-meta is-split">
										<span class="row-meta-text">
											{#if c.device.owner}<span
													><Icon name="person" /><span class="row-cut">{c.device.owner}</span></span
												>{/if}
											{#if c.device.folder}<span
													><Icon name="folder" /><span class="row-cut">{c.device.folder}</span></span
												>{/if}
											<span class="sr-only">{c.device.online ? t('chat.online') : t('chat.offline')}</span>
										</span>
										{#if c.state === 'closed'}
											<span class="row-tag">{t('chat.closed')}</span>
										{:else if c.assignee_id}
											<span class="row-tag is-person"
												><Avatar
													user={{ id: c.assignee_id, username: c.assignee_name }}
													size="sm"
												/>{c.assignee_id === chat.me?.id ? t('chat.assigned_you') : c.assignee_name}</span
											>
										{:else}
											<span class={['row-tag', waiting(c.waiting_since) && 'is-waiting']}
												>{t('chat.unassigned')}</span
											>
										{/if}
									</span>
								</span>
							</button>
						</li>
					{/each}
				</ul>
				{@render foundRows()}
			{/if}
		{:else if team === null}
			<p class="chat-list-state">{t('common.loading')}</p>
		{:else}
			{#snippet convRows(rows: TeamItem[], label: string)}
				<ul class="rows" aria-label={label}>
					{#each rows as c (c.id)}
						{@const unread = c.unread}
						{@const person = c.kind === 'dm' ? c.people[0] : undefined}
						<li>
							<button
								type="button"
								class={['row', active === c.id && 'is-current', unread > 0 && 'is-unread']}
								aria-current={active === c.id ? 'true' : undefined}
								onclick={(e) => onopen(c.id, e.currentTarget)}
								{...ahead(c.id)}
							>
								<span class="row-avatar">
									{#if person}
										<Avatar
											user={{ id: person.id, username: person.username, name: person.name }}
											size="md"
											presence={status(person)}
										/>
									{:else}
										<span class="row-icon" aria-hidden="true"
											><Icon name={c.kind === 'team' ? 'workspaces' : 'group'} /></span
										>
									{/if}
								</span>
								<span class="row-main">
									<span class="row-top">
										<span class="row-name">{teamName(c)}</span>
										{#if c.last_message}<span class="row-time"><Time value={c.last_message.at} /></span>{/if}
									</span>
									<span class="row-bottom">
										<span class="row-last">{lastLine(c.last_message, t)}</span>
										{#if unread > 0}<span class="row-badge"
												><span class="sr-only">{t('chat.unread_count', { count: unread })}</span><span
													aria-hidden="true">{unread > 99 ? '99+' : unread}</span
												></span
											>{/if}
									</span>
									<span class="row-meta">
										{#if person}<span>{t(`chat.presence_${status(person)}`)}</span><span
												>{t(`users.role_${person.role}`)}</span
											>{:else}<span>{t('chat.member_count', { count: c.people.length })}</span>{/if}
									</span>
								</span>
							</button>
						</li>
					{/each}
				</ul>
			{/snippet}
			{#if teamsShown.length}
				<h3 class="rows-title">{t('chat.teams')}</h3>
				{@render convRows(teamsShown, t('chat.teams'))}
			{/if}
			{#if teamShown.length}
				{#if teamsShown.length}<h3 class="rows-title">{t('chat.conversations')}</h3>{/if}
				{@render convRows(teamShown, t('chat.conversations'))}
			{/if}
			{#if others.length}
				<h3 class="rows-title">{t('chat.colleagues')}</h3>
				<ul class="rows" aria-label={t('chat.colleagues')}>
					{#each others as p (p.id)}
						<li>
							<button type="button" class="row" onclick={(e) => onperson(p, e.currentTarget)}>
								<span class="row-avatar"
									><Avatar
										user={{ id: p.id, username: p.username, name: p.name }}
										size="md"
										presence={status(p)}
									/></span
								>
								<span class="row-main">
									<span class="row-top"><span class="row-name">{p.name}</span></span>
									<span class="row-meta"
										><span>{t(`chat.presence_${status(p)}`)}</span><span>{t(`users.role_${p.role}`)}</span
										></span
									>
								</span>
							</button>
						</li>
					{/each}
				</ul>
			{/if}
			{#if oldShown.length}
				<h3 class="rows-title">{t('chat.old_messages')}</h3>
				<ul class="rows" aria-label={t('chat.old_messages')}>
					{#each oldShown as c (c.id)}
						<li>
							<button
								type="button"
								class={['row', active === c.id && 'is-current']}
								aria-current={active === c.id ? 'true' : undefined}
								onclick={(e) => onopen(c.id, e.currentTarget)}
								{...ahead(c.id)}
							>
								<span class="row-avatar"
									><span class="row-icon" aria-hidden="true"><Icon name="history" /></span></span
								>
								<span class="row-main">
									<span class="row-top">
										<span class="row-name">{c.title || t('chat.old_messages')}</span>
										{#if c.last_message}<span class="row-time"><Time value={c.last_message.at} /></span>{/if}
									</span>
									<span class="row-bottom"><span class="row-last">{lastLine(c.last_message, t)}</span></span>
									<span class="row-meta"><span>{t('chat.old_messages')}</span></span>
								</span>
							</button>
						</li>
					{/each}
				</ul>
			{/if}
			{@render foundRows()}
			{#if !teamsShown.length && !teamShown.length && !others.length && !oldShown.length && !found.length}
				<div class="chat-list-empty">
					<Icon name="group" />
					<p>{q.trim() ? t('chat.no_match') : t('chat.empty_team')}</p>
				</div>
			{/if}
		{/if}
	</div>
</div>

<style>
	.chat-list {
		display: flex;
		flex-direction: column;
		height: 100%;
		min-height: 0;
	}
	.chat-list-note {
		margin: var(--space-2) var(--space-3);
		padding: var(--space-2) var(--space-3);
		border: 1px solid var(--rule);
		border-radius: var(--radius);
		color: var(--text-muted);
	}
	.chat-list-top {
		display: flex;
		flex: none;
		flex-direction: column;
		gap: var(--space-2);
		padding: var(--space-3);
		border-bottom: 1px solid var(--rule);
	}
	.chat-list-top :global(.chat-sections),
	.chat-list-top :global(.chat-filters) {
		display: flex;
	}
	.chat-list-top :global(.chat-sections .filter-btn),
	.chat-list-top :global(.chat-filters .filter-btn) {
		flex: 1;
	}
	.chat-find {
		display: flex;
		align-items: center;
		gap: var(--space-2);
	}
	.chat-find :global(.search-field),
	.chat-find > :global(:first-child) {
		flex: 1;
		min-width: 0;
	}
	.chat-rows {
		flex: 1;
		min-height: 0;
		overflow-y: auto;
	}
	.chat-list-state {
		margin: 0;
		padding: var(--space-4) var(--space-3);
		color: var(--text-muted);
	}
	.chat-list-state.is-error {
		color: var(--danger);
	}
	.chat-list-empty {
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: var(--space-2);
		padding: var(--space-6, 48px) var(--space-4);
		color: var(--text-muted);
		text-align: center;
	}
	.chat-list-empty :global(.material-icons) {
		font-size: 40px;
	}
	.chat-list-empty p {
		margin: 0;
		max-width: 28ch;
	}
	.rows {
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.rows-title {
		margin: 0;
		padding: var(--space-3) var(--space-3) var(--space-1);
		color: var(--text-muted);
		font-size: var(--text-sm);
		font-weight: 600;
	}
	.row {
		display: flex;
		align-items: flex-start;
		gap: var(--space-3);
		width: 100%;
		padding: var(--space-3) var(--space-3) var(--space-3) calc(var(--space-3) - 3px);
		border: 0;
		border-inline-start: 3px solid transparent;
		background: transparent;
		color: var(--text);
		font: inherit;
		text-align: start;
		cursor: pointer;
	}
	.row:hover {
		background: var(--hover);
	}
	.row:focus-visible {
		outline: 2px solid var(--focus, var(--accent));
		outline-offset: -2px;
	}
	.row.is-current {
		border-inline-start-color: var(--accent);
		background: var(--accent-tint);
	}
	.row-icon {
		position: relative;
		display: inline-flex;
		flex: none;
		align-items: center;
		justify-content: center;
		width: 36px;
		height: 36px;
		border-radius: 50%;
		background: var(--hover);
		color: var(--text-muted);
	}
	.row-dot {
		position: absolute;
		inset-block-end: 0;
		inset-inline-end: 0;
		width: 10px;
		height: 10px;
		border: 2px solid var(--surface, var(--bg));
		border-radius: 50%;
		background: var(--text-muted);
	}
	.row-dot.is-on {
		background: var(--ok, #2e7d32);
	}
	.row-avatar {
		flex: none;
	}
	.row-main {
		display: flex;
		flex: 1;
		flex-direction: column;
		gap: 2px;
		min-width: 0;
	}
	.row-top,
	.row-bottom {
		display: flex;
		align-items: baseline;
		gap: var(--space-2);
	}
	.row-name {
		flex: 1;
		min-width: 0;
		overflow: hidden;
		font-weight: 600;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.row-time {
		flex: none;
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
	.row-meta,
	.row-meta-text {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 0 var(--space-3);
		min-width: 0;
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
	/* Owner and folder, then who has it; the tag wraps to the next line rather than cut the names */
	.row-meta.is-split {
		gap: 2px var(--space-2);
	}
	.row-meta-text {
		flex: 0 1 auto;
		gap: 0 var(--space-2);
	}
	.row-meta-text > span {
		display: inline-flex;
		align-items: center;
		gap: 4px;
		min-width: 0;
		max-width: 100%;
	}
	.row-cut {
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.row-meta-text :global(.material-icons) {
		flex: none;
		font-size: 14px;
	}
	.row-last {
		flex: 1;
		min-width: 0;
		overflow: hidden;
		color: var(--text-muted);
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.row-found {
		display: -webkit-box;
		overflow: hidden;
		color: var(--text-muted);
		-webkit-box-orient: vertical;
		-webkit-line-clamp: 2;
		line-clamp: 2;
		overflow-wrap: anywhere;
	}
	.row-found mark {
		border-radius: 2px;
		background: var(--accent-tint);
		color: var(--text);
		font-weight: 600;
	}
	.row-from {
		margin-inline-end: 0.3em;
		color: var(--text);
	}
	.is-unread .row-last {
		color: var(--text);
		font-weight: 600;
	}
	.row-badge {
		flex: none;
		min-width: 20px;
		padding: 0 6px;
		border-radius: 999px;
		background: var(--accent);
		color: var(--on-accent, #fff);
		font-size: var(--text-sm);
		font-weight: 700;
		line-height: 20px;
		text-align: center;
	}
	.row-tag {
		display: inline-flex;
		flex: none;
		align-items: center;
		gap: var(--space-1);
		max-width: 100%;
		padding: 0 var(--space-2);
		overflow: hidden;
		white-space: nowrap;
		border: 1px solid var(--rule);
		border-radius: var(--radius);
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
	.row-tag.is-person {
		padding-inline-start: 2px;
		border: 0;
	}
	.row-tag.is-waiting {
		border-color: var(--warning, #b26a00);
		color: var(--warning-text, var(--text));
	}
	/* A phone: the controls and rows line up with the page title */
	@media (max-width: 767px) {
		.chat-list:not(.is-compact) .chat-list-top {
			padding-inline: var(--page-pad);
		}
		.chat-list:not(.is-compact) .row {
			padding-inline: calc(var(--page-pad) - 3px) var(--page-pad);
		}
		.chat-list:not(.is-compact) .rows-title {
			padding-inline: var(--page-pad);
		}
	}
</style>
