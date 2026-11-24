/**
 * The remote screen has the keyboard: a key event whose target is a
 * viewer's screen (its canvas, or a display's tile). Every key there goes
 * to the remote computer, so the console's own shortcuts (search, Escape
 * for its panels) leave such events alone. F8 gives the keyboard back to
 * the console (RemoteViewer.svelte).
 */
export function fromRemoteScreen(e: Event): boolean {
	const target = e.target;
	return target instanceof Element && !!target.closest('.session-canvas, .tile-canvas');
}
