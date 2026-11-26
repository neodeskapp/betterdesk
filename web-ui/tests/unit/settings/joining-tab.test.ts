/**
 * Settings › Joining on the settings pattern. The join mode has one place,
 * Device tokens, under enrollment.manage (BUGS L100): here it shows with
 * where it comes from and links there. The approval form, the device
 * visibility and the availability history save with one bar, each part to
 * its own endpoint, with Undo in the toast; the approval save sends no mode.
 */
import { cleanup, render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { serve } from './serve.ts';

vi.mock('$app/paths', () => ({ resolve: (p: string) => p || '/', asset: (p: string) => '/' + p }));

import JoiningTab from '../../../src/lib/settings/tabs/JoiningTab.svelte';
import { session } from '../../../src/lib/state/session.svelte.ts';
import { toasts } from '../../../src/lib/ui/toasts.svelte.ts';

const ENROLLMENT = '/api/settings/enrollment';
const SCOPE = '/api/settings/device-scope';
const AVAILABILITY = '/api/settings/availability-history';

function routes(extra: Record<string, unknown> = {}) {
	return serve({
		[`GET ${ENROLLMENT}`]: { mode: 'managed', rich_approve: true, tag_picker: true, pending_count: 2 },
		[`GET ${SCOPE}`]: { mode: 'open' },
		[`GET ${AVAILABILITY}`]: { enabled: false, retention_days: 30 },
		'GET /api/panel/settings/sources': {
			'enrollment.mode': {
				value_source: 'stored',
				env_var: 'ENROLLMENT_MODE',
				locked: false,
				default: 'open'
			},
			'device_scope.mode': {
				value_source: 'default',
				env_var: 'DEVICE_SCOPE_DEFAULT',
				locked: false,
				default: 'open'
			}
		},
		...extra
	});
}

beforeEach(() => {
	session.permissions = ['server.config', 'enrollment.manage'];
	toasts.items = [];
});

afterEach(() => {
	cleanup();
	vi.unstubAllGlobals();
	session.permissions = [];
});

describe('the join mode', () => {
	test('shows with its source and a link to Device tokens; there is no mode to pick here', async () => {
		routes();
		render(JoiningTab);
		const link = await screen.findByRole('link', { name: 'settings.enrollment_mode_link' });
		expect(link.getAttribute('href')).toBe('/devices/add?tab=tokens');
		expect(screen.queryByRole('radio', { name: /tokens\.mode_open/ })).toBeNull();
		const row = document.getElementById('enrollment-mode')!;
		expect(row).toHaveTextContent('tokens.mode_managed');
		expect(row).toHaveTextContent('Set here');
	});

	test('set in the environment: said so, and no link to change it', async () => {
		routes({
			'GET /api/panel/settings/sources': {
				'enrollment.mode': { value_source: 'environment', env_var: 'ENROLLMENT_MODE', locked: true }
			}
		});
		render(JoiningTab);
		expect(
			await screen.findByText('Set in the environment file (ENROLLMENT_MODE), change it there')
		).toBeInTheDocument();
		expect(screen.queryByRole('link', { name: 'settings.enrollment_mode_link' })).toBeNull();
	});

	test('without enrollment.manage: no link, and the permission it needs is named', async () => {
		session.permissions = ['server.config'];
		routes();
		render(JoiningTab);
		expect(await screen.findByText(/Changing it needs the permission/)).toBeInTheDocument();
		expect(screen.queryByRole('link', { name: 'settings.enrollment_mode_link' })).toBeNull();
	});
});

describe('one save for the rest', () => {
	test('approvals, visibility and history go to their endpoints, the approvals without a mode; Undo puts them back', async () => {
		const server = routes();
		const user = userEvent.setup();
		render(JoiningTab);
		await user.click(await screen.findByRole('checkbox', { name: /settings\.enrollment_rich_approve/ }));
		await user.click(screen.getByRole('radio', { name: /settings\.general\.scope_restricted/ }));
		await user.click(screen.getByRole('checkbox', { name: /settings\.availability_history_enable/ }));
		expect(screen.getByRole('region', { name: 'Unsaved changes' })).toHaveTextContent('3 unsaved changes');
		await user.click(screen.getByRole('button', { name: 'Save changes' }));
		await vi.waitFor(() => expect(server.sent('POST', AVAILABILITY)).toHaveLength(1));
		expect(server.sent('PUT', ENROLLMENT)).toEqual([{ rich_approve: false, tag_picker: true }]);
		expect(server.sent('POST', SCOPE)).toEqual([{ mode: 'restricted' }]);
		expect(server.sent('POST', AVAILABILITY)).toEqual([{ enabled: true, retention_days: 30 }]);
		expect(server.calls.some((c) => c.path === '/api/panel/enrollment/mode' && c.method !== 'GET')).toBe(
			false
		);

		const done = await vi.waitFor(() => {
			const item = toasts.items.find((i) => i.action);
			if (!item) throw new Error('no toast with Undo');
			return item;
		});
		done.action!.onclick();
		await vi.waitFor(() =>
			expect(server.sent('POST', SCOPE)).toEqual([{ mode: 'restricted' }, { mode: 'open' }])
		);
		expect(server.sent('PUT', ENROLLMENT)[1]).toEqual({ rich_approve: true, tag_picker: true });
	});

	test('the history days are refused at the field, and the tag list says it needs the full form', async () => {
		const server = routes();
		const user = userEvent.setup();
		render(JoiningTab);
		const days = await screen.findByRole('spinbutton', { name: 'settings.general.retention_label' });
		expect(document.getElementById('availability-history-retention')).toHaveTextContent(
			'Has no effect while availability history is off.'
		);
		await user.clear(days);
		await user.type(days, '0');
		expect(document.getElementById('availability-history-retention-error')).toHaveTextContent(
			'settings.general.retention_error'
		);
		await user.click(screen.getByRole('button', { name: 'Save changes' }));
		expect(server.sent('POST', AVAILABILITY)).toEqual([]);

		await user.click(screen.getByRole('checkbox', { name: /settings\.enrollment_rich_approve/ }));
		expect(document.getElementById('enrollment-tag-picker')).toHaveTextContent(/full approval form is off/);
	});
});
