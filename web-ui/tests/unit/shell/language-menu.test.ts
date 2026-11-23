/**
 * The language menu opens on the language in use, not on the first one in
 * the list (Bahasa Indonesia), so the keyboard starts where the person is.
 */
import { cleanup, render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, expect, test } from 'vitest';
import { setStrings } from '../../../src/lib/i18n/i18n.svelte.ts';
import LanguageMenu from '../../../src/lib/shell/LanguageMenu.svelte';

afterEach(cleanup);

test('opens with focus on the current language', async () => {
	const lang = (code: string, native: string) => ({ code, name: native, native, flag: '', rtl: false });
	setStrings('de', 'ltr', {}, [
		lang('id', 'Bahasa Indonesia'),
		lang('cs', 'Čeština'),
		lang('de', 'Deutsch'),
		lang('en', 'English')
	]);
	const user = userEvent.setup();
	render(LanguageMenu);
	await user.click(screen.getByRole('button', { name: 'settings.language' }));
	expect(screen.getByRole('menuitemradio', { name: /Deutsch/ })).toHaveFocus();
	setStrings('en', 'ltr', {}, []);
});
