/**
 * NeoDesk Web Remote Client - Audio Decoder
 * Uses AudioDecoder (WebCodecs) for Opus decoding when available.
 * Falls back to raw PCM playback when AudioDecoder is not supported.
 * RustDesk sends Opus-encoded audio frames at 48kHz stereo by default.
 */
import { debugLog } from './log.ts';
import { page } from './page.ts';

/** The peer's AudioFormat */
export interface RDAudioFormat {
	sampleRate?: number;
	channels?: number;
}

/** An encoded audio frame for play() */
export interface RDAudioInput {
	data: Uint8Array;
	timestamp: number;
}

export interface RDAudioStats {
	initialized: boolean;
	enabled: boolean;
	sampleRate: number;
	channels: number;
	framesPlayed: number;
	volume: number;
	state: AudioContextState;
}

/** err.message as the JS read it: undefined when the caught value has none */
function errorMessage(err: unknown): unknown {
	return typeof err === 'object' && err !== null ? Reflect.get(err, 'message') : undefined;
}

export class RDAudio {
	audioCtx: AudioContext | null;
	/** Sample rate (from AudioFormat) */
	sampleRate: number;
	/** Number of channels */
	channels: number;
	/** Toolbar mute toggle */
	_userMuted: boolean;
	/** Active tab in multi-session viewer */
	_sessionActive: boolean;
	initialized: boolean;
	/** Next scheduled playback time */
	nextPlayTime: number;
	gainNode: GainNode | null;
	/** Volume (0-1) */
	volume: number;
	/** Frames played counter */
	framesPlayed: number;
	/** WebCodecs audio decoder for Opus */
	_audioDecoder: AudioDecoder | null;
	/** Whether Opus decoding via AudioDecoder is available */
	_opusSupported: boolean;
	/** Monotonic timestamp counter for Opus decoder (microseconds) */
	_opusTimestamp: number;
	/** Resumes this session's context when it is suspended (set by init) */
	_resumeHook?: (() => void) | null;

	constructor() {
		this.audioCtx = null;
		this.sampleRate = 48000;
		this.channels = 2;
		this._userMuted = false;
		this._sessionActive = true;
		this.initialized = false;
		this.nextPlayTime = 0;
		this.gainNode = null;
		this.volume = 1.0;
		this.framesPlayed = 0;
		this._audioDecoder = null;
		this._opusSupported = false;
		this._opusTimestamp = 0;
	}

	/**
	 * Check if Web Audio API is supported
	 */
	static isSupported(): boolean {
		return typeof AudioContext !== 'undefined' || typeof page().webkitAudioContext !== 'undefined';
	}

	/**
	 * Check if AudioDecoder (WebCodecs audio) is available for Opus decoding
	 */
	static isAudioDecoderSupported(): boolean {
		return typeof AudioDecoder !== 'undefined';
	}

	/**
	 * Initialize audio context (must be called after user gesture)
	 */
	async init(format: RDAudioFormat = {}): Promise<void> {
		if (format.sampleRate) this.sampleRate = format.sampleRate;
		if (format.channels) this.channels = format.channels;

		const AudioCtx = window.AudioContext || page().webkitAudioContext;
		const audioCtx = new AudioCtx({
			sampleRate: this.sampleRate,
			latencyHint: 'interactive'
		});
		this.audioCtx = audioCtx;

		// Lets a caller resume this session's context after a user gesture
		this._resumeHook = () => {
			if (this.audioCtx && this.audioCtx.state === 'suspended') {
				this.audioCtx.resume();
			}
		};

		// Create gain node for volume control
		const gainNode = audioCtx.createGain();
		this.gainNode = gainNode;
		gainNode.gain.value = this.volume;
		gainNode.connect(audioCtx.destination);

		// Try to initialize Opus decoding via AudioDecoder
		if (RDAudio.isAudioDecoderSupported()) {
			try {
				const support = await AudioDecoder.isConfigSupported({
					codec: 'opus',
					sampleRate: this.sampleRate,
					numberOfChannels: this.channels
				});
				if (support.supported) {
					this._initOpusDecoder();
					this._opusSupported = true;
					debugLog('[RDAudio] Opus decoding enabled via AudioDecoder');
				}
			} catch {
				debugLog('[RDAudio] AudioDecoder Opus not supported, using raw PCM fallback');
			}
		}

		if (!this._opusSupported) {
			debugLog('[RDAudio] Using raw PCM audio path (may produce noise if server sends Opus)');
		}

		this.nextPlayTime = 0;
		this.framesPlayed = 0;
		this._opusTimestamp = 0;
		this.initialized = true;
	}

	/**
	 * Initialize AudioDecoder for Opus decoding
	 */
	_initOpusDecoder(): void {
		const decoder = new AudioDecoder({
			output: (audioData) => this._handleDecodedAudio(audioData),
			error: (err) => console.warn('[RDAudio] Opus decode error:', err.message)
		});
		this._audioDecoder = decoder;

		decoder.configure({
			codec: 'opus',
			sampleRate: this.sampleRate,
			numberOfChannels: this.channels
		});
	}

	/**
	 * Handle decoded audio data from AudioDecoder
	 */
	_handleDecodedAudio(audioData: AudioData): void {
		if (!this.audioCtx || !this.gainNode) {
			audioData.close();
			return;
		}

		const numFrames = audioData.numberOfFrames;
		const numChannels = audioData.numberOfChannels;
		const sampleRate = audioData.sampleRate;

		const audioBuffer = this.audioCtx.createBuffer(numChannels, numFrames, sampleRate);

		// Copy decoded data to AudioBuffer
		for (let ch = 0; ch < numChannels; ch++) {
			const channelData = audioBuffer.getChannelData(ch);
			audioData.copyTo(channelData, { planeIndex: ch, format: 'f32-planar' });
		}

		audioData.close();

		// Schedule playback
		const source = this.audioCtx.createBufferSource();
		source.buffer = audioBuffer;
		source.connect(this.gainNode);

		const currentTime = this.audioCtx.currentTime;
		if (this.nextPlayTime < currentTime) {
			this.nextPlayTime = currentTime + 0.005;
		} else if (this.nextPlayTime - currentTime > 0.3) {
			// Too far ahead — drop queued scheduling lag to avoid BufferSource buildup
			this.nextPlayTime = currentTime + 0.005;
		}

		source.start(this.nextPlayTime);
		this.nextPlayTime += audioBuffer.duration;
		this.framesPlayed++;
	}

	/**
	 * Configure audio format (when AudioFormat message received)
	 */
	async configure(format: RDAudioFormat): Promise<void> {
		if (this.sampleRate !== format.sampleRate || this.channels !== format.channels) {
			this.close();
			await this.init(format);
		}
	}

	/**
	 * Decode and play an audio frame
	 */
	play(audioFrame: RDAudioInput): void {
		if (!this.initialized || !this._sessionActive || this._userMuted || !this.audioCtx) return;

		// Resume audio context if suspended (auto-play policy)
		if (this.audioCtx.state === 'suspended') {
			this.audioCtx.resume();
		}

		try {
			if (this._opusSupported && this._audioDecoder && this._audioDecoder.state !== 'closed') {
				// Decode Opus via AudioDecoder
				const chunk = new EncodedAudioChunk({
					type: 'key',
					timestamp: this._opusTimestamp,
					data: audioFrame.data
				});
				this._audioDecoder.decode(chunk);
				// Each Opus frame is 20ms = 20000µs
				this._opusTimestamp += 20000;
			} else {
				// Fallback: treat as raw PCM (works if server sends raw PCM)
				this._playRawPcm(audioFrame.data);
			}
		} catch (err) {
			console.warn('[RDAudio] Playback error:', errorMessage(err));
		}
	}

	/**
	 * Play raw PCM audio data (fallback path)
	 * RustDesk sends 16-bit signed integer PCM, interleaved
	 */
	_playRawPcm(pcmData: Uint8Array): void {
		if (!pcmData || pcmData.length === 0) return;

		// Convert Int16 PCM to Float32
		const int16View = new Int16Array(pcmData.buffer, pcmData.byteOffset, pcmData.byteLength / 2);
		const numSamples = Math.floor(int16View.length / this.channels);

		if (numSamples === 0) return;

		const audioCtx = this.audioCtx;
		if (!audioCtx) return;
		const audioBuffer = audioCtx.createBuffer(this.channels, numSamples, this.sampleRate);

		// De-interleave channels and convert to float32
		for (let ch = 0; ch < this.channels; ch++) {
			const channelData = audioBuffer.getChannelData(ch);
			for (let i = 0; i < numSamples; i++) {
				channelData[i] = (int16View[i * this.channels + ch] ?? NaN) / 32768.0;
			}
		}

		// Schedule playback
		const source = audioCtx.createBufferSource();
		source.buffer = audioBuffer;
		if (!this.gainNode) return;
		source.connect(this.gainNode);

		const currentTime = audioCtx.currentTime;
		if (this.nextPlayTime < currentTime) {
			this.nextPlayTime = currentTime + 0.01;
		}

		source.start(this.nextPlayTime);
		this.nextPlayTime += audioBuffer.duration;
		this.framesPlayed++;
	}

	/**
	 * Set volume level
	 * @param vol - 0.0 to 1.0
	 */
	setVolume(vol: number): void {
		this.volume = Math.max(0, Math.min(1, vol));
		this._updateGain();
	}

	/**
	 * Mute/unmute audio (toolbar toggle)
	 */
	setMuted(muted: boolean): void {
		this._userMuted = !!muted;
		this._updateGain();
	}

	/**
	 * Enable/disable playback for inactive viewer tabs
	 */
	setSessionActive(active: boolean): void {
		this._sessionActive = active !== false;
		this._updateGain();
	}

	_updateGain(): void {
		if (!this.gainNode) return;
		const silent = this._userMuted || !this._sessionActive;
		this.gainNode.gain.value = silent ? 0 : this.volume;
	}

	/**
	 * Get audio stats
	 */
	getStats(): RDAudioStats {
		return {
			initialized: this.initialized,
			enabled: this._sessionActive && !this._userMuted,
			sampleRate: this.sampleRate,
			channels: this.channels,
			framesPlayed: this.framesPlayed,
			volume: this.volume,
			state: this.audioCtx ? this.audioCtx.state : 'closed'
		};
	}

	/**
	 * Close audio context and release resources
	 */
	close(): void {
		// Close AudioDecoder for Opus
		if (this._audioDecoder && this._audioDecoder.state !== 'closed') {
			try {
				this._audioDecoder.close();
			} catch {
				// Ignore close errors
			}
		}
		this._audioDecoder = null;
		this._opusSupported = false;
		this._opusTimestamp = 0;

		if (this.audioCtx && this.audioCtx.state !== 'closed') {
			try {
				this.audioCtx.close();
			} catch {
				// Ignore close errors
			}
		}
		this.audioCtx = null;
		this._resumeHook = null;
		this.gainNode = null;
		this.initialized = false;
		this.framesPlayed = 0;
	}
}
