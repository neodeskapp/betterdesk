/**
 * The request sheet's approve form for a LAN request whose ID is already a
 * device (BUGS L55): it says so, asks a second time naming the device, and
 * sends the confirmation only after a yes; a 409 for an ID that became a
 * device meanwhile asks the same; a device outside the approver's scope
 * cannot be approved.
 */
import { cleanup, render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import RequestSheet from '../../../src/routes/(console)/registrations/RequestSheet.svelte';
import { fromLan } from '../../../src/lib/clients/registrations.ts';
import type { ExistingDevice } from '../../../src/lib/api/registrations.ts';
import { session } from '../../../src/lib/state/session.svelte.ts';
import { dialogRequests } from '../../../src/lib/ui/dialogs.svelte.ts';

let bodies: unknown[] = [];
let answers: { status: number; body: unknown }[] = [];

beforeEach(() => {
	bodies = [];
	answers = [];
	session.isSuperAdmin = true;
	session.permissions = [];
	vi.stubGlobal('fetch', async (_input: string, init: RequestInit = {}) => {
		if (init.method === 'PUT' && typeof init.body === 'string') bodies.push(JSON.parse(init.body));
		const answer = answers.shift() ?? { status: 200, body: { success: true } };
		return new Response(JSON.stringify(answer.body), {
			status: answer.status,
			headers: { 'Content-Type': 'application/json' }
		});
	});
});

afterEach(() => {
	cleanup();
	for (const d of dialogRequests.items) if (d.kind === 'confirm') d.settle(false);
	vi.unstubAllGlobals();
	session.isSuperAdmin = false;
});

const row = (existing: ExistingDevice | null) =>
	fromLan({
		id: 3,
		device_id: '900100004',
		hostname: 'new-pc',
		platform: 'Windows',
		version: '',
		ip_address: '10.0.0.4',
		ip: '',
		status: 'pending',
		approved_by: '',
		approved_at: '',
		rejected_reason: '',
		created_at: '2026-11-21T10:00:00Z',
		updated_at: '',
		existing_device: existing
	});

const device: ExistingDevice = { id: '900100004', name: 'Front desk', deleted: false, in_scope: true };

function open(existing: ExistingDevice | null, onapproved = vi.fn(), richApprove = true) {
	render(RequestSheet, {
		row: row(existing),
		decidedHere: false,
		folders: [{ id: 4, name: 'Office' } as never],
		groups: [],
		tags: [],
		tagPicker: false,
		richApprove,
		ready: true,
		onclose: () => {},
		onapproved,
		onreject: () => {},
		onopen: () => {},
		onallow: () => {},
		ondelete: () => {}
	});
	return onapproved;
}

async function question() {
	await vi.waitFor(() => expect(dialogRequests.items.some((d) => d.kind === 'confirm')).toBe(true));
	const d = dialogRequests.items.find((x) => x.kind === 'confirm');
	if (!d || d.kind !== 'confirm') throw new Error('no question');
	return d;
}

describe('approving a request whose ID is already a device', () => {
	test('the sheet says so, and Approve sends nothing until the second question gets a yes', async () => {
		const user = userEvent.setup();
		const onapproved = open(device);
		expect(document.querySelector('.existing')?.textContent).toContain('registrations.existing_hint');
		await user.click(screen.getByRole('button', { name: 'registrations.approve_btn' }));
		const d = await question();
		expect(d.options.title).toBe('registrations.existing_confirm_title');
		expect(bodies).toEqual([]);
		d.settle(true);
		await vi.waitFor(() => expect(onapproved).toHaveBeenCalled());
		expect(bodies).toEqual([
			{ display_name: 'new-pc', tags: '', folder_id: 0, group_guids: [], confirm_existing_device: true }
		]);
	});

	test('a no to the second question approves nothing', async () => {
		const user = userEvent.setup();
		const onapproved = open(device);
		await user.click(screen.getByRole('button', { name: 'registrations.approve_btn' }));
		(await question()).settle(false);
		await new Promise((r) => setTimeout(r, 20));
		expect(bodies).toEqual([]);
		expect(onapproved).not.toHaveBeenCalled();
	});

	test('an ID that became a device meanwhile: the server refuses, the sheet asks, then confirms', async () => {
		const user = userEvent.setup();
		answers = [
			{
				status: 409,
				body: { success: false, error: 'refused', code: 'existing_device', existing_device: device }
			}
		];
		const onapproved = open(null);
		await user.click(screen.getByRole('button', { name: 'registrations.approve_btn' }));
		const d = await question();
		expect(bodies).toHaveLength(1);
		expect(bodies[0]).not.toHaveProperty('confirm_existing_device');
		d.settle(true);
		await vi.waitFor(() => expect(onapproved).toHaveBeenCalled());
		expect(bodies[1]).toMatchObject({ confirm_existing_device: true });
	});

	test('a request without an existing device is approved without a second question', async () => {
		const user = userEvent.setup();
		const onapproved = open(null);
		expect(document.querySelector('.existing')).toBeNull();
		await user.click(screen.getByRole('button', { name: 'registrations.approve_btn' }));
		await vi.waitFor(() => expect(onapproved).toHaveBeenCalled());
		expect(dialogRequests.items).toEqual([]);
		expect(bodies).toEqual([{ display_name: 'new-pc', tags: '', folder_id: 0, group_guids: [] }]);
	});

	test("a device outside the approver's scope: no Approve, only the reason", () => {
		open({ id: '900100004', name: '', deleted: false, in_scope: false });
		expect(document.querySelector('.existing')?.textContent).toContain('registrations.existing_hidden');
		expect(screen.queryByRole('button', { name: 'registrations.approve_btn' })).toBeNull();
		expect(screen.getByRole('button', { name: /registrations.reject_btn/ })).toBeInTheDocument();
	});
});

describe('Full approval form (Settings > Sign-in, BUGS L381)', () => {
	test('on: name, folder and tags are asked', () => {
		open(null);
		expect(screen.getByRole('textbox', { name: 'registrations.field_name' })).toBeInTheDocument();
		expect(screen.getByRole('combobox', { name: 'registrations.field_folder' })).toBeInTheDocument();
		expect(screen.getByRole('textbox', { name: 'registrations.field_tags' })).toBeInTheDocument();
	});

	test('off: Approve alone, with the device name it asked with and nothing else', async () => {
		const user = userEvent.setup();
		const onapproved = open(null, vi.fn(), false);
		expect(screen.queryByRole('textbox', { name: 'registrations.field_name' })).toBeNull();
		expect(screen.queryByRole('combobox', { name: 'registrations.field_folder' })).toBeNull();
		expect(screen.queryByRole('textbox', { name: 'registrations.field_tags' })).toBeNull();
		await user.click(screen.getByRole('button', { name: 'registrations.approve_btn' }));
		await vi.waitFor(() => expect(onapproved).toHaveBeenCalledWith('new-pc'));
		expect(bodies).toEqual([{ display_name: 'new-pc', tags: '', folder_id: 0, group_guids: [] }]);
	});
});
