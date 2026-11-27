<!--
  Settings › Branding: roughly how NeoDesk clients show the client app
  fields (ClientAppBranding), following them as people type. The Branding
  tab shows it twice and lets CSS pick one: in the side column beside the
  fields from 1200 px (`place` side), at the end of the client app group
  below that (`place` group). The other one is display: none, so neither
  screen readers nor the eye meet it twice.
-->
<script lang="ts">
	import { buttonColors, logoSrc } from '#lib/clients/branding.ts';
	import { t, tf } from '#lib/i18n/i18n.svelte.ts';
	import BrandLogo from '#lib/shell/BrandLogo.svelte';
	import type { AppearanceForm } from './appearance-form.svelte.ts';
	import type { ClientValues } from './ClientAppBranding.svelte';

	let { form, place }: { form: AppearanceForm; place: 'side' | 'group' } = $props();

	const values = $derived(form.draft.values as unknown as Partial<ClientValues>);
	const text = (field: keyof ClientValues) => String(values[field] ?? '').trim();
	const logo = $derived(logoSrc(values['client.logo'] ?? null));
	const button = $derived(buttonColors(text('client.accent_color')));
	const contact = $derived(
		[text('client.phone'), text('client.email'), text('client.website')].filter(Boolean)
	);
	const titleId = $derived(`cb-preview-title-${place}`);
</script>

<section class={['cb-preview-part', `is-${place}`]} aria-labelledby={titleId} data-search-skip>
	{#if place === 'side'}
		<!-- Beside the console preview it needs its full name -->
		<h2 class="cb-title" id={titleId}>{tf('settings.brand.client_preview', 'Client app preview')}</h2>
	{:else}
		<h3 class="cb-sub" id={titleId}>{t('client_branding.preview_title')}</h3>
	{/if}
	<p class="cb-note">{t('client_branding.preview_note')}</p>
	<div class="cb-preview">
		<div class="cb-preview-head">
			{#if logo}
				<img class="cb-pv-logo" src={logo} alt="" width="32" height="32" />
			{:else}
				<span class="cb-pv-logo"><BrandLogo mark /></span>
			{/if}
			<span class="cb-pv-name">{text('client.company_name') || 'NeoDesk'}</span>
		</div>
		<div class="cb-preview-body">
			{#if text('client.support_contact')}
				<p class="cb-pv-support">{text('client.support_contact')}</p>
			{/if}
			{#if contact.length}
				<ul class="cb-pv-contact">
					{#each contact as line, i (i)}<li dir="ltr">{line}</li>{/each}
				</ul>
			{/if}
			<span class="cb-pv-button" style:background-color={button.background} style:color={button.text}
				>{t('client_branding.preview_button')}</span
			>
		</div>
	</div>
	<p class="cb-note">{t('client_branding.hint')}</p>
</section>

<style>
	.cb-preview-part {
		display: flex;
		flex-direction: column;
		gap: var(--space-2);
		min-width: 0;
	}
	.cb-preview-part.is-group {
		max-width: 420px;
		padding: var(--space-4) var(--row-pad-x) var(--space-3);
	}
	.cb-preview-part.is-side {
		margin-top: var(--space-6);
	}
	/* One of the two shows: beside the fields where there is a side column, else after them */
	@media (min-width: 1200px) {
		.cb-preview-part.is-group {
			display: none;
		}
	}
	@media (max-width: 1199.98px) {
		.cb-preview-part.is-side {
			display: none;
		}
	}
	.cb-title {
		margin: 0;
		font-size: var(--text-md);
	}
	.cb-sub {
		margin: 0;
		font-size: var(--text-sm);
		font-weight: 600;
		color: var(--text-muted);
	}
	.cb-note {
		margin: 0;
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
	/* A small window like the client's */
	.cb-preview {
		overflow: hidden;
		border: 1px solid var(--border);
		border-radius: var(--radius);
		background: var(--bg);
	}
	.cb-preview-head {
		display: flex;
		align-items: center;
		gap: var(--space-3);
		padding: var(--space-3) var(--space-4);
		border-bottom: 1px solid var(--rule);
		background: var(--bg-subtle);
	}
	.cb-pv-logo {
		display: block;
		flex: none;
		width: 32px;
		height: 32px;
		object-fit: contain;
	}
	.cb-pv-logo :global(svg) {
		display: block;
		width: 32px;
		height: 32px;
	}
	.cb-pv-name {
		min-width: 0;
		font-size: var(--text-md);
		font-weight: 600;
		overflow-wrap: anywhere;
	}
	.cb-preview-body {
		display: flex;
		flex-direction: column;
		align-items: flex-start;
		gap: var(--space-2);
		padding: var(--space-4);
	}
	.cb-pv-support {
		margin: 0;
		font-weight: 600;
		overflow-wrap: anywhere;
	}
	.cb-pv-contact {
		display: flex;
		flex-direction: column;
		gap: var(--space-1);
		margin: 0;
		padding: 0;
		list-style: none;
		color: var(--text-muted);
		font-size: var(--text-sm);
		overflow-wrap: anywhere;
	}
	.cb-pv-button {
		display: inline-flex;
		align-items: center;
		min-height: var(--control-h);
		margin-top: var(--space-2);
		padding: 0 var(--space-4);
		border-radius: var(--radius);
		font-weight: 600;
	}
</style>
