/**
 * BetterDesk Web Remote Client — touch and touchpad input for tablet / fold devices.
 */
import type { MessageInit } from './generated/protos.ts';
import { RDInput, type RDInputRenderer, type RDRemotePoint } from './input.ts';

/** A finger as RDTouch reads it: a Touch, or a plain object with the same fields */
export interface RDTouchPoint {
	identifier: number;
	clientX: number;
	clientY: number;
}

/** A touch event as RDTouch reads it: a TouchEvent, or a plain object with the same fields */
export interface RDTouchEventLike {
	preventDefault(): void;
	touches: ArrayLike<RDTouchPoint>;
	changedTouches: ArrayLike<RDTouchPoint>;
}

/** A finger on the screen in direct mode: where it is and where it went down */
interface RDActiveTouch {
	x: number;
	y: number;
	startX: number;
	startY: number;
}

export class RDTouch {
	canvas: HTMLCanvasElement;
	renderer: RDInputRenderer;
	sendMessage: (msg: MessageInit) => void;
	mode: 'direct' | 'touchpad';
	enabled: boolean;
	_activeTouches: Map<number, RDActiveTouch>;
	_touchpadLast: { x: number; y: number } | null;
	_touchpadStart: { x: number; y: number } | null;
	/** Where the touchpad cursor is on the remote screen (set by the first touchpad move) */
	_touchpadCursor?: RDRemotePoint;
	/** Vertical distance of the two fingers of a touchpad pinch, last move */
	_touchpadPinchDy?: number | null;
	/** Middle of the two fingers of a direct-mode pinch, last move */
	_pinchMidY?: number | null;
	_longPressTimer: ReturnType<typeof setTimeout> | null;
	_longPressTriggered: boolean;
	_directPressed: boolean;
	/** Last position on the screen of a direct drag */
	_dragPos: RDRemotePoint | null;
	_lastMoveTime: number;
	_moveThrottleMs: number;
	_onTouchStart: (e: RDTouchEventLike) => void;
	_onTouchMove: (e: RDTouchEventLike) => void;
	_onTouchEnd: (e: RDTouchEventLike) => void;
	_onTouchCancel: (e: RDTouchEventLike) => void;

	constructor(canvas: HTMLCanvasElement, renderer: RDInputRenderer, sendMessage: (msg: MessageInit) => void) {
		this.canvas = canvas;
		this.renderer = renderer;
		this.sendMessage = sendMessage;
		this.mode = 'direct';
		this.enabled = false;
		this._activeTouches = new Map();
		this._touchpadLast = null;
		this._touchpadStart = null;
		this._longPressTimer = null;
		this._longPressTriggered = false;
		this._directPressed = false;
		this._dragPos = null;
		this._lastMoveTime = 0;
		this._moveThrottleMs = 16;

		this._onTouchStart = this._handleTouchStart.bind(this);
		this._onTouchMove = this._handleTouchMove.bind(this);
		this._onTouchEnd = this._handleTouchEnd.bind(this);
		this._onTouchCancel = this._handleTouchEnd.bind(this);
	}

	setMode(mode: string): void {
		this.mode = mode === 'touchpad' ? 'touchpad' : 'direct';
	}

	start(): void {
		if (this.enabled) return;
		const c = this.canvas;
		c.addEventListener('touchstart', this._onTouchStart, { passive: false });
		c.addEventListener('touchmove', this._onTouchMove, { passive: false });
		c.addEventListener('touchend', this._onTouchEnd, { passive: false });
		c.addEventListener('touchcancel', this._onTouchCancel, { passive: false });
		c.style.touchAction = 'none';
		this.enabled = true;
	}

	stop(): void {
		if (!this.enabled) return;
		const c = this.canvas;
		c.removeEventListener('touchstart', this._onTouchStart);
		c.removeEventListener('touchmove', this._onTouchMove);
		c.removeEventListener('touchend', this._onTouchEnd);
		c.removeEventListener('touchcancel', this._onTouchCancel);
		c.style.touchAction = '';
		clearTimeout(this._longPressTimer ?? undefined);
		// A drag in progress lets go of the button on the device
		if (this._directPressed && this._dragPos) {
			this._sendButton(RDInput.MOUSE_TYPE_UP, RDInput.MOUSE_BUTTON_LEFT, this._dragPos.x, this._dragPos.y);
		}
		this._dragPos = null;
		this._activeTouches.clear();
		this._touchpadLast = null;
		this._touchpadStart = null;
		this._longPressTriggered = false;
		this._directPressed = false;
		this.enabled = false;
	}

	_posFromTouch(touch: RDTouchPoint): RDRemotePoint | null {
		const rect = this.canvas.getBoundingClientRect();
		const canvasX = touch.clientX - rect.left;
		const canvasY = touch.clientY - rect.top;
		const pos = this.renderer.canvasToRemote(canvasX, canvasY);
		if (pos.x < 0 || pos.y < 0 || pos.x > this.renderer.remoteWidth || pos.y > this.renderer.remoteHeight) {
			return null;
		}
		return pos;
	}

	_sendMove(x: number, y: number): void {
		const now = performance.now();
		if (now - this._lastMoveTime < this._moveThrottleMs) return;
		this._lastMoveTime = now;
		this.sendMessage({
			mouseEvent: { mask: 0, x, y, modifiers: [] }
		});
	}

	_sendButton(type: number, button: number, x: number, y: number): void {
		const mask = type | (button << 3);
		this.sendMessage({
			mouseEvent: { mask, x, y, modifiers: [] }
		});
	}

	_sendWheel(dx: number, dy: number): void {
		this.sendMessage({
			mouseEvent: {
				mask: RDInput.MOUSE_TYPE_WHEEL,
				x: dx,
				y: dy,
				modifiers: []
			}
		});
	}

	_handleTouchStart(e: RDTouchEventLike): void {
		if (!this.enabled) return;
		e.preventDefault();

		if (this.mode === 'touchpad') {
			const first = e.touches[0];
			if (e.touches.length === 1 && first) {
				this._touchpadLast = { x: first.clientX, y: first.clientY };
				this._touchpadStart = { x: first.clientX, y: first.clientY };
			}
			return;
		}

		for (let i = 0; i < e.changedTouches.length; i++) {
			const touch = e.changedTouches[i];
			if (!touch) continue;
			this._activeTouches.set(touch.identifier, {
				x: touch.clientX,
				y: touch.clientY,
				startX: touch.clientX,
				startY: touch.clientY
			});

			if (e.touches.length === 1) {
				this._directPressed = false;
				const pos = this._posFromTouch(touch);
				if (!pos) continue;
				this._longPressTriggered = false;
				this._longPressTimer = setTimeout(() => {
					this._longPressTriggered = true;
					this._sendButton(RDInput.MOUSE_TYPE_DOWN, RDInput.MOUSE_BUTTON_RIGHT, pos.x, pos.y);
					this._sendButton(RDInput.MOUSE_TYPE_UP, RDInput.MOUSE_BUTTON_RIGHT, pos.x, pos.y);
				}, 550);
			}
		}
	}

	_handleTouchMove(e: RDTouchEventLike): void {
		if (!this.enabled) return;
		e.preventDefault();
		clearTimeout(this._longPressTimer ?? undefined);

		if (this.mode === 'touchpad') {
			const t0 = e.touches[0];
			const t1 = e.touches[1];
			if (e.touches.length === 2 && t0 && t1) {
				const dy = t0.clientY - t1.clientY;
				const prevDy = this._touchpadPinchDy;
				this._touchpadPinchDy = dy;
				if (prevDy != null && Math.abs(dy - prevDy) > 8) {
					this._sendWheel(0, dy > prevDy ? -1 : 1);
				}
				return;
			}
			if (e.touches.length === 1 && this._touchpadLast && t0) {
				const t = t0;
				const dx = t.clientX - this._touchpadLast.x;
				const dy = t.clientY - this._touchpadLast.y;
				this._touchpadLast = { x: t.clientX, y: t.clientY };
				const sensitivity = 2;
				const last = this._touchpadCursor || {
					x: this.renderer.remoteWidth / 2,
					y: this.renderer.remoteHeight / 2
				};
				const nx = Math.max(0, Math.min(this.renderer.remoteWidth, last.x + dx * sensitivity));
				const ny = Math.max(0, Math.min(this.renderer.remoteHeight, last.y + dy * sensitivity));
				this._touchpadCursor = { x: nx, y: ny };
				this._sendMove(nx, ny);
			}
			return;
		}

		const t0 = e.touches[0];
		const t1 = e.touches[1];
		if (e.touches.length === 2 && t0 && t1) {
			const midY = (t0.clientY + t1.clientY) / 2;
			const prev = this._pinchMidY;
			this._pinchMidY = midY;
			if (prev != null && Math.abs(midY - prev) > 6) {
				this._sendWheel(0, midY > prev ? -1 : 1);
			}
			return;
		}

		const touch = e.touches[0];
		if (!touch) return;
		const pos = this._posFromTouch(touch);
		if (!pos) return;
		const active = this._activeTouches.get(touch.identifier);
		if (
			active &&
			!this._directPressed &&
			!this._longPressTriggered &&
			(Math.abs(touch.clientX - active.startX) > 8 || Math.abs(touch.clientY - active.startY) > 8)
		) {
			this._sendButton(RDInput.MOUSE_TYPE_DOWN, RDInput.MOUSE_BUTTON_LEFT, pos.x, pos.y);
			this._directPressed = true;
		}
		this._dragPos = pos;
		this._sendMove(pos.x, pos.y);
	}

	_handleTouchEnd(e: RDTouchEventLike): void {
		if (!this.enabled) return;
		e.preventDefault();
		clearTimeout(this._longPressTimer ?? undefined);
		const longPressTriggered = this._longPressTriggered;
		this._longPressTriggered = false;
		this._pinchMidY = null;
		this._touchpadPinchDy = null;

		if (this.mode === 'touchpad') {
			const touch = e.changedTouches[0];
			if (e.changedTouches.length === 1 && e.touches.length === 0 && touch) {
				const moved =
					this._touchpadStart &&
					(Math.abs(touch.clientX - this._touchpadStart.x) > 8 ||
						Math.abs(touch.clientY - this._touchpadStart.y) > 8);
				if (!moved && this._touchpadCursor) {
					const p = this._touchpadCursor;
					this._sendButton(RDInput.MOUSE_TYPE_DOWN, RDInput.MOUSE_BUTTON_LEFT, p.x, p.y);
					this._sendButton(RDInput.MOUSE_TYPE_UP, RDInput.MOUSE_BUTTON_LEFT, p.x, p.y);
				}
			}
			if (e.touches.length === 0) {
				this._touchpadLast = null;
				this._touchpadStart = null;
			}
			return;
		}

		for (let i = 0; i < e.changedTouches.length; i++) {
			const touch = e.changedTouches[i];
			if (!touch) continue;
			this._activeTouches.delete(touch.identifier);
			if (e.touches.length === 0 && !longPressTriggered) {
				// A drag that ends outside the screen lets go where it last was on it
				const pos = this._posFromTouch(touch) || (this._directPressed ? this._dragPos : null);
				if (!pos) continue;
				if (this._directPressed) {
					this._sendButton(RDInput.MOUSE_TYPE_UP, RDInput.MOUSE_BUTTON_LEFT, pos.x, pos.y);
					this._dragPos = null;
				} else {
					this._sendButton(RDInput.MOUSE_TYPE_DOWN, RDInput.MOUSE_BUTTON_LEFT, pos.x, pos.y);
					this._sendButton(RDInput.MOUSE_TYPE_UP, RDInput.MOUSE_BUTTON_LEFT, pos.x, pos.y);
				}
				this._directPressed = false;
			}
		}
	}
}
