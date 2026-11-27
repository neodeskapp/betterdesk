/**
 * The Settings tab of an organization (PATTERN.md rules 2, 5 and 8): each
 * policy group says whether it is set for this organization or that no
 * rule applies, with a link to this organization's Policies page; the
 * shared address book says what is in force and where it comes from; the
 * stored JSON sits behind Details; Delete organization is in a danger zone
 * that says what is lost.
 */
import { cleanup, render, screen, waitFor, within } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

vi.mock('$app/paths', () => ({ resolve: (p: string) => p || '/', asset: (p: string) => '/' + p, base: '' }));
vi.mock('$app/navigation', () => ({ goto: vi.fn() }));

const stored = vi.hoisted(() => ({
	rows: [] as { key: string; value: string }[]
}));
vi.mock('#lib/api/orgs.ts', async (orig) => ({
	...(await orig<typeof import('../../../src/lib/api/orgs.ts')>()),
	listOrgSettings: vi.fn(async () => stored.rows)
}));

import SettingsTab from '../../../src/routes/(console)/organizations/[id]/SettingsTab.svelte';
import { orgSettingsView } from '../../../src/lib/people/org-settings.ts';
import { session } from '../../../src/lib/state/session.svelte.ts';

const org = { id: 'org-1', name: 'Harbor', slug: 'harbor' } as never;

/** The row of a stored key, once the values are loaded */
async function row(key: string): Promise<HTMLElement> {
	return waitFor(() => {
		const el = document.querySelector<HTMLElement>(`[data-setting="${key}"]`);
		if (!el) throw new Error(`no row ${key}`);
		return el;
	});
}

beforeEach(() => {
	session.isSuperAdmin = true;
	stored.rows = [
		{
			key: 'policy_connection',
			value: '{"idle_lock_timeout":30,"max_session_duration":480,"mode":"unattended","wallpaper":"show"}'
		},
		{ key: 'timezone', value: 'Europe/Hamburg' }
	];
});

afterEach(() => {
	cleanup();
	session.isSuperAdmin = false;
});

describe('stored values in words', () => {
	test('a saved policy group names each value in words; an unsaved one says it is not set', async () => {
		render(SettingsTab, { org, canPolicies: true });
		const connection = await row('policy_connection');
		// Said to screen readers and marked on the row; the values say it on screen
		expect(within(connection).getByText('Set for this organization.')).toHaveClass('sr-only');
		expect(connection).toHaveClass('is-set');
		// No internal key on screen
		expect(connection.textContent).not.toContain('policy_connection');
		expect(connection.textContent).not.toContain('4 values');
		// In the Policies page's order, with its names and words for the values
		expect([...connection.querySelectorAll('li')].map((li) => li.textContent?.trim())).toEqual([
			'policies.connection_mode: policies.mode_unattended',
			'policies.max_session: 480',
			'policies.idle_lock: 30',
			'policies.wallpaper_policy: policies.wallpaper_show'
		]);
		const features = await row('policy_features');
		expect(features.textContent).toContain('Not set (each device decides)');
		expect(features.textContent).not.toContain('No rule');
		expect(features).not.toHaveClass('is-set');
	});

	test('an empty Network group says the server’s settings apply, not each device', async () => {
		render(SettingsTab, { org, canPolicies: true });
		const network = await row('policy_network');
		expect(network.textContent).toContain('Not set (the server’s connection settings apply)');
		expect(network.textContent).not.toContain('each device decides');
	});

	test('the policies group links to this organization’s Policies page', async () => {
		render(SettingsTab, { org, canPolicies: true });
		const link = await screen.findByRole('link', { name: 'Open this organization’s policies' });
		expect(link.getAttribute('href')).toBe('/policies/org-1');
	});

	test('without the right to open Policies there is no link', async () => {
		render(SettingsTab, { org, canPolicies: false });
		await row('policy_connection');
		expect(screen.queryByRole('link', { name: 'Open this organization’s policies' })).toBeNull();
	});

	test('the shared address book says what applies when nothing is stored', async () => {
		render(SettingsTab, { org, canPolicies: true });
		const book = await row('shared_address_book_enabled');
		expect(book.querySelector('output')!.textContent).toBe('On.');
		expect(book.textContent).toContain('Built-in default: on until it is turned off.');
		expect(
			within(book).getByRole('link', { name: 'Change on the Address book tab' }).getAttribute('href')
		).toBe('#address-book');
	});

	test('no stored JSON in the row; a value the console has no name for keeps its key', async () => {
		stored.rows = [
			{ key: 'policy_features', value: '{"allow_audio":false,"allow_clipboard":true,"future_switch":1}' }
		];
		render(SettingsTab, { org, canPolicies: true });
		const features = await row('policy_features');
		expect(features.querySelector('details')).toBeNull();
		expect(features.textContent).not.toContain('Stored value');
		expect([...features.querySelectorAll('li')].map((li) => li.textContent?.trim())).toEqual([
			'policies.allow_clipboard: On',
			'policies.allow_audio: Off',
			'future_switch: 1'
		]);
	});

	test('other stored values stay listed with their key and value', async () => {
		render(SettingsTab, { org, canPolicies: true });
		const table = await screen.findByRole('table');
		expect(within(table).getByText('timezone')).toBeTruthy();
		expect(within(table).getByText('Europe/Hamburg')).toBeTruthy();
	});

	test('delete sits in a danger zone that says what is lost', async () => {
		render(SettingsTab, { org, canPolicies: true });
		const zone = await screen.findByRole('region', { name: 'Danger zone' });
		expect(within(zone).getByRole('button', { name: /organizations.delete_org_button/ })).toBeTruthy();
		expect(zone.textContent).toContain('organizations.delete_org_desc');
	});
});

describe('orgSettingsView', () => {
	test('the address book is on unless stored as false, as on the server', () => {
		expect(orgSettingsView([]).addressBook).toBeNull();
		expect(orgSettingsView([{ key: 'shared_address_book_enabled', value: 'false' }]).addressBook?.on).toBe(
			false
		);
		expect(orgSettingsView([{ key: 'shared_address_book_enabled', value: 'yes' }]).addressBook?.on).toBe(
			true
		);
	});
});
