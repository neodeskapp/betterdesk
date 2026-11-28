<!--
  The list side of the chat (A242): Support and Team as two tabs, a search
  that finds computers by name, ID, host name, owner and folder (Support)
  or people and groups by name (Team), and the rows. Support has the
  inbox's three filters: Open (nobody's first), Mine, Closed. A computer's
  row says its name, whether it is online, owner, folder, who has it and
  its newest line; a colleague's row the name, role, status and newest
  line. A Support search also lists the computers that match and have no
  conversation yet ("Write to a computer"), and when Open or Mine finds
  no conversation, the closed ones that match. Team lists the teams one
  is in first, then the direct and group conversations, then the
  colleagues one has no conversation with yet (a direct conversation
  without a message counts as none), so it is plain who can be written to.
-->
<script lang="ts">
	import { untrack } from 'svelte';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import Avatar from '#lib/ui/Avatar.svelte';
	import Button from '#lib/ui/Button.svelte';
	import Icon from '#lib/ui/Icon.svelte';
	import Notice from '#lib/ui/Notice.svelte';
	import OsIcon from '#lib/ui/OsIcon.svelte';
	import SearchField from '#lib/ui/SearchField.svelte';
	import SegmentedFilter from '#lib/ui/SegmentedFilter.svelte';
	import States from '#lib/ui/States.svelte';
	import Status from '#lib/ui/Status.svelte';
	import Tabs from '#lib/ui/Tabs.svelte';
	import Time from '#lib/ui/Time.svelte';
	import { platformId } from '#lib/util/display.ts';
	import { findDevices, loadInbox, loadServerSettings, loadTeam, searchMessages } from './api.ts';
	import { chat } from './client.svelte.ts';
	import { lastLine, snippet } from './logic.ts';
	import type {
		Device,
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
	/** Computers the search text matches (Support) */
	let devices = $state.raw<Device[]>([]);
	let lookingUp: AbortController | null = null;
	/** Closed conversations with the search text, when Open or Mine has none */
	let closedFound = $state.raw<InboxItem[]>([]);
	/** "Chat with computers" is off (A259): computers can't open the chat */
	let computersOff = $state(false);

	async function findComputers(text: string) {
		lookingUp?.abort();
		if (section !== 'support' || !chat.may.support || text.length < 2) {
			devices = [];
			return;
		}
		const ctl = new AbortController();
		lookingUp = ctl;
		try {
			const r = await findDevices(text, ctl.signal);
			if (lookingUp === ctl) devices = r;
		} catch {
			if (lookingUp === ctl) devices = [];
		}
	}

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
		const text = q.trim();
		void findMessages(text);
		void findComputers(text);
		try {
			if (section === 'support' && chat.may.support) {
				void loadServerSettings().then(
					(s) => (computersOff = !s.computers),
					() => undefined
				);
				const r = await loadInbox(filter, text);
				// Nothing open or mine with the text: the closed ones may have it
				const closed =
					text && filter !== 'closed' && !r.conversations.length
						? (await loadInbox('closed', text)).conversations
						: [];
				support = r.conversations;
				counts = r.counts;
				closedFound = closed;
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
	/** A direct conversation counts once it has a message, or while it is open */
	const started = (c: TeamItem) => c.kind !== 'dm' || !!c.last_message || c.id === active;
	const teamShown = $derived(matching.filter((c) => c.kind !== 'team' && started(c)));
	const others = $derived.by(() => {
		const withDirect = new Set(
			(team ?? []).filter((c) => c.kind === 'dm' && started(c)).flatMap((c) => c.people.map((p) => p.id))
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

	/** The search's computers that have no row in the list yet */
	const newDevices = $derived.by(() => {
		const listed = new Set([...(support ?? []), ...closedFound].map((c) => c.device.id));
		return devices.filter((d) => !listed.has(d.id));
	});

	/** A label with its count in brackets, or alone when the count is 0 */
	const counted = (label: string, n: number) => (n ? `${label} (${n})` : label);

	const sections = $derived(
		[
			chat.may.support && {
				id: 'support',
				label: counted(t('chat.section_support'), chat.supportUnread)
			},
			chat.may.team && { id: 'team', label: counted(t('chat.section_team'), chat.teamUnread) }
		].filter((x) => !!x)
	);

	const filters = $derived([
		{ value: 'open' as const, label: counted(t('chat.filter_open'), counts.open) },
		{ value: 'mine' as const, label: counted(t('chat.filter_mine'), counts.mine) },
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
</script>

{#snippet iconBox(name: string)}
	<span class="row-icon" aria-hidden="true"><Icon {name} /></span>
{/snippet}

{#snippet deviceBox(platform: string)}
	{#if platformId(platform)}<span class="row-icon" aria-hidden="true"><OsIcon {platform} /></span
		>{:else}{@render iconBox('computer')}{/if}
{/snippet}

{#snippet unreadCount(unread: number)}
	{#if unread > 0}<span class="row-badge"
			><span class="sr-only">{t('chat.unread_count', { count: unread })}</span><span aria-hidden="true"
				>{unread > 99 ? '99+' : unread}</span
			></span
		>{/if}
{/snippet}

{#snippet deviceMeta(d: Device)}
	<Status tone={d.online ? 'good' : 'neutral'} label={d.online ? t('chat.online') : t('chat.offline')} />
	{#if d.owner}<span><Icon name="person" /><span class="row-cut">{d.owner}</span></span>{/if}
	{#if d.folder}<span><Icon name="folder" /><span class="row-cut">{d.folder}</span></span>{/if}
{/snippet}

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
						{@render iconBox(foundIn[m.conversation_id]?.kind === 'support' ? 'computer' : 'forum')}
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

{#snippet supportRows(rows: InboxItem[], label: string)}
	<ul class="rows" aria-label={label}>
		{#each rows as c (c.id)}
			{@const unread = c.unread}
			<li>
				<button
					type="button"
					class={['row', active === c.id && 'is-current', unread > 0 && 'is-unread']}
					aria-current={active === c.id ? 'true' : undefined}
					onclick={(e) => onopen(c.id, e.currentTarget)}
					{...ahead(c.id)}
				>
					{@render deviceBox(c.device.platform)}
					<span class="row-main">
						<span class="row-top">
							<span class="row-name">{c.device.name}</span>
							{#if c.last_message}<span class="row-time"><Time value={c.last_message.at} /></span>{/if}
						</span>
						<span class="row-bottom">
							<span class="row-last">{lastLine(c.last_message, t)}</span>
							{@render unreadCount(unread)}
						</span>
						<span class="row-meta is-split">
							<span class="row-meta-text">{@render deviceMeta(c.device)}</span>
							{#if c.state === 'closed'}
								<Status tone="neutral" label={t('chat.closed')} />
							{:else if c.assignee_id}
								<span class="row-who"
									><Avatar user={{ id: c.assignee_id, username: c.assignee_name }} size="sm" /><span
										class="row-cut"
										>{c.assignee_id === chat.me?.id ? t('chat.assigned_you') : c.assignee_name}</span
									></span
								>
							{:else if c.waiting_since}
								<Status tone="waiting" label={t('chat.unassigned')} />
							{/if}
						</span>
					</span>
				</button>
			</li>
		{/each}
	</ul>
{/snippet}

{#snippet deviceRows()}
	{#if newDevices.length}
		<h3 class="rows-title">{t('chat.new_support')}</h3>
		<ul class="rows" aria-label={t('chat.new_support')}>
			{#each newDevices as d (d.id)}
				<li>
					<button type="button" class="row" onclick={(e) => onopen(d.id, e.currentTarget)}>
						{@render deviceBox(d.platform)}
						<span class="row-main">
							<span class="row-top"><span class="row-name">{d.name}</span></span>
							<span class="row-meta"><span class="row-meta-text">{@render deviceMeta(d)}</span></span>
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
			<Tabs
				class="chat-sections"
				label={t('chat.sections')}
				tabs={sections}
				value={section}
				onchange={(id) => onsection(id as Section)}
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
			<Notice tone="info" class="chat-list-note" role="status" message={t('chat.computers_off')} />
		{/if}
		{#if failed}
			<States
				kind="error"
				class="chat-list-state"
				message={t('chat.list_failed')}
				onretry={() => void load()}
			/>
		{:else if section === 'support'}
			{#if support === null}
				<States kind="loading" class="chat-list-state" />
			{:else}
				{#if support.length}
					{@render supportRows(support, t('chat.section_support'))}
				{:else if closedFound.length}
					<h3 class="rows-title">{t('chat.filter_closed')}</h3>
					{@render supportRows(closedFound, t('chat.filter_closed'))}
				{:else if !found.length && !newDevices.length}
					<States
						kind="empty"
						class="chat-list-empty"
						message={q.trim()
							? t('chat.no_match')
							: filter === 'mine'
								? t('chat.empty_mine')
								: filter === 'closed'
									? t('chat.empty_closed')
									: t('chat.empty_support')}
					/>
				{/if}
				{@render deviceRows()}
				{@render foundRows()}
			{/if}
		{:else if team === null}
			<States kind="loading" class="chat-list-state" />
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
								{#if person}
									<Avatar
										user={{ id: person.id, username: person.username, name: person.name }}
										size="md"
										presence={status(person)}
									/>
								{:else}
									{@render iconBox(c.kind === 'team' ? 'workspaces' : 'group')}
								{/if}
								<span class="row-main">
									<span class="row-top">
										<span class="row-name">{teamName(c)}</span>
										{#if c.last_message}<span class="row-time"><Time value={c.last_message.at} /></span>{/if}
									</span>
									<span class="row-bottom">
										<span class="row-last">{lastLine(c.last_message, t)}</span>
										{@render unreadCount(unread)}
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
								<Avatar
									user={{ id: p.id, username: p.username, name: p.name }}
									size="md"
									presence={status(p)}
								/>
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
								{@render iconBox('history')}
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
				<States
					kind="empty"
					class="chat-list-empty"
					message={q.trim() ? t('chat.no_match') : t('chat.empty_team')}
				/>
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
	.chat-rows :global(.chat-list-note) {
		margin: var(--space-2) var(--space-3);
	}
	.chat-list-top {
		display: flex;
		flex: none;
		flex-direction: column;
		gap: var(--space-2);
		padding: var(--space-3);
		border-bottom: 1px solid var(--rule);
	}
	/* The filters keep their natural width */
	.chat-list-top :global(.chat-filters) {
		align-self: flex-start;
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
	.chat-rows :global(.chat-list-state),
	.chat-rows :global(.chat-list-empty) {
		padding-inline: var(--row-pad-x);
	}
	.rows {
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.rows-title {
		margin: 0;
		padding: var(--space-3) var(--row-pad-x) var(--space-1);
		color: var(--text-muted);
		font-size: var(--text-sm);
		font-weight: 600;
	}
	/* Rows on rules, as ListRow: a list under a heading starts with one */
	.rows-title + .rows {
		border-top: 1px solid var(--rule);
	}
	.row {
		display: flex;
		align-items: flex-start;
		gap: var(--space-3);
		width: 100%;
		min-height: var(--row-h);
		padding: var(--row-pad-y) var(--row-pad-x);
		border: 0;
		border-bottom: 1px solid var(--rule);
		background: transparent;
		color: var(--text);
		font: inherit;
		text-align: start;
		cursor: pointer;
	}
	.row:hover,
	.row.is-current {
		background: var(--bg-subtle);
	}
	.row:focus-visible {
		outline: var(--focus-ring);
		outline-offset: calc(-1 * var(--focus-offset) - 2px);
	}
	/* A square box for a computer's system mark, a group or a found message, the size of a md avatar */
	.row-icon {
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
	.row-icon > :global(.material-icons) {
		font-size: 20px;
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
	/* Online state, owner and folder, then who has it; that part wraps to the next line rather than cut the names */
	.row-meta.is-split {
		gap: 2px var(--space-3);
	}
	.row-meta-text {
		flex: 0 1 auto;
		gap: 0 var(--space-2);
	}
	.row-meta-text > span,
	.row-who {
		display: inline-flex;
		align-items: center;
		gap: 4px;
		min-width: 0;
		max-width: 100%;
	}
	.row-who {
		gap: var(--space-2);
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
		border-radius: var(--radius-sm);
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
	/* The unread count as the sidebar shows counts: a number, no box */
	.row-badge {
		flex: none;
		color: var(--text);
		font-size: var(--text-sm);
		font-weight: 600;
		font-variant-numeric: tabular-nums;
	}
	/* A phone: the controls and rows line up with the page title */
	@media (max-width: 767px) {
		.chat-list:not(.is-compact) .chat-list-top,
		.chat-list:not(.is-compact) .row,
		.chat-list:not(.is-compact) .rows-title,
		.chat-list:not(.is-compact) :global(.chat-list-state),
		.chat-list:not(.is-compact) :global(.chat-list-empty) {
			padding-inline: var(--page-pad);
		}
		.chat-list:not(.is-compact) :global(.chat-list-note) {
			margin-inline: var(--page-pad);
		}
	}
</style>
