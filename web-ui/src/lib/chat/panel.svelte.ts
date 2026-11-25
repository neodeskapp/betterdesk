/**
 * The chat panel (A242): the chat over any page of the console, from the
 * top bar's chat button or from a computer, a help request or a person.
 * openChat() opens a conversation there, or on the chat page when that is
 * the page in view.
 */

import { appPath } from '#lib/nav.ts';
import { goChat } from './nav.ts';
import type { InboxFilter, Section } from './types.ts';

class ChatPanel {
	open = $state(false);
	section = $state<Section>('support');
	filter = $state<InboxFilter>('open');
	conv = $state<string | null>(null);
	/** What had focus before the panel opened, for when it closes */
	opener: HTMLElement | null = null;
}

export const chatPanel = new ChatPanel();

export function sectionOf(conv: string): Section {
	return conv.startsWith('dm:') || conv.startsWith('group:') || conv.startsWith('old:') ? 'team' : 'support';
}

/** Opens a conversation (a device id, dm:..., group:...) or just the chat */
export function openChat(conv: string | null = null, opener: HTMLElement | null = null): void {
	if (typeof location !== 'undefined' && appPath(location.pathname) === '/chat') {
		// eslint-disable-next-line svelte/prefer-svelte-reactivity -- read once for an address
		const query = Object.fromEntries(new URLSearchParams(location.search));
		if (conv) {
			query.c = conv;
			query.s = sectionOf(conv);
		}
		goChat(query);
		return;
	}
	chatPanel.opener =
		opener ?? (document.activeElement instanceof HTMLElement ? document.activeElement : null);
	if (conv) {
		chatPanel.section = sectionOf(conv);
		chatPanel.conv = conv;
	}
	chatPanel.open = true;
}

export function closeChat(): void {
	chatPanel.open = false;
	const back = chatPanel.opener;
	chatPanel.opener = null;
	if (back?.isConnected) back.focus();
}
