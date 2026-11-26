/**
 * The colour choice of a folder and the access choice of an API token are
 * radio groups (APG radio group): the arrow keys move the choice, the group
 * has one name, and each choice has its own.
 */
import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { expect, test, vi } from 'vitest';

vi.mock('$app/paths', () => ({ resolve: (p: string) => '/app' + (p || '/'), asset: (p: string) => '/' + p }));
vi.mock('#lib/api/apiTokens.ts', async (orig) => ({
	...(await orig<typeof import('../../../src/lib/api/apiTokens.ts')>()),
	tokenPermissions: vi.fn(async () => ({ held: ['device.view'], all: ['device.view'] }))
}));
vi.mock('#lib/api/users.ts', async (orig) => ({
	...(await orig<typeof import('../../../src/lib/api/users.ts')>()),
	listUserGroups: vi.fn(async () => [])
}));
vi.setConfig({ testTimeout: 60_000 });

test('folder colours: arrow keys move the choice', async () => {
	const FolderDialog = (await import('../../../src/routes/(console)/devices/FolderDialog.svelte')).default;
	render(FolderDialog, { folder: null, onclose: () => {}, onsaved: () => {} });
	const group = await screen.findByRole('radiogroup', { name: 'folders.color' });
	const radios = Array.from(group.querySelectorAll<HTMLInputElement>('input[type="radio"]'));
	expect(radios.length).toBeGreaterThan(2);
	expect(radios[0]).toBeChecked();
	radios[0]!.focus();
	await userEvent.keyboard('{ArrowRight}');
	expect(radios[1]).toBeChecked();
	expect(radios[1]).toHaveFocus();
	await userEvent.keyboard('{ArrowLeft}');
	expect(radios[0]).toBeChecked();
});

test('token access: one named group, arrow keys move the choice', async () => {
	const Dialog = (await import('../../../src/lib/account/ApiTokenCreateDialog.svelte')).default;
	render(Dialog, { onclose: () => {}, oncreated: () => {} });
	const read = await screen.findByRole('radio', { name: /api_tokens\.read_only/ });
	// The fieldset names the group; a radiogroup inside it with the same name read it twice
	expect(screen.getAllByRole('group', { name: 'api_tokens.access' })).toHaveLength(1);
	expect(screen.queryByRole('radiogroup', { name: 'api_tokens.access' })).toBeNull();
	expect(read).toBeChecked();
	read.focus();
	await userEvent.keyboard('{ArrowDown}');
	expect(screen.getByRole('radio', { name: /api_tokens\.read_write/ })).toBeChecked();
});
