<!--
  What the Help panel shows: the way back to the setup guide, for accounts
  that may see it, and links to the project. Every link opens a new tab
  without access back to the console.
-->
<script lang="ts">
	import GuideLink from '#lib/guide/GuideLink.svelte';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import Icon from '#lib/ui/Icon.svelte';

	/** onguide: the setup guide was brought back (the panel closes for the overview) */
	let { onguide }: { onguide?: () => void } = $props();

	const REPO = 'https://github.com/neodeskapp/neodesk';
	const links = [
		{ href: REPO, icon: 'code', key: 'help.panel.link_repo' },
		{ href: `${REPO}/issues`, icon: 'bug_report', key: 'help.panel.link_issues' }
	];
</script>

<div class="help-content">
	<GuideLink level={3} onopen={onguide} />

	<section class="help-section">
		<h3>{t('help.panel.links')}</h3>
		<ul class="help-links">
			{#each links as l (l.key)}
				<li>
					<a href={l.href} target="_blank" rel="noopener noreferrer"><Icon name={l.icon} />{t(l.key)}</a>
				</li>
			{/each}
		</ul>
	</section>
</div>

<style>
	.help-content {
		display: flex;
		flex-direction: column;
		gap: var(--space-5);
	}
	.help-section h3 {
		margin-bottom: var(--space-2);
		font-size: var(--text-base);
		font-weight: 600;
	}
	.help-links {
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.help-links a {
		display: flex;
		align-items: center;
		gap: var(--space-2);
		min-height: var(--control-h);
		padding: var(--space-1) 0;
	}
	.help-links a > :global(.material-icons) {
		color: var(--text-muted);
		font-size: 18px;
	}
	@media (pointer: coarse) {
		.help-links a {
			min-height: 44px;
		}
	}
</style>
