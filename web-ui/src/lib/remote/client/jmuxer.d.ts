/**
 * Types for jmuxer 2.0.5, which has none of its own: the members the remote
 * client uses (video.ts feeds H.264 through it when WebCodecs is missing;
 * protocol.ts and cdap-adapter.ts only test that the class is there).
 * Read from node_modules/jmuxer/src/jmuxer.js and its README.
 */
declare module 'jmuxer' {
	/** What onError receives: a SourceBuffer append failed */
	export interface JMuxerBufferError {
		type: 'video' | 'audio';
		/** 'buffer' for a SourceBuffer error event, 'InvalidStateError' for a failed append */
		name: 'buffer' | 'InvalidStateError';
		error: 'buffer error';
	}

	export interface JMuxerOptions {
		/** The video element, or its id */
		node: string | HTMLVideoElement;
		/** Which tracks the stream has (default 'both') */
		mode?: 'both' | 'audio' | 'video';
		/** Milliseconds between buffer flushes; 0 flushes every feed (default 500) */
		flushingTime?: number;
		/** Frame rate for the frame duration when the data carries none (default 30) */
		fps?: number;
		/** Remove played media from the SourceBuffer every 10 seconds (default true) */
		clearBuffer?: boolean;
		/** Log to the console (default false) */
		debug?: boolean;
		/** Called once the MediaSource is open; isReset is true when it reopened after reset() */
		onReady?: (isReset: boolean) => void;
		/** Called when a SourceBuffer append fails (not for a full quota, which it cleans up) */
		onError?: (data: JMuxerBufferError) => void;
	}

	/** Media for feed(): raw H.264 NAL units (Annex-B) */
	export interface JMuxerFeedData {
		video?: Uint8Array;
	}

	/** Muxes raw H.264 into fragmented MP4 and plays it in a video element through MSE */
	export default class JMuxer {
		constructor(options: JMuxerOptions);
		feed(data: JMuxerFeedData): void;
		/** Stops the flush timer and ends the MediaSource */
		destroy(): void;
	}
}
