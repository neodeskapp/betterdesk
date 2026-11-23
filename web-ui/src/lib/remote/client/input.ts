/**
 * NeoDesk Web Remote Client - Input Manager
 * Captures keyboard and mouse events and converts them to RustDesk protocol messages
 */
import type { MessageInit } from './generated/protos.ts';
import { RDKeyboardEncoder, type RDKeyboardMode } from './keyboard-encoder.ts';
import { RDKeyboardScancode } from './keyboard-scancode.ts';

/** What RDInput needs of the renderer (RDRenderer): the remote screen's size and the canvas → remote mapping */
export interface RDInputRenderer {
	remoteWidth: number;
	remoteHeight: number;
	canvasToRemote(canvasX: number, canvasY: number): { x: number; y: number };
}

/** A position on the remote screen */
export interface RDRemotePoint {
	x: number;
	y: number;
}

export class RDInput {
	/** The canvas element to capture events from */
	canvas: HTMLCanvasElement;
	/** Renderer for coordinate mapping */
	renderer: RDInputRenderer;
	/** Callback to send protocol messages */
	sendMessage: (msg: MessageInit) => void;
	enabled: boolean;
	/** Pointer lock active */
	pointerLocked: boolean;
	/** Currently pressed keys (code → metadata) */
	pressedKeys: Map<string, { key: string }>;
	/** Mouse buttons held on the device (RustDesk button → last position) */
	_buttonsDown: Map<number, RDRemotePoint>;
	/** PeerInfo.platform for Map-mode scancode target */
	peerPlatform: string;
	/**
	 * Keyboard wire mode: 'Legacy' | 'Map' | 'Auto'.
	 * Auto resolves to Map (RustDesk native default).
	 */
	keyboardMode: RDKeyboardMode;
	_lastMouseSendTime: number;
	_mouseThrottleMs: number;
	_onMouseMove: (e: MouseEvent) => void;
	_onMouseDown: (e: MouseEvent) => void;
	_onMouseUp: (e: MouseEvent) => void;
	_onWheel: (e: WheelEvent) => void;
	_onKeyDown: (e: KeyboardEvent) => void;
	_onKeyUp: (e: KeyboardEvent) => void;
	_onCompositionStart: () => void;
	_onCompositionEnd: (e: CompositionEvent) => void;
	_onContextMenu: (e: Event) => void;
	_onPointerLockChange: () => void;
	_onWindowBlur: () => void;
	_onVisibilityChange: () => void;
	/** IME / dead-key composition in progress */
	_composing: boolean;
	/** Physical codes suppressed while awaiting compositionend */
	_deadCodes: Set<string>;
	/** While set, mouse moves, presses and the wheel go nowhere: a local file drag is under way */
	_mouseSuppressed: boolean;

	/**
	 * @param canvas - The canvas element to capture events from
	 * @param renderer - Renderer for coordinate mapping
	 * @param sendMessage - Callback to send protocol messages
	 */
	constructor(canvas: HTMLCanvasElement, renderer: RDInputRenderer, sendMessage: (msg: MessageInit) => void) {
		this.canvas = canvas;
		this.renderer = renderer;
		this.sendMessage = sendMessage;

		this.enabled = false;
		this.pointerLocked = false;
		this.pressedKeys = new Map();
		this._buttonsDown = new Map();

		this.peerPlatform = '';
		this.keyboardMode = 'Auto';

		this._lastMouseSendTime = 0;
		this._mouseThrottleMs = 16;

		this._onMouseMove = this._handleMouseMove.bind(this);
		this._onMouseDown = this._handleMouseDown.bind(this);
		this._onMouseUp = this._handleMouseUp.bind(this);
		this._onWheel = this._handleWheel.bind(this);
		this._onKeyDown = this._handleKeyDown.bind(this);
		this._onKeyUp = this._handleKeyUp.bind(this);
		this._onCompositionStart = this._handleCompositionStart.bind(this);
		this._onCompositionEnd = this._handleCompositionEnd.bind(this);
		this._onContextMenu = (e) => e.preventDefault();
		this._onPointerLockChange = this._handlePointerLockChange.bind(this);
		this._onWindowBlur = this._handleWindowBlur.bind(this);
		this._onVisibilityChange = this._handleVisibilityChange.bind(this);

		this._composing = false;
		this._deadCodes = new Set();

		this._mouseSuppressed = false;
	}

	/**
	 * @param platform - PeerInfo.platform from login response
	 */
	setPeerPlatform(platform: string): void {
		this.peerPlatform = platform || '';
	}

	/**
	 * @param mode 'Legacy' | 'Map' | 'Auto' (anything else is Auto)
	 */
	setKeyboardMode(mode: string): void {
		this.keyboardMode = mode === 'Legacy' || mode === 'Map' || mode === 'Auto' ? mode : 'Auto';
	}

	/**
	 * Release all keys held on the remote side (toolbar / recovery).
	 */
	resetKeyboard(): void {
		this._releaseAllKeys(true);
	}

	start(): void {
		if (this.enabled) return;

		const c = this.canvas;
		c.addEventListener('mousemove', this._onMouseMove);
		c.addEventListener('mousedown', this._onMouseDown);
		// A button pressed on the screen may be let go anywhere: listen on the window
		window.addEventListener('mouseup', this._onMouseUp);
		c.addEventListener('wheel', this._onWheel, { passive: false });
		c.addEventListener('contextmenu', this._onContextMenu);

		document.addEventListener('keydown', this._onKeyDown);
		document.addEventListener('keyup', this._onKeyUp);
		document.addEventListener('compositionstart', this._onCompositionStart);
		document.addEventListener('compositionend', this._onCompositionEnd);
		document.addEventListener('pointerlockchange', this._onPointerLockChange);
		window.addEventListener('blur', this._onWindowBlur);
		document.addEventListener('visibilitychange', this._onVisibilityChange);

		c.tabIndex = 0;
		c.focus();

		this.enabled = true;
	}

	stop(): void {
		if (!this.enabled) return;

		this._releaseAllKeys(false);
		this._releaseAllButtons();

		const c = this.canvas;
		c.removeEventListener('mousemove', this._onMouseMove);
		c.removeEventListener('mousedown', this._onMouseDown);
		window.removeEventListener('mouseup', this._onMouseUp);
		c.removeEventListener('wheel', this._onWheel);
		c.removeEventListener('contextmenu', this._onContextMenu);

		document.removeEventListener('keydown', this._onKeyDown);
		document.removeEventListener('keyup', this._onKeyUp);
		document.removeEventListener('compositionstart', this._onCompositionStart);
		document.removeEventListener('compositionend', this._onCompositionEnd);
		document.removeEventListener('pointerlockchange', this._onPointerLockChange);
		window.removeEventListener('blur', this._onWindowBlur);
		document.removeEventListener('visibilitychange', this._onVisibilityChange);

		if (this.pointerLocked) {
			document.exitPointerLock();
		}

		this.pressedKeys.clear();
		this._deadCodes.clear();
		this._composing = false;
		this.enabled = false;
	}

	requestPointerLock(): void {
		this.canvas.requestPointerLock();
	}

	exitPointerLock(): void {
		if (this.pointerLocked) {
			document.exitPointerLock();
		}
	}

	static MOUSE_TYPE_DOWN = 1;
	static MOUSE_TYPE_UP = 2;
	static MOUSE_TYPE_WHEEL = 3;

	static MOUSE_BUTTON_LEFT = 1;
	static MOUSE_BUTTON_RIGHT = 2;
	static MOUSE_BUTTON_MIDDLE = 4;

	_encoder(): typeof RDKeyboardEncoder {
		return RDKeyboardEncoder;
	}

	_getScancodeLib(): typeof RDKeyboardScancode {
		return RDKeyboardScancode;
	}

	_handleMouseMove(e: MouseEvent): void {
		if (!this.enabled || this._mouseSuppressed) return;

		const now = performance.now();
		if (now - this._lastMouseSendTime < this._mouseThrottleMs) return;
		this._lastMouseSendTime = now;

		const pos = this._getRemotePosition(e);
		if (!pos) return;

		this.sendMessage({
			mouseEvent: {
				mask: 0,
				x: pos.x,
				y: pos.y,
				modifiers: this._getMouseModifiers(e)
			}
		});
	}

	_handleMouseDown(e: MouseEvent): void {
		if (!this.enabled || this._mouseSuppressed) return;
		e.preventDefault();
		this.canvas.focus();

		const pos = this._getRemotePosition(e);
		if (!pos) return;

		let button = 0;
		switch (e.button) {
			case 0:
				button = RDInput.MOUSE_BUTTON_LEFT;
				break;
			case 1:
				button = RDInput.MOUSE_BUTTON_MIDDLE;
				break;
			case 2:
				button = RDInput.MOUSE_BUTTON_RIGHT;
				break;
		}

		if (button) {
			this._buttonsDown.set(button, { x: pos.x, y: pos.y });
			const mask = RDInput.MOUSE_TYPE_DOWN | (button << 3);
			this.sendMessage({
				mouseEvent: {
					mask: mask,
					x: pos.x,
					y: pos.y,
					modifiers: this._getMouseModifiers(e)
				}
			});
		}
	}

	/** Release a button pressed on the screen, also outside it (the position is kept on the screen's edge) */
	_handleMouseUp(e: MouseEvent): void {
		if (!this.enabled) return;

		let button = 0;
		switch (e.button) {
			case 0:
				button = RDInput.MOUSE_BUTTON_LEFT;
				break;
			case 1:
				button = RDInput.MOUSE_BUTTON_MIDDLE;
				break;
			case 2:
				button = RDInput.MOUSE_BUTTON_RIGHT;
				break;
		}
		if (!button || !this._buttonsDown.has(button)) return;
		this._buttonsDown.delete(button);
		if (e.target === this.canvas) e.preventDefault();

		const pos = this._getClampedRemotePosition(e);
		this.sendMessage({
			mouseEvent: {
				mask: RDInput.MOUSE_TYPE_UP | (button << 3),
				x: pos.x,
				y: pos.y,
				modifiers: this._getMouseModifiers(e)
			}
		});
	}

	/** Stop or resume sending mouse moves, presses and the wheel (a button held on the device is still let go) */
	setMouseSuppressed(suppressed: boolean): void {
		this._mouseSuppressed = !!suppressed;
	}

	/** A left click on the device at a point of the canvas (CSS pixels from its top left corner) */
	clickAtCanvas(x: number, y: number): void {
		if (!this.enabled) return;
		const pos = this.renderer.canvasToRemote(x, y);
		if (pos.x < 0 || pos.y < 0 || pos.x > this.renderer.remoteWidth || pos.y > this.renderer.remoteHeight) {
			return;
		}
		const left = RDInput.MOUSE_BUTTON_LEFT << 3;
		this.sendMessage({
			mouseEvent: { mask: RDInput.MOUSE_TYPE_DOWN | left, x: pos.x, y: pos.y, modifiers: [] }
		});
		this.sendMessage({
			mouseEvent: { mask: RDInput.MOUSE_TYPE_UP | left, x: pos.x, y: pos.y, modifiers: [] }
		});
	}

	/** Paste on the device (Ctrl+V) */
	sendCtrlV(): void {
		this._pressWithCtrl('KeyV', 'v');
	}

	/** Press and let go of a key with Ctrl held, as the keyboard would send it */
	_pressWithCtrl(code: string, key: string): void {
		if (!this.enabled) return;
		const ctrlHeld = this.pressedKeys.has('ControlLeft') || this.pressedKeys.has('ControlRight');
		if (!ctrlHeld) {
			this.pressedKeys.set('ControlLeft', { key: 'Control' });
			this._sendKeyForCode('ControlLeft', 'Control', true, false, null);
		}
		this.pressedKeys.set(code, { key });
		this._sendKeyForCode(code, key, true, false, null);
		this.pressedKeys.delete(code);
		this._sendKeyForCode(code, key, false, false, null);
		if (!ctrlHeld) {
			this.pressedKeys.delete('ControlLeft');
			this._sendKeyForCode('ControlLeft', 'Control', false, false, null);
		}
	}

	/** Send a release for every button still held on the device */
	_releaseAllButtons(): void {
		for (const [button, pos] of this._buttonsDown) {
			this.sendMessage({
				mouseEvent: { mask: RDInput.MOUSE_TYPE_UP | (button << 3), x: pos.x, y: pos.y, modifiers: [] }
			});
		}
		this._buttonsDown.clear();
	}

	_handleWheel(e: WheelEvent): void {
		if (!this.enabled || this._mouseSuppressed) return;
		e.preventDefault();

		const delta = this._normalizeWheelDelta(e);
		if (!delta) return;

		this.sendMessage({
			mouseEvent: {
				mask: RDInput.MOUSE_TYPE_WHEEL,
				x: delta.dx,
				y: delta.dy,
				modifiers: this._getMouseModifiers(e)
			}
		});
	}

	_handleKeyDown(e: KeyboardEvent): void {
		if (!this.enabled) return;
		if (this._isInputFocused()) return;

		// Dead keys need browser composition; preventDefault would drop ^ etc.
		const isDead = e.key === 'Dead';
		if (!isDead) {
			e.preventDefault();
		}
		e.stopPropagation();

		const keyCode = e.code;
		if (isDead) {
			this._deadCodes.add(keyCode);
			this._composing = true;
			// Keep Shift / modifiers tracked; do not emit Digit Map for Dead.
			if (e.shiftKey) this._ensureShiftTracked(e);
			this.pressedKeys.set(keyCode, { key: e.key });
			return;
		}

		const isRepeat = e.repeat || this.pressedKeys.has(keyCode);
		this.pressedKeys.set(keyCode, { key: e.key });

		if (isRepeat) {
			this._sendKey(e, false, true);
		} else {
			this._sendKey(e, true, false);
		}
	}

	_handleKeyUp(e: KeyboardEvent): void {
		if (!this.enabled) return;
		if (this._isInputFocused()) return;

		const wasDead = this._deadCodes.has(e.code) || e.key === 'Dead';
		if (!wasDead) {
			e.preventDefault();
		}
		e.stopPropagation();

		this.pressedKeys.delete(e.code);
		if (wasDead) {
			this._deadCodes.delete(e.code);
			return;
		}
		this._sendKey(e, false, false);
	}

	_handleCompositionStart(): void {
		if (!this.enabled || this._isInputFocused()) return;
		this._composing = true;
	}

	_handleCompositionEnd(e: CompositionEvent | null): void {
		if (!this.enabled || this._isInputFocused()) return;
		this._composing = false;
		this._deadCodes.clear();
		const data = e && typeof e.data === 'string' ? e.data : '';
		if (!data) return;
		const enc = this._encoder();
		for (const ch of data) {
			const keyEvent = enc.encodeCharPress(ch);
			if (keyEvent) this.sendMessage({ keyEvent });
		}
	}

	/**
	 * If the browser reports shiftKey but Shift was never keydown'd into
	 * pressedKeys, re-assert ShiftLeft on the wire so Map Digit/symbol
	 * scancodes are not applied unshifted (bare "6" instead of "^").
	 */
	_ensureShiftTracked(e: KeyboardEvent | null): void {
		if (!e || !e.shiftKey) return;
		if (this.pressedKeys.has('ShiftLeft') || this.pressedKeys.has('ShiftRight')) {
			return;
		}
		const enc = this._encoder();
		const evt = enc.buildShiftReassertDown(this._getScancodeLib(), this.peerPlatform);
		if (evt) {
			this.sendMessage({ keyEvent: evt });
		}
		this.pressedKeys.set('ShiftLeft', { key: 'Shift' });
	}

	_handleWindowBlur(): void {
		if (this.enabled) {
			this._releaseAllKeys(false);
			this._releaseAllButtons();
		}
	}

	_handleVisibilityChange(): void {
		if (this.enabled && typeof document !== 'undefined' && document.visibilityState === 'hidden') {
			this._releaseAllKeys(false);
		}
	}

	_releaseAllKeys(forceAllModifiers: boolean): void {
		const codes = [...this.pressedKeys.keys()];
		for (const code of codes) {
			const meta = this.pressedKeys.get(code);
			this._sendKeyForCode(code, meta?.key || '', false, false, null);
		}
		this.pressedKeys.clear();

		if (forceAllModifiers) {
			this._releaseAllModifiers();
		}
	}

	_releaseAllModifiers(): void {
		const enc = this._encoder();
		const events = enc.buildModifierReleaseEvents(
			this._getScancodeLib(),
			this.keyboardMode,
			this.peerPlatform
		);
		for (const keyEvent of events) {
			this.sendMessage({ keyEvent });
		}
	}

	_sendKey(e: KeyboardEvent, down: boolean, press: boolean): void {
		this._sendKeyForCode(e.code, e.key, down, press, e);
	}

	_sendKeyForCode(code: string, key: string, down: boolean, press: boolean, e: KeyboardEvent | null): void {
		const enc = this._encoder();
		const scLib = this._getScancodeLib();

		if (down && e && code !== 'ShiftLeft' && code !== 'ShiftRight') {
			this._ensureShiftTracked(e);
		}

		const keyEvent = enc.encodeKeyEvent({
			code,
			key,
			down,
			press,
			e,
			keyboardMode: this.keyboardMode,
			peerPlatform: this.peerPlatform,
			pressedCodes: this.pressedKeys.keys(),
			scancodeLib: scLib
		});

		if (keyEvent) {
			this.sendMessage({ keyEvent });
		}
	}

	_normalizeWheelDelta(e: WheelEvent): { dx: number; dy: number } | null {
		let rawDx = e.deltaX;
		let rawDy = e.deltaY;

		if (e.shiftKey && rawDy !== 0 && rawDx === 0) {
			rawDx = rawDy;
			rawDy = 0;
		}

		if (rawDx === 0 && rawDy === 0) return null;

		let dx = 0;
		let dy = 0;
		if (Math.abs(rawDx) > Math.abs(rawDy)) {
			dx = rawDx > 0 ? -1 : 1;
		} else if (rawDy !== 0) {
			dy = rawDy > 0 ? -1 : 1;
		}

		if (dx === 0 && dy === 0) return null;
		return { dx, dy };
	}

	_handlePointerLockChange(): void {
		this.pointerLocked = document.pointerLockElement === this.canvas;
	}

	_getRemotePosition(e: { clientX: number; clientY: number }): RDRemotePoint | null {
		const rect = this.canvas.getBoundingClientRect();
		const canvasX = e.clientX - rect.left;
		const canvasY = e.clientY - rect.top;

		const pos = this.renderer.canvasToRemote(canvasX, canvasY);

		if (pos.x < 0 || pos.y < 0 || pos.x > this.renderer.remoteWidth || pos.y > this.renderer.remoteHeight) {
			return null;
		}

		return pos;
	}

	/** The remote position of a pointer event, moved onto the screen's edge when it is outside */
	_getClampedRemotePosition(e: { clientX: number; clientY: number }): RDRemotePoint {
		const rect = this.canvas.getBoundingClientRect();
		const pos = this.renderer.canvasToRemote(e.clientX - rect.left, e.clientY - rect.top);
		return {
			x: Math.max(0, Math.min(this.renderer.remoteWidth, pos.x)),
			y: Math.max(0, Math.min(this.renderer.remoteHeight, pos.y))
		};
	}

	_getMouseModifiers(e: {
		shiftKey: boolean;
		ctrlKey: boolean;
		altKey: boolean;
		metaKey: boolean;
	}): number[] {
		const enc = this._encoder();
		const mods: number[] = [];
		if (e.shiftKey) mods.push(enc.MOD_SHIFT);
		if (e.ctrlKey) mods.push(enc.MOD_CTRL);
		if (e.altKey) mods.push(enc.MOD_ALT);
		if (e.metaKey) mods.push(enc.MOD_META);
		return mods;
	}

	_isInputFocused(): boolean {
		const el = document.activeElement;
		if (!el || el === this.canvas) return false;
		if ('isContentEditable' in el && el.isContentEditable) return true;
		const tag = el.tagName?.toLowerCase();
		if (tag === 'input' || tag === 'textarea' || tag === 'select') {
			if (('type' in el && el.type === 'hidden') || ('offsetParent' in el && el.offsetParent === null)) {
				return false;
			}
			return true;
		}
		// A focused viewer control (tab, button, link, menu item, disclosure) keeps
		// its keys, so Enter, Space and the arrow keys work on it. The remote
		// screen gets the keys while it (or nothing in particular) has focus.
		if (tag === 'button' || tag === 'a' || tag === 'summary') return true;
		if (typeof el.getAttribute === 'function' && el.getAttribute('role')) return true;
		return false;
	}

	close(): void {
		this.stop();
	}
}
