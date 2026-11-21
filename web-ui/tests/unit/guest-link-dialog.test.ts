/**
 * The guest link dialog offers View only only when every device can be
 * kept to watching: the server refuses a view-only link for a RustDesk
 * device (BUGS RV209, A94), so the dialog says the link gives full control
 * and sends view_only false.
 */
import { cleanup, render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, test, vi } from 'vitest';
import GuestLinkDialog from '../../src/lib/devices/dialogs/GuestLinkDialog.svelte';
import type { GuestLinkInput } from '../../src/lib/api/guest.ts';
import { watchable } from '../../src/lib/devices/model.ts';

afterEach(() => cleanup());

describe('guest link dialog', () => {
	test.each([
		[true, true],
		[false, false]
	])('with view only offered %s the link asks view_only %s', async (offered, sent) => {
		const create = vi.fn(async (_input: GuestLinkInput) => ({ url: 'http://x/g', expiresAt: '' }));
		render(GuestLinkDialog, { ids: ['a'], viewOnly: offered, create, settle: () => {} });
		expect(!!screen.queryByRole('checkbox')).toBe(offered);
		expect(!!screen.queryByText(/limited to watching/)).toBe(!offered);
		await userEvent.setup().click(screen.getByRole('button', { name: 'guest_access.create' }));
		await vi.waitFor(() => expect(create).toHaveBeenCalled());
		expect(create.mock.calls[0]![0].view_only).toBe(sent);
	});

	test('mesh and CDAP devices can be kept to watching, RustDesk devices cannot', () => {
		expect(watchable({ id: 'a', device_type: 'mesh_agent' })).toBe(true);
		expect(watchable({ id: 'a', device_type: 'os_agent' })).toBe(true);
		expect(watchable({ id: 'a', device_type: 'iot' })).toBe(true);
		expect(watchable({ id: 'a', device_type: 'rustdesk', cdap_connected: true })).toBe(true);
		expect(watchable({ id: 'a', device_type: 'rustdesk' })).toBe(false);
		expect(watchable({ id: 'a' })).toBe(false);
	});
});
