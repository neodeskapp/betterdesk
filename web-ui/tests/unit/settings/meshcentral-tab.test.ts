/**
 * Settings › MeshCentral: the .msh file of the install part is for the
 * group picked there (settings-pass-2.md rule B: the choice is on the
 * page, not a sentence that sends the admin to another row's menu).
 */
import { cleanup, render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';

vi.mock('#lib/api/settings.ts', async (orig) => ({
	...(await orig<typeof import('../../../src/lib/api/settings.ts')>()),
	getMeshStatus: vi.fn(async () => ({ enabled: true, agents_online: 0, cert_present: true })),
	getMeshGroups: vi.fn(async () => [
		{ id: 'default', name: 'Default Mesh', mesh_id: 'AAA111' },
		{ id: 'branch', name: 'Branch office', mesh_id: 'BBB222' }
	]),
	getRecordings: vi.fn(async () => [])
}));

import * as api from '../../../src/lib/api/settings.ts';
import MeshCentralTab from '../../../src/lib/settings/tabs/MeshCentralTab.svelte';
import { session } from '../../../src/lib/state/session.svelte.ts';

beforeEach(() => {
	session.isSuperAdmin = true;
});

afterEach(() => {
	cleanup();
	session.isSuperAdmin = false;
});

const download = () => screen.getByRole('link', { name: /mesh\.download_msh/ });

test('the install part downloads the file of the group picked beside it', async () => {
	const user = userEvent.setup();
	render(MeshCentralTab);
	const group = await screen.findByRole('combobox', { name: 'Group' });
	// The default group first, as before
	expect(group).toHaveValue('default');
	expect(download().getAttribute('href')).toContain('mesh_id=AAA111');
	await user.selectOptions(group, 'branch');
	expect(download().getAttribute('href')).toContain('mesh_id=BBB222');
	expect(screen.getByLabelText('settings.general.mesh_msh_name')).toHaveAccessibleDescription(
		'MeshAgent shows this name for the server. It only goes into the downloaded file and is not saved.'
	);
});

test('with one group there is nothing to pick', async () => {
	vi.mocked(api.getMeshGroups).mockResolvedValueOnce([
		{ id: 'default', name: 'Default Mesh', mesh_id: 'AAA111' }
	]);
	render(MeshCentralTab);
	await vi.waitFor(() => expect(download().getAttribute('href')).toContain('mesh_id=AAA111'));
	expect(screen.queryByRole('combobox', { name: 'Group' })).toBeNull();
});
