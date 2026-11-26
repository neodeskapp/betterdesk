/**
 * Keyboard focus is never left half hidden (WCAG 2.4.11). Browsers scroll
 * a focused control into view only when none of it shows: one that is
 * half under a pinned table column, half past the end of a row that
 * scrolls sideways or half under a sticky bar stays where it is. This
 * scrolls such a control fully into view with the smallest move, keeping
 * the scroll padding that base.css and the scroll boxes set for their
 * sticky parts.
 *
 * Only for focus from the keyboard (:focus-visible), so a click never
 * moves the page. The remote screen and other role=application parts
 * size themselves to their box and are left alone.
 */
export function revealFocus(root: Document = document): () => void {
	const onfocus = (e: FocusEvent) => {
		const el = e.target;
		if (!(el instanceof HTMLElement) || el === root.body) return;
		if (!el.matches(':focus-visible') || el.closest('[role="application"], [data-no-reveal]')) return;
		el.scrollIntoView({ block: 'nearest', inline: 'nearest' });
	};
	root.addEventListener('focusin', onfocus);
	return () => root.removeEventListener('focusin', onfocus);
}
