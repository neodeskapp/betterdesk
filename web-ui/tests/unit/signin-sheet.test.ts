/**
 * The saved sign-in sheet of a computer that is not Windows says that the
 * viewer won't use the details there (L510): the details can still be
 * saved, for a computer that will run Windows later, but nobody should
 * wonder why nothing happens.
 */
import { cleanup, render, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, test } from 'vitest';
import SigninSheet from '../../src/lib/devices/dialogs/SigninSheet.svelte';
import type { SigninStatus } from '../../src/lib/api/savedSignin.ts';

afterEach(() => cleanup());

const saved: SigninStatus = {
	available: true,
	set: true,
	username: 'labuser',
	form: 'password',
	auto: true,
	updated_by: '',
	updated_at: '',
	may_use: true,
	sealed_signin: false
};

function open(device: { id: string; platform?: string; os?: string }) {
	render(SigninSheet, {
		device,
		load: async () => saved,
		save: async () => {},
		change: async () => {},
		clear: async () => true,
		settle: () => {}
	});
}

// t() answers with the key in the unit tests
const notWindows = 'remote.signin_unsupported_platform';

describe('saved sign-in sheet', () => {
	test('says on a Linux computer that saved sign-in works on Windows only', async () => {
		open({ id: '900100013', platform: 'Linux' });
		expect(await screen.findByText(notWindows)).toBeTruthy();
	});

	test('names no such limit on a Windows computer or one whose system is not known yet', async () => {
		open({ id: '900100014', platform: 'Windows' });
		await screen.findByText('devices.signin_intro');
		expect(screen.queryByText(notWindows)).toBeNull();
		cleanup();
		open({ id: '900100015' });
		await screen.findByText('devices.signin_intro');
		expect(screen.queryByText(notWindows)).toBeNull();
	});
});
