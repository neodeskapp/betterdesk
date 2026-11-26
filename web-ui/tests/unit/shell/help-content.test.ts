/**
 * The Help panel links NeoDesk's documentation and its community on
 * Discord, each in a new tab without access back to the console.
 */
import { cleanup, render, screen } from '@testing-library/svelte';
import { afterEach, expect, test } from 'vitest';
import { DISCORD_URL, DOCS_URL } from '../../../src/lib/util/links.ts';

afterEach(() => cleanup());

test('Community on Discord sits next to the documentation and opens in a new tab', async () => {
	const HelpContent = (await import('../../../src/lib/shell/HelpContent.svelte')).default;
	render(HelpContent);
	const links = screen.getAllByRole('link');
	const docs = screen.getByRole('link', { name: 'help.panel.link_docs' });
	const discord = screen.getByRole('link', { name: 'help.panel.link_discord' });
	expect(docs).toHaveAttribute('href', DOCS_URL);
	expect(discord).toHaveAttribute('href', DISCORD_URL);
	expect(discord).toHaveAttribute('target', '_blank');
	expect(discord.getAttribute('rel')).toMatch(/\bnoopener\b/);
	expect(links.indexOf(discord)).toBe(links.indexOf(docs) + 1);
});

test('Help links the accessibility statement', async () => {
	const HelpContent = (await import('../../../src/lib/shell/HelpContent.svelte')).default;
	render(HelpContent);
	const statement = screen.getByRole('link', { name: 'help.panel.link_accessibility' });
	expect(statement).toHaveAttribute('href', `${DOCS_URL}/project/accessibility`);
	expect(statement).toHaveAttribute('target', '_blank');
});
