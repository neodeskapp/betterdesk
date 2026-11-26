/**
 * Open dialogs and sheets, the top one last (GUIDE section 11).
 *
 * A dialog opened while a sheet is open sits on top of it; any other new
 * dialog or sheet replaces what is open. Escape closes the top one only
 * (unless it is blocking). Closing a sheet also closes a dialog on it.
 * When a layer closes, focus goes back to what opened it.
 *
 * Everything in <body> outside the top layer is inert while it is open,
 * so screen readers in browse mode and on touch screens can't wander into
 * the page behind (aria-modal alone doesn't stop all of them). Elements
 * marked data-layer-live (the toast regions) stay live, so their messages
 * are still announced.
 *
 * Dialog.svelte and Sheet.svelte register here; pages don't.
 */

import { closePopups, overlays } from './popups.svelte.ts';

export type LayerKind = 'dialog' | 'sheet';

export interface LayerOptions {
	kind: LayerKind;
	/** The role="dialog" element */
	element: HTMLElement;
	/** The person asked to close it (Escape) */
	dismiss: () => void;
	/** Another layer replaced it, or the sheet under it closed: close without asking */
	replace: () => void;
	blocking: () => boolean;
}

interface Layer extends LayerOptions {
	opener: HTMLElement | null;
}

const stack: Layer[] = [];

/** Elements this module made inert, so it never undoes an inert set by others */
const madeInert = new Set<Element>();

/** Make every child of <body> but the top layer's inert; undo it when no layer is open */
function syncInert(): void {
	const top = stack.at(-1)?.element;
	for (const child of Array.from(document.body.children)) {
		const keep = !top || child.contains(top) || child.hasAttribute('data-layer-live');
		if (keep) {
			if (madeInert.delete(child)) child.removeAttribute('inert');
		} else if (!child.hasAttribute('inert')) {
			child.setAttribute('inert', '');
			madeInert.add(child);
		}
	}
	for (const el of madeInert) {
		if (!el.isConnected) madeInert.delete(el);
	}
}

function onKeydown(event: KeyboardEvent): void {
	// Menus, comboboxes and the like handle their own Escape first
	if (event.key !== 'Escape' || event.defaultPrevented) return;
	const top = stack.at(-1);
	if (!top) return;
	event.preventDefault();
	if (!top.blocking()) top.dismiss();
}

function activeElement(): HTMLElement | null {
	const el = document.activeElement;
	return el instanceof HTMLElement && el !== document.body ? el : null;
}

/**
 * Register an open dialog or sheet. Returns the function to call once it
 * has closed (after its markup is gone); that returns focus to the opener.
 */
export function openLayer(options: LayerOptions): () => void {
	closePopups();
	let opener = activeElement();
	for (let top = stack.at(-1); top; top = stack.at(-1)) {
		if (options.kind === 'dialog' && top.kind === 'sheet') break;
		stack.pop();
		// Opened from inside the layer it replaces: hand focus back to that one's opener later
		if (opener && top.element.contains(opener)) opener = top.opener;
		top.replace();
	}
	const layer: Layer = { ...options, opener };
	if (!stack.length) document.addEventListener('keydown', onKeydown);
	stack.push(layer);
	overlays.layers = stack.length;
	syncInert();

	return function closed() {
		const index = stack.indexOf(layer);
		if (index >= 0) {
			const above = stack.splice(index);
			for (const l of above.slice(1).reverse()) l.replace();
		}
		overlays.layers = stack.length;
		syncInert();
		if (!stack.length) document.removeEventListener('keydown', onKeydown);
		// Only take focus back when it went nowhere (the layer's markup is gone)
		// or is still inside it; not when another layer has taken it
		const active = document.activeElement;
		const lost = !active || active === document.body || layer.element.contains(active);
		if (lost && layer.opener?.isConnected) layer.opener.focus({ preventScroll: true });
	};
}

/** Number of open dialogs and sheets */
export function openLayers(): number {
	return stack.length;
}
