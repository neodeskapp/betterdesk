/**
 * Status messages reach screen readers (WCAG 4.1.3): one polite and one
 * assertive live region that exist before any message, outside the part
 * of the page a dialog makes inert. Toasts, loading and error states and
 * tables announce through them.
 */
import { render, screen, cleanup, within } from '@testing-library/svelte';
import { createRawSnippet, flushSync, tick } from 'svelte';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import Announcer from '../../../src/lib/ui/Announcer.svelte';
import ToastHost from '../../../src/lib/ui/ToastHost.svelte';
import States from '../../../src/lib/ui/States.svelte';
import Table from '../../../src/lib/ui/Table.svelte';
import { announce, resetAnnouncer } from '../../../src/lib/ui/announce.svelte.ts';
import type { Column } from '../../../src/lib/ui/table.ts';
import { toast, dismissAll } from '../../../src/lib/ui/toasts.svelte.ts';

beforeEach(() => vi.useFakeTimers());
afterEach(() => {
	cleanup();
	dismissAll();
	resetAnnouncer();
	vi.useRealTimers();
});

/** Let the announcer clear and refill its region */
async function settle() {
	await vi.advanceTimersByTimeAsync(200);
	flushSync();
}

function regions() {
	return {
		polite: document.querySelector<HTMLElement>('[data-announcer="polite"]')!,
		assertive: document.querySelector<HTMLElement>('[data-announcer="assertive"]')!
	};
}

describe('announcer', () => {
	test('both regions exist empty before any message, outside what a dialog makes inert', () => {
		render(Announcer);
		const { polite, assertive } = regions();
		expect(polite).toHaveAttribute('aria-live', 'polite');
		expect(assertive).toHaveAttribute('aria-live', 'assertive');
		expect(polite.textContent).toBe('');
		expect(polite.closest('[data-layer-live]')).not.toBeNull();
	});

	test('the same message twice is said twice: the region empties and fills again', async () => {
		render(Announcer);
		announce('Copied to clipboard');
		await settle();
		const { polite } = regions();
		expect(polite.textContent).toBe('Copied to clipboard');
		announce('Copied to clipboard');
		flushSync();
		expect(polite.textContent).toBe('');
		await settle();
		expect(polite.textContent).toBe('Copied to clipboard');
	});
});

describe('toasts', () => {
	test('a toast is announced, and its close button is not read with it', async () => {
		render(Announcer);
		render(ToastHost);
		toast.success('Settings saved');
		await settle();
		expect(regions().polite.textContent).toBe('Settings saved');
		for (const r of Object.values(regions())) expect(within(r).queryByRole('button')).toBeNull();
		// The close button says which message it closes
		expect(screen.getByRole('button', { name: 'Close: Settings saved' })).toBeInTheDocument();
	});

	test('an error is announced right away and stays until it is closed', async () => {
		render(Announcer);
		render(ToastHost);
		toast.error('Could not save the settings.');
		await settle();
		expect(regions().assertive.textContent).toContain('Could not save the settings.');
		await vi.advanceTimersByTimeAsync(120_000);
		flushSync();
		expect(screen.getByRole('button', { name: /Close: Could not save/ })).toBeInTheDocument();
	});

	test('a warning says it is a warning, not only by colour', async () => {
		render(Announcer);
		render(ToastHost);
		toast.warning('The relay is not reachable.');
		await settle();
		expect(regions().polite.textContent).toMatch(/^Warning: The relay is not reachable\.$/);
		expect(document.querySelector('.toast.warning')?.textContent).toContain('Warning:');
	});
});

describe('loading and error states', () => {
	test('loading is announced politely, an error assertively', async () => {
		render(Announcer);
		const { rerender } = render(States, { kind: 'loading', message: 'Loading devices' });
		await settle();
		expect(regions().polite.textContent).toBe('Loading devices');
		await rerender({ kind: 'error', message: 'The devices could not be loaded.' });
		await settle();
		expect(regions().assertive.textContent).toBe('The devices could not be loaded.');
	});
});

type Row = { name: string };
const nameCell = createRawSnippet<[Row]>((d) => ({ render: () => `<span>${d().name}</span>` }));

describe('tables', () => {
	test('the end of loading is announced with the number of rows', async () => {
		render(Announcer);
		const columns: Column<Row>[] = [{ id: 'name', label: 'Name', primary: true, cell: nameCell }];
		const { rerender } = render(Table<Row>, {
			label: 'Devices',
			columns,
			rows: [],
			loading: true,
			key: (r: Row) => r.name
		});
		await settle();
		await rerender({
			label: 'Devices',
			columns,
			rows: [{ name: 'a' }, { name: 'b' }, { name: 'c' }],
			loading: false,
			key: (r: Row) => r.name
		});
		await tick();
		await settle();
		expect(regions().polite.textContent).toBe('Devices: 3 rows');
	});

	test('an empty result says the empty sentence', async () => {
		render(Announcer);
		const columns: Column<Row>[] = [{ id: 'name', label: 'Name', primary: true, cell: nameCell }];
		const { rerender } = render(Table<Row>, {
			label: 'Devices',
			columns,
			rows: [],
			loading: true,
			key: (r: Row) => r.name,
			emptyText: 'No devices match the search.'
		});
		await settle();
		await rerender({
			label: 'Devices',
			columns,
			rows: [],
			loading: false,
			key: (r: Row) => r.name,
			emptyText: 'No devices match the search.'
		});
		await settle();
		expect(regions().polite.textContent).toBe('No devices match the search.');
	});
});
