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
	'new-device-token': '/devices/add?tab=tokens',
	'add-user': '/users',
	'new-api-token': '/account'
};

function isIntent(id: string): id is Intent {
	return id in PAGES;
}

export async function runAction(id: ActionId | string): Promise<void> {
	if (isIntent(id)) {
		requestIntent(id);
		const target = link(PAGES[id]);
		// The page takes the intent itself when it shows already (with its tab)
		if (location.pathname + location.search === target) return;
		// eslint-disable-next-line svelte/no-navigation-without-resolve -- link() adds the base
		await goto(target);
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
