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

	test('an Undo runs with Ctrl+Z (or Cmd+Z) while it shows, and says so on its button; not in a text field', async () => {
		const undo = vi.fn();
		render(ToastHost);
		const field = document.body.appendChild(document.createElement('input'));
		try {
			show({ type: 'success', message: 'Saved', action: { label: 'Undo', onclick: undo, undo: true } });
			flushSync();
			const button = screen.getByRole('button', { name: /Undo/ });
			expect(button).toHaveAttribute('aria-keyshortcuts', 'Control+Z Meta+Z');
			expect(button).toHaveTextContent('Ctrl+Z');
			// Typing in a field keeps the field's own undo
			field.focus();
			await userEvent.keyboard('{Control>}z{/Control}');
			expect(undo).not.toHaveBeenCalled();
			// A checkbox has no undo of its own
			const box = document.body.appendChild(document.createElement('input'));
			box.type = 'checkbox';
			box.focus();
			await userEvent.keyboard('{Control>}z{/Control}');
			box.remove();
			expect(undo).toHaveBeenCalledOnce();
			expect(toasts.items).toEqual([]);
			// No toast, no undo
			await userEvent.keyboard('{Meta>}z{/Meta}');
			expect(undo).toHaveBeenCalledOnce();
		} finally {
			field.remove();
		}
	});

	test('an action that is no undo has no shortcut', () => {
		render(ToastHost);
		show({ type: 'info', message: 'Copied', action: { label: 'Open', onclick: () => {} } });
		flushSync();
		expect(screen.getByRole('button', { name: 'Open' })).not.toHaveAttribute('aria-keyshortcuts');
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
