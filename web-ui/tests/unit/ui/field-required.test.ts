/**
 * Required fields say so in words next to the label, for everyone, not
 * only to screen readers (WCAG 3.3.2). The control carries `required`,
 * which screen readers read, so the visible word is hidden from them.
 */
import { render, screen, cleanup } from '@testing-library/svelte';
import { tick } from 'svelte';
import { afterEach, describe, expect, test } from 'vitest';
import Harness from './RequiredHarness.svelte';

afterEach(cleanup);

describe('required fields', () => {
	test('Field required: the control is required and the label shows it', async () => {
		render(Harness, { mode: 'prop' });
		await tick();
		const input = screen.getByRole('textbox', { name: 'Name' });
		expect(input).toBeRequired();
		const mark = document.querySelector('.form-label .form-required');
		expect(mark?.textContent).toBe('(required)');
		expect(mark).toHaveAttribute('aria-hidden', 'true');
	});

	test('a control made required by the page shows the mark too', async () => {
		render(Harness, { mode: 'attribute' });
		await tick();
		expect(screen.getByRole('textbox', { name: 'Email' })).toBeRequired();
		expect(document.querySelector('.form-label .form-required')?.textContent).toBe('(required)');
	});

	test('an optional field has no mark', async () => {
		render(Harness, { mode: 'none' });
		await tick();
		expect(document.querySelector('.form-required')).toBeNull();
	});
});
