<script lang="ts">
	import '#lib/styles/tokens.css';
	import '#lib/styles/base.css';
	import '#lib/styles/a11y.css';
	import { onMount, type Snippet } from 'svelte';
	import { afterNavigate, beforeNavigate, goto } from '$app/navigation';
	import { appPath, link } from '#lib/nav.ts';
	import { redirectTarget } from '#lib/redirects.ts';
	import A11yPanel from '#lib/shell/A11yPanel.svelte';
	import { a11y, initA11y } from '#lib/shell/a11y.svelte.ts';
	import Announcer from '#lib/ui/Announcer.svelte';
	import { revealFocus } from '#lib/ui/reveal-focus.ts';

	let { children }: { children: Snippet } = $props();

	// Keyboard focus never stays half hidden under a bar or a pinned column
	onMount(() => revealFocus());
	// The accessibility settings this browser keeps (the boot script applied them before the first paint)
	onMount(() => initA11y());

	// A link inside the app to an old console address goes to its new place
	// (lib/redirects.ts). An address the browser loads itself reaches the
	// server first, which follows the same table; the rules that need a #id
	// the server never sees, so the app follows them once it has started.
	afterNavigate((nav) => {
		if (nav.type !== 'enter') return;
		const target = redirectTarget(appPath(location.pathname), location.search, location.hash);
		// eslint-disable-next-line svelte/no-navigation-without-resolve -- link() adds the base to a whole address
		if (target) void goto(link(target), { replaceState: true });
	});

	beforeNavigate((nav) => {
		const to = nav.to?.url;
		if (!to || nav.willUnload || nav.type === 'popstate' || to.origin !== location.origin) return;
		const target = redirectTarget(appPath(to.pathname), to.search, to.hash);
		if (!target) return;
		nav.cancel();
		// eslint-disable-next-line svelte/no-navigation-without-resolve -- link() adds the base to a whole address
		void goto(link(target));
	});
</script>

<!-- One accessibility panel for every page, signed in or not -->
<A11yPanel bind:open={a11y.open} />
<!-- The live regions every status message is read from -->
<Announcer />
{@render children()}
