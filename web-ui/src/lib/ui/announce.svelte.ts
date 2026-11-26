/**
 * Status messages for screen readers (WCAG 4.1.3): `announce(text)` says
 * it politely, `announce(text, { assertive: true })` right away.
 * Announcer.svelte (mounted once in the root layout) holds the two live
 * regions. They exist before any message and hang off <body> outside the
 * page, so a dialog that makes the page inert doesn't silence them.
 *
 * Each message first empties its region and fills it a moment later, so
 * the same text twice ("Copied", then "Copied" again) is read twice. The
 * text goes away after a while, so it isn't met again when reading the
 * end of the page.
 */

type Kind = 'polite' | 'assertive';

class AnnouncerState {
	polite = $state('');
	assertive = $state('');
}

/** What Announcer.svelte shows */
export const announcer = new AnnouncerState();

/** Time between emptying a region and filling it: screen readers need the change */
const FILL_DELAY = 100;
/** How long the text stays in the region */
const CLEAR_AFTER = 10_000;

const timers: Record<Kind, ReturnType<typeof setTimeout>[]> = { polite: [], assertive: [] };

function clearTimers(kind: Kind) {
	for (const t of timers[kind]) clearTimeout(t);
	timers[kind] = [];
}

export function announce(text: string, { assertive = false }: { assertive?: boolean } = {}): void {
	const message = text.trim();
	if (!message) return;
	const kind: Kind = assertive ? 'assertive' : 'polite';
	clearTimers(kind);
	announcer[kind] = '';
	timers[kind].push(
		setTimeout(() => {
			announcer[kind] = message;
			timers[kind].push(setTimeout(() => (announcer[kind] = ''), CLEAR_AFTER));
		}, FILL_DELAY)
	);
}

/** Empty both regions and drop pending messages (tests, sign-out) */
export function resetAnnouncer(): void {
	clearTimers('polite');
	clearTimers('assertive');
	announcer.polite = '';
	announcer.assertive = '';
}
