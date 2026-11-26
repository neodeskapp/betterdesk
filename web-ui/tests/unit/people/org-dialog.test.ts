/**
 * Edit organization (PATTERN.md rules 5, 7 and 11): every field says what
 * it is for, a changed short name says what that changes, and the toast
 * after saving names what was saved.
 */
import { cleanup, render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';

vi.mock('#lib/api/orgs.ts', async (orig) => ({
	...(await orig<typeof import('../../../src/lib/api/orgs.ts')>()),
	updateOrg: vi.fn(async () => ({})),
	createOrg: vi.fn(async () => ({}))
}));
const toastMock = vi.hoisted(() => ({
	show: vi.fn(),
	success: vi.fn(),
	error: vi.fn(),
	warning: vi.fn(),
	info: vi.fn()
}));
vi.mock('#lib/ui/toasts.svelte.ts', async (orig) => ({
	...(await orig<typeof import('../../../src/lib/ui/toasts.svelte.ts')>()),
	toast: toastMock
}));

import * as api from '../../../src/lib/api/orgs.ts';
import OrgDialog from '../../../src/lib/people/OrgDialog.svelte';

const org = { id: 'o1', name: 'Harbor', slug: 'harbor', logo_url: '' } as never;

beforeEach(() => {
	for (const fn of Object.values(toastMock)) fn.mockReset();
	vi.mocked(api.updateOrg).mockClear();
});

afterEach(cleanup);

test('the name says where it shows', () => {
	render(OrgDialog, { org, onclose: vi.fn(), onsaved: vi.fn() });
	expect(screen.getByLabelText('organizations.name')).toHaveAccessibleDescription(
		'Shown on this organization’s page, in lists and to its members.'
	);
});

test('a changed short name says what changes; an unchanged one says nothing', async () => {
	const user = userEvent.setup();
	render(OrgDialog, { org, onclose: vi.fn(), onsaved: vi.fn() });
	const note = 'Sign-ins that name the organization by its short name need the new one after you save.';
	expect(screen.queryByText(note)).toBeNull();
	const slug = screen.getByLabelText('organizations.slug');
	await user.clear(slug);
	await user.type(slug, 'harbor-2');
	expect(screen.getByText(note)).toBeTruthy();
});

test('the toast after saving names what was saved', async () => {
	const user = userEvent.setup();
	const onsaved = vi.fn();
	render(OrgDialog, { org, onclose: vi.fn(), onsaved });
	const name = screen.getByLabelText('organizations.name');
	await user.clear(name);
	await user.type(name, 'Harbor Logistics');
	await user.click(screen.getByRole('button', { name: 'actions.save' }));
	await vi.waitFor(() => expect(onsaved).toHaveBeenCalled());
	expect(toastMock.success).toHaveBeenCalledWith('Saved: organizations.name Harbor Logistics.');
});

test('a new organization keeps its own toast', async () => {
	const user = userEvent.setup();
	const onsaved = vi.fn();
	render(OrgDialog, { org: null, onclose: vi.fn(), onsaved });
	await user.type(screen.getByLabelText('organizations.name'), 'Lab');
	await user.click(screen.getByRole('button', { name: 'organizations.create' }));
	await vi.waitFor(() => expect(onsaved).toHaveBeenCalled());
	expect(toastMock.success).toHaveBeenCalledWith('organizations.org_created');
});
