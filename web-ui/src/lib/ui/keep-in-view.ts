/**
 * Keeps the focused field of a dialog or sheet in view when the phone's
 * on-screen keyboard takes part of the screen: when the visible area
 * shrinks, or focus moves to a field, the field scrolls into the part
 * that is left. Returns the function that stops it.
 */
export function keepFocusInView(root: HTMLElement): () => void {
	const vv = typeof window !== 'undefined' ? window.visualViewport : null;
	const reveal = () => {
		const el = document.activeElement;
		if (!(el instanceof HTMLElement) || !root.contains(el)) return;
		if (!el.matches('input, select, textarea, [contenteditable="true"]')) return;
		const r = el.getBoundingClientRect();
		const bottom = vv ? vv.offsetTop + vv.height : window.innerHeight;
		const top = vv ? vv.offsetTop : 0;
		if (r.bottom > bottom || r.top < top) el.scrollIntoView({ block: 'center' });
	};
	// The keyboard opens after focus moves: look again once it has
	const onfocus = () => requestAnimationFrame(reveal);
	root.addEventListener('focusin', onfocus);
	vv?.addEventListener('resize', reveal);
	return () => {
		root.removeEventListener('focusin', onfocus);
		vv?.removeEventListener('resize', reveal);
	};
}
