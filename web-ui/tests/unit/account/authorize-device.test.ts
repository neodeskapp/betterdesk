/**
 * /authorize-device/<id> (Sign in with NeoDesk): the page shows which
 * computer asks before Allow and Deny, decides the request it was opened
 * for, and says when the request is over. A refused Allow keeps the
 * buttons with the reason.
 */
import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import { ApiError } from '../../../src/lib/api/client.ts';
import { setStrings } from '../../../src/lib/i18n/i18n.svelte.ts';

vi.setConfig({ testTimeout: 30_000 });

const pageState = vi.hoisted(() => ({
	params: { id: 'req-1' },
	url: new URL('http://localhost/authorize-device/req-1')
}));
vi.mock('$app/state', () => ({ page: pageState }));
vi.mock('$app/paths', () => ({ resolve: (p: string) => p || '/', asset: (p: string) => '/' + p }));

const calls = vi.hoisted(() => ({ get: vi.fn(), decide: vi.fn() }));
vi.mock('#lib/api/deviceLogin.ts', () => ({ getDeviceLogin: calls.get, decideDeviceLogin: calls.decide }));

const pending = {
	state: 'pending',
	device_id: '123456789',
	device_name: 'Front desk',
	device_os: 'Windows 11',
	ip: '198.51.100.4',
	created_at: new Date(Date.now() - 30_000).toISOString(),
	expires_at: new Date(Date.now() + 270_000).toISOString(),
	account: 'erin'
};

beforeEach(() => {
	setStrings('en', 'ltr', {});
});
afterEach(() => {
	vi.clearAllMocks();
});

async function open() {
	const Page = (await import('../../../src/routes/(console)/authorize-device/[id]/+page.svelte')).default;
	render(Page);
}

test('shows the computer before Allow, and Allow decides the request of the address', async () => {
	calls.get.mockResolvedValue(pending);
	calls.decide.mockResolvedValue({ ...pending, state: 'allowed' });
	await open();
	const allow = await screen.findByRole('button', { name: 'device_authorize.allow' });
	expect(calls.get).toHaveBeenCalledWith('req-1');
	for (const shown of ['Front desk', 'Windows 11', '123456789', '198.51.100.4'])
		expect(screen.getByText(shown)).toBeVisible();
	expect(screen.getByRole('button', { name: 'device_authorize.deny' })).toBeVisible();
	await userEvent.click(allow);
	expect(calls.decide).toHaveBeenCalledWith('req-1', true);
	const heading = await screen.findByRole('heading', { level: 1, name: 'device_authorize.allowed_title' });
	await vi.waitFor(() => expect(heading).toHaveFocus());
	expect(screen.queryByRole('button', { name: 'device_authorize.allow' })).toBeNull();
});

test('Deny declines the request', async () => {
	calls.get.mockResolvedValue(pending);
	calls.decide.mockResolvedValue({ ...pending, state: 'declined' });
	await open();
	await userEvent.click(await screen.findByRole('button', { name: 'device_authorize.deny' }));
	expect(calls.decide).toHaveBeenCalledWith('req-1', false);
	await screen.findByRole('heading', { level: 1, name: 'device_authorize.declined_title' });
});

test('a request that is over offers no buttons', async () => {
	calls.get.mockResolvedValue({ ...pending, state: 'expired', device_name: '', device_id: '' });
	await open();
	await screen.findByRole('heading', { level: 1, name: 'device_authorize.ended_title' });
	expect(screen.queryByRole('button', { name: 'device_authorize.allow' })).toBeNull();
});

test('a refused Allow keeps the buttons and says why', async () => {
	setStrings('en', 'ltr', {
		device_authorize: { errors: { totp_setup_required: 'Set up two-factor sign-in first.' } }
	});
	calls.get.mockResolvedValue(pending);
	calls.decide.mockRejectedValue(new ApiError('refused', 409, { code: 'totp_setup_required' }));
	await open();
	await userEvent.click(await screen.findByRole('button', { name: 'device_authorize.allow' }));
	expect(await screen.findByText('Set up two-factor sign-in first.')).toHaveAttribute('role', 'status');
	expect(screen.getByRole('button', { name: 'device_authorize.allow' })).toBeEnabled();
});

test('an Allow after the request ended shows that it ended', async () => {
	calls.get.mockResolvedValue(pending);
	calls.decide.mockRejectedValue(new ApiError('gone', 404, { code: 'not_found' }));
	await open();
	await userEvent.click(await screen.findByRole('button', { name: 'device_authorize.allow' }));
	await screen.findByRole('heading', { level: 1, name: 'device_authorize.ended_title' });
});
