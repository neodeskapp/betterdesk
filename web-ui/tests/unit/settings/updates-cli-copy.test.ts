/**
 * Settings › Updates on a Linux host install: an update from the console
 * replaces the server only, so when the command line's copy is older the
 * tab says to run `sudo neodesk-setup --update` once to update it too.
 * Nothing is said while the copy is current (or there is none).
 */
import { cleanup, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

const binary = vi.hoisted(() => ({
	status: { stale: false, dockerMode: false, detail: '', cliOutdated: false }
}));

vi.mock('#lib/api/settings-system.ts', async (orig) => ({
	...(await orig<typeof import('../../../src/lib/api/settings-system.ts')>()),
	getServerBinaryStatus: vi.fn(async () => binary.status)
}));

import UpdatesTab from '../../../src/lib/settings/tabs/UpdatesTab.svelte';
import { session } from '../../../src/lib/state/session.svelte.ts';

beforeEach(() => {
	session.isSuperAdmin = true;
	vi.stubGlobal(
		'fetch',
		vi.fn(
			async () =>
				new Response('{"success":false}', { status: 404, headers: { 'Content-Type': 'application/json' } })
		)
	);
});

afterEach(() => {
	cleanup();
	vi.unstubAllGlobals();
	session.isSuperAdmin = false;
});

describe('Updates tab and the command line', () => {
	test('an older copy of the command line: run neodesk-setup --update once', async () => {
		binary.status = { stale: false, dockerMode: false, detail: '', cliOutdated: true };
		render(UpdatesTab);
		await waitFor(() => expect(screen.getByText('updates.cli_outdated_title')).toBeInTheDocument());
		expect(screen.getByText('sudo neodesk-setup --update')).toBeInTheDocument();
	});

	test('a current copy says nothing', async () => {
		binary.status = { stale: false, dockerMode: false, detail: '', cliOutdated: false };
		render(UpdatesTab);
		await new Promise((r) => setTimeout(r, 50));
		expect(screen.queryByText('updates.cli_outdated_title')).toBeNull();
	});
});
