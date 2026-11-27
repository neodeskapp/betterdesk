/**
 * The Tags and Columns buttons on Devices open a group of checkboxes, not a
 * menu: a disclosure (aria-expanded, aria-controls) without aria-haspopup,
 * which screen readers announce as a menu (BUGS L328).
 */
import { cleanup, render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { flushSync } from 'svelte';
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

/**
 * A click on a checkbox's text focuses the page's main (tabindex -1), the
 * nearest focusable ancestor, before the click reaches the checkbox. That
 * must not close the popup, or the click ticks nothing. Focus moving to
 * something outside still closes it.
 */
describe('a click on the text beside a checkbox keeps the popup open', () => {
	function inMain() {
		const main = document.createElement('main');
		main.tabIndex = -1;
		const elsewhere = document.createElement('button');
		main.append(elsewhere);
		document.body.append(main);
		return { main, elsewhere };
	}

	async function check(button: HTMLElement, main: HTMLElement, elsewhere: HTMLElement) {
		await userEvent.setup().click(button);
		expect(button.getAttribute('aria-expanded')).toBe('true');
		main.focus();
		flushSync();
		expect(document.activeElement).toBe(main);
		expect(button.getAttribute('aria-expanded'), 'focus on an ancestor closed the popup').toBe('true');
		elsewhere.focus();
		flushSync();
		expect(button.getAttribute('aria-expanded')).toBe('false');
		main.remove();
	}

	test('Columns', async () => {
		const { main, elsewhere } = inMain();
		render(ColumnChooser, {
			target: main,
			props: {
				columns: [{ id: 'platform', label: 'Platform', hideable: true }],
				onchange: () => {}
			}
		});
		await check(screen.getByRole('button', { name: /Columns/ }), main, elsewhere);
	});

	test('Tags', async () => {
		const { main, elsewhere } = inMain();
		render(TagFilter, { target: main, props: { tags: ['office'], selected: [], onchange: () => {} } });
		await check(screen.getByRole('button', { name: /devices\.tags/ }), main, elsewhere);
	});
});
