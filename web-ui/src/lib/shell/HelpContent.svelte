<!--
  What the Help panel shows: the way back to the setup guide, for accounts
  that may see it, and NeoDesk's own links (documentation, the community
  on Discord, the source code and the issue tracker, from lib/util/links.ts).
  Every link opens a new tab without access back to the console. The
  console has no setting for an install's own help address yet; when it
  gets one, these links give way to it.
-->
<script lang="ts">
	import GuideLink from '#lib/guide/GuideLink.svelte';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import Icon from '#lib/ui/Icon.svelte';
	import { DISCORD_URL, DOCS_URL, REPO_URL } from '#lib/util/links.ts';

	/** onguide: the setup guide was brought back (the panel closes for the overview) */
	let { onguide }: { onguide?: () => void } = $props();

	const links = [
		{ href: DOCS_URL, icon: 'menu_book', key: 'help.panel.link_docs' },
		{ href: DISCORD_URL, icon: 'chat', key: 'help.panel.link_discord' },
		{ href: REPO_URL, icon: 'code', key: 'help.panel.link_repo' }
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
