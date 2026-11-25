/**
 * Moving to a place of the chat page (A242): an address made with link(),
 * which already holds the base, and the query of the place.
 */
import { goto } from '$app/navigation';
import { link } from '#lib/nav.ts';

/** Goes to /chat with the query; replace keeps the browser's history as it is */
export function goChat(query: Record<string, string>, replace = false): void {
	const params = new URLSearchParams(query);
	// link() adds the base; resolve() is for route paths
	// eslint-disable-next-line svelte/no-navigation-without-resolve
	void goto(link(`/chat?${params}`), { replaceState: replace });
}

/** Goes to another page of the console, by an address made with link() */
export function goTo(href: string): void {
	// eslint-disable-next-line svelte/no-navigation-without-resolve
	void goto(href);
}
