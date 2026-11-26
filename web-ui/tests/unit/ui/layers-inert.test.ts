/**
 * The page behind an open dialog or sheet is inert, so screen readers in
 * browse mode and touch screen readers can't leave it, and it comes back
 * when the layer closes. Toasts stay outside the inert part so they are
 * still announced.
 */
import { render, screen, cleanup } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, test } from 'vitest';
import Harness from './OverlaysHarness.svelte';
import ToastHost from '../../../src/lib/ui/ToastHost.svelte';
import { dismissAll } from '../../../src/lib/ui/toasts.svelte.ts';

afterEach(() => {
	cleanup();
	dismissAll();
});

/** The direct child of <body> that holds the element */
function bodyChild(el: Element): Element {
	let e: Element = el;
	while (e.parentElement && e.parentElement !== document.body) e = e.parentElement;
	return e;
}

describe('page behind layers', () => {
	test('a dialog makes the page inert, and closing it restores the page', async () => {
		const user = userEvent.setup();
		render(Harness, { scene: 'dialog' });
		const opener = screen.getByRole('button', { name: 'Open' });
		const page = bodyChild(opener);
		await user.click(opener);
		const dialog = screen.getByRole('dialog');
		expect(page).toHaveAttribute('inert');
		expect(bodyChild(dialog)).not.toHaveAttribute('inert');
		await user.keyboard('{Escape}');
		expect(page).not.toHaveAttribute('inert');
		expect(document.activeElement).toBe(opener);
	});

	test('a dialog on a sheet makes the sheet inert too; closing it gives the sheet back', async () => {
		const user = userEvent.setup();
		// The harness asks only when someone listens for the answer
		render(Harness, { scene: 'sheet', onanswer: () => undefined });
		const opener = screen.getByRole('button', { name: 'Edit target' });
		await user.click(opener);
		const sheet = screen.getByRole('dialog');
		await user.click(screen.getByRole('button', { name: 'Delete…' }));
		const confirm = screen.getByRole('dialog', { name: 'Delete target?' });
		expect(bodyChild(sheet)).toHaveAttribute('inert');
		expect(bodyChild(confirm)).not.toHaveAttribute('inert');
		expect(bodyChild(opener)).toHaveAttribute('inert');
		await user.keyboard('{Escape}');
		expect(bodyChild(sheet)).not.toHaveAttribute('inert');
		expect(bodyChild(opener)).toHaveAttribute('inert');
		await user.keyboard('{Escape}');
		expect(bodyChild(opener)).not.toHaveAttribute('inert');
	});

	test('toasts stay announced while a dialog is open', async () => {
		const user = userEvent.setup();
		render(ToastHost);
		render(Harness, { scene: 'dialog' });
		await user.click(screen.getByRole('button', { name: 'Open' }));
		const region = screen.getByRole('status', { hidden: true });
		expect(region.closest('[inert]')).toBeNull();
	});
});
