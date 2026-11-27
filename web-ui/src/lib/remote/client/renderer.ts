/**
 * NeoDesk Web Remote Client - Canvas Renderer
 * Renders one display's decoded video frames on a canvas with the cursor
 * over them. The canvas may sit in another window of the page (a display
 * opened in its own window): the renderer then draws on that window's
 * animation frames.
 *
 * The last picture stays until the next one comes, so moving the cursor,
 * resizing or panning a picture shown at its own size redraws at once
 * instead of waiting for the device's next frame. A resize draws it again
 * before it returns: resizing clears the canvas, and a cleared canvas
 * shown for one frame is a black flash.
 *
 * The canvas is not desynchronized: Chrome draws such a canvas past the
 * page's compositor and blanks it for a moment whenever the page around it
 * changes (a menu, tooltip or focus ring over it, the layout moving).
 */
import { viewTransform } from './display-layout.ts';
import type { ICursorData, ICursorPosition } from './generated/protos.ts';
import type { RDDecodedFrame } from './video.ts';

/** Scale and offset of the remote display on the canvas (canvas pixels) */
export interface RDRendererTransform {
	scale: number;
	offsetX: number;
	offsetY: number;
}

export interface RDRendererStats {
	fps: number;
	framesRendered: number;
	remoteWidth: number;
	remoteHeight: number;
	canvasWidth: number;
	canvasHeight: number;
	scaleMode: string;
	scale: number;
}

export interface RDRendererOptions {
	/**
	 * The window the canvas is in (default: the canvas' own). Given, the
	 * canvas is in a window of its own and follows that window's size: no
	 * page code there resizes it.
	 */
	win?: Window;
}

export class RDRenderer {
	/** The rendering canvas element */
	canvas: HTMLCanvasElement;
	/** The window whose animation frames and pixel ratio the canvas follows */
	win: Window;
	ctx: CanvasRenderingContext2D | null;
	/** Offscreen canvas for cursor */
	cursorCanvas: HTMLCanvasElement;
	cursorCtx: CanvasRenderingContext2D | null;
	/** Remote display width */
	remoteWidth: number;
	/** Remote display height */
	remoteHeight: number;
	/** Where the display sits on the device's desktop: mouse events and the cursor use desktop points */
	origin: { x: number; y: number };
	/** Desktop units per picture pixel (pointScale in displays.ts) */
	units: number;
	/** Scale mode: fit | fill | 1:1 | stretch (anything else scales like fit) */
	scaleMode: string;
	/** How far a picture larger than the canvas is moved (0 to 1, 1:1 only); it follows the pointer */
	pan: { x: number; y: number };
	/** Current scale/offset applied */
	transform: RDRendererTransform;
	/** Current cursor image */
	cursorImage: ImageBitmap | null;
	/** Cursor hotspot */
	cursorHotspot: { x: number; y: number };
	/** Cursor position (desktop point) */
	cursorPos: { x: number; y: number };
	/** Show remote cursor */
	showCursor: boolean;
	/** Frames rendered counter */
	framesRendered: number;
	/** Last FPS measurement time */
	_fpsTime: number;
	/** Frames in current second */
	_fpsCount: number;
	/** Current FPS */
	fps: number;
	/** Animation frame ID */
	_rafId: number;
	/** Latest frame to render */
	_pendingFrame: RDDecodedFrame | null;
	/** The picture on the canvas, kept for redraws */
	_shownFrame: RDDecodedFrame | null;
	/** Redraw the shown picture on the next animation frame */
	_dirty: boolean;
	_renderLoopActive: boolean;
	_onPointerMove: (e: PointerEvent) => void;
	/** Stops following the window the canvas is in (set for a window of its own) */
	_unfollow: (() => void) | null = null;
	/** Called after the canvas size changed (the client asks the peer for a keyframe) */
	onResizeRefresh?: () => void;
	/** Called with true once a remote cursor image is ready */
	onCursorReady?: (ready: boolean) => void;

	/**
	 * @param canvas - The rendering canvas element
	 */
	constructor(canvas: HTMLCanvasElement, opts: RDRendererOptions = {}) {
		this.canvas = canvas;
		this.win = opts.win ?? canvas.ownerDocument?.defaultView ?? window;
		this.ctx = canvas.getContext('2d', { alpha: false });
		this.cursorCanvas = canvas.ownerDocument.createElement('canvas');
		this.cursorCtx = this.cursorCanvas.getContext('2d');

		this.remoteWidth = 0;
		this.remoteHeight = 0;
		this.origin = { x: 0, y: 0 };
		this.units = 1;

		this.scaleMode = 'fit';
		this.pan = { x: 0.5, y: 0.5 };

		this.transform = { scale: 1, offsetX: 0, offsetY: 0 };

		this.cursorImage = null;
		this.cursorHotspot = { x: 0, y: 0 };
		this.cursorPos = { x: 0, y: 0 };
		this.showCursor = true;

		this.framesRendered = 0;
		this._fpsTime = 0;
		this._fpsCount = 0;
		this.fps = 0;

		this._rafId = 0;
		this._pendingFrame = null;
		this._shownFrame = null;
		this._dirty = false;
		this._renderLoopActive = false;

		this._onPointerMove = (e) => this.panTo(e.offsetX, e.offsetY);
		canvas.addEventListener('pointermove', this._onPointerMove);
		if (opts.win) this._followWindow(opts.win);
	}

	/** Resize with the window the canvas is in, and with its box (the bar above it can wrap) */
	_followWindow(win: Window): void {
		const onResize = () => this.resize();
		win.addEventListener('resize', onResize);
		const RO = (win as Window & typeof globalThis).ResizeObserver;
		const box = this.canvas.parentElement;
		let ro: ResizeObserver | null = null;
		if (typeof RO === 'function' && box) {
			ro = new RO(onResize);
			ro.observe(box);
		}
		this._unfollow = () => {
			win.removeEventListener('resize', onResize);
			ro?.disconnect();
		};
	}

	_dpr(): number {
		return this.win.devicePixelRatio || 1;
	}

	/**
	 * Set remote display dimensions and recalculate transform
	 */
	setRemoteSize(width: number, height: number): void {
		if (this.remoteWidth === width && this.remoteHeight === height) return;
		this.remoteWidth = width;
		this.remoteHeight = height;
		this._updateTransform();
	}

	/** Where the display sits on the device's desktop, and desktop units per picture pixel */
	setPlace(origin: { x: number; y: number }, units = 1): void {
		this.origin = { x: origin.x, y: origin.y };
		this.units = units > 0 ? units : 1;
		this._dirty = true;
	}

	/**
	 * Recalculate canvas size (call on window resize or fullscreen toggle)
	 */
	resize(): void {
		const container = this.canvas.parentElement;
		if (!container) return;

		const rect = container.getBoundingClientRect();
		const dpr = this._dpr();
		const width = Math.round(rect.width * dpr);
		const height = Math.round(rect.height * dpr);
		if (width <= 0 || height <= 0) return;

		const cssWidth = rect.width + 'px';
		const cssHeight = rect.height + 'px';
		const dimensionsChanged =
			this.canvas.width !== width ||
			this.canvas.height !== height ||
			this.canvas.style.width !== cssWidth ||
			this.canvas.style.height !== cssHeight;

		// Assigning canvas.width/height clears the bitmap. Mobile browsers
		// emit visualViewport scroll events while a finger moves, so avoid
		// clearing the remote desktop when its actual viewport is unchanged.
		if (!dimensionsChanged) {
			this._updateTransform();
			return;
		}

		this.canvas.width = width;
		this.canvas.height = height;
		this.canvas.style.width = cssWidth;
		this.canvas.style.height = cssHeight;

		this._updateTransform();
		// Resizing cleared the canvas: the kept picture goes back before the browser shows it
		if (this._shownFrame) this._renderFrame(this._shownFrame);
		else this._dirty = true;

		// Request a keyframe after resize to avoid blurry/corrupted frames
		if (this.onResizeRefresh) {
			this.onResizeRefresh();
		}
	}

	/**
	 * Set scale mode
	 * @param mode - 'fit' | 'fill' | '1:1' | 'stretch'
	 */
	setScaleMode(mode: string): void {
		this.scaleMode = mode;
		this._updateTransform();
		this._dirty = true;
	}

	/**
	 * At its own size, a picture larger than the canvas follows the pointer:
	 * the pointer's share of the canvas is the share of the picture in view.
	 * @param cssX, cssY - the pointer, CSS pixels from the canvas' top left
	 */
	panTo(cssX: number, cssY: number): void {
		if (this.scaleMode !== '1:1') return;
		const dpr = this._dpr();
		const cw = this.canvas.width;
		const ch = this.canvas.height;
		if (!cw || !ch) return;
		const x = Math.max(0, Math.min(1, (cssX * dpr) / cw));
		const y = Math.max(0, Math.min(1, (cssY * dpr) / ch));
		if (x === this.pan.x && y === this.pan.y) return;
		this.pan = { x, y };
		const before = this.transform;
		this._updateTransform();
		if (before.offsetX !== this.transform.offsetX || before.offsetY !== this.transform.offsetY) {
			this._dirty = true;
		}
	}

	/**
	 * Calculate transform based on canvas size, remote size, and scale mode
	 */
	_updateTransform(): void {
		if (!this.remoteWidth || !this.remoteHeight) return;

		const cw = this.canvas.width;
		const ch = this.canvas.height;
		const rw = this.remoteWidth;
		const rh = this.remoteHeight;

		switch (this.scaleMode) {
			case 'fill': {
				// Fill canvas, cropping excess, preserving aspect ratio
				const scale = Math.max(cw / rw, ch / rh);
				this.transform = { scale, offsetX: (cw - rw * scale) / 2, offsetY: (ch - rh * scale) / 2 };
				return;
			}
			case '1:1':
				// Native resolution: centred, or moved with the pointer where it is larger
				this.transform = viewTransform({ w: rw, h: rh }, { w: cw, h: ch }, '1:1', this._dpr(), this.pan);
				return;
			case 'stretch':
				// Stretch to fill canvas (ignores aspect ratio); handled separately in render
				this.transform = { scale: 1, offsetX: 0, offsetY: 0 };
				return;
			default:
				// Fit entire remote display inside canvas, preserving aspect ratio
				this.transform = viewTransform({ w: rw, h: rh }, { w: cw, h: ch }, 'fit');
		}
	}

	/**
	 * Convert canvas coordinates to picture pixels of the remote display
	 */
	canvasToRemote(canvasX: number, canvasY: number): { x: number; y: number } {
		const dpr = this._dpr();
		const px = canvasX * dpr;
		const py = canvasY * dpr;

		if (this.scaleMode === 'stretch') {
			return {
				x: Math.round((px / this.canvas.width) * this.remoteWidth),
				y: Math.round((py / this.canvas.height) * this.remoteHeight)
			};
		}

		const { scale, offsetX, offsetY } = this.transform;
		return {
			x: Math.round((px - offsetX) / scale),
			y: Math.round((py - offsetY) / scale)
		};
	}

	/**
	 * Start the render loop
	 */
	startRenderLoop(): void {
		if (this._renderLoopActive) return;
		this._renderLoopActive = true;
		this._fpsTime = performance.now();
		this._renderTick();
	}

	/**
	 * Stop the render loop
	 */
	stopRenderLoop(): void {
		this._renderLoopActive = false;
		if (this._rafId) {
			try {
				this.win.cancelAnimationFrame(this._rafId);
			} catch {
				// The canvas' window is gone
			}
			this._rafId = 0;
		}
	}

	/**
	 * Queue a VideoFrame for rendering
	 */
	pushFrame(frame: RDDecodedFrame): void {
		// Close previous pending frame if not yet rendered
		if (this._pendingFrame) {
			this._pendingFrame.close();
		}
		this._pendingFrame = frame;
	}

	/**
	 * Internal render tick
	 */
	_renderTick(): void {
		if (!this._renderLoopActive) return;

		if (this._pendingFrame) {
			// Count FPS only for genuinely new frames from the peer: every WebCodecs frame is one,
			// a JMuxer frame only when video was fed since the last one
			if (!('_isNew' in this._pendingFrame) || this._pendingFrame._isNew) {
				this._fpsCount++;
			}
			const frame = this._pendingFrame;
			this._pendingFrame = null;
			if (this._shownFrame && this._shownFrame !== frame) this._shownFrame.close();
			this._shownFrame = frame;
			this._renderFrame(frame);
			this.framesRendered++;
		} else if (this._dirty && this._shownFrame) {
			this._renderFrame(this._shownFrame);
		}
		this._dirty = false;

		// FPS calculation (actual new video frames per second from peer)
		const now = performance.now();
		if (now - this._fpsTime >= 1000) {
			this.fps = this._fpsCount;
			this._fpsCount = 0;
			this._fpsTime = now;
		}

		try {
			this._rafId = this.win.requestAnimationFrame(() => this._renderTick());
		} catch {
			// The canvas' window closed: nothing more to draw
			this._renderLoopActive = false;
		}
	}

	/**
	 * Render a single video frame to canvas
	 * @param frame - VideoFrame or fallback proxy with _source
	 */
	_renderFrame(frame: RDDecodedFrame): void {
		// Update remote dimensions from frame
		this.setRemoteSize(frame.displayWidth, frame.displayHeight);

		const ctx = this.ctx;
		const cw = this.canvas.width;
		const ch = this.canvas.height;
		// Use _source (the video element) for fallback frames, otherwise frame itself (VideoFrame)
		const drawSource = '_source' in frame ? frame._source : frame;
		if (!ctx) return;

		// Clear canvas
		ctx.fillStyle = '#000000';
		ctx.fillRect(0, 0, cw, ch);

		try {
			if (this.scaleMode === 'stretch') {
				ctx.drawImage(drawSource, 0, 0, cw, ch);
			} else {
				const { scale, offsetX, offsetY } = this.transform;
				ctx.drawImage(drawSource, offsetX, offsetY, this.remoteWidth * scale, this.remoteHeight * scale);
			}
		} catch {
			// A frame closed elsewhere (decoder reset) has nothing to draw
			return;
		}

		// Draw cursor overlay
		if (this.showCursor && this.cursorImage) {
			this._drawCursor(ctx);
		}
	}

	/**
	 * Update cursor image from CursorData message
	 */
	async updateCursor(cursorData: ICursorData): Promise<void> {
		try {
			// Proto field is 'colors' (RGBA pixel data)
			const pixelData = cursorData.colors;
			if (!pixelData || !cursorData.width || !cursorData.height) return;

			const w = cursorData.width;
			const h = cursorData.height;
			const expectedLen = w * h * 4;

			// Normalize protobuf bytes to Uint8Array
			let bytes = pixelData instanceof Uint8Array ? pixelData : new Uint8Array(pixelData);

			// Skip zstd-compressed cursor data (magic: 28 b5 2f fd)
			if (
				bytes.length >= 4 &&
				bytes[0] === 0x28 &&
				bytes[1] === 0xb5 &&
				bytes[2] === 0x2f &&
				bytes[3] === 0xfd
			) {
				return;
			}

			// Validate RGBA buffer size — must be exactly width * height * 4
			if (bytes.length < expectedLen) {
				return;
			}
			if (bytes.length > expectedLen) {
				bytes = bytes.subarray(0, expectedLen);
			}

			this.cursorHotspot.x = cursorData.hotx || 0;
			this.cursorHotspot.y = cursorData.hoty || 0;

			// Create ImageData from RGBA bytes (copy to avoid protobuf buffer issues)
			const imgData = new ImageData(new Uint8ClampedArray(bytes), w, h);

			// Convert to ImageBitmap for efficient drawing
			if (this.cursorImage) {
				this.cursorImage.close();
			}
			this.cursorImage = await createImageBitmap(imgData);
			this._dirty = true;

			// Signal that we have a valid remote cursor (CSS hides local cursor)
			if (this.onCursorReady) {
				this.onCursorReady(true);
			}
		} catch {
			// Silently skip invalid cursor data to prevent crash
		}
	}

	/**
	 * Update cursor position from CursorPosition message (a desktop point)
	 */
	updateCursorPosition(pos: ICursorPosition): void {
		const x = pos.x || 0;
		const y = pos.y || 0;
		if (x === this.cursorPos.x && y === this.cursorPos.y) return;
		const was = this._cursorOnPicture();
		this.cursorPos.x = x;
		this.cursorPos.y = y;
		// Redraw when the cursor moved on this display, or left it
		if (this.showCursor && this.cursorImage && (was || this._cursorOnPicture())) this._dirty = true;
	}

	/** The cursor's picture pixel, or null while it is on another display */
	_cursorOnPicture(): { x: number; y: number } | null {
		const fx = (this.cursorPos.x - this.origin.x) / this.units;
		const fy = (this.cursorPos.y - this.origin.y) / this.units;
		if (fx < 0 || fy < 0 || fx >= (this.remoteWidth || Infinity) || fy >= (this.remoteHeight || Infinity)) {
			return null;
		}
		return { x: fx, y: fy };
	}

	/**
	 * Draw cursor on canvas
	 */
	_drawCursor(ctx: CanvasRenderingContext2D): void {
		const at = this._cursorOnPicture();
		const image = this.cursorImage;
		if (!at || !image) return;
		const { scale, offsetX, offsetY } = this.transform;

		const cx = offsetX + (at.x - this.cursorHotspot.x) * scale;
		const cy = offsetY + (at.y - this.cursorHotspot.y) * scale;
		const cw = image.width * scale;
		const ch = image.height * scale;

		ctx.drawImage(image, cx, cy, cw, ch);
	}

	/**
	 * Get renderer statistics
	 */
	getStats(): RDRendererStats {
		return {
			fps: this.fps,
			framesRendered: this.framesRendered,
			remoteWidth: this.remoteWidth,
			remoteHeight: this.remoteHeight,
			canvasWidth: this.canvas.width,
			canvasHeight: this.canvas.height,
			scaleMode: this.scaleMode,
			scale: this.transform.scale
		};
	}

	/** Let go of the pictures held for redraws (the session ended) */
	releaseFrames(): void {
		if (this._pendingFrame) {
			this._pendingFrame.close();
			this._pendingFrame = null;
		}
		if (this._shownFrame) {
			this._shownFrame.close();
			this._shownFrame = null;
		}
	}

	/**
	 * Close and release resources
	 */
	close(): void {
		this.stopRenderLoop();
		this._unfollow?.();
		this._unfollow = null;
		this.canvas.removeEventListener('pointermove', this._onPointerMove);
		if (this._pendingFrame) {
			this._pendingFrame.close();
			this._pendingFrame = null;
		}
		if (this._shownFrame) {
			this._shownFrame.close();
			this._shownFrame = null;
		}
		if (this.cursorImage) {
			this.cursorImage.close();
			this.cursorImage = null;
		}
	}
}
