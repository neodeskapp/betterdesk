/**
 * Things to do that the search in the top bar (Ctrl K) finds next to pages
 * and settings: each with the check that shows its button elsewhere, so the
 * search never offers what the account can't do. Pure; run-action.ts does
 * them.
 */

import type { SearchAction } from './search.ts';

export type ActionId =
	| 'quick-support'
	| 'add-computer'
	| 'new-device-token'
	| 'add-user'
	| 'new-api-token'
	| 'chat-settings'
	| 'accessibility'
	| 'theme'
	| 'sign-out';

export interface ActionContext {
	can: (permission: string) => boolean;
	/** The account may make API tokens (lib/people/apiTokens.ts mayMakeTokens) */
	mayMakeTokens: boolean;
	/** The theme this browser shows now */
	dark: boolean;
	t: (key: string) => string;
}

interface ActionDef {
	id: ActionId;
	icon: string;
	title: (ctx: ActionContext) => string;
	/** Other words people type, English and German */
	keywords: string;
	allowed: (ctx: ActionContext) => boolean;
}

const ACTIONS: readonly ActionDef[] = [
	{
		id: 'quick-support',
		icon: 'support_agent',
		title: (c) => c.t('onboard.support.button'),
		keywords: 'support session link help customer fernwartung hilfe kunde sitzungslink',
		allowed: (c) => c.can('device.connect')
	},
	{
		id: 'add-computer',
		icon: 'add',
		title: (c) => c.t('onboard.invite.button'),
		keywords:
			'add computer device invite enroll install join new computer hinzufügen gerät einladen einladung installieren beitreten',
		allowed: (c) => c.can('enrollment.approve')
	},
	{
		id: 'new-device-token',
		icon: 'add',
		title: (c) => c.t('search.action_new_device_token'),
		keywords: 'device token enrollment create new gerätetoken registrierung erstellen neu',
		allowed: (c) => c.can('enrollment.manage')
	},
	{
		id: 'add-user',
		icon: 'add',
		title: (c) => c.t('users.add_user'),
		keywords: 'add user account person create new benutzer konto anlegen hinzufügen neu',
		allowed: (c) => c.can('user.create')
	},
	{
		id: 'new-api-token',
		icon: 'add',
		title: (c) => c.t('search.action_new_api_token'),
		keywords: 'api token key create new schlüssel erstellen neu',
		allowed: (c) => c.mayMakeTokens
	},
	{
		// The chat's settings for the server (A259): Chat with computers and the mail about a waiting person
		id: 'chat-settings',
		icon: 'settings',
		title: (c) => c.t('chat.server_settings'),
		keywords:
			'chat with computers mail waiting answer preview settings chat mit computern mail wartet antwort vorschau einstellungen',
		allowed: (c) => c.can('server.config') && (c.can('chat.support') || c.can('chat.team'))
	},
	{
		id: 'accessibility',
		icon: 'accessibility_new',
		title: (c) => c.t('search.action_accessibility'),
		keywords:
			'accessibility a11y contrast motion underline links readable font shortcuts live updates pause barrierefreiheit kontrast bewegung links schrift kurzbefehle aktualisierung',
		allowed: () => true
	},
	{
		id: 'theme',
		icon: 'contrast',
		title: (c) => c.t(c.dark ? 'search.action_theme_light' : 'search.action_theme_dark'),
		keywords: 'theme dark light mode dunkel hell modus design',
		allowed: () => true
	},
	{
		id: 'sign-out',
		icon: 'logout',
		title: (c) => c.t('user_menu.sign_out'),
		keywords: 'sign out log out logout abmelden ausloggen',
		allowed: () => true
	}
];

/** The actions this account may do, in the console language */
export function searchActions(ctx: ActionContext): SearchAction[] {
	return ACTIONS.filter((a) => a.allowed(ctx)).map((a) => ({
		id: a.id,
		title: a.title(ctx),
		keywords: a.keywords,
		icon: a.icon
	}));
}

export const ACTION_IDS: readonly ActionId[] = ACTIONS.map((a) => a.id);
