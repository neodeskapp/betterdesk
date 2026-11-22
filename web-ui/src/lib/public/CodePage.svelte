<!--
  The page of an invite (/join/<code>) or a quick support link (/s/<code>),
  as launch/design/DESIGN.md draws them: who sent it, one big button for
  this computer (lib/public/onboard.ts) with the file and its publisher,
  the steps for the system, the short code for a BetterDesk that is
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
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import Button from '#lib/ui/Button.svelte';
	import CodeBlock from '#lib/ui/CodeBlock.svelte';
	import Disclosure from '#lib/ui/Disclosure.svelte';
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
		stepKeys
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
	const vars = $derived({ server, publisher: data?.publisher ?? '', supporter: data?.supporter || server });
	const steps = $derived(
		data && main.kind === 'download' ? stepKeys(data.kind, mine?.platform ?? null).map((k) => t(k, vars)) : []
	);
	const archWords = $derived({ apple: t('onboard.public.mac_apple'), intel: t('onboard.public.mac_intel') });
	const heading = $derived.by(() => {
		if (!data) return '';
		if (!mine) return t(`onboard.public.phone_title_${data.kind}`);
		if (data.kind === 'join') return t('onboard.public.join_title');
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
</script>

<svelte:head>
	{#if data}<title>{heading}</title>{/if}
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
				{#if steps.length}<Steps items={steps} />{/if}
				{#if mine.platform === 'macos' && data.kind === 'join' && data.deep_link}
					<div class="open-app">
						<Button href={data.deep_link} class="open-betterdesk">{t('onboard.public.open_app')}</Button>
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
					<p class="code" dir="ltr">{data.display_code}</p>
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
	.lead,
	.file,
	.sender,
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
</style>
