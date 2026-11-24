/**
 * Connect by ID on the remote dashboard: on a computer the ID goes to the
 * browser viewer, to NeoDesk by its own scheme or to stock RustDesk (L423);
 * a phone has only the RustDesk app, and Enter opens it.
 */
import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { describe, expect, test } from 'vitest';
import QuickConnect from '../../../src/lib/remote/QuickConnect.svelte';

describe('QuickConnect', () => {
	test('a computer: Connect, Open in NeoDesk and Open in RustDesk', async () => {
		const connected: string[] = [];
		const apps: string[] = [];
		render(QuickConnect, {
			onconnect: (id: string) => connected.push(id),
			onapp: (id: string, app: string) => apps.push(`${app} ${id}`)
		});
		const user = userEvent.setup();
		await user.type(screen.getByRole('textbox'), '123456789');
		await user.click(screen.getByRole('button', { name: 'Connect' }));
		await user.click(screen.getByRole('button', { name: 'Open in NeoDesk' }));
		await user.click(screen.getByRole('button', { name: 'Open in RustDesk' }));
		expect(connected).toEqual(['123456789']);
		expect(apps).toEqual(['neodesk 123456789', 'rustdesk 123456789']);
	});

	test('a phone: the RustDesk app only, and Enter opens it', async () => {
		const apps: string[] = [];
		render(QuickConnect, {
			phone: true,
			onconnect: () => {},
			onapp: (id: string, app: string) => apps.push(`${app} ${id}`)
		});
		const user = userEvent.setup();
		expect(screen.queryByRole('button', { name: 'Open in NeoDesk' })).toBeNull();
		await user.type(screen.getByRole('textbox'), '123456789{Enter}');
		expect(apps).toEqual(['rustdesk 123456789']);
	});
});
