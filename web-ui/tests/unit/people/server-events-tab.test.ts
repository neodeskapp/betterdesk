import { render, screen, waitFor } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { beforeAll, describe, expect, test, vi } from 'vitest';
import { setStrings } from '../../../src/lib/i18n/i18n.svelte.ts';

const calls: { limit: number; offset?: number; action?: string }[] = [];

vi.mock('../../../src/lib/api/audit.ts', () => ({
	listServerEvents: vi.fn(async (q: { limit: number; offset?: number; action?: string }) => {
		calls.push(q);
		const events = Array.from({ length: Math.min(q.limit, 25) }, (_, i) => ({
			timestamp: '2026-11-22T09:00:00Z',
			created_at: '',
			action: q.action || 'auth_login_failed',
			actor: 'alice',
			user: '',
			target: `t${(q.offset ?? 0) + i}`,
			resource: '',
			details: undefined
		}));
		// The server counts the events of the filter (L91)
		return { events, total: q.action ? 60 : 200 };
	})
}));

const { default: EventsTab } = await import('../../../src/routes/(console)/security-audit/EventsTab.svelte');

beforeAll(() => {
	setStrings('en', 'ltr', {
		security_audit: {
			all_events: 'All events',
			event_login_failed: 'Failed sign-ins',
			show: 'Show',
			pages: 'Pages',
			tab_server_events: 'Server events'
		}
	} as never);
});

describe('Server events tab', () => {
	test('pages a filtered list by the total the server counts, with the offset of the page (L126)', async () => {
		render(EventsTab);
		const select = await screen.findByRole('combobox');
		await userEvent.selectOptions(select, 'login_failed');
		// 60 failed sign-ins are three pages of 25
		expect(await screen.findByRole('button', { name: 'Page 3' })).toBeTruthy();
		expect(screen.queryByRole('button', { name: 'Page 4' })).toBeNull();
		await userEvent.click(screen.getByRole('button', { name: 'Page 2' }));
		await waitFor(() => expect(calls.at(-1)).toEqual({ limit: 25, offset: 25, action: 'login_failed' }));
	});
});
