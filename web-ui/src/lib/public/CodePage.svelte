<!--
  The page of an invite (/join/<code>) or a quick support link (/s/<code>),
  as launch/design/DESIGN.md draws them: who sent it, one big button for
  this computer (lib/public/onboard.ts) with the file and its publisher,
  the steps for the system (for an invite the three steps of the welcome
  mail first, then what Windows or a Mac asks), the short code for a
  NeoDesk that is
  already installed, a warning when the console is not on HTTPS, the other
  systems' downloads folded away, and until when the link works. A phone
  gets the link to copy or mail to oneself. A code that cannot be used
  (wrong, expired, used, revoked, or of the other kind) shows one plain
  message: the server tells these apart for nobody.
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { ApiError } from '#lib/api/client.ts';
	import { getPublicCode, type PublicCode } from '#lib/api/onboard.ts';
	import { formatBytes, formatDate } from '#lib/i18n/format.ts';
	import { t, tf } from '#lib/i18n/i18n.svelte.ts';
	import Button from '#lib/ui/Button.svelte';
	import { Copier } from '#lib/ui/clipboard.svelte.ts';
	import CodeBlock from '#lib/ui/CodeBlock.svelte';
	import Disclosure from '#lib/ui/Disclosure.svelte';
	import Icon from '#lib/ui/Icon.svelte';
	import ListRow from '#lib/ui/ListRow.svelte';
	import Notice from '#lib/ui/Notice.svelte';
	import OsIcon from '#lib/ui/OsIcon.svelte';
	import RowList from '#lib/ui/RowList.svelte';
	import States from '#lib/ui/States.svelte';
	import Steps from '#lib/ui/Steps.svelte';
	import ValueCopy from '#lib/ui/ValueCopy.svelte';
	import { ICONS } from '#lib/util/display.ts';
	import DownloadFrame from './DownloadFrame.svelte';
	import { detectComputer } from './detect.ts';
	import {
		downloadLabel,
		downloadTitle,
		isInsecure,
		mailMyself,
		mainAction,
		otherDownloads,
		stepKeys,
		WELCOME_STEPS
	} from './onboard.ts';

	interface Props {
		/** The code of the address */
		code: string;
		/** The page's kind: a code of the other kind is not shown here */
		kind: 'join' | 'support';
	}

	let { code, kind }: Props = $props();

	const mine = detectComputer(navigator.userAgent);

	let data = $state<PublicCode | null>(null);
	let phase = $state<'loading' | 'ready' | 'gone' | 'failed'>('loading');

	async function load() {
		phase = 'loading';
		try {
			const out = await getPublicCode(code);
			if (out.kind !== kind) {
				phase = 'gone';
				return;
			}
			data = out;
			phase = 'ready';
		} catch (err) {
			phase = err instanceof ApiError && err.status === 404 ? 'gone' : 'failed';
		}
	}

	onMount(() => void load());

	const main = $derived(data ? mainAction(data, mine) : { kind: 'none' as const });
	const others = $derived(data ? otherDownloads(data, main) : []);
	const insecure = $derived(!!data && isInsecure(data, location.protocol));
	const server = $derived(data ? data.company || data.server.name : '');
	const vars = $derived({
		server,
		company: server,
		sender: data?.supporter ?? '',
		publisher: data?.publisher ?? '',
		supporter: data?.supporter || server
	});
	/** An invite on a computer: the welcome mail's three steps, then what the system asks */
	const welcome = $derived(!!data && data.kind === 'join' && !!mine && mine.platform !== 'linux');
	const steps = $derived.by(() => {
		if (!data || main.kind !== 'download') return [];
		// On Windows the three steps say it all but the permission prompt
		if (welcome && mine?.platform === 'windows') return [];
		return stepKeys(data.kind, mine?.platform ?? null, main.download.format).map((k) => t(k, vars));
	});
	const windowsPrompt = $derived(
		welcome && main.kind === 'download' && mine?.platform === 'windows'
			? t(stepKeys('join', 'windows', main.download.format)[2] ?? '', vars)
			: ''
	);
	const archWords = $derived({ apple: t('onboard.public.mac_apple'), intel: t('onboard.public.mac_intel') });
	const heading = $derived.by(() => {
		if (!data) return '';
		if (!mine) return t(`onboard.public.phone_title_${data.kind}`);
		if (data.kind === 'join')
			return data.supporter
				? t('onboard.public.welcome_title', { sender: data.supporter, server })
				: t('onboard.public.join_title');
		return data.supporter
			? t('onboard.public.support_title', { supporter: data.supporter })
			: t('onboard.public.support_title_plain', { company: server });
	});
	const lead = $derived.by(() => {
		if (!data) return '';
		if (!mine) return t(`onboard.public.phone_lead_${data.kind}`, vars);
		return data.kind === 'join' ? t('onboard.public.join_lead', vars) : t('onboard.public.support_lead');
	});
	const footer = $derived.by(() => {
		if (!data) return '';
		const date = formatDate(data.expires_at);
		if (data.kind === 'support') return t('onboard.public.footer_support', { date });
		return data.supporter
			? t('onboard.public.footer_join_sender', { date, sender: data.supporter, server })
			: t('onboard.public.footer_join', { date });
	});
	const fileLine = $derived.by(() => {
		if (!data || main.kind !== 'download') return '';
		const d = main.download;
		const file = d.size ? t('onboard.public.file_line', { file: d.file, size: formatBytes(d.size) }) : d.file;
		return mine?.platform === 'windows' && data.publisher
			? `${file}. ${t('onboard.public.publisher', { publisher: data.publisher })}`
			: file;
	});
	const here = typeof location === 'undefined' ? '' : location.href;
	/**
	 * Open NeoDesk with the join link: on a Mac right after the install
	 * steps, elsewhere in the box for a NeoDesk that is already there. The
	 * app (1.5.0 and later) asks before it joins and names the server. A
	 * support link opens a Quick Support that was kept installed, or a
	 * NeoDesk, in the box; the app asks before anyone connects.
	 */
	const macOpen = $derived(
		!!data &&
			main.kind === 'download' &&
			mine?.platform === 'macos' &&
			data.kind === 'join' &&
			!!data.deep_link
	);

	// The code and the server address are typed into the app, so a click
	// copies them. Public pages have no toasts: the icon turns into a tick.
	const codeCopier = new Copier();
	const addressCopier = new Copier();
	$effect(() => () => {
		codeCopier.dispose();
		addressCopier.dispose();
	});
	/** Stands in for the address so the sentence can be split around it */
	const MARK = '\u0001';
</script>

{#snippet copyable(value: string, copier: Copier)}
	<button
		type="button"
		class={['copyable', copier.copied && 'copied']}
		title={tf('common.copy_value', 'Copy {value}', { value })}
		onclick={() => void copier.copy(value)}
		><span dir="ltr">{value}</span><Icon name={copier.copied ? 'check' : 'copy'} /></button
	>
{/snippet}

<!-- Every state names the page in the title, the dead link and the failure too (WCAG 2.4.2) -->
<svelte:head>
	{#if data}<title>{heading}</title>
	{:else if phase === 'gone'}<title>{t('onboard.public.gone_title')}</title>
	{:else if phase === 'failed'}<title>{t('errors.server_error_title')}</title>
	{/if}
</svelte:head>

{#if phase === 'loading'}
	<DownloadFrame><States kind="loading" /></DownloadFrame>
{:else if phase === 'gone'}
	<DownloadFrame>
		<section class="code-page" aria-labelledby="code-gone-title">
			<h1 id="code-gone-title">{t('onboard.public.gone_title')}</h1>
			<p>{t(kind === 'join' ? 'onboard.public.gone_join' : 'onboard.public.gone_support')}</p>
		</section>
	</DownloadFrame>
{:else if phase === 'failed'}
	<DownloadFrame>
		<section class="code-page" role="alert">
			<p>{t('onboard.public.load_failed')}</p>
			<Button onclick={() => void load()}>{t('errors.try_again')}</Button>
		</section>
	</DownloadFrame>
{:else if data}
	<DownloadFrame>
		<section class="code-page" aria-labelledby="code-title">
			{#if insecure}
				<Notice tone="warning" class="insecure" message={t('onboard.public.insecure', vars)} />
			{/if}
			{#if data.kind === 'support' && data.supporter}
				<p class="sender">{data.supporter}, {server}</p>
			{/if}
			<h1 id="code-title">{heading}</h1>
			<p class="lead">{lead}</p>
			{#if welcome}
				<ol class="welcome-steps">
					{#each WELCOME_STEPS as key (key)}<li>{t(key, vars)}</li>{/each}
				</ol>
			{/if}

			{#if !mine}
				<div class="phone">
					<ValueCopy value={here} label={t('onboard.public.copy_link')} />
					<Button href={mailMyself(heading, here)}>{t('onboard.public.mail_myself')}</Button>
				</div>
			{:else if data.release_error}
				<Notice tone="warning" class="no-release" message={t('onboard.public.no_release')} />
			{:else if main.kind === 'download'}
				<div class="main-action">
					<Button
						variant="primary"
						icon={ICONS.download}
						href={main.download.url}
						download={main.download.file}
						class="main-download">{t(downloadLabel(data.kind, mine.platform))}</Button
					>
					<p class="file">{fileLine}</p>
				</div>
				{#if windowsPrompt}<p class="prompt-note">{windowsPrompt}</p>{/if}
				{#if steps.length && welcome}
					<h2 class="system-steps">{t('onboard.public.mac_steps_title')}</h2>
				{/if}
				{#if steps.length}<Steps items={steps} />{/if}
				{#if macOpen}
					<div class="open-app">
						<Button href={data.deep_link} class="open-neodesk">{t('onboard.public.open_app')}</Button>
					</div>
				{/if}
			{:else if main.kind === 'command'}
				<div class="main-action">
					<p>{t(`onboard.public.command_hint_${data.kind}`, vars)}</p>
					<CodeBlock code={main.command} title={t('onboard.public.command_title')} />
				</div>
			{/if}

			{#if mine}
				<div class="short-code">
					<p>{t(`onboard.public.code_hint_${data.kind}`)}</p>
					<p class="code">{@render copyable(data.display_code, codeCopier)}</p>
					{#if data.kind === 'join' && data.server.address}
						{@const [before, after = ''] = t('onboard.public.server_address', { address: MARK }).split(MARK)}
						<p class="server-address">
							{before}{@render copyable(data.server.address, addressCopier)}{after}
						</p>
					{/if}
					{#if data.deep_link && !macOpen}
						<div class="open-app">
							<Button href={data.deep_link} class="open-neodesk"
								>{t(
									data.kind === 'join' ? 'onboard.public.open_app' : 'onboard.public.open_support_app'
								)}</Button
							>
						</div>
					{/if}
				</div>
			{/if}

			{#if others.length && mine}
				<Disclosure title={t('onboard.public.other_systems')} level={2}>
					<RowList label={t('onboard.public.other_systems')}>
						{#each others as d (d.url)}
							<ListRow title={downloadTitle(d, archWords)} sub={d.file}>
								{#snippet start()}<OsIcon platform={d.os} />{/snippet}
								{#snippet end()}<Button href={d.url} download={d.file} size="sm"
										>{t('download.download')}</Button
									>{/snippet}
							</ListRow>
						{/each}
					</RowList>
				</Disclosure>
			{/if}

			<p class="footer">{footer}</p>
		</section>
	</DownloadFrame>
{/if}

<style>
	.code-page {
		display: flex;
		flex-direction: column;
		gap: var(--space-4);
	}
	.code-page h1 {
		margin: 0;
	}
	.welcome-steps {
		display: flex;
		flex-direction: column;
		gap: var(--space-2);
		margin: 0;
		padding-inline-start: 1.5rem;
	}
	.welcome-steps li {
		padding-inline-start: var(--space-1);
	}
	.system-steps {
		margin: 0;
		font-size: var(--text-base, 1rem);
	}
	.lead,
	.file,
	.sender,
	.prompt-note,
	.footer {
		margin: 0;
		color: var(--text-muted);
	}
	.footer {
		font-size: var(--text-sm);
	}
	.main-action {
		display: flex;
		flex-direction: column;
		align-items: flex-start;
		gap: var(--space-2);
	}
	.main-action :global(.main-download) {
		min-height: 3rem;
		padding-inline: var(--space-6);
		font-size: var(--text-lg, 1.125rem);
	}
	.main-action p {
		margin: 0;
	}
	.phone,
	.open-app {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-2) var(--space-3);
	}
	.short-code {
		display: flex;
		flex-direction: column;
		gap: var(--space-1);
		padding: var(--space-3) var(--space-4);
		border: 1px solid var(--rule);
		border-radius: var(--radius);
	}
	.short-code p {
		margin: 0;
	}
	.short-code .code {
		font-family: var(--font-mono, monospace);
		font-size: 1.75rem;
		font-weight: 600;
		letter-spacing: 0.12em;
	}
	.short-code .server-address {
		overflow-wrap: anywhere;
	}
	.short-code .open-app {
		margin-top: var(--space-2);
	}
	.copyable {
		display: inline-flex;
		align-items: center;
		gap: 0.35em;
		max-width: 100%;
		margin: 0;
		padding: 0;
		border: 0;
		border-radius: var(--control-radius);
		background: none;
		color: inherit;
		font: inherit;
		letter-spacing: inherit;
		text-align: start;
		cursor: pointer;
	}
	.copyable span {
		min-width: 0;
		overflow-wrap: anywhere;
		text-decoration: underline dotted;
		text-decoration-color: var(--text-muted);
		text-underline-offset: 0.2em;
	}
	.copyable :global(svg) {
		flex: none;
		width: 1em;
		height: 1em;
		color: var(--text-muted);
	}
	.copyable:hover span {
		text-decoration-style: solid;
	}
	.copyable.copied :global(svg) {
		color: var(--success);
	}
</style>
