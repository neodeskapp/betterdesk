/**
 * Session time limits (WCAG 2.2.1): the idle warning takes focus and gives
 * it back, is announced, and counts down in words every 10 seconds; a
 * session that ends after a fixed time (under 20 hours) is warned about
 * ahead, with time to save.
 */
import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { flushSync } from 'svelte';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import Announcer from '../../../src/lib/ui/Announcer.svelte';
import IdleWarning from '../../../src/lib/shell/IdleWarning.svelte';
import { resetAnnouncer } from '../../../src/lib/ui/announce.svelte.ts';
import { END_WARN_MS, guard, startGuard, stopGuard } from '../../../src/lib/shell/session-guard.svelte.ts';

const regions = () => ({
	polite: document.querySelector('[data-announcer="polite"]')!,
	assertive: document.querySelector('[data-announcer="assertive"]')!
});

beforeEach(() => vi.useFakeTimers({ shouldAdvanceTime: true }));
afterEach(() => {
	stopGuard();
	guard.warning = null;
	guard.deadline = null;
	guard.ending = null;
	resetAnnouncer();
	vi.useRealTimers();
	vi.unstubAllGlobals();
	document.body.replaceChildren();
});

function opener() {
	const b = document.createElement('button');
	b.textContent = 'Where I was';
	document.body.appendChild(b);
	b.focus();
	return b;
}

describe('idle warning', () => {
	test('takes focus on Stay signed in, is announced, and gives focus back when it goes', async () => {
		render(Announcer);
		render(IdleWarning);
		const was = opener();
		guard.deadline = Date.now() + 60_000;
		guard.warning = 60;
		flushSync();
		const stay = screen.getByRole('button', { name: 'auth.session_stay_signed_in' });
		expect(stay).toHaveFocus();
		await vi.advanceTimersByTimeAsync(200);
		expect(regions().assertive.textContent).toContain('auth.session_idle_warning_title');

		guard.warning = null;
		guard.deadline = null;
		flushSync();
		await vi.waitFor(() => expect(was).toHaveFocus());
	});

	test('counts down in words every 10 seconds, politely', async () => {
		render(Announcer);
		render(IdleWarning);
		opener();
		guard.deadline = Date.now() + 60_000;
		guard.warning = 60;
		flushSync();
		await vi.advanceTimersByTimeAsync(10_200);
		expect(regions().polite.textContent).toBe('50 seconds left');
		expect(screen.getByText(/in 50 seconds/)).toBeInTheDocument();
		await vi.advanceTimersByTimeAsync(10_000);
		expect(regions().polite.textContent).toBe('40 seconds left');
	});
});

describe('end of the session', () => {
	function verifyAnswer(now: number, maxAgeMs: number) {
		return () =>
			Promise.resolve(
				Response.json({
					success: true,
					session: {
						createdAt: now,
						lastActivityAt: now,
						idleTimeoutMs: 3 * 3_600_000,
						maxAgeMs,
						warnBeforeMs: 60_000,
						absoluteExpiresAt: now + maxAgeMs,
						// The server's clock at the time of the answer
						serverNow: Date.now()
					}
				})
			);
	}

	test('a session that ends after 2 hours warns before the end, with time to save', async () => {
		const now = Date.now();
		vi.stubGlobal('fetch', vi.fn().mockImplementation(verifyAnswer(now, 2 * 3_600_000)));
		startGuard({ idleTimeoutMs: 3 * 3_600_000, maxAgeMs: 2 * 3_600_000, warnBeforeMs: 60_000 });
		await vi.advanceTimersByTimeAsync(2 * 3_600_000 - END_WARN_MS - 1000);
		expect(guard.ending).toBeNull();
		await vi.advanceTimersByTimeAsync(2000);
		expect(guard.ending).toBe(now + 2 * 3_600_000);
		expect(END_WARN_MS).toBeGreaterThanOrEqual(2 * 60_000);
	});

	test('a session of 20 hours or more gets no such warning', async () => {
		const now = Date.now();
		vi.stubGlobal('fetch', vi.fn().mockImplementation(verifyAnswer(now, 24 * 3_600_000)));
		startGuard({ idleTimeoutMs: 30 * 3_600_000, maxAgeMs: 24 * 3_600_000, warnBeforeMs: 60_000 });
		await vi.advanceTimersByTimeAsync(24 * 3_600_000 - END_WARN_MS + 1000);
		expect(guard.ending).toBeNull();
	});

	test('the warning takes focus, says when the session ends, and OK gives focus back', async () => {
		const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
		render(Announcer);
		render(IdleWarning);
		const was = opener();
		guard.ending = Date.now() + 5 * 60_000;
		flushSync();
		const ok = screen.getByRole('button', { name: 'OK' });
		expect(ok).toHaveFocus();
		expect(screen.getByRole('alertdialog')).toHaveAccessibleDescription(/save your work/i);
		await user.click(ok);
		expect(screen.queryByRole('alertdialog')).toBeNull();
		await vi.waitFor(() => expect(was).toHaveFocus());
	});
});
