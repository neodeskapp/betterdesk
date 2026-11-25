/**
 * Does what a search result for an action names (actions.ts lists them).
 * Actions that open a dialog on a page go to that page and leave an
 * intent the page takes (intent.svelte.ts).
 */

import { goto } from '$app/navigation';
import { link } from '#lib/nav.ts';
import { openQuickSupport } from '#lib/onboard/open.svelte.ts';
import { setTheme, theme } from '#lib/state/theme.svelte.ts';
import type { ActionId } from './actions.ts';
import { a11y } from './a11y.svelte.ts';
import { requestIntent, type Intent } from './intent.svelte.ts';
import { signOut } from './sign-out.ts';

const PAGES: Record<Intent, string> = {
	'add-computer': '/devices',
	'new-device-token': '/tokens',
	'add-user': '/users',
	'new-api-token': '/api-tokens'
};

function isIntent(id: string): id is Intent {
	return id in PAGES;
}

export async function runAction(id: ActionId | string): Promise<void> {
	if (isIntent(id)) {
		requestIntent(id);
		const here = location.pathname === link(PAGES[id]);
		// eslint-disable-next-line svelte/no-navigation-without-resolve -- link() adds the base
		if (!here) await goto(link(PAGES[id]));
		return;
	}
	switch (id) {
		case 'quick-support':
			openQuickSupport();
			return;
		case 'accessibility':
			a11y.open = true;
			return;
		case 'theme':
			setTheme(theme.shown === 'dark' ? 'light' : 'dark');
			return;
		case 'sign-out':
			await signOut();
			return;
	}
}
