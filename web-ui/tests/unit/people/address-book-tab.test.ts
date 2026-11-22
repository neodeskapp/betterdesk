/**
 * The organization's address book tab asks for the stored password flags
 * only with org.manage_devices, which their route needs: without it the
 * flags show as unknown and no request is refused (L274).
 */
import { cleanup, render, screen } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import AddressBookTab from '../../../src/routes/(console)/organizations/[id]/AddressBookTab.svelte';
import { session } from '../../../src/lib/state/session.svelte.ts';

let requests: string[] = [];

beforeEach(() => {
	requests = [];
	vi.stubGlobal('fetch', async (input: string) => {
		const url = new URL(input, location.origin);
		requests.push(url.pathname);
		const data = url.pathname.endsWith('/address-book')
			? { enabled: true, data: { peers: [{ id: '900100001', alias: 'Front desk', tags: [] }], tags: [] } }
			: { credentials: [{ peer_id: '900100001', password_set: true }] };
		return new Response(JSON.stringify(data), { headers: { 'Content-Type': 'application/json' } });
	});
	session.isSuperAdmin = false;
});

afterEach(() => {
	cleanup();
	vi.unstubAllGlobals();
	session.permissions = [];
});

describe('address book tab', () => {
	test('without org.manage_devices the stored passwords are not asked for', async () => {
		session.permissions = ['user.view'];
		render(AddressBookTab, { orgId: 'org-1' });
		expect(await screen.findByText('900100001')).toBeTruthy();
		expect(requests).toEqual(['/api/panel/org/org-1/address-book']);
	});

	test('with org.manage_devices they are', async () => {
		session.permissions = ['user.view', 'org.manage_devices'];
		render(AddressBookTab, { orgId: 'org-1' });
		expect(await screen.findByText('900100001')).toBeTruthy();
		expect(requests.sort()).toEqual([
			'/api/panel/org/org-1/address-book',
			'/api/panel/org/org-1/peer-credentials'
		]);
	});
});
