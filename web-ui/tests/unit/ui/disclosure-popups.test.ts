/**
 * The Tags and Columns buttons on Devices open a group of checkboxes, not a
 * menu: a disclosure (aria-expanded, aria-controls) without aria-haspopup,
 * which screen readers announce as a menu (BUGS L328).
 */
import { cleanup, render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, test } from 'vitest';
import ColumnChooser from '../../../src/lib/ui/ColumnChooser.svelte';
import TagFilter from '../../../src/routes/(console)/devices/TagFilter.svelte';

afterEach(cleanup);

async function expectDisclosure(button: HTMLElement) {
	expect(button.hasAttribute('aria-haspopup'), 'aria-haspopup announces a menu').toBe(false);
	expect(button.getAttribute('aria-expanded')).toBe('false');
	await userEvent.setup().click(button);
	expect(button.getAttribute('aria-expanded')).toBe('true');
	const popup = document.getElementById(button.getAttribute('aria-controls') ?? '');
	expect(popup?.getAttribute('role')).toBe('group');
	expect(popup?.querySelectorAll('input[type="checkbox"]').length).toBeGreaterThan(0);
}

describe('popups of checkboxes on Devices', () => {
	test('Columns', async () => {
		render(ColumnChooser, {
			columns: [
				{ id: 'id', label: 'ID' },
				{ id: 'platform', label: 'Platform', hideable: true }
			],
			onchange: () => {}
		});
		await expectDisclosure(screen.getByRole('button', { name: /Columns/ }));
	});

	test('Tags', async () => {
		render(TagFilter, { tags: ['office', 'scanner'], selected: [], onchange: () => {} });
		await expectDisclosure(screen.getByRole('button', { name: /devices\.tags/ }));
	});
});
