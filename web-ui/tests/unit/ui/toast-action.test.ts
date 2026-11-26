import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { flushSync } from 'svelte';
import { afterEach, describe, expect, test, vi } from 'vitest';
import ToastHost from '../../../src/lib/ui/ToastHost.svelte';
import { dismissAll, show, toasts } from '../../../src/lib/ui/toasts.svelte.ts';

afterEach(() => dismissAll());

describe('a toast with an action', () => {
	test('shows the action as a button that runs it and closes the toast', async () => {
		const undo = vi.fn();
		render(ToastHost);
		show({ type: 'success', message: 'Saved: Delete devices off', action: { label: 'Undo', onclick: undo } });
		flushSync();
		await userEvent.click(screen.getByRole('button', { name: 'Undo' }));
		expect(undo).toHaveBeenCalledOnce();
		expect(toasts.items).toEqual([]);
	});

	test('stays longer than a plain toast so there is time to undo', () => {
		vi.useFakeTimers();
		try {
			show({ type: 'success', message: 'Saved', action: { label: 'Undo', onclick: () => {} } });
			vi.advanceTimersByTime(6000);
			expect(toasts.items).toHaveLength(1);
			vi.advanceTimersByTime(10000);
			expect(toasts.items).toHaveLength(0);
		} finally {
			vi.useRealTimers();
		}
	});
});
