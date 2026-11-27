/**
 * Settings › Branding on the settings pattern (PATTERN.md): one save bar
 * for the whole tab, the client app's fields too; a logo file waits for
 * Save like any change; examples and empty-field behaviour in the help,
 * not in the fields.
 */
import { cleanup, fireEvent, render, screen, within } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

vi.mock('#lib/api/settings.ts', async (orig) => ({
	...(await orig<typeof import('../../../src/lib/api/settings.ts')>()),
	getBranding: vi.fn(async () => ({
		data: { appName: 'Desk', logoType: 'image', logoUrl: '/uploads/old.png', loginPromoMode: 'classic' },
		readability: null
	})),
	listBackgrounds: vi.fn(async () => []),
	listLocalFonts: vi.fn(async () => []),
	listProfiles: vi.fn(async () => []),
	listThemes: vi.fn(async () => []),
	saveBranding: vi.fn(async (body: Record<string, unknown>) => ({ data: body, readability: null })),
	uploadLogo: vi.fn(async () => '/uploads/new-logo.png')
}));
vi.mock('#lib/api/clientBranding.ts', async (orig) => ({
	...(await orig<typeof import('../../../src/lib/api/clientBranding.ts')>()),
	getClientBranding: vi.fn(async () => ({
		company_name: 'Acme',
		support_contact: '',
		phone: '',
		email: '',
		website: '',
		accent_color: '#4f6ef7',
		logo: null,
		revision: ''
	})),
	saveClientBranding: vi.fn(async (b: Record<string, unknown>) => ({
		...b,
		logo: null,
		revision: '1764115200000'
	}))
}));

import * as settingsApi from '../../../src/lib/api/settings.ts';
import * as clientApi from '../../../src/lib/api/clientBranding.ts';
import BrandingTab from '../../../src/lib/settings/tabs/BrandingTab.svelte';
import { session } from '../../../src/lib/state/session.svelte.ts';

beforeEach(() => {
	session.isSuperAdmin = true;
	vi.mocked(settingsApi.saveBranding).mockClear();
	vi.mocked(settingsApi.uploadLogo).mockClear();
	vi.mocked(clientApi.saveClientBranding).mockClear();
});

afterEach(() => {
	cleanup();
	session.isSuperAdmin = false;
});

/** Pick a file: a writable files list, as the picker sets it back, which jsdom's getter refuses */
async function pick(file: File) {
	const picker = screen.getByLabelText('branding.logo_image_upload') as HTMLInputElement;
	Object.defineProperty(picker, 'files', { value: [file], writable: true, configurable: true });
	await fireEvent.change(picker);
}

async function loaded() {
	render(BrandingTab);
	const company = await screen.findByLabelText('client_branding.company_name');
	await vi.waitFor(() => expect(company).toHaveValue('Acme'));
	await vi.waitFor(() => expect(screen.getByLabelText('branding.app_name')).toHaveValue('Desk'));
}

describe('Settings › Branding', () => {
	test('never saved: the client app says its values are the ones clients use, not "Not saved yet"', async () => {
		await loaded();
		const group = document.getElementById('client-app')!;
		expect(group).not.toHaveTextContent('client_branding.never_saved');
		expect(group).toHaveTextContent(
			'Nothing saved here yet: clients show the values below until you save your own.'
		);
	});

	test('the console preview comes before the groups on a narrow screen, not next to the client app preview', async () => {
		await loaded();
		const body = document.querySelector('.settings-body')!;
		expect(body).not.toHaveClass('summary-last');
		expect(screen.getByRole('heading', { name: 'Console preview' })).toBeInTheDocument();
		expect(screen.getByRole('complementary', { name: 'Console preview' })).toBeInTheDocument();
	});

	test('the client app preview sits in the side column next to the fields and follows them', async () => {
		const user = userEvent.setup();
		await loaded();
		const side = screen.getByRole('complementary', { name: 'Console preview' });
		const preview = within(side).getByRole('region', { name: 'Client app preview' });
		const company = screen.getByLabelText('client_branding.company_name');
		await user.clear(company);
		await user.type(company, 'Acme Ltd');
		expect(preview).toHaveTextContent('Acme Ltd');
		// Below 1200 px it stays at the end of the client app group (CSS shows one of the two)
		expect(document.querySelector('#client-app .cb-preview-part.is-group')).toHaveTextContent('Acme Ltd');
		expect(document.querySelector('.branding-tab')).toHaveClass('side-rows', 'side-sticky');
		expect(document.body).not.toHaveTextContent('settings.appearance.save_effect');
	}, 20_000);

	test('one save bar lists the name and the client app change, and one Save sends both', async () => {
		const user = userEvent.setup();
		await loaded();
		const name = screen.getByLabelText('branding.app_name');
		await user.clear(name);
		await user.type(name, 'Help desk');
		const company = screen.getByLabelText('client_branding.company_name');
		await user.clear(company);
		await user.type(company, 'Acme Ltd');
		const bar = screen.getByRole('region', { name: 'Unsaved changes' });
		expect(bar).toHaveTextContent('2 unsaved changes');
		expect(bar).toHaveTextContent('branding.app_name: Desk → Help desk');
		expect(bar).toHaveTextContent('Acme → Acme Ltd');
		await user.click(within(bar).getByRole('button', { name: 'Save changes' }));
		await vi.waitFor(() => expect(clientApi.saveClientBranding).toHaveBeenCalledTimes(1));
		expect(vi.mocked(settingsApi.saveBranding).mock.calls[0]![0]).toMatchObject({ appName: 'Help desk' });
		expect(vi.mocked(clientApi.saveClientBranding).mock.calls[0]![0]).toMatchObject({
			company_name: 'Acme Ltd'
		});
		await vi.waitFor(() => expect(screen.queryByRole('region', { name: 'Unsaved changes' })).toBeNull());
	}, 20_000);

	test('a picked logo is a change that Save uploads; Discard drops it', async () => {
		const user = userEvent.setup();
		await loaded();
		const file = new File(['<svg/>'], 'logo.png', { type: 'image/png' });
		await pick(file);
		const bar = screen.getByRole('region', { name: 'Unsaved changes' });
		expect(bar).toHaveTextContent('logo.png');
		expect(settingsApi.uploadLogo).not.toHaveBeenCalled();
		await user.click(within(bar).getByRole('button', { name: 'Discard' }));
		expect(screen.queryByRole('region', { name: 'Unsaved changes' })).toBeNull();

		await pick(file);
		await user.click(screen.getByRole('button', { name: 'Save changes' }));
		await vi.waitFor(() => expect(settingsApi.saveBranding).toHaveBeenCalledTimes(1));
		expect(settingsApi.uploadLogo).toHaveBeenCalledTimes(1);
		expect(vi.mocked(settingsApi.saveBranding).mock.calls[0]![0]).toMatchObject({
			logoUrl: '/uploads/new-logo.png'
		});
	}, 20_000);

	test('a client accent that is no colour is refused at its row and nothing is sent', async () => {
		const user = userEvent.setup();
		await loaded();
		const accent = screen.getByLabelText('client_branding.accent_color', {
			selector: 'input:not([type=color])'
		});
		await fireEvent.input(accent, { target: { value: 'blue' } });
		expect(document.getElementById('client-app-accent-error')).toHaveTextContent(
			'client_branding.accent_invalid'
		);
		await user.click(screen.getByRole('button', { name: 'Save changes' }));
		await vi.waitFor(() =>
			expect(screen.getByRole('region', { name: 'Unsaved changes' })).toHaveTextContent(
				'client_branding.accent_invalid'
			)
		);
		expect(settingsApi.saveBranding).not.toHaveBeenCalled();
		expect(clientApi.saveClientBranding).not.toHaveBeenCalled();
	}, 20_000);

	test('fields hold no examples; the help says what an empty heading shows', async () => {
		await loaded();
		const placeholders = [...document.querySelectorAll<HTMLInputElement>('.branding-tab [placeholder]')]
			.filter((el) => el.type !== 'search')
			.map((el) => `${el.id}: ${el.placeholder}`);
		expect(placeholders).toEqual([]);
		expect(screen.getByLabelText('branding.login_heading')).toHaveAccessibleDescription(
			'The big line on the sign-in page. Empty shows "auth.login_title".'
		);
	}, 20_000);
});
