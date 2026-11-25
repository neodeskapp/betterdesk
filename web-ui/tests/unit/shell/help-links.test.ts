import { render, screen, cleanup } from '@testing-library/svelte';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import { setStrings } from '../../../src/lib/i18n/i18n.svelte.ts';

vi.mock('$app/paths', () => ({ resolve: (p: string) => '/app' + (p || '/'), asset: (p: string) => '/' + p }));

beforeEach(() =>
	setStrings('de', 'ltr', {
		help: { panel: { links: 'Links', link_repo: 'Quellcode', link_discord: 'Hilfe auf Discord' } }
	})
);
afterEach(() => cleanup());

// GitHub Issues are off in the repository: the Help panel sends people to
// the NeoDesk Discord, and no link goes to the issues.
test('the Help panel links the NeoDesk Discord, not GitHub issues', async () => {
	const HelpContent = (await import('../../../src/lib/shell/HelpContent.svelte')).default;
	render(HelpContent);
	const discord = screen.getByRole('link', { name: /Hilfe auf Discord/ });
	expect(discord.getAttribute('href')).toBe('https://discord.gg/nwERSawufr');
	for (const link of screen.getAllByRole('link')) {
		expect(link.getAttribute('href') ?? '').not.toMatch(/\/issues/);
	}
});
