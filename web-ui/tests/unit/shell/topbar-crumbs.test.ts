/**
 * The breadcrumb in the top bar has no links (a section has no page of its
 * own), so it is not a navigation landmark with nothing to navigate: a
 * named group with the current page marked.
 */
import { render, screen } from '@testing-library/svelte';
import { expect, test, vi } from 'vitest';

vi.mock('$app/paths', () => ({ resolve: (p: string) => '/app' + (p || '/'), asset: (p: string) => '/' + p }));
vi.setConfig({ testTimeout: 60_000 });

test('the breadcrumb is a named group, not a navigation landmark', async () => {
	const Topbar = (await import('../../../src/lib/shell/Topbar.svelte')).default;
	render(Topbar, {
		crumbs: ['Devices', 'All devices'],
		drawer: false,
		drawerOpen: false,
		sidebarId: 'sb',
		helpOpen: false,
		helpId: 'help',
		ondrawer: () => {},
		onsearch: () => {},
		onhelp: () => {}
	});
	expect(screen.queryByRole('navigation', { name: 'ux35.breadcrumb' })).toBeNull();
	const trail = screen.getByRole('group', { name: 'ux35.breadcrumb' });
	expect(trail).toHaveTextContent(/Devices\s*\/\s*All devices/);
	expect(screen.getByText('All devices')).toHaveAttribute('aria-current', 'page');
});
