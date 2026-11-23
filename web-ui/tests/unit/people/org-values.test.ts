/**
 * Organization forms refuse what the server would refuse, next to the
 * field, and send nothing (BUGS L398): a logo address that is no http(s)
 * address, a new account's username with a space, an invitation address
 * that is no email address. A device ID the server doesn't know comes
 * back as 404 and shows at the device field.
 */
import { cleanup, render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import OrgDialog from '../../../src/lib/people/OrgDialog.svelte';
import AddMemberDialog from '../../../src/routes/(console)/organizations/[id]/AddMemberDialog.svelte';
import AssignDeviceDialog from '../../../src/routes/(console)/organizations/[id]/AssignDeviceDialog.svelte';
import InviteDialog from '../../../src/routes/(console)/organizations/[id]/InviteDialog.svelte';
import { dismissAll } from '../../../src/lib/ui/toasts.svelte.ts';

let writes: string[] = [];
let reply: { status: number; body: unknown } = { status: 200, body: { success: true } };

beforeEach(() => {
	writes = [];
	reply = { status: 200, body: { success: true } };
	vi.stubGlobal('fetch', async (input: string, init: RequestInit = {}) => {
		const method = init.method ?? 'GET';
		const path = new URL(input, location.origin).pathname;
		if (method === 'GET')
			return new Response('{"users":[]}', { status: 200, headers: { 'Content-Type': 'application/json' } });
		writes.push(`${method} ${path}`);
		return new Response(JSON.stringify(reply.body), {
			status: reply.status,
			headers: { 'Content-Type': 'application/json' }
		});
	});
});

afterEach(() => {
	cleanup();
	dismissAll();
	vi.unstubAllGlobals();
});

const noop = () => {};

describe('organization values', () => {
	test('a logo address that is no http(s) address is refused at the field', async () => {
		const user = userEvent.setup();
		render(OrgDialog, { org: null, onclose: noop, onsaved: noop });
		await user.type(screen.getByLabelText('organizations.name'), 'Acme');
		const logo = screen.getByLabelText('organizations.logo_url');
		await user.type(logo, 'javascript:alert(1)');
		await user.click(screen.getByRole('button', { name: 'organizations.create' }));
		await vi.waitFor(() => expect(logo).toHaveAccessibleDescription(/organizations.logo_url_invalid/));
		expect(writes).toEqual([]);
	});

	test("a new account's username with a space is refused at the field", async () => {
		const user = userEvent.setup();
		render(AddMemberDialog, { orgId: 'org-1', onclose: noop, onsaved: noop });
		const name = screen.getByLabelText('organizations.username');
		await user.type(name, 'a b');
		await user.type(screen.getByLabelText('organizations.password'), 'eight888');
		await user.click(screen.getByRole('button', { name: 'organizations.add_user' }));
		await vi.waitFor(() => expect(name).toHaveAccessibleDescription(/organizations.username_invalid/));
		expect(writes).toEqual([]);
	});

	test('an invitation address that is no email address is refused at the field', async () => {
		const user = userEvent.setup();
		render(InviteDialog, { orgId: 'org-1', onclose: noop, onsaved: noop });
		const email = screen.getByLabelText('organizations.email');
		await user.type(email, 'not-mail');
		await user.click(screen.getByRole('button', { name: 'organizations.create_invitation' }));
		await vi.waitFor(() => expect(email).toHaveAccessibleDescription(/organizations.email_invalid/));
		expect(writes).toEqual([]);
	});

	test("a device ID the server doesn't know shows at the device field", async () => {
		const user = userEvent.setup();
		reply = {
			status: 404,
			body: { success: false, error: 'device not found: no device has this ID on the server' }
		};
		render(AssignDeviceDialog, { orgId: 'org-1', onclose: noop, onsaved: noop });
		const id = screen.getByLabelText('organizations.device_id');
		await user.type(id, 'nope-device');
		await user.click(screen.getByRole('button', { name: 'organizations.assign_device' }));
		await vi.waitFor(() => expect(id).toHaveAccessibleDescription(/organizations.device_not_found/));
		expect(writes).toEqual(['POST /api/panel/org/org-1/devices']);
	});
});
