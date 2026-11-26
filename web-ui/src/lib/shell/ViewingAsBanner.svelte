<!--
  Sign in as: while an admin views the console as another account, this
  bar stays at the top of every page and can't be closed. It names the
  account, says nothing can be changed, shows the time left, offers
  "Extend 15 minutes" once and Stop. Stop (and the end of the time) loads
  the console again as the admin, on the users page. The bar's height is
  --viewing-banner-h, so the top bar and the sidebar stay below it.
-->
<script lang="ts">
	import { onMount, untrack } from 'svelte';
	import { ApiError } from '#lib/api/client.ts';
	import {
		announceImpersonationChange,
		extendImpersonation,
		stopImpersonation
	} from '#lib/api/impersonation.ts';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import { session } from '#lib/state/session.svelte.ts';
	import Button from '#lib/ui/Button.svelte';
	import Icon from '#lib/ui/Icon.svelte';
	import { toast } from '#lib/ui/toasts.svelte.ts';
	import { timeLeft, viewedName } from './viewing-as.ts';

	const imp = $derived(session.impersonation);
	/** The person viewed, by display name */
	const username = $derived(viewedName(session.user));
	/** The server's clock minus this one's, from the bootstrap */
	const skew = untrack(() => (imp ? imp.serverNow - Date.now() : 0));
	let now = $state(Date.now());
	let busy = $state(false);
	let bar = $state<HTMLElement | null>(null);

	const left = $derived(imp ? imp.expiresAt - (now + skew) : 0);

	function back() {
		announceImpersonationChange();
		location.assign('/users');
	}

	async function stop() {
		busy = true;
		try {
			await stopImpersonation();
		} catch {
			/* Over already: loading again shows the admin */
		}
		back();
	}

	async function extend() {
		if (!imp) return;
		busy = true;
		try {
			const s = await extendImpersonation();
			session.impersonation = { ...imp, expiresAt: s.expires_at, canExtend: s.can_extend };
		} catch (err) {
			toast.error(err instanceof ApiError ? err.message : t('errors.server_error'));
		} finally {
			busy = false;
		}
	}

	onMount(() => {
		const timer = setInterval(() => {
			now = Date.now();
			if (imp && imp.expiresAt - (now + skew) <= 0) {
				clearInterval(timer);
				back();
			}
		}, 1000);
		const root = document.documentElement;
		const observer = new ResizeObserver(() => {
			root.style.setProperty('--viewing-banner-h', `${bar?.offsetHeight ?? 0}px`);
		});
		if (bar) observer.observe(bar);
		return () => {
			clearInterval(timer);
			observer.disconnect();
			root.style.removeProperty('--viewing-banner-h');
		};
	});
</script>

{#if imp}
	<div
		class="viewing-banner"
		bind:this={bar}
		role="region"
		aria-label={t('impersonation.tab_title', { username })}
	>
		<Icon name="visibility" />
		<p class="viewing-text">
			<strong>{t('impersonation.banner', { username })}</strong>
			<span>{t('impersonation.banner_read_only')}</span>
		</p>
		<span class="viewing-left">{t('impersonation.time_left', { time: timeLeft(left) })}</span>
		<div class="viewing-actions">
			{#if imp.canExtend}
				<Button size="sm" disabled={busy} onclick={() => void extend()}
					>{t('impersonation.extend', { minutes: 15 })}</Button
				>
			{/if}
			<Button size="sm" variant="primary" disabled={busy} onclick={() => void stop()}
				>{t('impersonation.stop')}</Button
			>
		</div>
	</div>
{/if}

<style>
	.viewing-banner {
		position: sticky;
		top: 0;
		z-index: calc(var(--layer-topbar) + 1);
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-2) var(--space-3);
		padding: var(--space-2) var(--page-pad);
		background: var(--bg-subtle);
		border-bottom: 2px solid var(--warning);
		color: var(--text);
	}
	.viewing-text {
		display: flex;
		flex-wrap: wrap;
		gap: 0 var(--space-2);
		flex: 1 1 16rem;
		min-width: 0;
		margin: 0;
	}
	.viewing-left {
		color: var(--text-muted);
		font-variant-numeric: tabular-nums;
	}
	.viewing-actions {
		display: flex;
		gap: var(--space-2);
		margin-inline-start: auto;
	}
</style>
