/**
 * RustDesk-compatible KeyEvent encoder (mirrors upstream keyboard.rs client path).
 *
 * Map/Auto: scancode in chr for physical keys (including Shift/Ctrl/Alt); lock_modes only.
 * Auto/Windows hybrid: letters + modifiers/nav → Map; unshifted digits/symbols → Legacy chr.
 * Legacy: controlKey for nav/modifiers; lowercase chr + legacy_modifiers for printable keys.
 */
import type { ControlKeyName, KeyEventInit } from './generated/protos.ts';
import { RDKeyboardScancode } from './keyboard-scancode.ts';

/** The keyboard wire mode the viewer picks; Auto resolves per key (see shouldUseMapScancode) */
export type RDKeyboardMode = 'Legacy' | 'Map' | 'Auto';

/** Which modifiers are held, from the pressed key codes */
export interface RDModifierState {
	shift: boolean;
	ctrl: boolean;
	alt: boolean;
	meta: boolean;
}

/** What the encoder uses of a scancode table: RDKeyboardScancode or a stand-in */
export interface RDScancodeLib {
	MODIFIER_CODES?: string[];
	codeToScancode(code: string, peerPlatform?: string | null): number | null;
}

export interface RDEncodeKeyOptions {
	code: string;
	key: string;
	down: boolean;
	press: boolean;
	e?: KeyboardEvent | null;
	keyboardMode: RDKeyboardMode;
	peerPlatform?: string;
	pressedCodes: Iterable<string>;
	scancodeLib?: RDScancodeLib | null;
}

/** DOM code → RustDesk ControlKey name (Legacy fallback). */
const KEY_MAP: Record<string, ControlKeyName> = {
	Escape: 'Escape',
	Backspace: 'Backspace',
	Tab: 'Tab',
	Enter: 'Return',
	ShiftLeft: 'Shift',
	ShiftRight: 'RShift',
	ControlLeft: 'Control',
	ControlRight: 'RControl',
	AltLeft: 'Alt',
	AltRight: 'RAlt',
	MetaLeft: 'Meta',
	MetaRight: 'RWin',
	Pause: 'Pause',
	CapsLock: 'CapsLock',
	Space: 'Space',
	PageUp: 'PageUp',
	PageDown: 'PageDown',
	End: 'End',
	Home: 'Home',
	ArrowLeft: 'LeftArrow',
	ArrowUp: 'UpArrow',
	ArrowRight: 'RightArrow',
	ArrowDown: 'DownArrow',
	PrintScreen: 'Snapshot',
	Insert: 'Insert',
	Delete: 'Delete',
	ScrollLock: 'Scroll',
	NumLock: 'NumLock',
	F1: 'F1',
	F2: 'F2',
	F3: 'F3',
	F4: 'F4',
	F5: 'F5',
	F6: 'F6',
	F7: 'F7',
	F8: 'F8',
	F9: 'F9',
	F10: 'F10',
	F11: 'F11',
	F12: 'F12',
	Numpad0: 'Numpad0',
	Numpad1: 'Numpad1',
	Numpad2: 'Numpad2',
	Numpad3: 'Numpad3',
	Numpad4: 'Numpad4',
	Numpad5: 'Numpad5',
	Numpad6: 'Numpad6',
	Numpad7: 'Numpad7',
	Numpad8: 'Numpad8',
	Numpad9: 'Numpad9',
	NumpadMultiply: 'Multiply',
	NumpadAdd: 'Add',
	NumpadSubtract: 'Subtract',
	NumpadDecimal: 'Decimal',
	NumpadDivide: 'Divide',
	NumpadEnter: 'NumpadEnter',
	ContextMenu: 'Apps',
	AudioVolumeMute: 'VolumeMute',
	AudioVolumeDown: 'VolumeDown',
	AudioVolumeUp: 'VolumeUp'
};

/** Physical key → Legacy chr (lowercase), upstream legacy_keyboard_mode. */
const LEGACY_CHAR_MAP: Record<string, string> = {
	Digit1: '1',
	Digit2: '2',
	Digit3: '3',
	Digit4: '4',
	Digit5: '5',
	Digit6: '6',
	Digit7: '7',
	Digit8: '8',
	Digit9: '9',
	Digit0: '0',
	KeyA: 'a',
	KeyB: 'b',
	KeyC: 'c',
	KeyD: 'd',
	KeyE: 'e',
	KeyF: 'f',
	KeyG: 'g',
	KeyH: 'h',
	KeyI: 'i',
	KeyJ: 'j',
	KeyK: 'k',
	KeyL: 'l',
	KeyM: 'm',
	KeyN: 'n',
	KeyO: 'o',
	KeyP: 'p',
	KeyQ: 'q',
	KeyR: 'r',
	KeyS: 's',
	KeyT: 't',
	KeyU: 'u',
	KeyV: 'v',
	KeyW: 'w',
	KeyX: 'x',
	KeyY: 'y',
	KeyZ: 'z',
	Comma: ',',
	Period: '.',
	Semicolon: ';',
	Quote: "'",
	BracketLeft: '[',
	BracketRight: ']',
	Slash: '/',
	Backslash: '\\',
	Minus: '-',
	Equal: '=',
	Backquote: '`'
};

/** Upstream legacy_keyboard_mode: no wire event for lock keys. */
const SILENT_LOCK_CODES: string[] = ['CapsLock', 'NumLock', 'ScrollLock'];

export const RDKeyboardEncoder = {
	MOD_ALT: 1,
	MOD_CAPS_LOCK: 3,
	MOD_CTRL: 4,
	MOD_META: 23,
	MOD_SHIFT: 29,
	MOD_NUM_LOCK: 63,

	SILENT_LOCK_CODES,

	KEY_MAP,

	LEGACY_CHAR_MAP,

	isLetterCode(code: string): boolean {
		return /^Key[A-Z]$/.test(code);
	},

	isNumpadCode(code: string): boolean {
		return /^Numpad/.test(code);
	},

	/**
	 * Whether this key should use Map scancode encoding.
	 */
	shouldUseMapScancode(
		code: string,
		keyboardMode: RDKeyboardMode,
		peerPlatform: string,
		modState: RDModifierState,
		scancodeLib?: RDScancodeLib | null
	): boolean {
		if (keyboardMode === 'Legacy') return false;
		if (keyboardMode === 'Map') return true;

		const platform = (peerPlatform || '').toLowerCase();
		if (platform !== 'windows') return false;

		if (this.isLetterCode(code)) return true;
		if (this.KEY_MAP[code]) return true;

		const scLib = this._getScancodeLib(scancodeLib);
		if (modState.shift || modState.ctrl || modState.alt || modState.meta) {
			return scLib.codeToScancode(code, peerPlatform) != null;
		}
		return false;
	},

	modifierStateFromPressed(pressedCodes: Iterable<string>): RDModifierState {
		const set: Set<string> = pressedCodes instanceof Set ? pressedCodes : new Set(pressedCodes);
		return {
			shift: set.has('ShiftLeft') || set.has('ShiftRight'),
			ctrl: set.has('ControlLeft') || set.has('ControlRight'),
			alt: set.has('AltLeft') || set.has('AltRight'),
			meta: set.has('MetaLeft') || set.has('MetaRight')
		};
	},

	/**
	 * Port client::legacy_modifiers — exclude self-referential control keys.
	 */
	legacyModifiers(code: string, modState: RDModifierState): number[] {
		const mods: number[] = [];
		if (modState.alt && code !== 'AltLeft' && code !== 'AltRight') {
			mods.push(this.MOD_ALT);
		}
		if (modState.shift && code !== 'ShiftLeft' && code !== 'ShiftRight') {
			mods.push(this.MOD_SHIFT);
		}
		if (modState.ctrl && code !== 'ControlLeft' && code !== 'ControlRight') {
			mods.push(this.MOD_CTRL);
		}
		if (modState.meta && code !== 'MetaLeft' && code !== 'MetaRight') {
			mods.push(this.MOD_META);
		}
		return mods;
	},

	/**
	 * Port add_lock_modes_modifiers (Caps/Num only).
	 */
	lockModeModifiers(code: string, e: KeyboardEvent | null): number[] {
		const mods: number[] = [];
		if (!e || typeof e.getModifierState !== 'function') return mods;
		if (this.isLetterCode(code) && e.getModifierState('CapsLock')) {
			mods.push(this.MOD_CAPS_LOCK);
		}
		if (this.isNumpadCode(code) && e.getModifierState('NumLock')) {
			mods.push(this.MOD_NUM_LOCK);
		}
		return mods;
	},

	/**
	 * Apply Shift XOR CapsLock case for Legacy ASCII letters (matches RustDesk client).
	 */
	resolveLegacyLetterCase(e: KeyboardEvent | null): string {
		const key = e?.key;
		if (!key || key.length !== 1 || !e || !this.isLetterCode(e.code)) return key || '';
		const cp = key.codePointAt(0);
		if (cp === undefined || cp < 0x41 || cp > 0x7a) return key;
		if (cp > 0x5a && cp < 0x61) return key;

		const caps = typeof e.getModifierState === 'function' && e.getModifierState('CapsLock');
		const upper = e.shiftKey !== caps;
		if (upper) {
			return String.fromCodePoint(cp <= 0x5a ? cp : cp - 32);
		}
		return String.fromCodePoint(cp >= 0x61 ? cp : cp + 32);
	},

	/**
	 * Legacy chr already encodes Shift/Caps; strip them unless Ctrl/Alt/Meta hotkey.
	 */
	stripResolvedCharModifiers(mods: number[], e: KeyboardEvent | null): number[] {
		if (!e || e.ctrlKey || e.altKey || e.metaKey) return mods;
		return mods.filter((m) => m !== this.MOD_SHIFT && m !== this.MOD_CAPS_LOCK);
	},

	legacyCharCodePoint(code: string, key: string, e: KeyboardEvent | null): number | null {
		if (this.isLetterCode(code) && e) {
			const resolved = this.resolveLegacyLetterCase(e);
			if (resolved && resolved.length === 1) {
				return resolved.codePointAt(0) ?? null;
			}
		}
		const mapped = this.LEGACY_CHAR_MAP[code];
		if (mapped) return mapped.codePointAt(0) ?? null;
		if (!key || key.length === 0 || key === 'Unidentified' || key === 'Dead') {
			return null;
		}
		return key.codePointAt(0) ?? null;
	},

	/**
	 * AltGr on Windows appears as Ctrl+Alt; for printable Legacy chr omit Ctrl+Alt.
	 */
	applyAltGrLegacyFilter(code: string, e: KeyboardEvent | null, mods: number[]): number[] {
		if (this.KEY_MAP[code]) return mods;
		const altGraph = e && typeof e.getModifierState === 'function' && e.getModifierState('AltGraph');
		if (!altGraph) return mods;
		return mods.filter((m) => m !== this.MOD_CTRL && m !== this.MOD_ALT);
	},

	_getScancodeLib(scancodeLib?: RDScancodeLib | null): RDScancodeLib {
		if (scancodeLib) return scancodeLib;
		return RDKeyboardScancode;
	},

	/**
	 * Inject a composed / typed character without a DOM KeyboardEvent
	 * (dead-key compositionend, toolbar type-text single chars).
	 * @param ch - one Unicode character (uses first code point)
	 */
	encodeCharPress(ch: string): KeyEventInit | null {
		if (!ch || typeof ch !== 'string') return null;
		const cp = ch.codePointAt(0);
		if (cp == null) return null;
		// BMP Latin-1 and ASCII → Legacy chr (RustDesk host path).
		if (cp <= 0xff) {
			return {
				chr: cp,
				down: true,
				press: true,
				modifiers: [],
				mode: 'Legacy'
			};
		}
		return {
			unicode: cp,
			down: true,
			press: true,
			modifiers: [],
			mode: 'Legacy'
		};
	},

	/**
	 * KeyEvent.seq payload for typing a string as remote keystrokes.
	 */
	encodeTextSeq(text: string): KeyEventInit | null {
		if (!text || typeof text !== 'string') return null;
		return {
			seq: text,
			down: true,
			press: true,
			modifiers: [],
			mode: 'Legacy'
		};
	},

	/**
	 * Map-mode ShiftLeft keydown used when e.shiftKey is set but Shift was
	 * never recorded in pressedKeys (sticky / missed keydown desync).
	 */
	buildShiftReassertDown(scancodeLib: RDScancodeLib | null, peerPlatform = ''): KeyEventInit {
		const scLib = this._getScancodeLib(scancodeLib);
		const sc = scLib.codeToScancode('ShiftLeft', peerPlatform);
		if (sc != null) {
			return {
				chr: sc,
				down: true,
				press: false,
				modifiers: [],
				mode: 'Map'
			};
		}
		return {
			controlKey: 'Shift',
			down: true,
			press: false,
			modifiers: [],
			mode: 'Legacy'
		};
	},

	/**
	 * Build zero or one RustDesk keyEvent payload.
	 */
	encodeKeyEvent(opts: RDEncodeKeyOptions): KeyEventInit | null {
		const {
			code,
			key,
			down,
			press,
			e = null,
			keyboardMode,
			peerPlatform = '',
			pressedCodes,
			scancodeLib = null
		} = opts;

		// Dead keys (e.g. PL Shift+6 → ^) must not emit Map Digit scancodes;
		// RDInput waits for compositionend and sends encodeCharPress instead.
		if (key === 'Dead') {
			return null;
		}

		const modState = this.modifierStateFromPressed(pressedCodes);
		const scLib = this._getScancodeLib(scancodeLib);
		const controlKey = this.KEY_MAP[code];
		const useMap = this.shouldUseMapScancode(code, keyboardMode, peerPlatform, modState, scLib);

		if (this.SILENT_LOCK_CODES.includes(code)) {
			return null;
		}

		if (useMap) {
			const sc = scLib.codeToScancode(code, peerPlatform);
			if (sc != null) {
				return {
					chr: sc,
					down,
					press,
					modifiers: this.lockModeModifiers(code, e),
					mode: 'Map'
				};
			}
			if (controlKey) {
				let modifiers = this.legacyModifiers(code, modState);
				modifiers = modifiers.concat(this.lockModeModifiers(code, e));
				return {
					controlKey,
					down,
					press,
					modifiers,
					mode: 'Legacy'
				};
			}
		}

		if (controlKey) {
			let modifiers = this.legacyModifiers(code, modState);
			modifiers = modifiers.concat(this.lockModeModifiers(code, e));
			return {
				controlKey,
				down,
				press,
				modifiers,
				mode: 'Legacy'
			};
		}

		const chr = this.legacyCharCodePoint(code, key, e);
		if (chr == null) return null;

		let modifiers = this.legacyModifiers(code, modState);
		modifiers = modifiers.concat(this.lockModeModifiers(code, e));
		modifiers = this.applyAltGrLegacyFilter(code, e, modifiers);
		modifiers = this.stripResolvedCharModifiers(modifiers, e);

		return {
			chr,
			down,
			press,
			modifiers,
			mode: 'Legacy'
		};
	},

	/**
	 * Build modifier keyup events for toolbar recovery.
	 */
	buildModifierReleaseEvents(
		scancodeLib: RDScancodeLib | null,
		keyboardMode: RDKeyboardMode,
		peerPlatform = ''
	): KeyEventInit[] {
		const lib = this._getScancodeLib(scancodeLib);
		const codes = lib.MODIFIER_CODES || [
			'ShiftLeft',
			'ShiftRight',
			'ControlLeft',
			'ControlRight',
			'AltLeft',
			'AltRight',
			'MetaLeft',
			'MetaRight'
		];
		const platform = (peerPlatform || '').toLowerCase();
		const useMapRelease = keyboardMode === 'Map' || (keyboardMode === 'Auto' && platform === 'windows');

		const events: KeyEventInit[] = [];
		for (const code of codes) {
			if (useMapRelease) {
				const sc = lib.codeToScancode(code, peerPlatform);
				if (sc != null) {
					events.push({
						chr: sc,
						down: false,
						press: false,
						modifiers: [],
						mode: 'Map'
					});
					continue;
				}
			}
			const ck = this.KEY_MAP[code];
			if (ck) {
				events.push({
					controlKey: ck,
					down: false,
					press: false,
					modifiers: [],
					mode: 'Legacy'
				});
			}
		}
		return events;
	}
};
