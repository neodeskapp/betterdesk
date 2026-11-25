<!--
  The list side of the chat (A242): Support and Team as two sections of
  equal weight, a search that finds computers by name, ID, host name,
  owner and folder (Support) or people and groups by name (Team), and the
  rows. Support has the inbox's three filters: Open (nobody's first),
  Mine, Closed. A computer's row says its name, folder, whether it is
  online, who has it and its newest line; a colleague's row the name,
  role, status and newest line. Team also lists the colleagues one has no
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
	import { loadInbox, loadTeam } from './api.ts';
	import { chat } from './client.svelte.ts';
	import { lastLine } from './logic.ts';
	import type { InboxCounts, InboxFilter, InboxItem, Person, Section, TeamItem } from './types.ts';

	interface Props {
		section: Section;
		filter: InboxFilter;
		active: string | null;
		onsection: (s: Section) => void;
		onfilter: (f: InboxFilter) => void;
		onopen: (conv: string, row: HTMLElement | null) => void;
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

	async function load() {
		failed = false;
		try {
			if (section === 'support' && chat.may.support) {
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
	const teamShown = $derived(
		(team ?? []).filter(
			(c) =>
				!tq ||
				(c.title ?? '').toLowerCase().includes(tq) ||
				c.people.some((p) => p.name.toLowerCase().includes(tq) || p.username.toLowerCase().includes(tq))
		)
	);
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

	function waiting(ms: number | undefined): boolean {
		return !!ms && Date.now() - ms > 60_000;
	}
</script>

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
			<Button icon="edit_square" size="sm" onclick={onnew}>{t('chat.new')}</Button>
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
		{#if failed}
			<p class="chat-list-state is-error">
				{t('chat.list_failed')}
				<Button size="sm" onclick={() => void load()}>{t('actions.retry')}</Button>
			</p>
		{:else if section === 'support'}
			{#if support === null}
				<p class="chat-list-state">{t('common.loading')}</p>
			{:else if !support.length}
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
									<span class="row-meta">
										{#if c.device.folder}<span>{c.device.folder}</span>{/if}
										{#if c.device.owner}<span>{c.device.owner}</span>{/if}
										<span class="sr-only">{c.device.online ? t('chat.online') : t('chat.offline')}</span>
									</span>
									<span class="row-bottom">
										<span class="row-last">{lastLine(c.last_message, t)}</span>
										{#if unread > 0}<span
												class="row-badge"
												aria-label={t('chat.unread_count', { count: unread })}
												>{unread > 99 ? '99+' : unread}</span
											>{/if}
									</span>
									<span class="row-tags">
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
			{/if}
		{:else if team === null}
			<p class="chat-list-state">{t('common.loading')}</p>
		{:else}
			{#if teamShown.length}
				<ul class="rows" aria-label={t('chat.conversations')}>
					{#each teamShown as c (c.id)}
						{@const unread = c.unread}
						{@const person = c.kind === 'dm' ? c.people[0] : undefined}
						<li>
							<button
								type="button"
								class={['row', active === c.id && 'is-current', unread > 0 && 'is-unread']}
								aria-current={active === c.id ? 'true' : undefined}
								onclick={(e) => onopen(c.id, e.currentTarget)}
							>
								<span class="row-avatar">
									{#if person}
										<Avatar
											user={{ id: person.id, username: person.username, name: person.name }}
											size="md"
											presence={status(person)}
										/>
									{:else}
										<span class="row-icon" aria-hidden="true"><Icon name="group" /></span>
									{/if}
								</span>
								<span class="row-main">
									<span class="row-top">
										<span class="row-name">{teamName(c)}</span>
										{#if c.last_message}<span class="row-time"><Time value={c.last_message.at} /></span>{/if}
									</span>
									<span class="row-meta">
										{#if person}<span>{t(`chat.presence_${status(person)}`)}</span><span
												>{t(`users.role_${person.role}`)}</span
											>{:else}<span>{t('chat.member_count', { count: c.people.length })}</span>{/if}
									</span>
									<span class="row-bottom">
										<span class="row-last">{lastLine(c.last_message, t)}</span>
										{#if unread > 0}<span
												class="row-badge"
												aria-label={t('chat.unread_count', { count: unread })}
												>{unread > 99 ? '99+' : unread}</span
											>{/if}
									</span>
								</span>
							</button>
						</li>
					{/each}
				</ul>
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
							>
								<span class="row-avatar"
									><span class="row-icon" aria-hidden="true"><Icon name="history" /></span></span
								>
								<span class="row-main">
									<span class="row-top">
										<span class="row-name">{c.title || t('chat.old_messages')}</span>
										{#if c.last_message}<span class="row-time"><Time value={c.last_message.at} /></span>{/if}
									</span>
									<span class="row-meta"><span>{t('chat.old_messages')}</span></span>
									<span class="row-bottom"><span class="row-last">{lastLine(c.last_message, t)}</span></span>
								</span>
							</button>
						</li>
					{/each}
				</ul>
			{/if}
			{#if !teamShown.length && !others.length && !oldShown.length}
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
		gap: var(--space-3);
		width: 100%;
		padding: var(--space-2) var(--space-3);
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
	.row-meta {
		display: flex;
		flex-wrap: wrap;
		gap: 0 var(--space-3);
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
	.row-last {
		flex: 1;
		min-width: 0;
		overflow: hidden;
		color: var(--text-muted);
		text-overflow: ellipsis;
		white-space: nowrap;
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
	.row-tags {
		display: flex;
		gap: var(--space-1);
		margin-top: 2px;
	}
	.row-tag {
		display: inline-flex;
		align-items: center;
		gap: var(--space-1);
		padding: 0 var(--space-2);
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
</style>
