<!--
  Connect by ID: the field and Connect (and, on the dashboard, "Open in
  NeoDesk" and "Open in RustDesk"; on a phone only the RustDesk app,
  since there is no NeoDesk app for phones). An ID that is not 3 to 64
  letters, digits, - or _ says so under the field instead of connecting.
-->
<script lang="ts">
	import { tf } from '#lib/i18n/i18n.svelte.ts';
	import Button from '#lib/ui/Button.svelte';
	import { APP_NAMES, DESKTOP_APPS, type DesktopApp } from '#lib/util/app-links.ts';
	import { readQuickId } from './address-book.ts';

	interface Props {
		onconnect: (id: string) => void;
		/** The desktop apps next to Connect (the dashboard): NeoDesk and RustDesk, on a phone RustDesk */
		onapp?: ((id: string, app: DesktopApp) => void) | null;
		/** Phones: the app is the one action and Enter opens it */
		phone?: boolean;
		hint?: string;
		element?: HTMLInputElement | null;
	}

	let { onconnect, onapp = null, phone = false, hint = '', element = $bindable(null) }: Props = $props();

	const uid = $props.id();
	let value = $state('');
	let error = $state('');

	function read(): string {
		const id = readQuickId(value);
		error = id
			? ''
			: tf('remote_dashboard.quick_connect_invalid', 'Enter a device ID: 3 to 64 letters, digits, - or _.');
		if (!id) element?.focus();
		return id;
	}

	function connect() {
		const id = read();
		if (id) onconnect(id);
	}

	function app(name: DesktopApp) {
		const id = read();
		if (id) onapp?.(id, name);
	}
</script>

<div class="quick">
	<label class="field-label" for="{uid}-id">{tf('remote_dashboard.col_id', 'ID')}</label>
	<div class="quick-row">
		<input
			bind:this={element}
			id="{uid}-id"
			class="quick-input mono"
			type="text"
			inputmode="numeric"
			autocomplete="off"
			spellcheck="false"
			autocapitalize="none"
			enterkeyhint="go"
			bind:value
			aria-invalid={error ? 'true' : undefined}
			aria-describedby={error || hint ? `${uid}-status` : undefined}
			onkeydown={(e) => {
				if (e.key !== 'Enter') return;
				e.preventDefault();
				if (phone && onapp) app('rustdesk');
				else connect();
			}}
		/>
		{#if !phone}
			<Button variant="primary" onclick={connect}>{tf('remote_dashboard.connect', 'Connect')}</Button>
		{/if}
		{#if onapp && phone}
			<Button variant="primary" onclick={() => app('rustdesk')}
				>{tf('remote_viewer.open_in_app', 'Open in RustDesk app')}</Button
			>
		{:else if onapp}
			{#each DESKTOP_APPS as name (name)}
				<Button onclick={() => app(name)}
					>{tf('actions.open_in_app', 'Open in {app}', { app: APP_NAMES[name] })}</Button
				>
			{/each}
		{/if}
	</div>
	{#if error || hint}
		<p class={['quick-status', error && 'is-error']} id="{uid}-status" aria-live="polite">{error || hint}</p>
	{/if}
</div>

<style>
	.quick {
		display: flex;
		flex-direction: column;
		gap: var(--space-1);
		min-width: 0;
	}
	.field-label {
		font-weight: 600;
	}
	.quick-row {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
	}
	.quick-input {
		box-sizing: border-box;
		flex: 1 1 12rem;
		max-width: 20rem;
		min-height: var(--control-h);
		padding: 0 var(--space-3);
		border: 1px solid var(--border);
		border-radius: var(--control-radius);
		background: var(--bg);
		color: var(--text);
		font-size: inherit;
	}
	.quick-input:focus-visible {
		outline: var(--focus-ring);
		outline-offset: 1px;
	}
	.quick-input[aria-invalid='true'] {
		border-color: var(--danger);
	}
	.quick-status {
		margin: 0;
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
	.quick-status.is-error {
		color: var(--danger);
	}
</style>
