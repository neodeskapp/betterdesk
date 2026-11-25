<!--
  What the Help panel shows: the way back to the setup guide, for accounts
  that may see it; in the console, Keyboard shortcuts (a dialog) and
  Platform support (device.view; it left the Devices section of the
  sidebar); and NeoDesk's own links (documentation, the community on
  Discord, the source code, from lib/util/links.ts). Every outside link
  opens a new tab without access back to the console. The
  console has no setting for an install's own help address yet; when it
  gets one, these links give way to it.
-->
<script lang="ts">
	import GuideLink from '#lib/guide/GuideLink.svelte';
	import { i18n, t } from '#lib/i18n/i18n.svelte.ts';
	import { link } from '#lib/nav.ts';
	import { can } from '#lib/state/session.svelte.ts';
	import Dialog from '#lib/ui/Dialog.svelte';
	import Icon from '#lib/ui/Icon.svelte';
	import { DISCORD_URL, DOCS_URL, REPO_URL } from '#lib/util/links.ts';
	import { shortcutHint } from './search.ts';

	/** onguide: the setup guide was brought back (the panel closes for the overview) */
	let { onguide, onleave }: { onguide?: () => void; onleave?: () => void } = $props();

	let shortcuts = $state(false);
	const isMac =
		typeof navigator !== 'undefined' && /Mac|iP(hone|ad|od)/.test(navigator.platform || navigator.userAgent);
	const keys = $derived([
		{ keys: [shortcutHint(i18n.lang, isMac), '/'], what: t('help.shortcuts.search') },
		{ keys: ['↑ ↓', 'Enter'], what: t('help.shortcuts.results') },
		{ keys: ['Esc'], what: t('help.shortcuts.close') },
		{ keys: ['Tab', 'Shift Tab'], what: t('help.shortcuts.move') }
	]);

	const links = [
		{ href: DOCS_URL, icon: 'menu_book', key: 'help.panel.link_docs' },
		{ href: DISCORD_URL, icon: 'chat', key: 'help.panel.link_discord' },
		{ href: REPO_URL, icon: 'code', key: 'help.panel.link_repo' }
	];
</script>

<div class="help-content">
	<GuideLink level={3} onopen={onguide} />

	<section class="help-section">
		<h3>{t('help.panel.console')}</h3>
		<ul class="help-links">
			<li>
				<button type="button" class="help-link" aria-haspopup="dialog" onclick={() => (shortcuts = true)}
					><Icon name="keyboard" />{t('help.panel.shortcuts')}</button
				>
			</li>
			{#if can('device.view')}
				<li>
					<a href={link('/cross-platform')} onclick={() => onleave?.()}
						><Icon name="devices" />{t('nav.cross_platform')}</a
					>
				</li>
			{/if}
		</ul>
	</section>

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

{#if shortcuts}
	<Dialog title={t('help.panel.shortcuts')} onclose={() => (shortcuts = false)}>
		<dl class="shortcuts">
			{#each keys as k (k.what)}
				<dt>
					{#each k.keys as key, i (key)}{#if i > 0}<span class="or">{t('help.shortcuts.or')}</span>{/if}<kbd
							>{key}</kbd
						>{/each}
				</dt>
				<dd>{k.what}</dd>
			{/each}
		</dl>
	</Dialog>
{/if}

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
	.help-links a,
	.help-link {
		display: flex;
		align-items: center;
		gap: var(--space-2);
		min-height: var(--control-h);
		padding: var(--space-1) 0;
	}
	.help-link {
		width: 100%;
		border: 0;
		background: transparent;
		color: var(--accent);
		font: inherit;
		text-align: start;
		cursor: pointer;
	}
	.help-link:hover {
		text-decoration: underline;
	}
	.help-links :is(a, .help-link) > :global(.material-icons) {
		color: var(--text-muted);
		font-size: 18px;
	}
	.shortcuts {
		display: grid;
		grid-template-columns: auto 1fr;
		gap: var(--space-2) var(--space-4);
		align-items: baseline;
		margin: 0;
	}
	.shortcuts dt {
		white-space: nowrap;
	}
	.shortcuts dd {
		margin: 0;
	}
	.shortcuts kbd {
		padding: 0 var(--space-1);
		border: 1px solid var(--rule);
		border-radius: var(--radius-sm);
		font-family: var(--font);
		font-size: var(--text-sm);
	}
	.or {
		margin-inline: var(--space-1);
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
	@media (pointer: coarse) {
		.help-links a,
		.help-link {
			min-height: 44px;
		}
	}
</style>
