<!--
  "Add a computer" (Devices, enrollment.approve), a side sheet as
  launch/design/DESIGN.md draws it: an invite link that sets up BetterDesk
  on a computer and joins it to this server, approved, named and in its
  folder. Every field is optional: a name, a folder, an address to send
  the link to (only with mail set up), how long it works (7 days) and how
  often (once). Create invite shows the link once (CodeResult); the open
  invites are listed below with Revoke.
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import type { Folder } from '#lib/api/devices.ts';
	import { ApiError } from '#lib/api/client.ts';
	import {
		createInvite,
		getOnboardStatus,
		listInvites,
		mailInvite,
		revokeInvite,
		type Invite,
		type NewInvite,
		type OnboardStatus
	} from '#lib/api/onboard.ts';
	import { formatDate } from '#lib/i18n/format.ts';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import Button from '#lib/ui/Button.svelte';
	import Disclosure from '#lib/ui/Disclosure.svelte';
	import Field from '#lib/ui/Field.svelte';
	import FormSection from '#lib/ui/FormSection.svelte';
	import Input from '#lib/ui/Input.svelte';
	import ListRow from '#lib/ui/ListRow.svelte';
	import RowList from '#lib/ui/RowList.svelte';
	import Select, { type SelectOption } from '#lib/ui/Select.svelte';
	import Sheet from '#lib/ui/Sheet.svelte';
	import { toast } from '#lib/ui/toasts.svelte.ts';
	import CodeResult from './CodeResult.svelte';
	import { inviteBody, LIFETIMES, UNLIMITED_USES, USES } from './invite.ts';

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
		name = '';
		email = '';
	}

	function close() {
		open = false;
		onclose();
	}

	const resultTitle = $derived(
		made?.invite.name
			? t('onboard.invite.result_title_named', { name: made.invite.name })
			: t('onboard.invite.result_title')
	);
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
	subtitle={made ? resultText : t('onboard.invite.subtitle')}
	status={error}
	statusTone={error ? 'error' : 'neutral'}
	{onclose}
>
	<div class="add-computer">
		{#if made}
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
		{#if made}
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
	.hint {
		margin: 0;
		color: var(--text-muted);
	}
</style>
