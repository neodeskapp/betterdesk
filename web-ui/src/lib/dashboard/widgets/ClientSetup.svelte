<!--
  Client setup: the values to type into RustDesk (ID, relay and API server,
  key), the address they are for, and the ways to hand them over (copy,
  QR code, deploy string, Intune script, pairing a NeoDesk agent). Drawn
  once the values are in (or failed), so nothing in it moves after it
  appears.
-->
<script lang="ts">
	import { addTabs } from '#lib/devices-area/add-computers.ts';
	import { clientHostFixed, configText } from '#lib/devices-area/dashboard.ts';
	import ExternalRow from '#lib/devices-area/ExternalRow.svelte';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import { link } from '#lib/nav.ts';
	import { can, session } from '#lib/state/session.svelte.ts';
	import Button from '#lib/ui/Button.svelte';
	import { Copier } from '#lib/ui/clipboard.svelte.ts';
	import Field from '#lib/ui/Field.svelte';
	import Input from '#lib/ui/Input.svelte';
	import ListRow from '#lib/ui/ListRow.svelte';
	import MenuButton from '#lib/ui/MenuButton.svelte';
	import MenuItem from '#lib/ui/MenuItem.svelte';
	import Notice from '#lib/ui/Notice.svelte';
	import RowList from '#lib/ui/RowList.svelte';
	import States from '#lib/ui/States.svelte';
	import ValueField from '#lib/ui/ValueField.svelte';
	import { toast } from '#lib/ui/toasts.svelte.ts';
	import { writeClientHost } from '#lib/devices-area/dashboard.ts';
	import { ICONS } from '#lib/util/display.ts';
	import type { WidgetProps } from './types.ts';

	let { widget, data, actions }: WidgetProps = $props();
	const uid = $props.id();

	$effect(() => data.wantConfig());

	const canApprove = can('enrollment.approve');
	// Support agents and the server key are tabs of Devices, Add computers
	const addComputerTabs = addTabs({ can, role: session.user?.role });
	const showAgentsLink = addComputerTabs.includes('agents');
	const showKeysLink = addComputerTabs.includes('manual');

	// Only a public ID server in Settings fixes the address
	const hostFixed = $derived(clientHostFixed(data.config));
	const fallbackHost = typeof location !== 'undefined' ? location.hostname : '';
	let applying = $state(false);

	async function applyHost(e?: SubmitEvent) {
		e?.preventDefault();
		if (applying || hostFixed) return;
		applying = true;
		// An empty field goes back to the server's own address
		if (!data.host.trim()) writeClientHost('');
		await data.loadConfig(true);
		applying = false;
	}

	const copyAll = new Copier();
	const copyDeploy = new Copier();
	$effect(() => () => {
		copyAll.dispose();
		copyDeploy.dispose();
	});

	function copyConfig() {
		if (!data.config) return void toast.warning(t('dashboard.config_not_ready'));
		void copyAll.copy(configText(data.config, t));
	}

	function copyDeployString() {
		const value = data.config?.deploy_config_string.trim();
		if (!value) return void toast.warning(t('dashboard.deploy_string_not_ready'));
		void copyDeploy.copy(value);
	}
</script>

{#if data.configLoaded}
	<section class="section client-setup" aria-labelledby="{uid}-title">
		<div class="section-head">
			<h2 id="{uid}-title">{widget.title || t('dashboard.client_config_title')}</h2>
		</div>
		<p class="section-note">{t('dashboard.client_config_hint')}</p>
		{#if data.config?.phone_unreachable_host}
			<Notice tone="warning" message={t('dashboard.client_config_local_warning')} />
		{/if}

		<form class="host-form" onsubmit={applyHost}>
			<Field
				label={t('dashboard.client_server_host_label')}
				hint={hostFixed ? t('dashboard.client_server_host_env_hint') : t('dashboard.client_server_host_hint')}
			>
				{#snippet children(f)}
					<div class="host-controls">
						<Input
							{...f}
							class="host-input"
							placeholder={t('dashboard.client_server_host_placeholder')}
							autocomplete="off"
							spellcheck={false}
							disabled={hostFixed}
							bind:value={data.host}
						/>
						<Button type="submit" busy={applying} disabled={hostFixed}
							>{t('dashboard.apply_client_host')}</Button
						>
					</div>
				{/snippet}
			</Field>
			{#if hostFixed}
				<p class="env-link">
					<a href={link('/settings#public-endpoints')}>{t('dashboard.client_host_settings_link')}</a>
				</p>
			{/if}
		</form>

		<dl class="facts values">
			<dt>{t('dashboard.config_server_id')}</dt>
			<dd>
				<ValueField
					label={t('dashboard.config_server_id')}
					value={data.configFailed ? fallbackHost : data.config?.server_id}
				/>
			</dd>
			<dt>{t('dashboard.config_relay_server')}</dt>
			<dd>
				<ValueField
					label={t('dashboard.config_relay_server')}
					value={data.configFailed ? fallbackHost : data.config?.relay_server}
				/>
			</dd>
			<dt>{t('dashboard.config_api_server')}</dt>
			<dd><ValueField label={t('dashboard.config_api_server')} value={data.config?.api_url} /></dd>
			<dt>{t('dashboard.config_key')}</dt>
			<dd>
				{#if data.configFailed}
					<span class="text-danger">{t('errors.load_key_failed')}</span>
				{:else}
					<ValueField
						hidden
						label={t('dashboard.config_key')}
						value={data.config?.public_key}
						empty={t('keys.no_key')}
					/>
				{/if}
			</dd>
		</dl>

		<div class="page-actions setup-actions">
			{#if canApprove}
				<Button variant="primary" icon="pin" onclick={actions.openPair}
					>{t('dashboard.pair_with_code')}</Button
				>
			{/if}
			<Button icon={copyAll.copied ? 'check' : ICONS.copy} onclick={copyConfig}
				>{t('dashboard.copy_all_config')}</Button
			>
			<Button icon={ICONS.qr} onclick={actions.openQr}>{t('dashboard.show_config_qr')}</Button>
			<MenuButton label={t('mobile_nav.more')}>
				<MenuItem icon={copyDeploy.copied ? 'check' : ICONS.copy} onselect={copyDeployString}
					>{t('dashboard.copy_deploy_string')}</MenuItem
				>
				<MenuItem icon={ICONS.terminal} onselect={actions.openScript}
					>{t('dashboard.show_intune_script')}</MenuItem
				>
			</MenuButton>
		</div>

		<RowList>
			{#if canApprove}
				<ListRow
					title={t('onboard.invite.button')}
					sub={t('setup_guide.steps.computer.hint')}
					onclick={actions.openAddComputer}
				/>
			{/if}
			<ExternalRow
				href="https://github.com/rustdesk/rustdesk/releases/latest"
				title={t('dashboard.get_rustdesk')}
				sub={t('dashboard.get_rustdesk_hint')}
			/>
			{#if showAgentsLink}
				<ListRow
					title={t('dashboard.open_agents')}
					sub={t('dashboard.agents_hint')}
					href={link('/devices/add?tab=agents')}
				/>
			{/if}
			{#if showKeysLink}
				<ListRow title={t('dashboard.open_keys')} href={link('/devices/add?tab=manual')} />
			{/if}
		</RowList>
	</section>
{:else}
	<section class="section" aria-label={widget.title || t('dashboard.client_config_title')}>
		<States kind="loading" message={t('status.checking')} />
	</section>
{/if}

<style>
	/* The address field with its button on one line */
	.host-form :global(.form-group) {
		margin-bottom: 0;
	}
	.host-controls {
		display: flex;
		gap: var(--space-2);
	}
	.host-controls > :global(.host-input) {
		flex: 1;
		min-width: 0;
	}
	.env-link {
		margin: var(--space-1) 0 0;
		font-size: var(--text-sm);
	}

	/* Values read top to bottom: label above value */
	.values {
		grid-template-columns: minmax(0, 1fr);
	}
	.values > dt {
		min-height: 0;
		padding-bottom: 0;
		border-bottom: 0;
		font-size: var(--text-sm);
	}
	.values > dd {
		padding-top: var(--space-1);
	}
</style>
