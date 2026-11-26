/**
 * The remote screen has the keyboard: a key event whose target is a
 * viewer's screen (its canvas, or a display's tile). Every key there goes
 * to the remote computer, so the console's own shortcuts (search, Escape
 * for its panels) leave such events alone. F8, or Ctrl+Alt+Shift+G on
 * keyboards where F8 needs Fn or is a media key, gives the keyboard back
 * to the console (RemoteViewer.svelte, DisplayWindow.svelte).
 */
/** The key that leaves the remote screen: F8 alone, or Ctrl+Alt+Shift+G (the physical key, on any layout) */
export function isReleaseKey(e: KeyboardEvent): boolean {
	if (e.metaKey) return false;
	if (e.key === 'F8') return !e.ctrlKey && !e.altKey && !e.shiftKey;
	return e.code === 'KeyG' && e.ctrlKey && e.altKey && e.shiftKey;
}

export function fromRemoteScreen(e: Event): boolean {
	const target = e.target;
	return target instanceof Element && !!target.closest('.session-canvas, .tile-canvas');
}
