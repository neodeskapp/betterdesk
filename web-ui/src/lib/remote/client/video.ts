/**
 * BetterDesk Web Remote Client - Video Decoder
 * Uses WebCodecs API for hardware-accelerated video decoding (preferred).
 * Falls back to JMuxer (H.264 via MSE) when WebCodecs is unavailable
 * (e.g., insecure HTTP context on non-localhost).
 * Supports VP9, H.264, AV1, VP8 codecs via WebCodecs;
 * H.264 only via JMuxer fallback.
 */
import JMuxer from 'jmuxer';
import { debugLog } from './log.ts';
import { page } from './page.ts';

/** An encoded frame for decode() */
export interface RDVideoChunk {
	data: Uint8Array;
	key?: boolean;
	pts?: number;
	/** Logical codec (vp9, h264, av1, vp8, h265); another one than the current re-initialises */
	codec?: string;
	/** WebCodecs codec string from the agent */
	codecString?: string | null;
}

export interface RDVideoInitOptions {
	/** WebCodecs codec string from agent */
	codecString?: string | null;
	/** Codec description (e.g. AV1CodecConfigurationRecord) for the decoder configuration */
	description?: AllowSharedBufferSource;
}

/** What the JMuxer path hands to onFrame: the hidden video element as the image */
export interface RDVideoProxyFrame {
	displayWidth: number;
	displayHeight: number;
	_source: HTMLVideoElement;
	/** Whether data was fed since the previous emitted frame */
	_isNew: boolean;
	close(): void;
}

/** A decoded frame for onFrame: a WebCodecs VideoFrame or the JMuxer proxy */
export type RDDecodedFrame = VideoFrame | RDVideoProxyFrame;

/** Logical codec name to decodable (WebCodecs, or H.264 through JMuxer) */
export type RDVideoCodecSupport = Record<string, boolean>;

export interface RDVideoStats {
	codec: string | null;
	initialized: boolean;
	frameCount: number;
	droppedFrames: number;
	displayWidth: number;
	displayHeight: number;
	queueSize: number;
	fallbackMode: boolean;
	videoFps: number;
	mseBufferSec: number;
	pendingFeeds: number;
}

/** err.message or err.name as the JS read them: undefined when the caught value has none */
function errorField(err: unknown, key: 'message' | 'name'): unknown {
	return typeof err === 'object' && err !== null ? Reflect.get(err, key) : undefined;
}

export class RDVideo {
	decoder: VideoDecoder | null;
	/** Current codec */
	currentCodec: string | null;
	/** Callback for decoded frames */
	onFrame: ((frame: RDDecodedFrame) => void) | null;
	/** Skip decode/render while viewer tab is in background */
	_backgroundMode: boolean;
	/** Callback for errors */
	onError: ((err: unknown) => void) | null;
	/** Decoded frame counter */
	frameCount: number;
	/** Dropped frame counter */
	droppedFrames: number;
	initialized: boolean;
	/** Display width */
	displayWidth: number;
	/** Display height */
	displayHeight: number;
	/** Using JMuxer fallback mode */
	fallbackMode: boolean;
	/** JMuxer instance for H.264 MSE fallback */
	_jmuxer: JMuxer | null;
	/** Hidden video element for JMuxer */
	_videoEl: HTMLVideoElement | null;
	/** RAF id for video-to-canvas sync */
	_syncRafId: number;
	/** Whether JMuxer video has started playing */
	_videoPlaying: boolean;
	/** Timestamps of recent feeds for FPS calculation */
	_feedTimestamps: number[];
	/** Monotonic feed counter for new-frame detection */
	_feedId: number;
	/** Health check interval id */
	_healthInterval: ReturnType<typeof setInterval> | 0;
	/** Last diagnostic log time */
	_lastDiagTime: number;
	/** Last feed timestamp for stall detection */
	_lastFeedTime: number;
	/** Whether autoplay is currently blocked */
	_autoplayBlocked: boolean;
	/** Callback when autoplay is blocked */
	onAutoplayBlocked: (() => void) | null;
	/** WebCodecs decoder is waiting for a keyframe before it can decode */
	_needKeyframe: boolean;
	/** Callback asking the client to request a keyframe (refresh_video) */
	onNeedKeyframe: (() => void) | null;
	/** Monotonic counter for WebCodecs chunk timestamps */
	_decodeInputCount: number;
	/** Last WebCodecs decoder configuration (for recovery) */
	_codecConfig: VideoDecoderConfig | null;
	/** Whether we already retried software decoding after a hardware failure */
	_softwareRetry: boolean;
	/** Called when decoder cannot recover (codec switch needed) */
	onCodecFailed: ((codec: string | null, err: unknown) => void) | null;
	/** Decode attempts since last successful frame */
	_decodeErrorsSinceFrame: number;
	/** Whether AV1 description was applied from a keyframe */
	_av1DescriptionApplied: boolean;
	/** Last decoder error timestamp (ms) for throttled recovery */
	_lastDecodeErrorTime: number;
	/** Buffer for frames that arrive before JMuxer is ready (set by _initJMuxer) */
	_pendingFeeds?: Uint8Array[];
	_pendingFeedsMax?: number;
	_jmuxerReady?: boolean;
	_healthIntervalMs?: number;
	/** When the health check last recreated JMuxer */
	_lastReinitTime?: number;
	/** The session's AudioContext, set by the client; retryPlay() resumes it */
	getAudioContext?: () => AudioContext | null;

	constructor() {
		this.decoder = null;
		this.currentCodec = null;
		this.onFrame = null;
		this._backgroundMode = false;
		this.onError = null;
		this.frameCount = 0;
		this.droppedFrames = 0;
		this.initialized = false;
		this.displayWidth = 0;
		this.displayHeight = 0;
		this.fallbackMode = false;
		this._jmuxer = null;
		this._videoEl = null;
		this._syncRafId = 0;
		this._videoPlaying = false;
		this._feedTimestamps = [];
		this._feedId = 0;
		this._healthInterval = 0;
		this._lastDiagTime = 0;
		this._lastFeedTime = 0;
		this._autoplayBlocked = false;
		this.onAutoplayBlocked = null;
		this._needKeyframe = false;
		this.onNeedKeyframe = null;
		this._decodeInputCount = 0;
		this._codecConfig = null;
		this._softwareRetry = false;
		this.onCodecFailed = null;
		this._decodeErrorsSinceFrame = 0;
		this._av1DescriptionApplied = false;
		this._lastDecodeErrorTime = 0;
	}

	/**
	 * Check if hardware WebCodecs is supported (requires secure context)
	 */
	static isSupported(): boolean {
		return typeof VideoDecoder !== 'undefined';
	}

	/**
	 * Check if secure context is available (needed for WebCodecs)
	 */
	static isSecureContext(): boolean {
		return window.isSecureContext === true;
	}

	/** WebKitGTK / Safari — AV1 HW decode is often broken; prefer software. */
	static isWebKit(): boolean {
		const ua = navigator.userAgent || '';
		return /AppleWebKit/i.test(ua) && !/Edg\//.test(ua) && !/Chrome\//.test(ua);
	}

	/** Chromium / WebView2 — full WebCodecs stack (incl. AV1 on recent builds). */
	static isChromiumWebView(): boolean {
		const ua = navigator.userAgent || '';
		return /Chrome\//.test(ua) || /Edg\//.test(ua);
	}

	/** Tauri RdClient desktop shell (WebKitGTK on Linux, WKWebView on macOS, WebView2 on Windows). */
	static isRdClientDesktop(): boolean {
		const w = page();
		return w.__BETTERDESK_RDCLIENT_DESKTOP__ === true || !!(w.__TAURI__ && w.__TAURI__.core);
	}

	/** Linux WebKitGTK — WebCodecs AV1 is advertised but fails at decode time. */
	static isWebKitGTK(): boolean {
		if (RDVideo.isChromiumWebView()) return false;
		const ua = navigator.userAgent || '';
		if (!/AppleWebKit/i.test(ua)) return false;
		if (/Linux/i.test(ua)) return true;
		return RDVideo.isRdClientDesktop() && !/Macintosh|Windows|CrOS/i.test(ua);
	}

	/**
	 * Whether AV1 should be offered to the encoder. WebKit runtimes often pass
	 * VideoDecoder.isConfigSupported for AV1 but fail with "Decode error" at runtime.
	 */
	static av1ReliableOnRuntime(): boolean {
		if (RDVideo.isWebKitGTK()) return false;
		if (RDVideo.isWebKit() && !RDVideo.isChromiumWebView()) return false;
		return true;
	}

	/**
	 * Default hardwareAcceleration for a logical codec on this runtime.
	 */
	static defaultAcceleration(codecName: string): HardwareAcceleration {
		if (codecName === 'av1' && RDVideo.isWebKit()) {
			return 'prefer-software';
		}
		return 'prefer-hardware';
	}

	/**
	 * Check if JMuxer fallback is available
	 */
	static isJMuxerAvailable(): boolean {
		return typeof JMuxer === 'function';
	}

	/**
	 * Codec string candidates per logical codec (broadest / HW-friendly first).
	 */
	static codecCandidates(codecName: string): string[] {
		const map: Record<string, string[] | undefined> = {
			vp9: ['vp09.00.10.08', 'vp09.00.40.08', 'vp09.00.50.08'],
			h264: ['avc1.640028', 'avc1.4d4028', 'avc1.42E01E'],
			av1: ['av01.0.04M.08', 'av01.0.08M.08', 'av01.0.05M.08', 'av01.0.01M.08', 'av01.0.15M.08'],
			vp8: ['vp8'],
			h265: ['hev1.1.6.L93.B0', 'hvc1.1.6.L93.B0']
		};
		return map[codecName] || [];
	}

	/**
	 * Pick the first VideoDecoder-supported config from codec candidates.
	 * @param accel hardwareAcceleration preference
	 * @returns supported VideoDecoderConfig
	 */
	static async resolveCodecConfig(
		codecName: string,
		accel?: HardwareAcceleration,
		explicitCodec?: string | null
	): Promise<VideoDecoderConfig | null> {
		const candidates = explicitCodec
			? [explicitCodec].concat(RDVideo.codecCandidates(codecName).filter((c) => c !== explicitCodec))
			: RDVideo.codecCandidates(codecName);
		if (!candidates.length) return null;

		const baseAccel = accel || RDVideo.defaultAcceleration(codecName);
		const probes: Pick<VideoDecoderConfig, 'hardwareAcceleration'>[] = baseAccel
			? [{ hardwareAcceleration: baseAccel }, { hardwareAcceleration: 'prefer-software' }, {}]
			: [{ hardwareAcceleration: 'prefer-hardware' }, { hardwareAcceleration: 'prefer-software' }, {}];

		for (const codec of candidates) {
			for (const extra of probes) {
				try {
					const cfg: VideoDecoderConfig = { codec, optimizeForLatency: true, ...extra };
					const support = await VideoDecoder.isConfigSupported(cfg);
					if (support && support.supported === true) {
						return support.config || cfg;
					}
				} catch {
					/* try next */
				}
			}
		}
		return null;
	}

	/**
	 * Get supported codecs
	 * @returns Map of codec name to boolean
	 */
	static async getSupportedCodecs(): Promise<RDVideoCodecSupport> {
		if (!RDVideo.isSupported()) {
			// In fallback mode, only H.264 via JMuxer is supported
			return {
				vp9: false,
				h264: RDVideo.isJMuxerAvailable(),
				av1: false,
				vp8: false,
				h265: false
			};
		}

		const names = ['vp9', 'h264', 'av1', 'vp8', 'h265'];
		const result: RDVideoCodecSupport = {};
		for (const name of names) {
			if (name === 'av1' && !RDVideo.av1ReliableOnRuntime()) {
				result[name] = false;
				continue;
			}
			result[name] = !!(await RDVideo.resolveCodecConfig(name, RDVideo.defaultAcceleration(name)));
		}
		return result;
	}

	/**
	 * Ordered wire codecs safe to advertise to the agent (most efficient first).
	 * AV1 is omitted when the runtime cannot decode it reliably (WebKit HW bugs).
	 */
	static async probeDecodableWireCodecs(): Promise<string[]> {
		const list: string[] = [];
		if (!RDVideo.isSupported()) {
			if (RDVideo.isJMuxerAvailable()) list.push('h264');
			list.push('webp', 'jpeg');
			return list;
		}
		const support = await RDVideo.getSupportedCodecs();
		if (support.av1 && RDVideo.av1ReliableOnRuntime()) list.push('av1');
		if (support.vp9) list.push('vp9');
		if (support.h264) list.push('h264');
		list.push('webp', 'jpeg');
		return list;
	}

	/**
	 * Initialize decoder for a specific codec.
	 * Uses WebCodecs if available, otherwise falls back to JMuxer for H.264.
	 * @param codecName - vp9, h264, av1, vp8
	 */
	async init(codecName: string, opts?: RDVideoInitOptions): Promise<void> {
		opts = opts || {};
		if (this.decoder || this._jmuxer) {
			this.close();
		}

		// If WebCodecs not available, use JMuxer fallback for H.264
		if (!RDVideo.isSupported()) {
			if (codecName !== 'h264') {
				console.warn('[RDVideo] WebCodecs unavailable and JMuxer only supports H.264, got:', codecName);
				// Still initialize — frames will be dropped but won't crash
			}

			if (!RDVideo.isJMuxerAvailable()) {
				console.error('[RDVideo] JMuxer not loaded. Cannot decode video in HTTP mode.');
				throw new Error('JMuxer not available for H.264 fallback decoding');
			}

			debugLog('[RDVideo] Using JMuxer (H.264 via MSE) fallback for', codecName);
			this.fallbackMode = true;
			this._initJMuxer();
			this.currentCodec = codecName;
			this.frameCount = 0;
			this.droppedFrames = 0;
			this.initialized = true;
			return;
		}

		const codecMap: Record<string, string | undefined> = {
			vp9: 'vp09.00.10.08',
			h264: 'avc1.42E01E',
			av1: 'av01.0.08M.08',
			vp8: 'vp8',
			h265: 'hev1.1.6.L93.B0'
		};

		if (!codecMap[codecName]) {
			throw new Error(`Unsupported codec: ${codecName}`);
		}

		const accel = this._softwareRetry ? 'prefer-software' : RDVideo.defaultAcceleration(codecName);

		const resolved = await RDVideo.resolveCodecConfig(codecName, accel, opts.codecString || null);
		if (!resolved || !resolved.codec) {
			throw new Error(`Codec ${codecName} not supported by browser`);
		}

		this._softwareRetry =
			resolved.hardwareAcceleration === 'prefer-software' ||
			resolved.hardwareAcceleration === 'no-preference';

		const decoder = new VideoDecoder({
			output: (frame) => this._handleDecodedFrame(frame),
			error: (err) => this._handleError(err)
		});
		this.decoder = decoder;

		// Remember the configuration so the decoder can be rebuilt on error.
		const config: VideoDecoderConfig = {
			codec: resolved.codec,
			hardwareAcceleration: resolved.hardwareAcceleration || accel,
			optimizeForLatency: true
		};
		this._codecConfig = config;
		if (opts.description) {
			config.description = opts.description;
		}
		decoder.configure(config);

		debugLog(
			'[RDVideo] Configured',
			codecName,
			config.codec,
			'hw=' + (config.hardwareAcceleration || 'default'),
			'runtime=' + (RDVideo.isWebKitGTK() ? 'webkitgtk' : RDVideo.isChromiumWebView() ? 'chromium' : 'other')
		);

		this.fallbackMode = false;
		this.currentCodec = codecName;
		this.frameCount = 0;
		this.droppedFrames = 0;
		this.initialized = true;
		// A freshly configured decoder must receive a keyframe before any delta
		// frame. Drop deltas until one arrives and proactively request one.
		this._needKeyframe = true;
		this._decodeInputCount = 0;
		this._decodeErrorsSinceFrame = 0;
		this._av1DescriptionApplied = false;
		if (this.onNeedKeyframe) {
			this.onNeedKeyframe();
		}
	}

	/**
	 * Create hidden video element and JMuxer instance for H.264 MSE decoding.
	 * JMuxer takes raw H.264 NALUs (Annex-B), wraps them in fMP4, and feeds to
	 * a <video> element via MSE. We then draw the video to canvas each frame.
	 */
	_initJMuxer(): void {
		// Create hidden video element
		const videoEl = document.createElement('video');
		this._videoEl = videoEl;
		videoEl.id = 'rd-jmuxer-video';
		videoEl.style.cssText = 'position:absolute;top:-9999px;left:-9999px;width:1px;height:1px;';
		videoEl.muted = true;
		videoEl.autoplay = true;
		videoEl.playsInline = true;
		document.body.appendChild(videoEl);

		// Buffer for frames that arrive before JMuxer is ready
		this._pendingFeeds = [];
		this._pendingFeedsMax = 45;
		this._jmuxerReady = false;

		videoEl.addEventListener('error', (e) => {
			const me = this._videoEl?.error;
			console.error(
				'[RDVideo] Video element error: code=' +
					(me ? me.code : '?') +
					' message=' +
					(me ? me.message : e.type)
			);
		});

		// Monitor stalled/waiting states for automatic recovery
		videoEl.addEventListener('stalled', () => {
			debugLog('[RDVideo] Video stalled, recovering...');
			this._recoverVideo();
		});
		videoEl.addEventListener('waiting', () => {
			this._recoverVideo();
		});
		videoEl.addEventListener('ended', () => {
			debugLog('[RDVideo] Video ended unexpectedly, recovering...');
			this._recoverVideo();
		});

		// Create JMuxer instance
		this._jmuxer = new JMuxer({
			node: videoEl,
			mode: 'video',
			flushingTime: 0, // Flush immediately for low latency
			// HTTP/mobile fallback uses MSE/JMuxer and must not be fed at a
			// 60fps target; that causes queue growth and visible stalls on
			// tablet browsers.
			fps: RDVideo.isSupported() ? 60 : 30,
			clearBuffer: false, // We manage buffer trimming in _startHealthCheck
			debug: false,
			onReady: () => {
				debugLog('[RDVideo] JMuxer ready, buffered frames:', this._pendingFeeds?.length);
				this._jmuxerReady = true;
				// Replay any frames that arrived before ready
				for (const data of this._pendingFeeds ?? []) {
					this._jmuxer?.feed({ video: data });
					this.frameCount++;
					this._feedId++;
					this._feedTimestamps.push(performance.now());
				}
				this._pendingFeeds = [];
				// Force play after feeding buffered data
				this._tryPlay();
				// Recover after MSE finishes processing initial data
				setTimeout(() => this._recoverVideo(), 100);
			},
			onError: (err) => {
				console.warn('[RDVideo] JMuxer error:', err);
			}
		});

		// Start the video-to-canvas sync loop and health checker
		this._startVideoSync();
		this._startHealthCheck();
	}

	/**
	 * Attempt to play the hidden video element.
	 * If autoplay is blocked by browser policy, signal the UI so it can
	 * show a "click to start" overlay. When the user interacts, call retryPlay().
	 */
	_tryPlay(): void {
		if (!this._videoEl) return;
		const playPromise = this._videoEl.play();
		if (playPromise && playPromise.catch) {
			playPromise.catch((err: unknown) => {
				if (errorField(err, 'name') === 'NotAllowedError') {
					if (!this._autoplayBlocked) {
						this._autoplayBlocked = true;
						console.warn('[RDVideo] Autoplay blocked by browser policy. User gesture required.');
						if (this.onAutoplayBlocked) {
							this.onAutoplayBlocked();
						}
					}
				} else {
					console.warn('[RDVideo] play() failed:', errorField(err, 'message'));
				}
			});
		}
	}

	/**
	 * Retry play after a user gesture (e.g., clicking the canvas overlay).
	 * Called from the UI layer when user clicks the "click to start" overlay.
	 */
	retryPlay(): void {
		this._autoplayBlocked = false;
		if (this._videoEl) {
			this._videoEl
				.play()
				.then(() => {
					debugLog('[RDVideo] Playback started after user gesture');
				})
				.catch((err: unknown) => {
					console.warn('[RDVideo] retryPlay failed:', errorField(err, 'message'));
				});
		}
		// Also resume AudioContext if it exists for this session
		if (typeof this.getAudioContext === 'function') {
			const ctx = this.getAudioContext();
			if (ctx && ctx.state === 'suspended') {
				ctx.resume();
			}
		}
	}

	/**
	 * RAF loop that captures the <video> element content and emits as frames
	 * for the renderer to draw on the main canvas.
	 * Does NOT seek — seeking is handled by _recoverVideo() and health check.
	 */
	_startVideoSync(): void {
		let loggedState = false;
		let lastRenderedFeedId = -1;
		const tick = () => {
			if (!this._videoEl || !this.fallbackMode) return;

			const v = this._videoEl;
			// Check if video has decodable data (readyState >= 2 = HAVE_CURRENT_DATA)
			if (v.videoWidth > 0 && v.videoHeight > 0 && v.readyState >= 2) {
				const vw = v.videoWidth;
				const vh = v.videoHeight;

				if (vw !== this.displayWidth || vh !== this.displayHeight) {
					this.displayWidth = vw;
					this.displayHeight = vh;
					debugLog('[RDVideo] JMuxer resolution:', vw, 'x', vh);
				}

				// Always emit frames (needed for cursor overlay), mark genuinely new ones
				if (this.onFrame) {
					const isNew = this._feedId !== lastRenderedFeedId;
					if (isNew) lastRenderedFeedId = this._feedId;
					const proxyFrame: RDVideoProxyFrame = {
						displayWidth: vw,
						displayHeight: vh,
						_source: v,
						_isNew: isNew,
						close: () => {}
					};
					this.onFrame(proxyFrame);
				}
				// No seeking here - health check handles buffer management
			} else if (!loggedState && this._jmuxerReady) {
				// Log once for diagnostics
				debugLog(
					'[RDVideo] Sync waiting: videoWidth=' +
						v.videoWidth +
						' readyState=' +
						v.readyState +
						' paused=' +
						v.paused +
						' currentTime=' +
						v.currentTime.toFixed(2)
				);
				loggedState = true;
			}

			this._syncRafId = requestAnimationFrame(tick);
		};
		this._syncRafId = requestAnimationFrame(tick);
	}

	/**
	 * Recover video element from stalled/waiting state.
	 * Seeks to the latest buffered data and ensures playback.
	 */
	_recoverVideo(): void {
		const v = this._videoEl;
		if (!v || !v.buffered || v.buffered.length === 0) return;

		const end = v.buffered.end(v.buffered.length - 1);
		const start = v.buffered.start(0);

		// If currentTime is outside buffered range, or behind, seek to live edge
		if (v.currentTime < start || v.currentTime > end + 0.5 || end - v.currentTime > 0.1) {
			v.currentTime = Math.max(start, end - 0.02);
		}

		// Ensure video is playing
		if (v.paused) {
			this._tryPlay();
		}
	}

	/**
	 * Periodic health check for MSE video element.
	 * Monitors buffer state, recovers from stalls, and logs diagnostics.
	 * Uses aggressive seeking and gentle playback rate adjustment.
	 */
	_startHealthCheck(): void {
		this._healthIntervalMs = 1000;
		this._healthInterval = setInterval(() => {
			if (!this._videoEl || !this.fallbackMode) return;
			const v = this._videoEl;

			// Periodic diagnostics (every 5 seconds, debug only)
			const now = performance.now();
			if (typeof window !== 'undefined' && page().BetterDesk?.debugRelay === true) {
				if (!this._lastDiagTime || now - this._lastDiagTime > 5000) {
					this._lastDiagTime = now;
					let bufInfo = 'none';
					if (v.buffered && v.buffered.length > 0) {
						bufInfo = v.buffered.start(0).toFixed(2) + '-' + v.buffered.end(v.buffered.length - 1).toFixed(2);
					}
					while (this._feedTimestamps.length > 0 && (this._feedTimestamps[0] ?? now) < now - 1000) {
						this._feedTimestamps.shift();
					}
					debugLog(
						'[RDVideo] Health: frames=' +
							this.frameCount +
							' fps=' +
							this._feedTimestamps.length +
							' readyState=' +
							v.readyState +
							' currentTime=' +
							v.currentTime.toFixed(2) +
							' buffered=' +
							bufInfo +
							' paused=' +
							v.paused +
							' dropped=' +
							this.droppedFrames
					);
				}
			}

			// Recovery: catch up to live edge if fallen behind
			if (v.buffered && v.buffered.length > 0) {
				const end = v.buffered.end(v.buffered.length - 1);
				const latency = end - v.currentTime;

				if (latency > 0.3) {
					// Fallen behind — hard seek to near live edge
					v.currentTime = end - 0.02;
					v.playbackRate = 1.0;
				} else if (latency > 0.06) {
					// Slightly behind — speed up to catch up
					v.playbackRate = 1.5;
				} else {
					// At live edge — normal speed
					v.playbackRate = 1.0;
				}

				// Resume if paused
				if (v.paused && this.frameCount > 0) {
					this._tryPlay();
				}

				// If video appears stuck (readyState < 2 but we have buffer)
				if (v.readyState < 2 && end > 0) {
					v.currentTime = end - 0.01;
					this._tryPlay();
				}
			}

			// Reinit fallback: if frames are being fed but video never reaches
			// playable state (readyState < 2) for 3+ seconds, recreate JMuxer
			if (this._lastFeedTime > 0) {
				const feedAge = now - this._lastFeedTime;
				if (
					v.readyState < 2 &&
					feedAge < 2000 &&
					this.frameCount > 10 &&
					(!this._lastReinitTime || now - this._lastReinitTime > 5000)
				) {
					console.warn(
						'[RDVideo] MSE stuck: readyState=' +
							v.readyState +
							' despite ' +
							this.frameCount +
							' frames fed. Reinitializing...'
					);
					this._lastReinitTime = now;
					this._reinitJMuxer();
				}
			}
		}, this._healthIntervalMs || 1000);
	}

	/**
	 * Reinitialize JMuxer when MSE pipeline is stuck
	 * (readyState stays below 2 despite continuous frame feeding).
	 */
	_reinitJMuxer(): void {
		const savedFrameCount = this.frameCount;
		const savedDropped = this.droppedFrames;

		// Destroy current JMuxer
		if (this._jmuxer) {
			try {
				this._jmuxer.destroy();
			} catch {
				/* ignore */
			}
			this._jmuxer = null;
		}

		// Remove old video element
		if (this._videoEl && this._videoEl.parentNode) {
			this._videoEl.pause();
			this._videoEl.removeAttribute('src');
			this._videoEl.parentNode.removeChild(this._videoEl);
			this._videoEl = null;
		}

		// Stop old sync/health loops (they will be restarted by _initJMuxer)
		if (this._syncRafId) {
			cancelAnimationFrame(this._syncRafId);
			this._syncRafId = 0;
		}
		if (this._healthInterval) {
			clearInterval(this._healthInterval);
			this._healthInterval = 0;
		}

		// Recreate everything
		this._initJMuxer();
		this.frameCount = savedFrameCount;
		this.droppedFrames = savedDropped;
	}

	/**
	 * Pause local decode/render while the session tab is inactive.
	 * The peer should already be throttled via customFps / quality_set.
	 */
	setBackgroundMode(on: boolean): void {
		this._backgroundMode = !!on;
	}

	/**
	 * Feed an encoded frame to the decoder
	 */
	async decode(frameData: RDVideoChunk): Promise<void> {
		if (!this.initialized) {
			return;
		}

		if (this._backgroundMode) {
			this.droppedFrames++;
			return;
		}

		// Switch codec if needed
		if (frameData.codec && frameData.codec !== this.currentCodec) {
			await this.init(frameData.codec, { codecString: frameData.codecString });
		}

		// AV1: do not trust a contradictory wire keyframe flag blindly. A
		// malformed key flag makes Chromium reject the chunk before decoding.
		let key = !!frameData.key;
		if (this.currentCodec === 'av1') {
			const detectedKey = RDVideo.av1KeyframeState(frameData.data);
			if (detectedKey !== null) {
				key = detectedKey;
			}
		}

		// AV1: apply codec description from the first keyframe (WebKitGTK needs this).
		if (this.currentCodec === 'av1' && key && !this._av1DescriptionApplied) {
			const desc = RDVideo.av1DescriptionFromKeyframe(frameData.data);
			if (desc) {
				try {
					const config = this._codecConfig;
					// No WebCodecs configuration yet (the JMuxer path): fails as writing to null does
					if (!config) throw new TypeError("Cannot set properties of null (setting 'description')");
					config.description = desc;
					if (this.decoder && this.decoder.state === 'configured') {
						this.decoder.configure(config);
					}
					this._av1DescriptionApplied = true;
					this._needKeyframe = false;
				} catch (e) {
					console.warn('[RDVideo] AV1 description configure failed:', e && errorField(e, 'message'));
				}
			}
		}

		// JMuxer fallback mode
		if (this.fallbackMode) {
			return this._decodeFallback(frameData);
		}

		if (!this.decoder) return;

		// Check decoder state
		if (this.decoder.state === 'closed') {
			return;
		}

		// A WebCodecs decoder must start from a keyframe. Drop delta frames
		// until the first keyframe (or one requested after an error) arrives.
		if (this._needKeyframe) {
			if (!key) {
				this.droppedFrames++;
				return;
			}
			this._needKeyframe = false;
		}

		// If the decode queue is backing up (slow/overloaded decoder), drop
		// delta frames to catch up. Keyframes are always decoded.
		if (!key && this.decoder.decodeQueueSize > 30) {
			this.droppedFrames++;
			return;
		}

		try {
			// WebCodecs expects monotonically increasing timestamps in
			// microseconds. Use a dedicated input counter (the output
			// frameCount can stay 0 while decoding has not produced a frame
			// yet, which would yield duplicate timestamps).
			const timestamp = this._decodeInputCount++ * 16667; // ~60fps in microseconds
			const chunk = new EncodedVideoChunk({
				type: key ? 'key' : 'delta',
				timestamp: timestamp,
				data: frameData.data
			});

			this.decoder.decode(chunk);
		} catch (err) {
			this.droppedFrames++;
			// The next frame must be a keyframe to resynchronise the decoder.
			this._needKeyframe = true;
			this._handleError(err);
		}
	}

	/**
	 * JMuxer fallback: feed H.264 NALUs to JMuxer for MSE decoding.
	 * RustDesk sends H.264 frames as EncodedVideoFrame.data which contains
	 * Annex-B formatted NAL units (with 00 00 00 01 start codes).
	 */
	_decodeFallback(frameData: RDVideoChunk): void {
		if (!frameData.data || frameData.data.length === 0) {
			this.droppedFrames++;
			return;
		}

		try {
			// Ensure we have a proper Uint8Array (protobuf.js may return Buffer-like)
			let videoData = frameData.data;
			if (!(videoData instanceof Uint8Array) || videoData.buffer.byteLength !== videoData.length) {
				videoData = new Uint8Array(videoData);
			}

			// Debug: log first frame details
			if (this.frameCount < 1) {
				const hex = Array.from(videoData.slice(0, 20))
					.map((b) => b.toString(16).padStart(2, '0'))
					.join(' ');
				debugLog(
					'[RDVideo] H.264 feed: ' + videoData.length + ' bytes, key=' + frameData.key + ', first 20: ' + hex
				);
			}

			// If JMuxer not ready yet, buffer the frame and replay on ready
			if (!this._jmuxerReady) {
				const pending = this._pendingFeeds ?? [];
				const maxPending = this._pendingFeedsMax || 45;
				if (pending.length >= maxPending) {
					pending.shift();
					this.droppedFrames++;
				}
				pending.push(videoData);
				return;
			}

			this._jmuxer?.feed({
				video: videoData
			});
			this.frameCount++;
			this._feedId++;
			const now = performance.now();
			this._feedTimestamps.push(now);
			this._lastFeedTime = now;

			// Ensure video is playing
			if (this._videoEl) {
				if (this._videoEl.paused) {
					this._tryPlay();
				}
				// Nudge to live edge only when significantly behind (avoid stutter from constant seeks)
				if (this._videoEl.buffered && this._videoEl.buffered.length > 0) {
					const end = this._videoEl.buffered.end(this._videoEl.buffered.length - 1);
					if (end - this._videoEl.currentTime > 0.3) {
						this._videoEl.currentTime = end - 0.02;
					}
				}
			}
		} catch (err) {
			console.warn('[RDVideo] JMuxer feed error:', err);
			this.droppedFrames++;
			if (this.onError) {
				this.onError(err);
			}
		}
	}

	/**
	 * Handle decoded video frame (WebCodecs path)
	 */
	_handleDecodedFrame(frame: VideoFrame): void {
		this.frameCount++;
		this._decodeErrorsSinceFrame = 0;
		// Record timestamp so getStats() can report real FPS on the WebCodecs path.
		this._feedTimestamps.push(performance.now());

		// Track display dimensions
		if (frame.displayWidth !== this.displayWidth || frame.displayHeight !== this.displayHeight) {
			this.displayWidth = frame.displayWidth;
			this.displayHeight = frame.displayHeight;
		}

		if (this.onFrame) {
			if (this._backgroundMode) {
				frame.close();
				return;
			}
			this.onFrame(frame);
		} else {
			// Must close frame if not consumed
			frame.close();
		}
	}

	/**
	 * Handle decoder error.
	 * Rebuilds the WebCodecs decoder (falling back to software decoding once)
	 * and requests a fresh keyframe so the stream can resynchronise instead of
	 * staying stuck on a black screen.
	 */
	_handleError(err: unknown): void {
		console.error('[RDVideo] Decoder error:', errorField(err, 'message') || err);
		this._decodeErrorsSinceFrame++;
		if (this.onError) {
			this.onError(err);
		}

		if (this.fallbackMode || !RDVideo.isSupported()) {
			return;
		}

		// WebKitGTK / Safari: AV1 passes isConfigSupported but fails at runtime — switch codec immediately.
		if (this.currentCodec === 'av1' && !RDVideo.av1ReliableOnRuntime()) {
			console.warn('[RDVideo] AV1 decode failed on WebKit runtime — requesting VP9/H.264 fallback');
			if (this.onCodecFailed) {
				this.onCodecFailed(this.currentCodec, err);
			}
			return;
		}

		// Give AV1 one software retry, but do not keep rebuilding a decoder
		// when the software path rejects the same stream. This usually means
		// malformed/contradictory keyframe metadata, not a hardware issue.
		if (this.currentCodec === 'av1' && this._softwareRetry && this.onCodecFailed) {
			console.warn('[RDVideo] AV1 software decode failed — requesting codec fallback');
			this.onCodecFailed(this.currentCodec, err);
			return;
		}

		// Throttle recovery attempts to once per second.
		const now = performance.now();
		if (now - this._lastDecodeErrorTime < 1000) {
			return;
		}
		this._lastDecodeErrorTime = now;

		// First recovery after a hardware decode failure: retry with software.
		if (!this._softwareRetry) {
			this._softwareRetry = true;
			console.warn('[RDVideo] Rebuilding decoder with software decoding fallback');
		} else if (this._decodeErrorsSinceFrame > 8 && this.onCodecFailed) {
			console.warn('[RDVideo] Codec', this.currentCodec, 'unrecoverable — requesting fallback');
			this.onCodecFailed(this.currentCodec, err);
			return;
		}

		this._needKeyframe = true;
		this._av1DescriptionApplied = false;

		try {
			if (this.decoder && this.decoder.state !== 'closed') {
				this.decoder.close();
			}
		} catch {
			// ignore
		}

		const codecName = this.currentCodec;
		const savedCodecString = this._codecConfig && this._codecConfig.codec;
		this.decoder = null;
		this.initialized = false;
		if (codecName) {
			this.init(codecName, { codecString: savedCodecString }).catch((e: unknown) => {
				console.error('[RDVideo] Decoder rebuild failed:', errorField(e, 'message') || e);
				if (this.onCodecFailed) {
					this.onCodecFailed(codecName, e);
				}
			});
		}
	}

	/**
	 * Detect an AV1 frame type from the uncompressed frame header when the
	 * access unit contains a frame OBU. Return null when the payload cannot
	 * be classified safely, so the peer's metadata remains the fallback.
	 */
	static av1KeyframeState(data: Uint8Array): boolean | null {
		if (!data || data.length < 2) return null;

		let offset = 0;
		while (offset < data.length) {
			const header = data[offset++] ?? 0;
			const obuType = (header >> 3) & 0x0f;
			const hasExtension = (header >> 2) & 0x01;
			const hasSize = (header >> 1) & 0x01;

			if (hasExtension) {
				if (offset >= data.length) return null;
				offset++;
			}

			let size: number;
			if (hasSize) {
				const parsed = RDVideo._readLeb128(data, offset);
				if (!parsed) return null;
				size = parsed.value;
				offset += parsed.bytes;
			} else {
				size = data.length - offset;
			}
			if (size < 1 || offset + size > data.length) return null;

			// OBU_FRAME_HEADER (3) and OBU_FRAME (6) start with
			// show_existing_frame (1 bit) followed by frame_type (2 bits).
			if (obuType === 3 || obuType === 6) {
				const firstByte = data[offset] ?? 0;
				if ((firstByte & 0x80) !== 0) return false;
				return ((firstByte >> 5) & 0x03) === 0;
			}
			offset += size;
		}
		return null;
	}

	/**
	 * Build WebCodecs AV1CodecConfigurationRecord (av1C) from a keyframe OBU stream.
	 */
	static av1DescriptionFromKeyframe(data: Uint8Array): Uint8Array<ArrayBuffer> | null {
		if (!data || data.length < 2) return null;
		let i = 0;
		let fullSeqObu: Uint8Array | null = null;
		let seqPayload: Uint8Array | null = null;
		while (i < data.length) {
			const obuStart = i;
			const hdr = data[i++] ?? 0;
			const obuType = (hdr >> 3) & 0x0f;
			const extFlag = (hdr >> 2) & 0x01;
			const hasSize = (hdr >> 1) & 0x01;
			if (extFlag === 1 && i < data.length) i++;
			let size: number;
			if (hasSize === 1) {
				const parsed = RDVideo._readLeb128(data, i);
				if (!parsed) break;
				size = parsed.value;
				i += parsed.bytes;
			} else {
				size = data.length - i;
			}
			if (i + size > data.length) break;
			if (obuType === 1) {
				fullSeqObu = data.subarray(obuStart, i + size);
				seqPayload = data.subarray(i, i + size);
			}
			i += size;
		}
		if (!fullSeqObu || !seqPayload || seqPayload.length < 4) return null;

		const br = { data: seqPayload, pos: 0 };
		const read = (n: number) => RDVideo._readBits(br, n);
		const profile = read(3);
		const level = read(5);
		const tier = read(1);
		const highBitdepth = read(1);
		const twelveBit = read(1);
		const monochrome = read(1);
		const subsamplingX = read(1);
		const subsamplingY = read(1);
		const samplePos = read(2);
		read(3); // reserved

		const out = new Uint8Array(4 + fullSeqObu.length);
		out[0] = 0x81;
		out[1] = (profile << 5) | (level & 0x1f);
		out[2] =
			((level >> 5) & 0xff) |
			(tier << 7) |
			(highBitdepth << 6) |
			(twelveBit << 5) |
			(monochrome << 4) |
			(subsamplingX << 3) |
			(subsamplingY << 2) |
			(samplePos >> 1);
		out[3] = (samplePos & 1) << 7;
		out.set(fullSeqObu, 4);
		return out;
	}

	static _readLeb128(b: Uint8Array, start: number): { value: number; bytes: number } | null {
		let v = 0;
		for (let i = 0; i < 8 && start + i < b.length; i++) {
			const byte = b[start + i] ?? 0;
			v |= (byte & 0x7f) << (7 * i);
			if ((byte & 0x80) === 0) {
				return { value: v, bytes: i + 1 };
			}
		}
		return null;
	}

	static _readBits(br: { data: Uint8Array; pos: number }, n: number): number {
		let v = 0;
		for (let i = 0; i < n; i++) {
			const bytePos = br.pos >> 3;
			if (bytePos >= br.data.length) {
				br.pos++;
				v <<= 1;
				continue;
			}
			const bit = ((br.data[bytePos] ?? 0) >> (7 - (br.pos & 7))) & 1;
			v = (v << 1) | bit;
			br.pos++;
		}
		return v;
	}

	/**
	 * Flush pending frames
	 */
	async flush(): Promise<void> {
		if (this.decoder && this.decoder.state !== 'closed') {
			try {
				await this.decoder.flush();
			} catch {
				// Ignore flush errors on closed decoder
			}
		}
	}

	/**
	 * Get decoder statistics
	 */
	getStats(): RDVideoStats {
		// Calculate actual video FPS from recent feed timestamps (last 1 second)
		const now = performance.now();
		while (this._feedTimestamps.length > 0 && (this._feedTimestamps[0] ?? now) < now - 1000) {
			this._feedTimestamps.shift();
		}
		const videoFps = this._feedTimestamps.length;

		let mseBufferSec = 0;
		if (this.fallbackMode && this._videoEl && this._videoEl.buffered && this._videoEl.buffered.length > 0) {
			const b = this._videoEl.buffered;
			mseBufferSec = b.end(b.length - 1) - b.start(0);
		}

		return {
			codec: this.currentCodec,
			initialized: this.initialized,
			frameCount: this.frameCount,
			droppedFrames: this.droppedFrames,
			displayWidth: this.displayWidth,
			displayHeight: this.displayHeight,
			queueSize: this.decoder ? this.decoder.decodeQueueSize : 0,
			fallbackMode: this.fallbackMode,
			videoFps: videoFps,
			mseBufferSec: mseBufferSec,
			pendingFeeds: this._pendingFeeds ? this._pendingFeeds.length : 0
		};
	}

	/**
	 * Close the decoder and release resources
	 */
	close(): void {
		// Close WebCodecs decoder
		if (this.decoder && this.decoder.state !== 'closed') {
			try {
				this.decoder.close();
			} catch {
				// Ignore close errors
			}
		}
		this.decoder = null;

		// Stop video sync loop
		if (this._syncRafId) {
			cancelAnimationFrame(this._syncRafId);
			this._syncRafId = 0;
		}

		// Stop health check
		if (this._healthInterval) {
			clearInterval(this._healthInterval);
			this._healthInterval = 0;
		}

		// Clear feed tracking
		this._feedTimestamps = [];
		this._feedId = 0;

		// Destroy JMuxer
		if (this._jmuxer) {
			try {
				this._jmuxer.destroy();
			} catch {
				// Ignore destroy errors
			}
			this._jmuxer = null;
		}

		// Remove hidden video element
		if (this._videoEl) {
			this._videoEl.pause();
			this._videoEl.src = '';
			if (this._videoEl.parentNode) {
				this._videoEl.parentNode.removeChild(this._videoEl);
			}
			this._videoEl = null;
		}

		this._videoPlaying = false;
		this.fallbackMode = false;
		this.currentCodec = null;
		this.initialized = false;
		this._softwareRetry = false;
		this._av1DescriptionApplied = false;
	}
}
