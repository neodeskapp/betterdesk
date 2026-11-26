/**
 * Pagination keeps focus on Previous and Next at the ends (WCAG 2.4.3)
 * and says which page is shown after a change (4.1.3).
 */
import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import Announcer from '../../../src/lib/ui/Announcer.svelte';
import Pagination from '../../../src/lib/ui/Pagination.svelte';
import { resetAnnouncer } from '../../../src/lib/ui/announce.svelte.ts';

beforeEach(() => vi.useFakeTimers({ shouldAdvanceTime: true }));
afterEach(() => {
	resetAnnouncer();
	vi.useRealTimers();
});

const polite = () => document.querySelector('[data-announcer="polite"]')!;

describe('Pagination focus and announcements', () => {
	test('Previous keeps focus when it reaches the first page, and does nothing there', async () => {
		const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
		const onchange = vi.fn();
		render(Pagination, { page: 2, pageCount: 3, onchange });
		const prev = screen.getByRole('button', { name: 'Previous page' });
		prev.focus();
		await user.keyboard('{Enter}');
		expect(onchange).toHaveBeenLastCalledWith(1);
		expect(prev).toHaveFocus();
		expect(prev).not.toBeDisabled();
		expect(prev).toHaveAttribute('aria-disabled', 'true');
		await user.keyboard('{Enter}');
		expect(onchange).toHaveBeenCalledTimes(1);
	});

	test('Next keeps focus on the last page', async () => {
		const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
		render(Pagination, { page: 2, pageCount: 3 });
		const next = screen.getByRole('button', { name: 'Next page' });
		next.focus();
		await user.keyboard('{Enter}');
		expect(next).toHaveFocus();
		expect(next).toHaveAttribute('aria-disabled', 'true');
		expect(screen.getByRole('button', { name: 'Page 3' })).toHaveAttribute('aria-current', 'page');
	});

	test('a page change is announced with the summary', async () => {
		const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
		render(Announcer);
		render(Pagination, { page: 1, total: 312, pageSize: 25 });
		await user.click(screen.getByRole('button', { name: 'Next page' }));
		await vi.advanceTimersByTimeAsync(200);
		expect(polite().textContent).toBe('Showing 26–50 of 312');
	});

	test('without a total, the page number is announced', async () => {
		const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
		render(Announcer);
		render(Pagination, { page: 1, pageCount: 4 });
		await user.click(screen.getByRole('button', { name: 'Page 2' }));
		await vi.advanceTimersByTimeAsync(200);
		expect(polite().textContent).toBe('Page 2 of 4');
	});
});
