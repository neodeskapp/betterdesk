<script lang="ts">
	import '#lib/styles/tokens.css';
	import '#lib/styles/base.css';
	import '#lib/styles/a11y.css';
	import type { Snippet } from 'svelte';
	import { beforeNavigate, goto } from '$app/navigation';
	import { appPath, link } from '#lib/nav.ts';
	import { redirectTarget } from '#lib/redirects.ts';
	import A11yEffects from '#lib/shell/A11yEffects.svelte';

	let { children }: { children: Snippet } = $props();

	// A link inside the app to an old console address goes to its new place
	// (lib/redirects.ts). An address the browser loads itself reaches the
	// server first, which follows the same table.
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

<!-- Colour filters and the reading guide of the accessibility settings, on every page -->
<A11yEffects />
{@render children()}
