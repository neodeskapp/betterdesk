<!--
  Devices › Add computers › Receiver app (/devices/add?tab=receiver;
  enrollment.manage): install the NeoDesk app on a computer without anyone
  clicking through it. The admin gives a device token (pasted, or made here
  with the token dialog, which also picks the folder and the policies) and
  optionally a folder, and gets the silent msiexec command and a download:
  the generic Support Agent release's MSI as neodesk-support.msi with an
  install.cmd that runs the command (console/routes/receiverapp). The MSI
  comes from the release imported on Support agents; without one the page
  says where to import it.

  Its own tab and not a part of Support agents: that tab is about the
  generic agent release and pairing a waiting agent, this one is a token
  and a command for an install that joins on its own. It reuses the
  release imported there.
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { downloadInstaller, getReceiverSetup, type ReceiverSetup } from '#lib/api/receiver.ts';
	import { ApiError } from '#lib/api/client.ts';
	import type { NewToken } from '#lib/api/tokens.ts';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import { link } from '#lib/nav.ts';
	import { onRefresh } from '#lib/shell/page.svelte.ts';
	import Button from '#lib/ui/Button.svelte';
	import CodeBlock from '#lib/ui/CodeBlock.svelte';
	import Field from '#lib/ui/Field.svelte';
	import FormStatus from '#lib/ui/FormStatus.svelte';
	import Input from '#lib/ui/Input.svelte';
	import Notice from '#lib/ui/Notice.svelte';
	import Select from '#lib/ui/Select.svelte';
	import States from '#lib/ui/States.svelte';
	import ValueField from '#lib/ui/ValueField.svelte';
	import { folderOptions, loadPlaces, type Places } from './placement.ts';
	import { ARCH_LABEL, MSI_NAME, installCommand, properties, tokenLooksRight } from './receiver-command.ts';
	import TokenDialog from './TokenDialog.svelte';

	let setup = $state.raw<ReceiverSetup | null>(null);
	let loadError = $state('');
	let places = $state<Places>({ folders: [], orgs: [] });

	let token = $state('');
	let folder = $state('0');
	let arch = $state('x86_64');
	let creating = $state(false);

	let busy = $state(false);
	let status = $state<{ message: string; tone: 'neutral' | 'ok' | 'error' }>({
		message: '',
		tone: 'neutral'
	});

	async function load() {
		try {
			setup = await getReceiverSetup();
			loadError = '';
			const archs = setup.release?.packages.map((p) => p.arch) ?? [];
			if (archs.length && !archs.includes(arch)) arch = archs[0] ?? 'x86_64';
		} catch (err) {
			loadError = err instanceof Error && err.message ? err.message : t('errors.server_error');
		}
	}

	onMount(() => {
		void load();
		void loadPlaces().then((p) => (places = p));
	});
	$effect(() => onRefresh(() => void load()));

	const tokenOk = $derived(tokenLooksRight(token));
	const values = $derived({
		idServer: setup?.server.id_server ?? '',
		key: setup?.server.key ?? '',
		api: setup?.server.api_server ?? '',
		token: tokenOk ? token.trim() : '',
		group: Number(folder) || 0
	});
	const command = $derived(installCommand(values));
	const props = $derived(properties(values));
	const folderChoices = $derived(folderOptions(places.folders, t('onboard.invite.no_folder')));
	const archChoices = $derived(
		(setup?.release?.packages ?? []).map((p) => ({ value: p.arch, label: t(ARCH_LABEL[p.arch] ?? p.arch) }))
	);

	function created(made: NewToken | null) {
		creating = false;
		if (made?.token) {
			token = made.token;
			status = { message: t('receiver_app.token_filled'), tone: 'ok' };
		}
	}

	async function download() {
		if (busy || !tokenOk) return;
		busy = true;
		status = { message: t('receiver_app.downloading'), tone: 'neutral' };
		try {
			const { blob, filename } = await downloadInstaller({ token: token.trim(), group: values.group, arch });
			const url = URL.createObjectURL(blob);
			const a = document.createElement('a');
			a.href = url;
			a.download = filename;
			a.click();
			setTimeout(() => URL.revokeObjectURL(url), 0);
			status = { message: t('receiver_app.downloaded'), tone: 'ok' };
		} catch (err) {
			status = {
				message: err instanceof ApiError || err instanceof Error ? err.message : t('errors.server_error'),
				tone: 'error'
			};
		} finally {
			busy = false;
		}
	}
</script>

<div class="add-tab receiver-page">
	{#if loadError}
		<States kind="error" message={loadError} onretry={() => void load()} />
	{:else if !setup}
		<States kind="loading" />
	{:else}
		<div class="page-cols">
			<div class="page-main">
				<section class="section" aria-labelledby="receiver-token-title">
					<div class="section-head"><h2 id="receiver-token-title">{t('receiver_app.token_title')}</h2></div>
					<p class="section-note">{t('receiver_app.token_note')}</p>
					<Field
						label={t('receiver_app.token')}
						hint={t('receiver_app.token_hint')}
						error={token.trim() && !tokenOk ? t('receiver_app.token_invalid') : undefined}
					>
						{#snippet children(f)}
							<Input
								{...f}
								width="host"
								autocomplete="off"
								spellcheck={false}
								class="receiver-token"
								bind:value={token}
							/>
						{/snippet}
					</Field>
					<div class="form-actions">
						<Button icon="add" class="receiver-new-token" onclick={() => (creating = true)}
							>{t('tokens.create')}</Button
						>
					</div>
					{#if places.folders.length}
						<Field label={t('onboard.invite.folder')} hint={t('receiver_app.folder_hint')}>
							{#snippet children(f)}
								<Select {...f} width="medium" options={folderChoices} bind:value={folder} />
							{/snippet}
						</Field>
					{/if}
					{#if archChoices.length > 1}
						<Field label={t('receiver_app.arch')}>
							{#snippet children(f)}
								<Select {...f} width="medium" options={archChoices} bind:value={arch} />
							{/snippet}
						</Field>
					{/if}
				</section>

				<section class="section" aria-labelledby="receiver-install-title">
					<div class="section-head">
						<h2 id="receiver-install-title">{t('receiver_app.install_title')}</h2>
					</div>
					{#if !setup.release}
						<Notice tone="warning">
							{t('receiver_app.no_release')}
							<a href={link('/devices/add?tab=agents')}>{t('nav.agents')}</a>
						</Notice>
					{:else}
						<p class="section-note">{t('receiver_app.install_note', { msi: MSI_NAME })}</p>
						<div class="form-actions">
							<Button
								variant="primary"
								icon="download"
								{busy}
								disabled={!tokenOk}
								class="receiver-download"
								onclick={() => void download()}>{t('receiver_app.download')}</Button
							>
							<FormStatus message={status.message} tone={status.tone} />
						</div>
						{#if tokenOk}
							<CodeBlock code={command} title={t('receiver_app.command')} />
						{:else}
							<p class="text-muted receiver-wait">{t('receiver_app.command_needs_token')}</p>
						{/if}
					{/if}
				</section>
			</div>

			<aside class="page-side" aria-label={t('receiver_app.properties_title')}>
				<section class="section" aria-labelledby="receiver-props-title">
					<div class="section-head">
						<h2 id="receiver-props-title">{t('receiver_app.properties_title')}</h2>
					</div>
					<p class="section-note">{t('receiver_app.properties_note')}</p>
					<dl class="facts receiver-props">
						{#each props as p (p.name)}
							<dt><code>{p.name}</code></dt>
							<dd><ValueField value={p.value} label={p.name} hidden={p.name === 'TOKEN'} /></dd>
						{/each}
					</dl>
				</section>
			</aside>
		</div>
	{/if}
</div>

{#if creating}
	<TokenDialog token={null} onclose={() => (creating = false)} onsaved={created} />
{/if}

<style>
	.receiver-page :global(.code-block) {
		white-space: pre-wrap;
		word-break: break-all;
	}
	.receiver-props dt code {
		font-size: var(--text-sm);
	}
</style>
