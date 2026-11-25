<!--
  A page of settings forms that is not a tab of Settings: the Server
  section's Backups, Updates and Configuration files, and Your account. It
  does for one page what the Settings page does for its tabs: the title
  and its sentence, the check for the permission it needs (anyone else
  sees the no-access view; Your account needs none), a #<id> in the
  address scrolls to that setting and marks it (lib/settings/reveal.ts,
  also when the top bar search jumps here), and leaving with unsaved
  changes asks first (lib/settings/dirty.ts).
-->
<script lang="ts">
	import type { Snippet } from 'svelte';
	import { tick, untrack } from 'svelte';
	import { page } from '$app/state';
	import { beforeNavigate, goto } from '$app/navigation';
	import { tf } from '#lib/i18n/i18n.svelte.ts';
	import NoAccess from '#lib/shell/NoAccess.svelte';
	import { setPageTitle } from '#lib/shell/page.svelte.ts';
	import { can } from '#lib/state/session.svelte.ts';
	import { confirm } from '#lib/ui/dialogs.svelte.ts';
	import { warnOnLeave } from '#lib/ui/form.svelte.ts';
	import { anyDirty, mayLeave } from './dirty.ts';
	import { clearAllReveals, findTarget, highlight, onControlChange, whenPresent } from './reveal.ts';
	import { REVEAL_EVENT } from './reveal-event.ts';
	import { decodeHash } from './tabs.ts';

	interface Props {
		title: string;
		description: string;
		/** The permission the page needs; '' for every account (Your account) */
		need?: string;
		/** Use the whole width (the configuration file editor) */
		wide?: boolean;
		/** A name for the page in tests and styles */
		id: string;
		children: Snippet;
	}

	let { title, description, need = 'server.config', wide = false, id, children }: Props = $props();

	const allowed = $derived(need === '' || can(need));

	$effect(() => setPageTitle(title));

	/* ── #<id>: scroll to the setting and mark it ── */

	let following: AbortController | null = null;

	async function followHash(hash: string): Promise<void> {
		following?.abort();
		following = null;
		if (!hash || hash === '#') return;
		const ctrl = new AbortController();
		following = ctrl;
		// The forms draw parts of themselves when their data arrives
		const target = await whenPresent(decodeHash(hash), ctrl.signal);
		if (!target || ctrl.signal.aborted) return;
		await tick();
		highlight(target);
	}

	$effect(() => {
		const hash = page.url.hash;
		if (!allowed) return;
		untrack(() => void followHash(hash));
	});

	$effect(() => () => {
		following?.abort();
		clearAllReveals();
	});

	// The search in the top bar found a setting this address shows already
	$effect(() => {
		const again = (e: Event) => {
			const target = (e as CustomEvent<unknown>).detail;
			const el = typeof target === 'string' ? findTarget(target) : null;
			if (el) highlight(el);
		};
		window.addEventListener(REVEAL_EVENT, again);
		return () => window.removeEventListener(REVEAL_EVENT, again);
	});

	// A switch that changes ends the "shown for the search" state of what it controls
	$effect(() => {
		const onChange = (e: Event) => onControlChange(e.target);
		document.addEventListener('change', onChange);
		return () => document.removeEventListener('change', onChange);
	});

	/* ── Unsaved changes ── */

	$effect(() => warnOnLeave(anyDirty));

	let leaving = false;
	/** Where the page goes once it may be left; set while that is being decided */
	let leaveTo: URL | null = null;
	beforeNavigate((nav) => {
		if (leaving || nav.willUnload || !nav.to || !anyDirty()) return;
		const to = nav.to.url;
		// Only the #id changes: nothing is lost
		if (to.pathname === page.url.pathname && to.search === page.url.search) return;
		nav.cancel();
		if (leaveTo) {
			leaveTo = to;
			return;
		}
		leaveTo = to;
		void mayLeave(() =>
			confirm({
				title: tf('settings.page.discard_title', 'Discard unsaved changes?'),
				message: tf('settings.page.discard_message', 'The changes on this page are not saved yet.'),
				confirmLabel: tf('settings.page.discard_confirm', 'Discard changes'),
				danger: true
			})
		).then((ok) => {
			const target = leaveTo;
			leaveTo = null;
			if (!ok || !target) return;
			leaving = true;
			// The address the person chose, as it was
			// eslint-disable-next-line svelte/no-navigation-without-resolve
			void goto(target.pathname + target.search + target.hash).finally(() => (leaving = false));
		});
	});
</script>

{#if !allowed}
	<NoAccess />
{:else}
	<div class={['page', 'settings-page', wide ? 'is-wide' : 'settings-page-frame']} data-page={id}>
		<div class="page-head">
			<h1>{title}</h1>
			<p class="page-description">{description}</p>
		</div>
		{@render children()}
	</div>
{/if}

<style>
	.settings-page-frame {
		max-width: calc(2 * var(--measure) + var(--side-w) + 2 * var(--space-7));
	}

	/* A setting reached from the search or a link: outlined for a moment (reveal.ts),
	   as on the Settings page */
	.settings-page
		:global(:is(.form-group, .check-field, .choice, .facts, .settings-subgroup, .section, .danger-zone)) {
		outline: 2px solid transparent;
		outline-offset: 4px;
		transition: outline-color var(--motion);
	}
	.settings-page :global(.is-found) {
		outline-color: var(--accent);
		border-radius: var(--radius);
	}
</style>
