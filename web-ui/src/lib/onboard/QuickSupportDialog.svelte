<!--
  "Quick support" (top bar, Help requests; device.connect), as
  launch/design/DESIGN.md draws it: opening it makes a session link at
  once (one hour, one use) and shows it (CodeResult); then the dialog asks
  for the session's state every 2 s while it is open: waiting for the link,
  the app is open and asks the person, then Connect (browser viewer) and
  Open in the desktop app. The browser viewer asks the server for the
  session password itself when the device wants it; the desktop app gets
  it in its link. Declined, ended or expired: say so and offer a new link.
  Closing the dialog keeps the session; Help requests lists it.
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { ApiError } from '#lib/api/client.ts';
	import {
		createSession,
		endSession,
		getOnboardStatus,
		getSession,
		mailSession,
		takeSessionPassword,
		type NewSession,
		type OnboardStatus,
		type SupportSession
	} from '#lib/api/onboard.ts';
	import { formatDate } from '#lib/i18n/format.ts';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import { poll } from '#lib/state/live.ts';
	import Button from '#lib/ui/Button.svelte';
	import Dialog from '#lib/ui/Dialog.svelte';
	import FormStatus from '#lib/ui/FormStatus.svelte';
	import States from '#lib/ui/States.svelte';
	import Status from '#lib/ui/Status.svelte';
	import { toast } from '#lib/ui/toasts.svelte.ts';
	import { ICONS } from '#lib/util/display.ts';
	import CodeResult from './CodeResult.svelte';
	import {
		canConnect,
		desktopLink,
		go,
		isOver,
		POLL_MS,
		stateKey,
		stateTone,
		viewerPath
	} from './quick-support.ts';

	interface Props {
		onclose: () => void;
		/** Opens the dialog on a session made before (from the list of open sessions) */
		session?: SupportSession | null;
	}

	let { onclose, session: given = null }: Props = $props();

	let open = $state(true);
	let creating = $state(false);
	let error = $state('');
	let made = $state<NewSession | null>(null);
	// svelte-ignore state_referenced_locally
	let current = $state<SupportSession | null>(given);
	let status = $state<OnboardStatus | null>(null);
	let ending = $state(false);
	let connecting = $state(false);

	async function create() {
		if (creating) return;
		creating = true;
		error = '';
		try {
			made = await createSession({});
			current = made.session;
		} catch (err) {
			error =
				err instanceof ApiError && err.code
					? t(`onboard.errors.${err.code}`)
					: t('onboard.support.create_failed');
		} finally {
			creating = false;
		}
	}

	onMount(() => {
		void getOnboardStatus()
			.then((s) => (status = s))
			.catch(() => {});
		if (!current) void create();
	});

	// While the session is open, follow its state
	const following = $derived(!!current && !isOver(current.status));
	$effect(() => {
		if (!following) return;
		const id = current?.id ?? '';
		return poll(
			async () => {
				current = await getSession(id);
			},
			{ every: POLL_MS, immediate: false }
		);
	});

	async function mail(to: string) {
		if (!made) return;
		await mailSession(made.session.id, made.code, to);
	}

	function connectBrowser() {
		if (!current) return;
		go.tab(viewerPath(current.device_id));
	}

	async function connectApp() {
		if (!current || connecting) return;
		connecting = true;
		let password = '';
		try {
			password = await takeSessionPassword(current.id);
		} catch {
			// Without it the app asks for the password on the person's screen
		} finally {
			connecting = false;
		}
		go.here(desktopLink(current.device_id, password));
	}

	async function end() {
		if (!current || ending) return;
		ending = true;
		try {
			current = await endSession(current.id);
		} catch (err) {
			toast.error(
				err instanceof ApiError && err.code
					? t(`onboard.errors.${err.code}`)
					: t('onboard.support.end_failed')
			);
		} finally {
			ending = false;
		}
	}

	function again() {
		made = null;
		current = null;
		void create();
	}

	function close() {
		open = false;
		onclose();
	}

	const computer = $derived(current?.hostname || current?.device_id || '');
	const line = $derived(
		current
			? t(stateKey(current), {
					time: formatDate(current.expires_at, { year: undefined, month: undefined, day: undefined }),
					computer
				})
			: ''
	);
</script>

<Dialog bind:open title={t('onboard.support.title')} {onclose}>
	<div class="quick-support">
		{#if !current}
			{#if creating}<States kind="loading" message={t('onboard.support.creating')} />{/if}
			<FormStatus message={error} tone={error ? 'error' : 'neutral'} />
		{:else}
			{#if made && current.status === 'waiting'}
				<p class="hint">{t('onboard.support.text')}</p>
				<CodeResult
					link={made.link}
					linkLabel={t('onboard.result.session_link')}
					displayCode={made.display_code}
					showCode={false}
					https={status?.console.https ?? made.link.startsWith('https:')}
					mail={status?.mail ?? false}
					onmail={mail}
				/>
			{/if}
			<div class="state" role="status">
				<Status tone={stateTone(current.status)} label={line} />
				{#if current.status === 'allowed' && current.password_ready}
					<p class="hint">{t('onboard.support.state_allowed_hint')}</p>
				{/if}
				{#if computer && !isOver(current.status)}
					<dl class="facts">
						<dt>{t('onboard.support.computer')}</dt>
						<dd>{[current.hostname, current.device_id].filter(Boolean).join(', ')}</dd>
						{#if current.platform}
							<dt>{t('onboard.support.system')}</dt>
							<dd>{current.platform}</dd>
						{/if}
					</dl>
				{/if}
			</div>
			{#if current.status === 'waiting' || current.status === 'opened' || current.status === 'asking'}
				<p class="hint">{t('onboard.support.close_hint')}</p>
			{/if}
		{/if}
	</div>
	{#snippet footer()}
		{#if current && !isOver(current.status)}
			<Button
				variant="danger-quiet"
				class="modal-footer-start end-session"
				busy={ending}
				onclick={() => void end()}
				>{current.status === 'waiting' ? t('onboard.support.cancel_link') : t('onboard.support.end')}</Button
			>
		{/if}
		{#if current && canConnect(current)}
			<Button icon={ICONS.connect_app} class="connect-app" busy={connecting} onclick={() => void connectApp()}
				>{t('onboard.support.connect_app')}</Button
			>
			<Button variant="primary" icon={ICONS.connect} class="connect-browser" onclick={connectBrowser}
				>{t('onboard.support.connect_browser')}</Button
			>
		{:else if current && isOver(current.status)}
			<Button onclick={close}>{t('actions.close')}</Button>
			<Button variant="primary" onclick={again}>{t('onboard.support.again')}</Button>
		{:else}
			<Button onclick={close}>{t('actions.close')}</Button>
		{/if}
	{/snippet}
</Dialog>

<style>
	.quick-support {
		display: flex;
		flex-direction: column;
		gap: var(--space-4);
	}
	.hint {
		margin: 0;
		color: var(--text-muted);
	}
	.state {
		display: flex;
		flex-direction: column;
		gap: var(--space-2);
	}
	.facts {
		display: grid;
		grid-template-columns: max-content 1fr;
		gap: var(--space-1) var(--space-4);
		margin: 0;
	}
	.facts dt {
		color: var(--text-muted);
	}
	.facts dd {
		margin: 0;
	}
</style>
