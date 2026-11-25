/**
 * Cmd as Ctrl for common shortcuts: on a Mac, when the remote computer is
 * not a Mac, Cmd with C, V, X, A, Z, S or F reaches the remote as Ctrl
 * with the same key, so copy, paste, cut, select all, undo, save and find
 * work as the person's hands expect. Cmd alone still is the remote's
 * Windows key, Cmd with any other key stays Cmd.
 *
 * Cmd's own key press is held until the next key says what it is for: one
 * of the seven turns it into Ctrl, anything else (or letting go) sends it
 * as Cmd. handle() returns the key events to send instead of the one given,
 * or null to let it pass as it is. The viewer's keyboard menu turns it off
 * ("Send Cmd as Ctrl for common shortcuts", per account in this browser).
 */

export const CMD_AS_CTRL_CODES: ReadonlySet<string> = new Set([
	'KeyC',
	'KeyV',
	'KeyX',
	'KeyA',
	'KeyZ',
	'KeyS',
	'KeyF'
]);

/** A key event to send to the remote screen */
export interface KeyOut extends KeyboardEventInit {
	type: 'keydown' | 'keyup';
	code: string;
}

function isCmd(code: string): boolean {
	return code === 'MetaLeft' || code === 'MetaRight';
}

function out(e: KeyboardEvent, patch: Partial<KeyOut> = {}): KeyOut {
	return {
		type: e.type === 'keyup' ? 'keyup' : 'keydown',
		key: e.key,
		code: e.code,
		location: e.location,
		repeat: e.repeat,
		ctrlKey: e.ctrlKey,
		shiftKey: e.shiftKey,
		altKey: e.altKey,
		metaKey: e.metaKey,
		...patch
	};
}

const CTRL_DOWN: KeyOut = { type: 'keydown', key: 'Control', code: 'ControlLeft', ctrlKey: true };
const CTRL_UP: KeyOut = { type: 'keyup', key: 'Control', code: 'ControlLeft' };

export class CmdAsCtrl {
	private on: () => boolean;
	/** Cmd is down here and not sent yet */
	private held: KeyOut | null = null;
	/** Cmd went to the remote as Cmd, or as Ctrl */
	private cmdSent = false;
	private ctrlSent = false;
	/** Keys sent with Ctrl in place of Cmd: their release goes the same way */
	private mapped = new Map<string, KeyOut>();

	/** on: a Mac here, not a Mac there, and the menu's switch on */
	constructor(on: () => boolean) {
		this.on = on;
	}

	handle(e: KeyboardEvent): KeyOut[] | null {
		if (!this.on() && !this.held && !this.ctrlSent && !this.mapped.size) return null;
		const down = e.type === 'keydown';
		if (isCmd(e.code)) {
			if (down) {
				if (this.held || this.ctrlSent || this.cmdSent) return e.repeat ? [] : null;
				this.held = out(e);
				return [];
			}
			if (this.ctrlSent) {
				this.ctrlSent = false;
				return [CTRL_UP];
			}
			if (this.held) {
				// Cmd alone: the Windows key, pressed and let go
				const press = this.held;
				this.held = null;
				return [press, out(e)];
			}
			this.cmdSent = false;
			return null;
		}
		if (!down) {
			const m = this.mapped.get(e.code);
			if (!m) return null;
			this.mapped.delete(e.code);
			return [out(e, { ctrlKey: true, metaKey: false })];
		}
		const cmd = e.metaKey && !e.ctrlKey && !e.altKey;
		if (cmd && CMD_AS_CTRL_CODES.has(e.code) && (this.held || this.ctrlSent || !this.cmdSent)) {
			const sent: KeyOut[] = [];
			if (!this.ctrlSent) sent.push(CTRL_DOWN);
			this.ctrlSent = true;
			this.held = null;
			const k = out(e, { ctrlKey: true, metaKey: false });
			this.mapped.set(e.code, k);
			sent.push(k);
			return sent;
		}
		if (this.held) {
			// Cmd with another key: Cmd it is
			const press = this.held;
			this.held = null;
			this.cmdSent = true;
			return [press, out(e)];
		}
		if (this.ctrlSent && e.metaKey) {
			this.ctrlSent = false;
			this.cmdSent = true;
			return [CTRL_UP, { type: 'keydown', key: 'Meta', code: 'MetaLeft', metaKey: true }, out(e)];
		}
		return null;
	}

	/** The focus went away: let go on the remote of what this sent in place of Cmd */
	reset(): KeyOut[] {
		const sent: KeyOut[] = [];
		for (const k of this.mapped.values()) sent.push({ ...k, type: 'keyup' });
		this.mapped.clear();
		if (this.ctrlSent) sent.push(CTRL_UP);
		this.ctrlSent = false;
		this.cmdSent = false;
		this.held = null;
		return sent;
	}
}
