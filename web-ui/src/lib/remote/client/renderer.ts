/**
 * BetterDesk Web Remote Client - Canvas Renderer
 * Renders decoded video frames on canvas with cursor overlay
 */
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

export class RDRenderer {
	/** The rendering canvas element */
	canvas: HTMLCanvasElement;
	ctx: CanvasRenderingContext2D | null;
	/** Offscreen canvas for cursor */
	cursorCanvas: HTMLCanvasElement;
	cursorCtx: CanvasRenderingContext2D | null;
	/** Remote display width */
	remoteWidth: number;
	/** Remote display height */
	remoteHeight: number;
	/** Scale mode: fit | fill | 1:1 | stretch (anything else scales like fit) */
	scaleMode: string;
	/** Current scale/offset applied */
	transform: RDRendererTransform;
	/** Current cursor image */
	cursorImage: ImageBitmap | null;
	/** Cursor hotspot */
	cursorHotspot: { x: number; y: number };
	/** Cursor position (remote coordinates) */
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
	_renderLoopActive: boolean;
	/** Called after the canvas size changed (the client asks the peer for a keyframe) */
	onResizeRefresh?: () => void;
	/** Called with true once a remote cursor image is ready */
	onCursorReady?: (ready: boolean) => void;

	/**
	 * @param canvas - The rendering canvas element
	 */
	constructor(canvas: HTMLCanvasElement) {
		this.canvas = canvas;
		this.ctx = canvas.getContext('2d', { alpha: false, desynchronized: true });
		this.cursorCanvas = document.createElement('canvas');
		this.cursorCtx = this.cursorCanvas.getContext('2d');

		this.remoteWidth = 0;
		this.remoteHeight = 0;

		this.scaleMode = 'fit';

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
		this._renderLoopActive = false;
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

	/**
	 * Recalculate canvas size (call on window resize or fullscreen toggle)
	 */
	resize(): void {
		const container = this.canvas.parentElement;
		if (!container) return;

		const rect = container.getBoundingClientRect();
		const dpr = window.devicePixelRatio || 1;
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

		let scale: number, offsetX: number, offsetY: number;

		switch (this.scaleMode) {
			case 'fit': {
				// Fit entire remote display inside canvas, preserving aspect ratio
				scale = Math.min(cw / rw, ch / rh);
				offsetX = (cw - rw * scale) / 2;
				offsetY = (ch - rh * scale) / 2;
				break;
			}
			case 'fill': {
				// Fill canvas, cropping excess, preserving aspect ratio
				scale = Math.max(cw / rw, ch / rh);
				offsetX = (cw - rw * scale) / 2;
				offsetY = (ch - rh * scale) / 2;
				break;
			}
			case '1:1': {
				// Native resolution, centered
				const dpr = window.devicePixelRatio || 1;
				scale = dpr;
				offsetX = (cw - rw * scale) / 2;
				offsetY = (ch - rh * scale) / 2;
				break;
			}
			case 'stretch': {
				// Stretch to fill canvas (ignores aspect ratio)
				// Handled separately in render
				scale = 1;
				offsetX = 0;
				offsetY = 0;
				break;
			}
			default:
				scale = Math.min(cw / rw, ch / rh);
				offsetX = (cw - rw * scale) / 2;
				offsetY = (ch - rh * scale) / 2;
		}

		this.transform = { scale, offsetX, offsetY };
	}

	/**
	 * Convert canvas coordinates to remote display coordinates
	 */
	canvasToRemote(canvasX: number, canvasY: number): { x: number; y: number } {
		const dpr = window.devicePixelRatio || 1;
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
			cancelAnimationFrame(this._rafId);
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
			// Count FPS only for genuinely new frames from the peer
			if ('_isNew' in this._pendingFrame && this._pendingFrame._isNew) {
				this._fpsCount++;
			}
			this._renderFrame(this._pendingFrame);
			this._pendingFrame = null;
		}

		// FPS calculation (actual new video frames per second from peer)
		const now = performance.now();
		if (now - this._fpsTime >= 1000) {
			this.fps = this._fpsCount;
			this._fpsCount = 0;
			this._fpsTime = now;
		}

		this._rafId = requestAnimationFrame(() => this._renderTick());
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

		if (this.scaleMode === 'stretch') {
			ctx.drawImage(drawSource, 0, 0, cw, ch);
		} else {
			const { scale, offsetX, offsetY } = this.transform;
			ctx.drawImage(drawSource, offsetX, offsetY, this.remoteWidth * scale, this.remoteHeight * scale);
		}

		frame.close();
		this.framesRendered++;

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

			// Signal that we have a valid remote cursor (CSS hides local cursor)
			if (this.onCursorReady) {
				this.onCursorReady(true);
			}
		} catch {
			// Silently skip invalid cursor data to prevent crash
		}
	}

	/**
	 * Update cursor position from CursorPosition message
	 */
	updateCursorPosition(pos: ICursorPosition): void {
		this.cursorPos.x = pos.x || 0;
		this.cursorPos.y = pos.y || 0;
	}

	/**
	 * Draw cursor on canvas
	 */
	_drawCursor(ctx: CanvasRenderingContext2D): void {
		const { scale, offsetX, offsetY } = this.transform;

		const cx = offsetX + (this.cursorPos.x - this.cursorHotspot.x) * scale;
		const cy = offsetY + (this.cursorPos.y - this.cursorHotspot.y) * scale;
		const image = this.cursorImage;
		if (!image) return;
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

	/**
	 * Close and release resources
	 */
	close(): void {
		this.stopRenderLoop();
		if (this._pendingFrame) {
			this._pendingFrame.close();
			this._pendingFrame = null;
		}
		if (this.cursorImage) {
			this.cursorImage.close();
			this.cursorImage = null;
		}
	}
}
