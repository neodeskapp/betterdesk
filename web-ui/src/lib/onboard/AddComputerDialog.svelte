<!--
  "Add a computer" (Devices, enrollment.approve), a side sheet as
  launch/design/DESIGN.md draws it: an invite link that sets up NeoDesk
  on a computer and joins it to this server, approved, named and in its
  folder. Every field is optional: a name, a folder, an address to send
  the link to (only with mail set up), how long it works (7 days) and how
  often (once). Create invite shows the link once (CodeResult); the open
  invites are listed below with Revoke.

  The sheet then follows the join (guide.ts): it says it is still waiting,
  offers Approve new key for a computer that came back with another key,
  and once a computer joined walks through its first steps (JoinGuide).
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import type { Folder } from '#lib/api/devices.ts';
	import { ApiError } from '#lib/api/client.ts';
	import {
		createInvite,
		getInvite,
		getOnboardStatus,
		listInvites,
		mailInvite,
		revokeInvite,
		type Invite,
		type InviteDetail,
		type NewInvite,
		type OnboardStatus
	} from '#lib/api/onboard.ts';
	import { approveNewKey } from '#lib/devices/actions.svelte.ts';
	import { formatDate } from '#lib/i18n/format.ts';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import Button from '#lib/ui/Button.svelte';
	import Disclosure from '#lib/ui/Disclosure.svelte';
	import Field from '#lib/ui/Field.svelte';
	import FormSection from '#lib/ui/FormSection.svelte';
	import Input from '#lib/ui/Input.svelte';
	import ListRow from '#lib/ui/ListRow.svelte';
	import Notice from '#lib/ui/Notice.svelte';
	import RowList from '#lib/ui/RowList.svelte';
	import Select, { type SelectOption } from '#lib/ui/Select.svelte';
	import Sheet from '#lib/ui/Sheet.svelte';
	import { toast } from '#lib/ui/toasts.svelte.ts';
	import { poll } from '#lib/state/live.ts';
	import CodeResult from './CodeResult.svelte';
	import { deviceLabel, FOLLOW_MS, nextDevice } from './guide.ts';
	import { inviteBody, LIFETIMES, UNLIMITED_USES, USES } from './invite.ts';
	import JoinGuide from './JoinGuide.svelte';

	interface Props {
		folders: readonly Folder[];
		onclose: () => void;
	}

	let { folders, onclose }: Props = $props();

	const uid = $props.id();
	const formId = `${uid}-form`;

	let open = $state(true);
	let name = $state('');
	let folder = $state<number>(0);
	let email = $state('');
	let hours = $state<number>(168);
	let uses = $state<number>(1);
	let creating = $state(false);
	let error = $state('');
	let made = $state<NewInvite | null>(null);
	let status = $state<OnboardStatus | null>(null);
	let invites = $state.raw<Invite[]>([]);
	let revoking = $state('');
	/** What happened with the made invite, followed while the sheet is open */
	let detail = $state<InviteDetail | null>(null);
	/** The computers walked through in this sheet, and the last one's result */
	let walked = $state<Set<string>>(new Set());
	let finished = $state<{ name: string; folder: string } | null>(null);
	let keyApproved = $state('');
	let approving = $state(false);

	$effect(() => {
		const id = made?.invite.id;
		if (!id) return;
		return poll(
			async () => {
				detail = await getInvite(id);
			},
			{ every: FOLLOW_MS, immediate: true }
		);
	});

	const guiding = $derived(nextDevice(detail, walked));
	const refused = $derived(detail?.refused ?? null);
	/** Uses the invite has left; before the first answer, those it was made with */
	const usesLeft = $derived(detail ? detail.invite.uses_left : (made?.invite.uses_left ?? 0));

	function walkedThrough(device: string, result: { name: string; folder: string }) {
		walked = new Set([...walked, device]);
		finished = result;
	}

	/** The device panel's Approve new key, with its confirmation: the key change ends the computer's tokens */
	async function approveKey(device: string) {
		approving = true;
		try {
			if (await approveNewKey({ id: device })) keyApproved = device;
		} finally {
			approving = false;
		}
	}

	const folderOptions = $derived<SelectOption<number>[]>([
		{ value: 0, label: t('onboard.invite.no_folder') },
		...folders.map((f) => ({ value: f.id, label: f.name }))
	]);
	const lifetimeOptions = $derived<SelectOption<number>[]>(
		LIFETIMES.map((h) => ({ value: h, label: t(`onboard.invite.lifetime_${h}`) }))
	);
	const usesOptions = $derived<SelectOption<number>[]>(
		USES.map((n) => ({
			value: n,
			label:
				n === 1
					? t('onboard.invite.uses_once')
					: n === UNLIMITED_USES
						? t('onboard.invite.uses_unlimited')
						: t('onboard.invite.uses_n', { count: n })
		}))
	);

	async function refreshInvites() {
		try {
			invites = await listInvites();
		} catch {
			// The list is extra; the sheet works without it
		}
	}

	onMount(() => {
		void getOnboardStatus()
			.then((s) => (status = s))
			.catch(() => {});
		void refreshInvites();
	});

	function why(err: unknown): string {
		if (err instanceof ApiError && err.code) return t(`onboard.errors.${err.code}`);
		return t('onboard.invite.create_failed');
	}

	async function create(e?: SubmitEvent) {
		e?.preventDefault();
		if (creating) return;
		creating = true;
		error = '';
		try {
			made = await createInvite(inviteBody({ name, folder, email: status?.mail ? email : '', hours, uses }));
			void refreshInvites();
		} catch (err) {
			error = why(err);
		} finally {
			creating = false;
		}
	}

	async function mail(to: string) {
		if (!made) return;
		await mailInvite(made.invite.id, made.code, to);
	}

	async function revoke(inv: Invite) {
		revoking = inv.id;
		try {
			await revokeInvite(inv.id);
			invites = invites.filter((i) => i.id !== inv.id);
			if (made?.invite.id === inv.id) made = null;
			toast.success(t('onboard.invite.revoked'));
		} catch (err) {
			toast.error(why(err));
		} finally {
			revoking = '';
		}
	}

	function another() {
		made = null;
		detail = null;
		walked = new Set();
		finished = null;
		keyApproved = '';
		name = '';
		email = '';
	}

	function close() {
		open = false;
		onclose();
	}

	const resultTitle = $derived.by(() => {
		if (guiding) return t('onboard.guide.joined_title', { name: deviceLabel(guiding) });
		if (finished) return t('onboard.guide.joined_title', { name: finished.name });
		return made?.invite.name
			? t('onboard.invite.result_title_named', { name: made.invite.name })
			: t('onboard.invite.result_title');
	});
	const waitingLine = $derived.by(() => {
		if (!made || detail?.devices.length) return '';
		const date = formatDate(made.invite.expires_at);
		return made.invite.name
			? t('onboard.guide.waiting_named', { name: made.invite.name, date })
			: t('onboard.guide.waiting', { date });
	});
	const resultText = $derived.by(() => {
		if (!made) return '';
		const date = formatDate(made.invite.expires_at);
		if (made.invite.max_uses === 1) return t('onboard.invite.result_once', { date });
		if (made.invite.max_uses >= UNLIMITED_USES) return t('onboard.invite.result_unlimited', { date });
		return t('onboard.invite.result_many', { count: made.invite.max_uses, date });
	});
	const resultNote = $derived.by(() => {
		if (!made?.invite.name) return t('onboard.invite.result_note');
		if (made.invite.folder_name)
			return t('onboard.invite.result_note_folder', {
				name: made.invite.name,
				folder: made.invite.folder_name
			});
		return t('onboard.invite.result_note_named', { name: made.invite.name });
	});
	const usesLine = (inv: Invite) =>
		inv.max_uses > 1 && inv.max_uses < UNLIMITED_USES
			? t('onboard.invite.uses_left', { left: inv.uses_left, count: inv.max_uses })
			: '';
</script>

<Sheet
	bind:open
	title={made ? resultTitle : t('onboard.invite.title')}
	subtitle={guiding
		? t('onboard.guide.joined_lead')
		: made && !finished
			? resultText
			: made
				? ''
				: t('onboard.invite.subtitle')}
	status={error}
	statusTone={error ? 'error' : 'neutral'}
	{onclose}
>
	<div class="add-computer">
		{#snippet finishedLines(f: { name: string; folder: string })}
			<p class="finished">{t('onboard.guide.finished', { name: f.name })}</p>
			<p class="hint">
				{f.folder
					? t('onboard.guide.finished_folder', { folder: f.folder })
					: t('onboard.guide.finished_plain')}
			</p>
		{/snippet}
		{#if made && guiding}
			{#key guiding.id}
				<JoinGuide device={guiding} {folders} onfinish={(r) => walkedThrough(guiding.id, r)} />
			{/key}
		{:else if made && finished && !usesLeft}
			{@render finishedLines(finished)}
		{:else if made}
			<!-- An invite for more computers keeps its link after the first one joined -->
			{#if finished}{@render finishedLines(finished)}{/if}
			{#if refused && refused.reason === 'key_changed'}
				<Notice tone="warning" title={t('onboard.guide.key_changed')}>
					{#if keyApproved === refused.device_id || !refused.key_request}
						<p>{t('onboard.guide.key_approved')}</p>
					{:else}
						<p>{t('onboard.guide.key_changed_hint')}</p>
						<Button size="sm" busy={approving} onclick={() => void approveKey(refused.device_id)}
							>{t('onboard.guide.approve_key')}</Button
						>
					{/if}
				</Notice>
			{:else if refused}
				<Notice tone="warning" message={t('onboard.guide.refused', { id: refused.device_id })} />
			{/if}
			<CodeResult
				link={made.link}
				displayCode={made.display_code}
				https={status?.console.https ?? made.link.startsWith('https:')}
				mail={status?.mail ?? false}
				email={made.invite.email}
				mailed={made.mailed}
				mailError={made.mail_error}
				onmail={mail}
			/>
			<p class="hint">{resultNote}</p>
			{#if waitingLine}<p class="waiting" role="status">{waitingLine}</p>{/if}
		{:else}
			<form id={formId} onsubmit={create} novalidate>
				<Field label={t('onboard.invite.name')} hint={t('onboard.invite.name_hint')}>
					{#snippet children(f)}
						<Input {...f} width="medium" maxlength={64} autocomplete="off" bind:value={name} />
					{/snippet}
				</Field>
				{#if folders.length}
					<Field label={t('onboard.invite.folder')}>
						{#snippet children(f)}<Select {...f} options={folderOptions} bind:value={folder} />{/snippet}
					</Field>
				{/if}
				<FormSection title={t('onboard.invite.section')}>
					{#if status?.mail}
						<Field label={t('onboard.invite.email')} hint={t('onboard.invite.email_hint')}>
							{#snippet children(f)}
								<Input {...f} type="email" width="host" autocomplete="off" bind:value={email} />
							{/snippet}
						</Field>
					{/if}
					<Field label={t('onboard.invite.lifetime')}>
						{#snippet children(f)}<Select {...f} options={lifetimeOptions} bind:value={hours} />{/snippet}
					</Field>
					<Field label={t('onboard.invite.uses')}>
						{#snippet children(f)}<Select {...f} options={usesOptions} bind:value={uses} />{/snippet}
					</Field>
				</FormSection>
				<p class="hint">{t('onboard.invite.note')}</p>
				<button type="submit" hidden tabindex="-1" aria-hidden="true"></button>
			</form>
		{/if}

		{#if invites.length}
			<Disclosure title={t('onboard.invite.open_title')} meta={String(invites.length)} level={3}>
				<RowList label={t('onboard.invite.open_title')}>
					{#each invites as inv (inv.id)}
						<ListRow
							title={inv.name || inv.email || t('onboard.invite.unnamed')}
							sub={[
								inv.folder_name,
								t('onboard.invite.until', { date: formatDate(inv.expires_at) }),
								usesLine(inv)
							]
								.filter(Boolean)
								.join(', ')}
						>
							{#snippet end()}
								<Button
									size="sm"
									variant="danger-quiet"
									busy={revoking === inv.id}
									onclick={() => void revoke(inv)}>{t('onboard.invite.revoke')}</Button
								>
							{/snippet}
						</ListRow>
					{/each}
				</RowList>
			</Disclosure>
		{/if}
	</div>
	{#snippet footer()}
		{#if made && guiding}
			<!-- The guide has its own Back, Skip and Next -->
		{:else if made}
			<Button onclick={another}>{t('onboard.invite.another')}</Button>
			<Button variant="primary" onclick={close}>{t('onboard.invite.done')}</Button>
		{:else}
			<Button class="sheet-footer-start" onclick={close}>{t('actions.cancel')}</Button>
			<Button variant="primary" type="submit" form={formId} busy={creating}
				>{t('onboard.invite.create')}</Button
			>
		{/if}
	{/snippet}
</Sheet>

<style>
	.add-computer {
		display: flex;
		flex-direction: column;
		gap: var(--space-4);
	}
	.hint,
	.finished,
	.waiting {
		margin: 0;
		color: var(--text-muted);
	}
	.finished {
		color: var(--text);
		font-weight: 600;
	}
</style>
