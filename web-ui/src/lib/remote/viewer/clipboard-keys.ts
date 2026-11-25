/**
 * The keys and events of the clipboard in the viewer's window, ahead of
 * the remote screen's own key handling (capture phase):
 * - Ctrl+V (Cmd+V on a Mac, Shift+Insert) on the remote screen is held,
 *   the browser's paste event sends this computer's clipboard to the
 *   remote, then the held keys go on in their order, so the remote
 *   pastes what was just sent. Without a paste event within PASTE_WAIT_MS
 *   the keys go on anyway and the remote pastes its own clipboard.
 * - Ctrl+C or Ctrl+X goes to the remote as usual and arms the copy wait.
 * - Any key or click on the remote screen is a user gesture: content the
 *   remote copied that waits is written to this computer's clipboard then.
 * - On a Mac, with a remote that isn't one, Cmd with C, V, X, A, Z, S or F
 *   goes over as Ctrl (cmd-as-ctrl.ts), the paste above first.
 */
import { PasteGate, isCopyKey, isPasteKey } from '../clipboard-bridge.svelte.ts';
import { CmdAsCtrl, type KeyOut } from './cmd-as-ctrl.ts';
import type { Viewer } from '../viewer.svelte.ts';

/** How long the held keys wait for the clipboard to go out once the paste event came */
const SEND_WAIT_MS = 10_000;

export function isMacPlatform(): boolean {
	const nav = navigator as Navigator & { userAgentData?: { platform?: string } };
	const platform = nav.userAgentData?.platform || nav.platform || '';
	return /mac|iphone|ipad/i.test(platform);
}

/** The remote screen that has the keyboard: a session canvas or a display tile inside root */
export function focusedScreen(doc: Document, root: Element | null): HTMLCanvasElement | null {
	const el = doc.activeElement;
	if (!(el instanceof HTMLCanvasElement) || !el.matches('.session-canvas, .tile-canvas')) return null;
	if (root && !root.contains(el)) return null;
	return el;
}

export interface ClipboardKeysOptions {
	/** The viewer's own element: only its screens count */
	root(): Element | null;
	/** The viewer is on screen (in the console it stays mounted on other pages) */
	shown(): boolean;
}

export function attachClipboardKeys(viewer: Viewer, opts: ClipboardKeysOptions): () => void {
	const mac = isMacPlatform();
	let replaying = false;
	let target: HTMLCanvasElement | null = null;
	const cmdAsCtrl = new CmdAsCtrl(() => mac && viewer.cmdAsCtrlOn());

	/** Key events to the screen, as its own handler takes them */
	const send = (screen: HTMLCanvasElement | null, keys: KeyOut[]) => {
		if (!screen?.isConnected) return;
		replaying = true;
		try {
			for (const { type, ...init } of keys)
				screen.dispatchEvent(
					new KeyboardEvent(type, { ...init, bubbles: true, cancelable: true, composed: true })
				);
		} finally {
			replaying = false;
		}
	};
	const asIs = (e: KeyboardEvent): KeyOut => ({
		type: e.type === 'keyup' ? 'keyup' : 'keydown',
		key: e.key,
		code: e.code,
		location: e.location,
		repeat: e.repeat,
		ctrlKey: e.ctrlKey,
		shiftKey: e.shiftKey,
		altKey: e.altKey,
		metaKey: e.metaKey
	});

	// The held keys go to the screen that had them, Cmd turned into Ctrl where it is for that
	const gate = new PasteGate((e) => send(target, cmdAsCtrl.handle(e) ?? [asIs(e)]));

	const onKey = (e: KeyboardEvent) => {
		if (replaying) return;
		if (gate.take(e)) {
			// Held for the paste: nobody else sees it now, the browser does nothing with it
			e.stopImmediatePropagation();
			e.preventDefault();
			return;
		}
		if (!opts.shown()) return;
		const screen = focusedScreen(document, opts.root());
		if (!screen) return;
		if (e.type === 'keydown') {
			viewer.clipboard.gesture();
			if (isPasteKey(e, mac) && !viewer.clipboardOff()) {
				target = screen;
				gate.hold(e);
				// Not prevented: the browser fires its paste event with the clipboard
				e.stopImmediatePropagation();
				return;
			}
			if (isCopyKey(e, mac)) viewer.clipboard.copyKey();
		}
		const keys = cmdAsCtrl.handle(e);
		if (keys === null) return;
		e.stopImmediatePropagation();
		e.preventDefault();
		send(screen, keys);
	};

	const onPaste = (e: ClipboardEvent) => {
		if (!opts.shown()) return;
		const held = gate.held;
		if (!held && !focusedScreen(document, opts.root())) return;
		e.preventDefault();
		e.stopImmediatePropagation();
		if (held) gate.extend(SEND_WAIT_MS);
		void viewer.clipboard.fromPaste(e.clipboardData).finally(() => {
			if (held) gate.release();
		});
	};

	const onPointer = (e: PointerEvent) => {
		if (!opts.shown()) return;
		const root = opts.root();
		if (!root || !(e.target instanceof Element) || !root.contains(e.target)) return;
		// The clipboard's own button does what its state says on its click
		if (e.target.closest('[data-clipboard]')) return;
		viewer.clipboard.gesture();
	};

	const onBlur = () => {
		// The keys held for a paste go on: the remote must not keep Ctrl pressed
		if (gate.held) gate.release();
		send(focusedScreen(document, opts.root()) ?? target, cmdAsCtrl.reset());
	};

	window.addEventListener('keydown', onKey, true);
	window.addEventListener('keyup', onKey, true);
	window.addEventListener('pointerdown', onPointer, true);
	window.addEventListener('blur', onBlur);
	document.addEventListener('paste', onPaste, true);
	void viewer.clipboard.watchPermission();
	return () => {
		window.removeEventListener('keydown', onKey, true);
		window.removeEventListener('keyup', onKey, true);
		window.removeEventListener('pointerdown', onPointer, true);
		window.removeEventListener('blur', onBlur);
		document.removeEventListener('paste', onPaste, true);
		gate.clear();
	};
}
