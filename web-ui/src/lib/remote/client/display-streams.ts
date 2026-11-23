/**
 * Several displays of one device over one connection (RustDesk 1.2.4 and
 * later): which display's video goes to which decoder and screen, which
 * displays the device streams, and what each costs.
 *
 * Every video frame names its display (VideoFrame.display). The session's
 * own screen (the single layout) shows one display; each display shown on
 * its own (a tile of the arranged or grid layout, or a window of its own)
 * has a decoder and a screen of its own. A frame for a display nobody shows
 * is dropped. A device that cannot stream several displays sends the one it
 * shows, and all of its frames go to the session's screen.
 *
 * The device streams exactly the displays in the capture set
 * (Misc.capture_displays with `set`; `sub` there drops every display *not*
 * listed, so it is never used). A display leaves the set while its window
 * is hidden or minimised, and, by the background setting, while another
 * display is the one being worked on: live keeps it streaming, paused
 * stops it, slow lets it in for one picture every few seconds.
 */
import type { RDDecodedFrame, RDVideoChunk } from './video.ts';

/** What the streams need of a decoder (RDVideo) */
export interface RDDisplayDecoder {
	initialized: boolean;
	currentCodec: string | null;
	frameCount: number;
	_needKeyframe: boolean;
	onFrame: ((frame: RDDecodedFrame) => void) | null;
	init(codec: string): Promise<void>;
	decode(chunk: RDVideoChunk): Promise<void>;
	setBackgroundMode(on: boolean): void;
	getStats(): { videoFps: number };
	close(): void;
}

/** What the streams need of a screen (RDRenderer) */
export interface RDDisplayScreen {
	pushFrame(frame: RDDecodedFrame): void;
}

/** How displays that are not the one being worked on stream */
export type BackgroundMode = 'live' | 'slow' | 'paused';

export const BACKGROUND_MODES: readonly BackgroundMode[] = ['live', 'slow', 'paused'];

/** A display shown on its own */
export interface RDDisplayView<Extra = unknown> {
	idx: number;
	decoder: RDDisplayDecoder;
	screen: RDDisplayScreen;
	/** Its window shows (not hidden, not minimised) */
	visible: boolean;
	/** Another display is the one being worked on */
	background: boolean;
	/** What the client keeps with it (input, window) */
	extra: Extra;
}

/** One display's cost, for the session info */
export interface RDDisplayTraffic {
	idx: number;
	/** Pictures decoded in the last second */
	fps: number;
	/** Video received in the last second, kilobits per second */
	kbps: number;
	/** In the capture set: the device streams it */
	streaming: boolean;
	/** Shown on the session's screen, on its own, or not at all */
	shown: 'main' | 'own' | 'none';
}

export interface RDDisplayStreamsOptions {
	/** The session screen's decoder */
	main: RDDisplayDecoder;
	/** Ask the device to stream exactly these displays */
	capture(set: number[]): void;
	/** Ask the device for a fresh picture of one display */
	refresh(idx: number): void;
	now?: () => number;
	/** Slow background displays get a picture this often */
	slowEveryMs?: number;
	/** A slow snapshot ends after its first picture, or after this long */
	snapshotMs?: number;
}

/** Traffic is counted over this window */
const WINDOW_MS = 1000;

export class RDDisplayStreams<Extra = unknown> {
	/** The device streams several displays to this connection */
	together = false;
	/** The display the session's screen shows, null while every display shows on its own */
	mainDisplay: number | null = 0;
	/** The session's screen shows (its window is not hidden) */
	mainVisible = true;
	background: BackgroundMode = 'live';
	views = new Map<number, RDDisplayView<Extra>>();
	main: RDDisplayDecoder;
	_opts: RDDisplayStreamsOptions;
	_now: () => number;
	/** The set the device streams now, as sent (or as it started) */
	_streaming: number[] | null = null;
	/** Slow background displays streaming for one picture */
	_snapshots = new Map<number, ReturnType<typeof setTimeout>>();
	_slowTimer: ReturnType<typeof setInterval> | null = null;
	/** Bytes received per display: [time, bytes] in the last second */
	_traffic = new Map<number, [number, number][]>();

	constructor(opts: RDDisplayStreamsOptions) {
		this._opts = opts;
		this.main = opts.main;
		this._now = opts.now ?? (() => performance.now());
	}

	/** The device's peer info said whether it streams several displays; it streams `current` now */
	setTogether(on: boolean, current: number): void {
		this.together = on;
		if (this._streaming === null) this._streaming = [current];
		if (this.mainDisplay !== null) this.mainDisplay = current;
		this.sync();
	}

	/**
	 * The decoder for a frame of a display, or null when nobody shows it.
	 * Counts the frame's bytes for the display either way.
	 */
	decoderFor(idx: number, bytes: number): RDDisplayDecoder | null {
		const now = this._now();
		const list = this._traffic.get(idx) ?? [];
		list.push([now, bytes]);
		while (list.length && (list[0]?.[0] ?? now) <= now - WINDOW_MS) list.shift();
		this._traffic.set(idx, list);
		if (!this.together) return this.main;
		const view = this.views.get(idx);
		if (view) return view.decoder;
		return idx === this.mainDisplay ? this.main : null;
	}

	/** Show a display on its own: its frames go to this decoder, its pictures to this screen */
	attach(
		idx: number,
		decoder: RDDisplayDecoder,
		screen: RDDisplayScreen,
		extra: Extra
	): RDDisplayView<Extra> {
		this.detach(idx);
		const view: RDDisplayView<Extra> = { idx, decoder, screen, visible: true, background: false, extra };
		decoder.onFrame = (frame) => {
			screen.pushFrame(frame);
			this._pictureShown(idx);
		};
		this.views.set(idx, view);
		this.sync();
		return view;
	}

	/** Stop showing a display on its own; its decoder closes */
	detach(idx: number): RDDisplayView<Extra> | null {
		const view = this.views.get(idx);
		if (!view) return null;
		this.views.delete(idx);
		view.decoder.onFrame = null;
		view.decoder.close();
		this._endSnapshot(idx, false);
		this.sync();
		return view;
	}

	/** The display the session's screen shows; null while each display shows on its own */
	setMainDisplay(idx: number | null): void {
		this.mainDisplay = idx;
		this.sync();
	}

	setMainVisible(visible: boolean): void {
		this.mainVisible = visible;
		this.sync();
	}

	setVisible(idx: number, visible: boolean): void {
		const view = this.views.get(idx);
		if (!view || view.visible === visible) return;
		view.visible = visible;
		this.sync();
	}

	setBackground(idx: number, background: boolean): void {
		const view = this.views.get(idx);
		if (!view || view.background === background) return;
		view.background = background;
		this.sync();
	}

	setBackgroundMode(mode: BackgroundMode): void {
		this.background = mode;
		this.sync();
	}

	/** The displays the device should stream now, smallest index first */
	captureSet(): number[] {
		const set = new Set<number>();
		if (this.mainDisplay !== null && this.mainVisible && !this.views.has(this.mainDisplay)) {
			set.add(this.mainDisplay);
		}
		for (const view of this.views.values()) {
			if (!view.visible) continue;
			if (
				!view.background ||
				this.background === 'live' ||
				(this.background === 'slow' && this._snapshots.has(view.idx))
			) {
				set.add(view.idx);
			}
		}
		return [...set].sort((a, b) => a - b);
	}

	/** Whether any display streams: with none, a quiet connection is no stall */
	get streamingAny(): boolean {
		return !this.together || (this._streaming?.length ?? 1) > 0;
	}

	/** Send the capture set when it changed; displays that join get a fresh picture */
	sync(): void {
		if (!this.together) {
			this._stopSlowTimer();
			return;
		}
		const set = this.captureSet();
		const was = this._streaming ?? [];
		if (set.length !== was.length || set.some((v, i) => v !== was[i])) {
			this._streaming = set;
			this._opts.capture(set);
			for (const idx of set) {
				if (was.includes(idx)) continue;
				const decoder = this.views.get(idx)?.decoder ?? (idx === this.mainDisplay ? this.main : null);
				if (decoder) decoder._needKeyframe = true;
				this._opts.refresh(idx);
			}
		}
		this._updateSlowTimer();
	}

	_slowViews(): RDDisplayView<Extra>[] {
		if (this.background !== 'slow') return [];
		return [...this.views.values()].filter((v) => v.visible && v.background);
	}

	_updateSlowTimer(): void {
		if (!this._slowViews().length) {
			this._stopSlowTimer();
			return;
		}
		if (this._slowTimer) return;
		this._slowTimer = setInterval(() => this._takeSnapshots(), this._opts.slowEveryMs ?? 4000);
	}

	_stopSlowTimer(): void {
		if (this._slowTimer) clearInterval(this._slowTimer);
		this._slowTimer = null;
	}

	/** Let each slow background display in for one picture */
	_takeSnapshots(): void {
		const views = this._slowViews().filter((v) => !this._snapshots.has(v.idx));
		if (!views.length) return;
		for (const v of views) {
			this._snapshots.set(
				v.idx,
				setTimeout(() => this._endSnapshot(v.idx, true), this._opts.snapshotMs ?? 2500)
			);
		}
		this.sync();
	}

	_endSnapshot(idx: number, sync: boolean): void {
		const timer = this._snapshots.get(idx);
		if (timer === undefined) return;
		clearTimeout(timer);
		this._snapshots.delete(idx);
		if (sync) this.sync();
	}

	_pictureShown(idx: number): void {
		if (this._snapshots.has(idx)) this._endSnapshot(idx, true);
	}

	/** Pictures decoded on every decoder: the stall check watches it grow */
	get decodedFrames(): number {
		let n = this.main.frameCount;
		for (const v of this.views.values()) n += v.decoder.frameCount;
		return n;
	}

	/** Each display's cost; `count` is how many displays the device has */
	traffic(count: number): RDDisplayTraffic[] {
		const now = this._now();
		const streaming = this.together
			? (this._streaming ?? [])
			: this.mainDisplay !== null
				? [this.mainDisplay]
				: [];
		const out: RDDisplayTraffic[] = [];
		for (let idx = 0; idx < count; idx++) {
			const bytes = (this._traffic.get(idx) ?? [])
				.filter(([t]) => t > now - WINDOW_MS)
				.reduce((sum, [, b]) => sum + b, 0);
			const view = this.views.get(idx);
			const onMain = !view && idx === this.mainDisplay;
			const decoder = view?.decoder ?? (onMain ? this.main : null);
			out.push({
				idx,
				fps: decoder ? decoder.getStats().videoFps : 0,
				kbps: Math.round((bytes * 8) / WINDOW_MS),
				streaming: streaming.includes(idx),
				shown: view ? 'own' : onMain ? 'main' : 'none'
			});
		}
		return out;
	}

	/** The session's tab went to the background or came back: decoders pause their work */
	setDecodersBackground(on: boolean): void {
		for (const v of this.views.values()) v.decoder.setBackgroundMode(on);
	}

	/** A codec change: every decoder waits for the next keyframe */
	needKeyframes(): void {
		this.main._needKeyframe = true;
		for (const v of this.views.values()) v.decoder._needKeyframe = true;
	}

	/** The session ended: every view goes, nothing more is sent */
	close(): RDDisplayView<Extra>[] {
		const views = [...this.views.values()];
		for (const v of views) {
			v.decoder.onFrame = null;
			v.decoder.close();
		}
		this.views.clear();
		for (const t of this._snapshots.values()) clearTimeout(t);
		this._snapshots.clear();
		this._stopSlowTimer();
		this._streaming = null;
		this.together = false;
		this.mainDisplay = 0;
		this.mainVisible = true;
		this._traffic.clear();
		return views;
	}
}
