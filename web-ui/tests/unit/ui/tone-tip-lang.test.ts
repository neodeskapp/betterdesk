/**
 * Small display parts:
 * - Status and Tag explanations reach keyboard and touch users: a tooltip
 *   that opens on focus and hover and closes with Escape (WCAG 1.3.1,
 *   1.4.13), not only a title.
 * - A notice says its tone in a word for screen readers and an icon for
 *   the eye, not only by the colour of its rule (1.4.1).
 * - A language name is marked with its language (3.1.2).
 */
import { fireEvent, render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { describe, expect, test, vi } from 'vitest';
import LangFlag from '../../../src/lib/ui/LangFlag.svelte';
import Notice from '../../../src/lib/ui/Notice.svelte';
import Status from '../../../src/lib/ui/Status.svelte';
import Tag from '../../../src/lib/ui/Tag.svelte';

vi.mock('$app/paths', () => ({ resolve: (p: string) => '/app' + (p || '/'), asset: (p: string) => '/' + p }));

describe('explanations on focus', () => {
	test('a status with an explanation: focus shows it, Escape hides it, screen readers get it', async () => {
		render(Status, {
			tone: 'warning',
			label: 'No signal',
			title: 'The device has not reported for 10 minutes.'
		});
		expect(document.querySelector('[title]')).toBeNull();
		// The label takes focus and carries the explanation
		const status = document.querySelector('.status .tip') as HTMLElement;
		expect(status.tabIndex).toBe(0);
		expect(status).toHaveAccessibleDescription('The device has not reported for 10 minutes.');
		const tip = screen.getByRole('tooltip', { hidden: true });
		expect(tip).not.toBeVisible();
		status.focus();
		await fireEvent.focus(status);
		expect(tip).toBeVisible();
		await userEvent.keyboard('{Escape}');
		expect(tip).not.toBeVisible();
		await fireEvent.blur(status);
		await fireEvent.pointerEnter(status);
		expect(tip).toBeVisible();
	});

	test('a status without one stays plain text, out of the tab order', () => {
		render(Status, { tone: 'good', label: 'Online' });
		const status = screen.getByText('Online');
		expect(status.closest('[tabindex]')).toBeNull();
		expect(screen.queryByRole('tooltip', { hidden: true })).toBeNull();
	});

	test('a long tag shows its full text on focus', async () => {
		const long = 'a tag that is much longer than the column';
		render(Tag, { label: long });
		expect(document.querySelector('[title]')).toBeNull();
		const tag = document.querySelector('.tag .tip') as HTMLElement;
		tag.focus();
		await fireEvent.focus(tag);
		expect(screen.getByRole('tooltip')).toHaveTextContent(long);
	});
});

describe('notice tone', () => {
	test.each([
		['warning', 'common.warning'],
		['danger', 'common.error'],
		['ok', 'common.success']
	] as const)('a %s notice starts with its tone in words and has an icon', (tone, word) => {
		const { container } = render(Notice, { tone, message: 'Check the settings.' });
		const notice = container.querySelector('.notice')!;
		expect(notice.querySelector('.sr-only')).toHaveTextContent(word);
		expect(notice.querySelector('.material-icons')).toHaveAttribute('aria-hidden', 'true');
	});

	test('an info notice has the icon and no extra word', () => {
		const { container } = render(Notice, { message: 'Good to know.' });
		const notice = container.querySelector('.notice')!;
		expect(notice.querySelector('.material-icons')).not.toBeNull();
		expect(notice.querySelector('.sr-only')).toBeNull();
	});
});

describe('LangFlag', () => {
	test('the language name is marked with its language', () => {
		render(LangFlag, { code: 'de', name: 'Deutsch' });
		expect(screen.getByText('Deutsch')).toHaveAttribute('lang', 'de');
	});
});
