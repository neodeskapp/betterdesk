/**
 * While the console or a public page starts, screen readers hear that it
 * is loading instead of nothing (WCAG 4.1.3).
 */
import { render } from '@testing-library/svelte';
import { afterEach, expect, test, vi } from 'vitest';
import Announcer from '../../../src/lib/ui/Announcer.svelte';
import { resetAnnouncer } from '../../../src/lib/ui/announce.svelte.ts';

vi.mock('$app/paths', () => ({ resolve: (p: string) => '/app' + (p || '/'), asset: (p: string) => '/' + p }));
vi.mock('$app/state', () => ({ page: { url: new URL('http://localhost/devices') } }));
vi.mock('#lib/state/session.svelte.ts', () => ({
	session: { ready: false, error: null, raw: null, path: '' },
	sectionDenied: () => false,
	startSession: () => new Promise(() => {})
}));
vi.mock('#lib/public/boot.svelte.ts', () => ({
	publicPage: { ready: false, failed: false },
	startPublic: () => new Promise(() => {})
}));
// The shell itself is never drawn here (the session does not start)
vi.mock('#lib/shell/Shell.svelte', () => ({ default: () => {} }));
vi.mock('#lib/shell/NoAccess.svelte', () => ({ default: () => {} }));
vi.mock('#lib/shell/session-guard.svelte.ts', () => ({ goLogin: () => {} }));
vi.mock('#lib/state/presence.svelte.ts', () => ({ initPresence: () => {} }));
vi.mock('#lib/state/theme.svelte.ts', () => ({ initTheme: () => {} }));
vi.setConfig({ testTimeout: 60_000 });

const polite = () => document.querySelector('[data-announcer="polite"]')!;

afterEach(() => {
	resetAnnouncer();
	vi.useRealTimers();
});

test('the console start frame says it is loading, in text and to screen readers', async () => {
	vi.useFakeTimers({ shouldAdvanceTime: true });
	const Layout = (await import('../../../src/routes/(console)/+layout.svelte')).default;
	render(Announcer);
	render(Layout, { props: { children: (() => {}) as never } });
	expect(document.querySelector('.start-frame')).toHaveTextContent('Loading the console');
	await vi.advanceTimersByTimeAsync(200);
	expect(polite().textContent).toBe('Loading the console');
});

test('a public page says it is loading while its strings come', async () => {
	vi.useFakeTimers({ shouldAdvanceTime: true });
	const Layout = (await import('../../../src/routes/(public)/+layout.svelte')).default;
	render(Announcer);
	render(Layout, { props: { children: (() => {}) as never } });
	await vi.advanceTimersByTimeAsync(600);
	expect(polite().textContent).toBe('Loading…');
});
