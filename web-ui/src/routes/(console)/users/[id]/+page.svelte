<!--
  One user (/users/<id>): everything about one account on one page. The
  main column has the profile (display name, email with a change that
  waits, language, time zone, picture), the role and what it allows in
  plain sentences (its permissions in effect, overrides included), the
  devices it reaches and how, its sign-in (method, two-factor sign-in,
  lock, a new password due, a reset link or an invite that waits, the end
  date), its signed-in devices, its API tokens, its activity in the audit
  log and its sign-in history. The side column has the actions of the
  users row menu in the same groups (lib/people/user-actions.ts); the ones
  that end something are apart at the end of the page.

  Read-only with user.view: everything shown, no actions. The signed-in
  devices need user.edit (A239), the activity audit.view, revoking a token
  user.security. An id without an account answers 404 and the page says
  so with a way back to the list. A reload keeps the page (deep link).
-->
<script lang="ts">
	import { untrack } from 'svelte';
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { ApiError } from '#lib/api/client.ts';
	import { listUserApiTokens, revokeUserApiToken, type ApiToken } from '#lib/api/apiTokens.ts';
	import { listUserSecurityEvents, type SecurityEvent } from '#lib/api/security-events.ts';
	import {
		getEffectiveScope,
		getUser,
		listInvites,
		listUserActivity,
		listUserGroupRows,
		listUserOrgs,
		type ActivityEntry,
		type EffectiveScope,
		type UserDetail,
		type UserGroupRow,
		type UserOrg
	} from '#lib/api/users.ts';
	import type { AccessSubject } from '#lib/api/access.ts';
	import { eventFacts, eventTitle } from '#lib/account/security-events.ts';
	import { formatDate } from '#lib/i18n/format.ts';
	import { i18n, t } from '#lib/i18n/i18n.svelte.ts';
	import { link } from '#lib/nav.ts';
	import SessionsRows from '#lib/people/SessionsRows.svelte';
	import SessionsSheet from '#lib/people/SessionsSheet.svelte';
	import UserActionMenuItems from '#lib/people/UserActionMenuItems.svelte';
	import { madeList, revokeMessage, stateTone } from '#lib/people/apiTokens.ts';
	import { actionLabel as auditLabel } from '#lib/people/audit.ts';
	import { runUserAction } from '#lib/people/run-user-action.ts';
	import { SessionsList } from '#lib/people/sessions-list.svelte.ts';
	import { actionLabel, userActionGroups, type UserActionId } from '#lib/people/user-actions.ts';
	import {
		accessLines,
		auditHref,
		orgsLine,
		permissionSentences,
		signInFacts
	} from '#lib/people/user-page.ts';
	import { mayChangeUser, providerOf } from '#lib/people/users.ts';
	import NoAccess from '#lib/shell/NoAccess.svelte';
	import { onRefresh, setPageTitle } from '#lib/shell/page.svelte.ts';
	import { can, sectionWritable, session } from '#lib/state/session.svelte.ts';
	import Avatar from '#lib/ui/Avatar.svelte';
	import Button from '#lib/ui/Button.svelte';
	import DangerZone, { type DangerRow } from '#lib/ui/DangerZone.svelte';
	import Facts from '#lib/ui/Facts.svelte';
	import ListRow from '#lib/ui/ListRow.svelte';
	import MenuButton from '#lib/ui/MenuButton.svelte';
	import ReadOnlyHint from '#lib/ui/ReadOnlyHint.svelte';
	import RowList from '#lib/ui/RowList.svelte';
	import States from '#lib/ui/States.svelte';
	import Status from '#lib/ui/Status.svelte';
	import Tag from '#lib/ui/Tag.svelte';
	import Time from '#lib/ui/Time.svelte';
	import { confirm } from '#lib/ui/dialogs.svelte.ts';
	import { toast } from '#lib/ui/toasts.svelte.ts';
	import { displayName, ICONS } from '#lib/util/display.ts';
	import AccessSheet from '../AccessSheet.svelte';
	import DeleteUserDialog from '../DeleteUserDialog.svelte';
	import ResetPasswordDialog from '../ResetPasswordDialog.svelte';
	import SignInAsDialog from '../SignInAsDialog.svelte';
	import UserOrgsSheet from '../UserOrgsSheet.svelte';
	import UserSheet from '../UserSheet.svelte';
	import ProfileDialog from './ProfileDialog.svelte';

	const userId = $derived(page.params.id ?? '');

	const allowed = can('user.view');
	const canEdit = can('user.edit');
	const canDelete = can('user.delete') && sectionWritable();
	const canSecurity = can('user.security') && sectionWritable();
	const canWithdraw = can('user.create') && sectionWritable();
	const canViewAs = can('user.impersonate') && !session.impersonation;
	const canAudit = can('audit.view');
	const meId = String(session.user?.id ?? '');
	const rights = {
		meId,
		myRole: session.user?.role,
		canEdit,
		canDelete,
		canSecurity,
		canWithdraw,
		canViewAs,
		canTeamChat: can('chat.team')
	};

	function roleLabel(role: string): string {
		const key = `users.role_${role}`;
		const text = t(key);
		return text === key ? role : text;
	}
	function providerLabel(provider: string): string {
		const key = `users.provider_${provider}`;
		const text = t(key);
		return text === key ? provider : text;
	}

	/* ── Data ── */

	let user = $state.raw<UserDetail | null>(null);
	let loadError = $state<{ status: number; message: string } | null>(null);
	let scope = $state.raw<EffectiveScope | 'error' | undefined>(undefined);
	let orgs = $state.raw<UserOrg[] | 'error' | undefined>(undefined);
	let groups = $state.raw<UserGroupRow[]>([]);
	let mailReady = $state(false);
	let tokens = $state.raw<ApiToken[] | null>(null);
	let tokensError = $state(false);
	let activity = $state.raw<ActivityEntry[] | null>(null);
	let activityError = $state(false);
	let events = $state.raw<SecurityEvent[] | null>(null);
	let eventsError = $state(false);
	let sessions = $state.raw<SessionsList | null>(null);

	const changeable = $derived(!!user && mayChangeUser(session.user?.role, meId, user));
	const self = $derived(!!user && String(user.id) === meId);
	const name = $derived(user ? displayName(user) || user.username : '');

	async function loadUser(): Promise<boolean> {
		try {
			user = await getUser(userId);
			loadError = null;
			return true;
		} catch (err) {
			const status = err instanceof ApiError ? err.status : 0;
			loadError = {
				status,
				message:
					status === 404 || status === 400
						? t('users.page_not_found')
						: status === 403
							? t('users.page_no_permission')
							: t('users.page_load_failed')
			};
			return false;
		}
	}

	async function loadTokens() {
		tokensError = false;
		try {
			tokens = await listUserApiTokens(userId);
		} catch {
			tokensError = true;
		}
	}
	async function loadActivity() {
		if (!canAudit) return;
		activityError = false;
		try {
			activity = await listUserActivity(userId, 30);
		} catch {
			activityError = true;
		}
	}
	async function loadEvents() {
		eventsError = false;
		try {
			events = await listUserSecurityEvents(userId);
		} catch {
			eventsError = true;
		}
	}

	async function loadAll() {
		if (!(await loadUser()) || !user) return;
		const id = user.id;
		void getEffectiveScope(id).then(
			(s) => (scope = s),
			() => (scope = 'error')
		);
		void listUserOrgs(id).then(
			(o) => (orgs = o),
			() => (orgs = 'error')
		);
		void listUserGroupRows().then(
			(g) => (groups = g),
			() => (groups = [])
		);
		void listInvites().then(
			(i) => (mailReady = i.mail_ready),
			() => (mailReady = false)
		);
		void loadTokens();
		void loadActivity();
		void loadEvents();
		if (canEdit && mayChangeUser(session.user?.role, meId, user)) {
			const list = new SessionsList({ id, username: name });
			sessions = list;
			void list.load();
		} else sessions = null;
	}

	$effect(() => {
		if (!allowed) return;
		void userId;
		untrack(() => {
			user = null;
			scope = orgs = undefined;
			tokens = activity = events = null;
			void loadAll();
		});
	});
	$effect(() => onRefresh(() => void loadAll()));
	$effect(() => {
		setPageTitle(name || t('users.page_title'));
		return () => setPageTitle(null);
	});

	const groupName = (guid: string) => groups.find((g) => g.guid === guid)?.name || guid;

	/* ── Actions ── */

	let editing = $state(false);
	let editingProfile = $state(false);
	let resetting = $state(false);
	let deleting = $state(false);
	let orgsOpen = $state(false);
	let sessionsOpen = $state(false);
	let viewingAs = $state(false);
	let accessFor = $state.raw<{ subject: AccessSubject; name: string; role?: string } | null>(null);

	const actionGroups = $derived(
		user
			? userActionGroups(user, rights, {
					mailReady,
					invited: !!user.invite,
					teamChat: user.team_chat
				})
			: []
	);
	const sideGroups = $derived(actionGroups.filter((g) => g.id !== 'danger'));
	const dangerGroup = $derived(actionGroups.find((g) => g.id === 'danger'));

	async function act(id: UserActionId) {
		const u = user;
		if (!u) return;
		switch (id) {
			case 'edit':
				editing = true;
				return;
			case 'orgs':
				orgsOpen = true;
				return;
			case 'access':
				accessFor = { subject: { kind: 'user', id: u.id }, name, role: u.role };
				return;
			case 'sign_in_as':
				viewingAs = true;
				return;
			case 'reset_password':
				resetting = true;
				return;
			case 'sessions':
				sessionsOpen = true;
				return;
			case 'delete':
				deleting = true;
				return;
		}
		const changed = await runUserAction(id, u);
		if (changed === 'gone') backToList();
		else if (changed) void loadAll();
	}

	function backToList() {
		// link() adds the base; resolve() is for route paths
		// eslint-disable-next-line svelte/no-navigation-without-resolve
		void goto(link('/users'));
	}

	/** The consequence of each action that ends something, one sentence */
	const CONSEQUENCE: Partial<Record<UserActionId, string>> = {
		withdraw_invite: 'users.page_withdraw_consequence',
		disable: 'users.page_disable_consequence',
		delete: 'users.page_delete_consequence'
	};
	const dangerRows = $derived<DangerRow[]>(
		(dangerGroup?.actions ?? []).map((a) => ({
			title: actionLabel(a, t, (k) => t(k)),
			consequence: t(CONSEQUENCE[a.id] ?? '', { username: name }),
			action: actionLabel(a, t, (k) => t(k)),
			onclick: () => void act(a.id)
		}))
	);

	const mayRevokeTokens = $derived(canSecurity && changeable);

	async function revokeToken(tok: ApiToken) {
		const ok = await confirm({
			title: t('api_tokens.revoke_title'),
			message: revokeMessage(tok, t),
			confirmLabel: t('api_tokens.revoke'),
			confirmIcon: ICONS.revoke,
			danger: true
		});
		if (!ok || !user) return;
		try {
			const ended = madeList(await revokeUserApiToken(user.id, tok.id), t);
			toast.success(
				ended
					? t('api_tokens.revoked_ended', { name: tok.name, list: ended })
					: t('api_tokens.revoked', { name: tok.name })
			);
			void loadTokens();
			void loadActivity();
		} catch (err) {
			toast.error(`${t('api_tokens.revoke_failed')} ${err instanceof Error ? err.message : ''}`.trim());
		}
	}

	/* ── Texts ── */

	const languageName = $derived.by(() => {
		const code = user?.preferred_language ?? '';
		if (!code) return t('users.page_language_default');
		const l = i18n.available.find((x) => x.code === code);
		return l ? l.native || l.name || code : code;
	});
	const sentenceGroups = $derived(user ? permissionSentences(user.permissions, t) : []);
	const scopeLines = $derived(user ? accessLines(user, scope, groupName, t) : []);
	const orgsText = $derived(orgsLine(orgs, t));
	const states = $derived(user ? signInFacts(user) : []);
	const provider = $derived(user ? providerOf(user) : 'local');

	function personName(p: ActivityEntry['actor']): string {
		return p ? p.display_name || p.username || `#${p.id}` : '';
	}
	function activityFacts(e: ActivityEntry): string[] {
		const out: string[] = [];
		const actor = e.actor && String(e.actor.id) !== String(user?.id) ? personName(e.actor) : '';
		const target = e.target && String(e.target.id) !== String(user?.id) ? personName(e.target) : '';
		if (actor) out.push(t('users.page_activity_by', { name: actor }));
		if (target) out.push(t('users.page_activity_about', { name: target }));
		if (e.details) out.push(e.details);
		return out;
	}
</script>

{#snippet emailValue()}{#if user?.email}{user.email}{:else}<span class="text-muted"
			>{t('users.page_no_email')}</span
		>{/if}{#if user?.email_pending}<span class="sub"
			><Status
				tone={user.email_pending.expired ? 'warning' : 'waiting'}
				label={t('users.email_pending', { email: user.email_pending.email })}
			/></span
		>{/if}{/snippet}
{#snippet lastValue()}<Time value={user?.last_login} empty={t('users.never')} />{/snippet}
{#snippet createdValue()}<Time
		value={user?.created_at}
		relative={false}
		format={{ hour: undefined, minute: undefined }}
	/>{/snippet}

{#if !allowed}
	<NoAccess />
{:else}
	<div class="page page-wide user-page">
		<p class="back"><a href={link('/users')}>{t('users.page_back')}</a></p>
		{#if loadError}
			<div class="page-head"><h1>{t('users.page_title')}</h1></div>
			<States
				kind="error"
				message={loadError.message}
				onretry={loadError.status === 404 || loadError.status === 400 ? undefined : () => void loadAll()}
			/>
			{#if loadError.status === 404 || loadError.status === 400}
				<p><Button onclick={backToList}>{t('users.page_back')}</Button></p>
			{/if}
		{:else if !user}
			<div class="page-head"><h1>{t('users.page_title')}</h1></div>
			<States kind="loading" message={t('users.page_loading')} />
		{:else}
			{@const u = user}
			<div class="page-head">
				<div class="title-row">
					<Avatar user={u} size="lg" />
					<div class="title-text">
						<h1>{name}</h1>
						<p class="title-sub">
							{#if name !== u.username}<span class="username">{u.username}</span>{/if}
							<Tag label={roleLabel(u.role)} />
							{#if provider !== 'local'}<Tag label={providerLabel(provider)} />{/if}
							{#if self}<span class="text-muted">{t('users.this_is_you')}</span>{/if}
							{#if u.disabled}<Status tone="warning" label={t('users.account_disabled')} />{/if}
						</p>
					</div>
				</div>
				{#if actionGroups.length}
					<div class="page-actions">
						{#if canEdit && changeable}
							<Button class="edit-user" icon={ICONS.edit} onclick={() => (editing = true)}
								>{t('users.edit')}</Button
							>
						{/if}
						<MenuButton label={t('users.page_more_actions')} class="more-actions">
							<UserActionMenuItems groups={actionGroups} onselect={(id) => void act(id)} />
						</MenuButton>
					</div>
				{/if}
			</div>
			<ReadOnlyHint writes={['user.edit', 'user.security', 'user.delete']} />

			<div class="page-cols">
				<div class="page-main">
					<section class="section" id="profile" aria-labelledby="profile-title">
						<div class="section-head">
							<h2 id="profile-title">{t('users.page_profile')}</h2>
							{#if canEdit && changeable}
								<Button
									size="sm"
									class="edit-profile"
									icon={ICONS.edit}
									onclick={() => (editingProfile = true)}>{t('users.edit_profile')}</Button
								>
							{/if}
						</div>
						<Facts
							items={[
								{ label: t('users.display_name'), value: name },
								{ label: t('users.username'), value: u.username },
								{ label: t('users.email'), value: emailValue },
								{ label: t('settings.language'), value: languageName },
								{ label: t('users.time_zone'), value: u.time_zone, empty: t('users.time_zone_none') },
								{ label: t('users.last_login'), value: lastValue },
								{ label: t('users.created'), value: createdValue }
							]}
						/>
					</section>

					<section class="section" id="role" aria-labelledby="role-title">
						<h2 id="role-title">{t('users.page_role', { role: roleLabel(u.role) })}</h2>
						{#if t(`users.role_desc_${u.role}`) !== `users.role_desc_${u.role}`}
							<p class="section-note">{t(`users.role_desc_${u.role}`)}</p>
						{/if}
						{#if !sentenceGroups.length}
							<p class="note">{t('users.page_role_nothing')}</p>
						{:else}
							<div class="allows">
								{#each sentenceGroups as g (g.id)}
									<div class="allows-group">
										<h3>{g.label}</h3>
										<ul>
											{#each g.sentences as s (s)}<li>{s}</li>{/each}
										</ul>
									</div>
								{/each}
							</div>
						{/if}
						<p class="section-note">
							{t('users.page_role_note')}
							{#if session.isSuperAdmin}<a href={link(`/permissions?role=${encodeURIComponent(u.role)}`)}
									>{t('users.roles_change_link')}</a
								>{/if}
						</p>
					</section>

					<section class="section" id="access" aria-labelledby="access-title">
						<h2 id="access-title">{t('users.page_access')}</h2>
						{#if scope === undefined}
							<States kind="loading" />
						{:else}
							<ul class="lines">
								{#each scopeLines as line (line)}<li>{line}</li>{/each}
								{#if orgsText}<li>{orgsText}</li>{/if}
							</ul>
						{/if}
					</section>

					<section class="section" id="sign-in" aria-labelledby="sign-in-title">
						<h2 id="sign-in-title">{t('users.page_sign_in')}</h2>
						<ul class="lines states">
							{#each states as s (s.key)}
								<li>
									<Status
										tone={s.tone}
										label={t(s.key, {
											provider: providerLabel(provider),
											date: s.date ? formatDate(s.date, {}, '') : ''
										})}
									/>
								</li>
							{/each}
						</ul>
					</section>

					<section class="section" id="sessions" aria-labelledby="sessions-title">
						<h2 id="sessions-title">{t('sessions.title')}</h2>
						{#if sessions}
							<SessionsRows model={sessions} />
							{#if sessions.list && sessions.others.length}
								<p>
									<Button
										size="sm"
										variant="danger"
										disabled={sessions.busy}
										onclick={() => sessions?.signOutAll()}>{sessions.allLabel}</Button
									>
								</p>
							{/if}
						{:else}
							<p class="note">
								{canEdit ? t('users.page_sessions_above') : t('users.page_sessions_need_edit')}
							</p>
						{/if}
					</section>

					<section class="section" id="api-tokens" aria-labelledby="tokens-title">
						<h2 id="tokens-title">{t('api_tokens.title')}</h2>
						{#if tokensError}
							<States
								kind="error"
								message={t('users.page_tokens_failed')}
								onretry={() => void loadTokens()}
							/>
						{:else if tokens === null}
							<States kind="loading" />
						{:else if !tokens.length}
							<p class="note">{t('users.page_tokens_none')}</p>
						{:else}
							<RowList label={t('api_tokens.title')}>
								{#each tokens as tok (tok.id)}
									{#snippet sub()}
										<span class="facts-line">
											<Status tone={stateTone(tok.state)} label={t(`api_tokens.state_${tok.state}`)} />
											{#if tok.expires_at}<span
													>{t('users.page_token_expires', {
														date: formatDate(tok.expires_at, { hour: undefined, minute: undefined }, '')
													})}</span
												>{/if}
											<span
												>{tok.last_used_at
													? t('users.page_token_used', { date: formatDate(tok.last_used_at, {}, '') })
													: t('users.page_token_never_used')}</span
											>
										</span>
									{/snippet}
									{#snippet revoke()}
										{#if mayRevokeTokens && tok.state === 'active'}
											<Button size="sm" variant="danger" onclick={() => void revokeToken(tok)}
												>{t('api_tokens.revoke')}…</Button
											>
										{/if}
									{/snippet}
									<ListRow title={`${tok.name} (${tok.hint})`} {sub} actions={revoke} />
								{/each}
							</RowList>
						{/if}
					</section>

					<section class="section" id="activity" aria-labelledby="activity-title">
						<div class="section-head">
							<h2 id="activity-title">{t('users.page_activity')}</h2>
							{#if canAudit}<a href={link(auditHref(u.username))}>{t('users.page_activity_all')}</a>{/if}
						</div>
						{#if !canAudit}
							<p class="note">{t('users.page_activity_need_audit')}</p>
						{:else if activityError}
							<States
								kind="error"
								message={t('users.page_activity_failed')}
								onretry={() => void loadActivity()}
							/>
						{:else if activity === null}
							<States kind="loading" />
						{:else if !activity.length}
							<p class="note">{t('users.page_activity_none')}</p>
						{:else}
							<RowList label={t('users.page_activity')}>
								{#each activity as e (e.id)}
									{#snippet sub()}
										<span class="facts-line">
											<Time value={e.at} />
											{#each activityFacts(e) as fact (fact)}<span>{fact}</span>{/each}
										</span>
									{/snippet}
									<ListRow title={auditLabel(e.action, t)} {sub} />
								{/each}
							</RowList>
						{/if}
					</section>

					<section class="section" id="security-events" aria-labelledby="events-title">
						<h2 id="events-title">{t('account.security_events')}</h2>
						{#if eventsError}
							<States
								kind="error"
								message={t('account.security_events_failed')}
								onretry={() => void loadEvents()}
							/>
						{:else if events === null}
							<States kind="loading" />
						{:else if !events.length}
							<p class="note">{t('users.page_events_none')}</p>
						{:else}
							<RowList label={t('account.security_events')}>
								{#each events as e (e.id)}
									{#snippet sub()}
										<span class="facts-line">
											<Time value={e.at} />
											{#each eventFacts(e, t) as fact (fact)}<span>{fact}</span>{/each}
										</span>
									{/snippet}
									<ListRow
										title={eventTitle(e, t)}
										{sub}
										mark={e.action === 'impersonation_started' ? 'info' : undefined}
									/>
								{/each}
							</RowList>
						{/if}
					</section>

					{#if dangerRows.length}
						<DangerZone title={t('users.menu_group_danger')} rows={dangerRows} />
					{/if}
				</div>

				{#if sideGroups.length}
					<aside class="page-side" aria-labelledby="actions-title">
						<section class="section" aria-labelledby="actions-title">
							<h2 id="actions-title">{t('users.page_actions')}</h2>
							{#each sideGroups as g (g.id)}
								<div class={['action-group', g.cls]}>
									<h3>{t(g.label)}</h3>
									<div class="action-list">
										{#each g.actions as a (a.id)}
											<Button class={a.cls} icon={a.icon} onclick={() => void act(a.id)}
												>{actionLabel(a, t, (k) => t(k))}</Button
											>
										{/each}
									</div>
								</div>
							{/each}
						</section>
					</aside>
				{/if}
			</div>
		{/if}
	</div>

	{#if user && editing}
		<UserSheet
			{user}
			{groups}
			{roleLabel}
			{providerLabel}
			{mailReady}
			onclose={() => (editing = false)}
			onsaved={() => {
				editing = false;
				void loadAll();
			}}
		/>
	{/if}
	{#if user && editingProfile}
		<ProfileDialog
			{user}
			{providerLabel}
			onclose={() => (editingProfile = false)}
			onsaved={(saved) => {
				editingProfile = false;
				user = saved;
				void loadActivity();
			}}
		/>
	{/if}
	{#if user && resetting}
		<ResetPasswordDialog
			{user}
			{mailReady}
			security={canSecurity && !self}
			{providerLabel}
			onclose={() => (resetting = false)}
			onchanged={() => void loadAll()}
		/>
	{/if}
	{#if user && deleting}
		<DeleteUserDialog
			{user}
			{roleLabel}
			onclose={() => (deleting = false)}
			ondeleted={() => {
				deleting = false;
				backToList();
			}}
		/>
	{/if}
	{#if user && sessionsOpen}
		<SessionsSheet
			user={{ id: user.id, username: name }}
			onclose={() => {
				sessionsOpen = false;
				void sessions?.load();
			}}
		/>
	{/if}
	{#if user && viewingAs}
		<SignInAsDialog {user} onclose={() => (viewingAs = false)} />
	{/if}
	{#if user && orgsOpen}
		<UserOrgsSheet {user} onclose={() => (orgsOpen = false)} onchanged={(next) => (orgs = next)} />
	{/if}
	{#if accessFor}
		<AccessSheet
			subject={accessFor.subject}
			name={accessFor.name}
			role={accessFor.role}
			{roleLabel}
			onclose={() => (accessFor = null)}
		/>
	{/if}
{/if}

<style>
	.back {
		margin: 0;
		font-size: var(--text-sm);
	}
	.title-row {
		display: flex;
		align-items: center;
		gap: var(--space-3);
		min-width: 0;
	}
	.title-text {
		min-width: 0;
	}
	.title-text h1 {
		overflow-wrap: anywhere;
	}
	.title-sub {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-2);
		margin: var(--space-1) 0 0;
		color: var(--text-muted);
	}
	.username {
		unicode-bidi: isolate;
	}
	.section-head {
		display: flex;
		flex-wrap: wrap;
		align-items: baseline;
		justify-content: space-between;
		gap: var(--space-2) var(--space-4);
	}
	.note {
		margin: 0;
		color: var(--text-muted);
	}
	.lines {
		display: flex;
		flex-direction: column;
		gap: var(--space-2);
		max-width: var(--measure);
		margin: 0;
		padding-inline-start: var(--space-5);
	}
	.lines.states {
		padding: 0;
		list-style: none;
	}
	.allows {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(min(100%, 240px), 1fr));
		gap: var(--space-4) var(--space-6);
	}
	.allows-group h3,
	.action-group h3 {
		margin: 0 0 var(--space-2);
		font-size: var(--text-md);
	}
	.allows-group ul {
		margin: 0;
		padding-inline-start: var(--space-5);
		color: var(--text);
	}
	.allows-group li + li {
		margin-top: var(--space-1);
	}
	.facts-line {
		display: flex;
		flex-wrap: wrap;
		gap: 0 var(--space-3);
	}
	.sub {
		display: block;
		margin-top: var(--space-1);
	}
	.action-group + .action-group {
		margin-top: var(--space-5);
	}
	.action-list {
		display: flex;
		flex-direction: column;
		align-items: stretch;
		gap: var(--space-2);
	}
	.action-list :global(.btn) {
		justify-content: flex-start;
	}
	/* Touch: 44 px buttons */
	@media (pointer: coarse) {
		.action-list :global(.btn) {
			min-height: 44px;
		}
	}
</style>
