<!--
  Signed-in devices (A239). Without `user`: the signed-in account's own
  sessions, from the account menu. "This browser" is marked and signs out
  with Sign out; every other entry has Sign out, and "Sign out everywhere
  else" ends them all. With `user`: that account's sessions on the user's
  page (user.edit), every entry with Sign out, and "Sign out everywhere".
  A browser signed out here goes to the sign-in page at its next step and
  is told why.
-->
<script lang="ts">
	import { tick, untrack } from 'svelte';
	import {
		endAllUserSessions,
		endOtherSessions,
		endOwnSession,
		endUserSession,
		listOwnSessions,
		listUserSessions,
		type SignedInDevice,
		type SignedInDevices
	} from '#lib/api/sessions.ts';
	import { formatDate, formatRelative } from '#lib/i18n/format.ts';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import Button from '#lib/ui/Button.svelte';
	import ListRow from '#lib/ui/ListRow.svelte';
	import RowList from '#lib/ui/RowList.svelte';
	import Sheet from '#lib/ui/Sheet.svelte';
	import States from '#lib/ui/States.svelte';
	import Tag from '#lib/ui/Tag.svelte';
	import { confirm } from '#lib/ui/dialogs.svelte.ts';
	import { deviceLabel, limitText } from './sessions.ts';

	interface Props {
		/** Another account (the user's page); none: your own */
		user?: { id: number | string; username: string } | null;
		onclose: () => void;
	}

	let { user = null, onclose }: Props = $props();

	const other = untrack(() => user);
	let list = $state.raw<SignedInDevices | null>(null);
	let loadError = $state('');
	let busy = $state(false);
	let status = $state('');
	let statusTone = $state<'neutral' | 'ok' | 'error'>('neutral');
	let sheet = $state<HTMLElement | null>(null);

	const others = $derived(list ? list.sessions.filter((s) => !s.current) : []);

	async function load() {
		loadError = '';
		try {
			list = other ? await listUserSessions(other.id) : await listOwnSessions();
		} catch (err) {
			loadError = err instanceof Error && err.message ? err.message : t('sessions.load_failed');
		}
	}

	$effect(() => {
		untrack(() => void load());
	});

	async function run(
		ask: { title: string; message: string; confirmLabel: string },
		work: () => Promise<string>
	) {
		if (busy) return;
		if (!(await confirm({ ...ask, danger: true }))) return;
		busy = true;
		status = '';
		try {
			status = await work();
			statusTone = 'ok';
			await load();
		} catch (err) {
			status = err instanceof Error && err.message ? err.message : t('errors.server_error');
			statusTone = 'error';
		} finally {
			busy = false;
		}
		// A removed row takes its button along: focus stays in the sheet
		await tick();
		if (!sheet?.contains(document.activeElement) || document.activeElement === document.body)
			sheet?.querySelector<HTMLElement>('.list .btn, .sheet-footer .btn')?.focus();
	}

	function signOut(d: SignedInDevice) {
		const name = deviceLabel(d, t);
		void run(
			{
				title: t('sessions.sign_out'),
				message: t('sessions.confirm_sign_out', { device: name }),
				confirmLabel: t('sessions.sign_out')
			},
			async () => {
				if (other) await endUserSession(other.id, d.id);
				else await endOwnSession(d.id);
				return t('sessions.signed_out_one', { device: name });
			}
		);
	}

	function signOutAll() {
		const label = other ? t('sessions.sign_out_all') : t('sessions.sign_out_everywhere_else');
		void run(
			{
				title: label,
				message: other
					? t('sessions.confirm_all_user', { username: other.username })
					: t('sessions.confirm_everywhere_else'),
				confirmLabel: label
			},
			async () => {
				const ended = other ? await endAllUserSessions(other.id) : await endOtherSessions();
				return t('sessions.signed_out_count', { count: ended });
			}
		);
	}
</script>

<Sheet
	title={other ? t('sessions.user_title', { username: other.username }) : t('sessions.title')}
	subtitle={other ? t('sessions.subtitle_user', { username: other.username }) : t('sessions.subtitle_own')}
	{status}
	{statusTone}
	bind:element={sheet}
	{onclose}
>
	{#if list === null && !loadError}
		<States kind="loading" />
	{:else if loadError}
		<States kind="error" message={loadError} onretry={() => void load()} />
	{:else if list}
		{#if list.sessions.length}
			<RowList label={other ? t('sessions.user_title', { username: other.username }) : t('sessions.title')}>
				{#each list.sessions as d (d.id)}
					{#snippet sub()}
						<span class="facts">
							<span>{t('sessions.address')}: {d.ipAddress || '-'}</span>
							<span title={formatDate(d.createdAt)}
								>{t('sessions.signed_in')}: {formatRelative(d.createdAt)}</span
							>
							<span title={formatDate(d.lastActivityAt)}
								>{t('sessions.last_used')}: {formatRelative(d.lastActivityAt)}</span
							>
						</span>
					{/snippet}
					{#snippet here()}<Tag label={t('sessions.this_browser')} icon="check_circle" />{/snippet}
					{#snippet signOutButton()}<Button
							size="sm"
							variant="danger-quiet"
							disabled={busy}
							onclick={() => signOut(d)}
							>{t('sessions.sign_out')}<span class="sr-only"> {deviceLabel(d, t)}</span></Button
						>{/snippet}
					<ListRow
						title={deviceLabel(d, t)}
						{sub}
						end={d.current ? here : undefined}
						actions={d.current ? undefined : signOutButton}
					/>
				{/each}
			</RowList>
		{:else}
			<p class="note">{other ? t('sessions.none_user', { username: other.username }) : t('sessions.none')}</p>
		{/if}
		{#if !other && list.sessions.length && !others.length}
			<p class="note">{t('sessions.none_other')}</p>
		{/if}
		<p class="note">{limitText(list.limit, t)}</p>
	{/if}

	{#snippet footer()}
		{#if list && others.length}
			<Button variant="danger" disabled={busy} onclick={signOutAll}
				>{other ? t('sessions.sign_out_all') : t('sessions.sign_out_everywhere_else')}</Button
			>
		{/if}
		<Button onclick={onclose}>{t('actions.close')}</Button>
	{/snippet}
</Sheet>

<style>
	.facts {
		display: flex;
		flex-wrap: wrap;
		gap: 0 var(--space-3);
	}
	.note {
		margin: var(--space-3) 0 0;
		color: var(--text-muted);
	}
</style>
