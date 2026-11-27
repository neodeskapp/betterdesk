/**
 * The device token dialog: editing an unlimited token keeps it unlimited.
 */
import { cleanup, render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import TokenDialog from '../../../src/lib/devices-area/add/TokenDialog.svelte';
import type { DeviceToken } from '../../../src/lib/api/tokens.ts';

let bodies: unknown[] = [];

beforeEach(() => {
	bodies = [];
	vi.stubGlobal('fetch', async (input: string, init: RequestInit = {}) => {
		if (init.method === 'PUT' && typeof init.body === 'string') bodies.push(JSON.parse(init.body));
		return new Response('{"success":true}', { status: 200, headers: { 'Content-Type': 'application/json' } });
	});
});

afterEach(() => {
	cleanup();
	vi.unstubAllGlobals();
});

const token = (over: Partial<DeviceToken>): DeviceToken => ({
	id: '7',
	name: 'Bundle',
	token_prefix: 'nd_',
	status: 'active',
	max_uses: 0,
	use_count: 3,
	peer_id: '',
	note: 'keep me',
	expires_at: '',
	created_at: '',
	created_by: '',
	folder_id: 0,
	org_id: '',
	...over
});

describe('token dialog', () => {
	test('renaming an unlimited token keeps it unlimited', async () => {
		const user = userEvent.setup();
		const onsaved = vi.fn();
		render(TokenDialog, { token: token({}), onclose: () => {}, onsaved });
		const uses = screen.getByLabelText('tokens.max_uses');
		expect(uses).toHaveValue(0);
		const name = screen.getByLabelText('tokens.name');
		await user.clear(name);
		await user.type(name, 'Bundle 2');
		await user.click(screen.getByRole('button', { name: 'tokens.save' }));
		await vi.waitFor(() => expect(onsaved).toHaveBeenCalled());
		expect(bodies).toEqual([{ name: 'Bundle 2', max_uses: 0, note: 'keep me' }]);
	});

	test('Uses allowed below 0, a fraction or nothing is refused next to the field and nothing is sent (BUGS L380)', async () => {
		const user = userEvent.setup();
		const onsaved = vi.fn();
		render(TokenDialog, { token: token({ max_uses: 5 }), onclose: () => {}, onsaved });
		const uses = screen.getByLabelText('tokens.max_uses');
		for (const typed of ['-3', '2.5', '']) {
			await user.clear(uses);
			if (typed) await user.type(uses, typed);
			await user.click(screen.getByRole('button', { name: 'tokens.save' }));
			await vi.waitFor(() => expect(uses).toHaveAccessibleDescription(/tokens.max_uses_invalid/));
		}
		expect(bodies).toEqual([]);
		expect(onsaved).not.toHaveBeenCalled();
	});

	test('a token with a limit keeps its number', () => {
		render(TokenDialog, { token: token({ max_uses: 5 }), onclose: () => {}, onsaved: () => {} });
		expect(screen.getByLabelText('tokens.max_uses')).toHaveValue(5);
	});

	test('a new token can name a folder and an organization whose policies the computer gets', async () => {
		const posted: unknown[] = [];
		vi.stubGlobal('fetch', async (input: string, init: RequestInit = {}) => {
			const json = (body: unknown) =>
				new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } });
			if (String(input).startsWith('/api/folders'))
				return json({ success: true, data: { folders: [{ id: 4, name: 'Lab', color: '', icon: '' }] } });
			if (String(input).startsWith('/api/panel/org'))
				return json({ organizations: [{ id: 'org-9', name: 'Acme' }] });
			if (init.method === 'POST' && typeof init.body === 'string') posted.push(JSON.parse(init.body));
			return json({
				success: true,
				data: { id: 1, name: 'Lab PCs', token: '0123456789abcdef0123456789abcdef' }
			});
		});
		const user = userEvent.setup();
		const onsaved = vi.fn();
		render(TokenDialog, { token: null, onclose: () => {}, onsaved });
		const folder = await screen.findByLabelText('onboard.invite.folder');
		await user.selectOptions(folder, 'Lab');
		await user.selectOptions(screen.getByLabelText('tokens.policies'), 'Acme');
		await user.type(screen.getByLabelText('tokens.name'), 'Lab PCs');
		await user.click(screen.getByRole('button', { name: 'tokens.create' }));
		await vi.waitFor(() => expect(onsaved).toHaveBeenCalled());
		expect(posted).toEqual([
			{ name: 'Lab PCs', max_uses: 1, note: '', folder_id: 4, org_id: 'org-9', expires_in: 0 }
		]);
	});
});
