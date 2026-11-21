/**
 * The capacity test page after Stop: a stopped run stores no result, so
 * the status brings the earlier one back; the page says the test stopped.
 */
import { cleanup, render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

const earlier = {
	testedAt: '2026-11-20T10:00:00Z',
	maxConnections: 900,
	tier: 'iron',
	valid: true,
	aborted: false,
	mode: 'measured'
};
const state = vi.hoisted(() => ({ running: true }));
vi.mock('#lib/api/serverAttestation.ts', async (orig) => ({
	...(await orig<typeof import('../../../src/lib/api/serverAttestation.ts')>()),
	getStatus: vi.fn(async () => ({
		running: state.running,
		phase: state.running ? 'ramp-up' : 'idle',
		progress: { phase: state.running ? 'ramp-up' : 'idle', connections: 10, message: '', result: null },
		lastResult: earlier
	})),
	stopTest: vi.fn(async () => {
		state.running = false;
	}),
	getBadge: vi.fn(async () => ({ config: { badgeTitle: '', tierLabels: {} }, presentation: null }))
}));
const toastMock = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn(), warning: vi.fn(), info: vi.fn() }));
vi.mock('#lib/ui/toasts.svelte.ts', async (orig) => ({
	...(await orig<typeof import('../../../src/lib/ui/toasts.svelte.ts')>()),
	toast: toastMock
}));

import Page from '../../../src/routes/(console)/server-attestation/+page.svelte';
import { session } from '../../../src/lib/state/session.svelte.ts';

beforeEach(() => {
	state.running = true;
	session.isSuperAdmin = true;
	for (const fn of Object.values(toastMock)) fn.mockReset();
});

afterEach(() => {
	cleanup();
	session.isSuperAdmin = false;
});

describe('capacity test page', () => {
	test('after Stop it says the test stopped, not that it finished with the earlier numbers', async () => {
		const user = userEvent.setup();
		render(Page);
		await user.click(await screen.findByRole('button', { name: 'server_attestation.abort' }));
		await vi.waitFor(() => expect(toastMock.info).toHaveBeenCalled(), { timeout: 5000 });
		expect(toastMock.success).not.toHaveBeenCalled();
		expect(toastMock.info).toHaveBeenCalledWith(
			'Capacity test stopped. The result shown is from the test before.'
		);
	}, 15_000);
});
