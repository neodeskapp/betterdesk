/**
 * NeoDesk Web Remote Client - Protocol Handler
 * Implements RustDesk rendezvous and peer-to-peer protocol using protobuf
 */
import JMuxer from 'jmuxer';
import { RDCompress } from './compress.ts';
import { LocalFiles } from './local-files.ts';
import * as hbb from './generated/protos.ts';

/** The generated message classes the client works with */
function protoTypes() {
	return {
		RendezvousMessage: hbb.RendezvousMessage,
		Message: hbb.Message,
		PunchHoleRequest: hbb.PunchHoleRequest,
		PunchHoleResponse: hbb.PunchHoleResponse,
		RequestRelay: hbb.RequestRelay,
		RelayResponse: hbb.RelayResponse,
		LoginRequest: hbb.LoginRequest,
		LoginResponse: hbb.LoginResponse,
		SignedId: hbb.SignedId,
		PublicKey: hbb.PublicKey,
		Hash: hbb.Hash,
		MouseEvent: hbb.MouseEvent,
		KeyEvent: hbb.KeyEvent,
		VideoFrame: hbb.VideoFrame,
		AudioFrame: hbb.AudioFrame,
		CursorData: hbb.CursorData,
		CursorPosition: hbb.CursorPosition,
		TestDelay: hbb.TestDelay,
		Misc: hbb.Misc,
		OptionMessage: hbb.OptionMessage,
		SupportedDecoding: hbb.SupportedDecoding,
		Clipboard: hbb.Clipboard,
		MultiClipboards: hbb.MultiClipboards,
		PeerInfo: hbb.PeerInfo,
		IdPk: hbb.IdPk,
		Resolution: hbb.Resolution,
		ChatMessage: hbb.ChatMessage,
		TogglePrivacyMode: hbb.TogglePrivacyMode,
		SwitchDisplay: hbb.SwitchDisplay,
		Auth2FA: hbb.Auth2FA,

		FileAction: hbb.FileAction,
		FileResponse: hbb.FileResponse,
		FileDirectory: hbb.FileDirectory,
		FileEntry: hbb.FileEntry,
		ReadDir: hbb.ReadDir,
		FileTransferSendRequest: hbb.FileTransferSendRequest,
		FileTransferReceiveRequest: hbb.FileTransferReceiveRequest,
		FileTransferBlock: hbb.FileTransferBlock,
		FileTransferDigest: hbb.FileTransferDigest,
		FileTransferDone: hbb.FileTransferDone,
		FileTransferCancel: hbb.FileTransferCancel,
		FileTransferSendConfirmRequest: hbb.FileTransferSendConfirmRequest,
		FileTransferError: hbb.FileTransferError,
		FileDirCreate: hbb.FileDirCreate,
		FileRemoveDir: hbb.FileRemoveDir,
		FileRemoveFile: hbb.FileRemoveFile,
		FileRename: hbb.FileRename,
		FileTransfer: hbb.FileTransfer,

		Cliprdr: hbb.Cliprdr,
		CliprdrMonitorReady: hbb.CliprdrMonitorReady,
		CliprdrFormat: hbb.CliprdrFormat,
		CliprdrServerFormatList: hbb.CliprdrServerFormatList,
		CliprdrServerFormatListResponse: hbb.CliprdrServerFormatListResponse,
		CliprdrServerFormatDataRequest: hbb.CliprdrServerFormatDataRequest,
		CliprdrServerFormatDataResponse: hbb.CliprdrServerFormatDataResponse,
		CliprdrFileContentsRequest: hbb.CliprdrFileContentsRequest,
		CliprdrFileContentsResponse: hbb.CliprdrFileContentsResponse,
		CliprdrTryEmpty: hbb.CliprdrTryEmpty
	};
}

/** The enums the client works with; each enum's names and numbers are in .values */
function protoEnums() {
	return {
		ConnType: { values: hbb.ConnType },
		NatType: { values: hbb.NatType },
		ControlKey: { values: hbb.ControlKey },
		KeyboardMode: { values: hbb.KeyboardMode },
		ImageQuality: { values: hbb.ImageQuality },
		ClipboardFormat: { values: hbb.ClipboardFormat },
		FileType: { values: hbb.FileType },
		PreferCodec: { values: hbb.SupportedDecoding_PreferCodec }
	};
}

export type RDProtoTypes = ReturnType<typeof protoTypes>;
export type RDProtoEnums = ReturnType<typeof protoEnums>;

/**
 * `values[key]` of a generated enum: a name reads its number, a number
 * (as a string) its name through the prototype; anything else undefined.
 */
function enumLookup<E extends object>(values: E, key: string): number | Extract<keyof E, string> | undefined {
	const value: unknown = Reflect.get(values, key);
	if (typeof value === 'number') return value;
	if (typeof value === 'string' && isEnumName(values, value)) return value;
	return undefined;
}

function isEnumName<E extends object>(values: E, name: string): name is Extract<keyof E, string> {
	return Object.prototype.hasOwnProperty.call(values, name);
}

/** Probed decode abilities of the browser, by codec */
export interface RDCodecAbilities {
	vp9?: boolean | null;
	h264?: boolean | null;
	h265?: boolean | null;
	av1?: boolean | null;
	vp8?: boolean | null;
}

export interface RDSupportedDecodingOptions {
	/** probed abilities */
	abilities?: RDCodecAbilities | null;
	/** PreferCodec name (Auto/VP9/H264/H265/VP8/AV1) */
	prefer?: string | null;
	hasWebCodecs?: boolean;
	hasJMuxer?: boolean;
}

export interface RDLoginRequestOptions {
	username?: string;
	myId?: string;
	myName?: string;
	/** ImageQuality name (Best/Balanced/Low) */
	imageQuality?: string;
	codecAbilities?: RDCodecAbilities | null;
	preferCodec?: string | null;
	fps?: number;
	disableAudio?: boolean;
	/** Watch-only guests: the device ignores keyboard and mouse */
	disableKeyboard?: boolean;
}

export interface RDFileTransferLoginOptions {
	username?: string;
	myId?: string;
	myName?: string;
	dir?: string | null;
	showHidden?: boolean;
}

/** A clipboard entry as RDClipboard decodes it; at least one value is required */
export interface RDMultiClipboardEntry {
	text?: string | null;
	html?: string | null;
	pngBlob?: BlobPart | null;
}

/** The desktop bridge's native clipboard format ids and names */
export interface RDCliprdrFormatNames {
	fileDescriptorFormatId?: number | string | null;
	file_descriptor_format_id?: number | string | null;
	fileDescriptorFormatName?: string | null;
	file_descriptor_format_name?: string | null;
	fileContentsFormatId?: number | string | null;
	file_contents_format_id?: number | string | null;
	fileContentsFormatName?: string | null;
	file_contents_format_name?: string | null;
}

export interface RDCliprdrFileContentsRequestOptions {
	streamId?: number;
	listIndex?: number;
	dwFlags?: number;
	nPositionLow?: number;
	nPositionHigh?: number;
	cbRequested?: number;
	haveClipDataId?: boolean;
	clipDataId?: number;
}

/** Bytes for a cliprdr or file block field; a string is base64 from the desktop bridge */
export type RDBinaryPayload = Uint8Array | ArrayBuffer | number[] | string | null | undefined;

/** OptionMessage fields for a mid-session change; booleans become BoolOption Yes/No */
export interface RDOptionMiscOptions {
	/** ImageQuality name (Best/Balanced/Low) */
	imageQuality?: string;
	customImageQuality?: number;
	customFps?: number;
	lockAfterSessionEnd?: boolean;
	showRemoteCursor?: boolean;
	privacyMode?: boolean;
	blockInput?: boolean;
	disableAudio?: boolean;
	disableClipboard?: boolean;
	disableKeyboard?: boolean;
	supportedDecoding?: RDSupportedDecodingOptions;
}

/** A file for FileAction.receive (camelCase or the desktop bridge's snake_case) */
export interface RDFileReceiveEntry {
	entryType?: number | null;
	entry_type?: number | null;
	name?: string | null;
	size?: number | null;
	modifiedTime?: number | null;
	modified_time?: number | null;
	isHidden?: boolean | null;
	is_hidden?: boolean | null;
}

export interface RDFileDigestOptions {
	isUpload?: boolean;
	isIdentical?: boolean;
	isResume?: boolean;
	transferredSize?: number | null;
}

export type RDVideoCodec = 'vp9' | 'h264' | 'h265' | 'vp8' | 'av1' | 'rgb' | 'yuv';

/** One encoded frame of a VideoFrame */
export interface RDEncodedFrame {
	data: Uint8Array;
	key: boolean;
	pts: number;
	codec: Exclude<RDVideoCodec, 'rgb' | 'yuv'>;
}

/** Reassembles RustDesk frames from WebSocket chunks */
export interface RDStreamDecoder {
	/** Feed raw data from WebSocket; returns the raw protobuf payloads (without frame headers) */
	feed(data: ArrayBuffer | Uint8Array): Uint8Array[];
	/** Reset internal buffer */
	reset(): void;
}

export class RDProtocol {
	/** Message types cache */
	types: RDProtoTypes;
	enums: RDProtoEnums;
	loaded: boolean;

	constructor() {
		this.types = protoTypes();
		this.enums = protoEnums();
		this.loaded = false;
	}

	/**
	 * Bind the generated protobuf classes (generated/protos.ts) — no runtime .proto parse / eval.
	 */
	async load(): Promise<void> {
		if (this.loaded) return;

		this.types = protoTypes();
		this.enums = protoEnums();

		this.loaded = true;
	}

	// ---- Encoding helpers ----

	/**
	 * Encode a RendezvousMessage with RustDesk variable-length frame header
	 * (used for direct rendezvous communication, no encryption)
	 */
	encodeRendezvous(msgObj: hbb.IRendezvousMessage): Uint8Array<ArrayBuffer> {
		return this._encodeFrame(this.serializeRendezvous(msgObj));
	}

	/**
	 * Serialize a RendezvousMessage without a TCP BytesCodec header.
	 * Native RustDesk WebSocket relay endpoints use one protobuf payload per
	 * binary WebSocket message.
	 */
	serializeRendezvous(msgObj: hbb.IRendezvousMessage): Uint8Array {
		const msg = this.types.RendezvousMessage.create(msgObj);
		const buf = this.types.RendezvousMessage.encode(msg).finish();
		return buf;
	}

	/**
	 * Serialize a Message to raw protobuf bytes (NO frame header)
	 * Use this when encryption will be applied before framing
	 *
	 * Uses fromObject() (not create()) so that string enum names — e.g. a
	 * KeyEvent's `controlKey: 'Backspace'` or `mode: 'Legacy'` — are converted
	 * to their numeric enum values. create() leaves them as strings, which the
	 * encoder writes as 0 (Unknown), breaking every non-character key. Byte
	 * fields supplied as Uint8Array (clipboard content, file blocks, keys) are
	 * preserved as-is by fromObject().
	 */
	serializeMessage(msgObj: hbb.MessageInit): Uint8Array {
		const msg = this.types.Message.fromObject(msgObj);
		return this.types.Message.encode(msg).finish();
	}

	/**
	 * Encode a Message (peer-to-peer) with RustDesk variable-length frame header
	 * (used when no encryption is active)
	 */
	encodeMessage(msgObj: hbb.MessageInit): Uint8Array<ArrayBuffer> {
		return this._encodeFrame(this.serializeMessage(msgObj));
	}

	/**
	 * Add frame header to raw bytes (e.g. after encryption)
	 */
	frameBytes(rawBytes: Uint8Array): Uint8Array<ArrayBuffer> {
		return this._encodeFrame(rawBytes);
	}

	/**
	 * Decode a RendezvousMessage from raw protobuf bytes (frame header already stripped)
	 * @param protoBytes - Raw protobuf payload
	 */
	decodeRendezvous(protoBytes: Uint8Array): hbb.RendezvousMessage {
		return this.types.RendezvousMessage.decode(protoBytes);
	}

	/**
	 * Decode a Message (peer-to-peer) from raw protobuf bytes (frame header already stripped)
	 * @param protoBytes - Raw protobuf payload
	 */
	decodeMessage(protoBytes: Uint8Array): hbb.Message {
		return this.types.Message.decode(protoBytes);
	}

	// Client version advertised to RustDesk peers (keep in sync with product baseline)
	static CLIENT_VERSION = 'NeoDesk-Web/1.4.9';

	/**
	 * The version a session's sign-in names. A device reads it as a number
	 * (hbb_common get_version_number): CLIENT_VERSION reads as 0 there, and a
	 * device sends a client below 1.3.0 its clipboard as text only, never an
	 * image. 1.3.0 is the first version it sends every format to; the file
	 * transfer session keeps CLIENT_VERSION.
	 */
	static SESSION_VERSION = '1.3.0';

	/**
	 * Build PunchHoleRequest for connecting to a device
	 * @param serverKey - Server public key (base64) for licence_key validation
	 */
	buildPunchHoleRequest(
		deviceId: string,
		serverKey?: string | null,
		connType?: number | null
	): hbb.IRendezvousMessage {
		const ct = connType != null ? connType : this.enums.ConnType.values.DEFAULT_CONN;
		return {
			punchHoleRequest: {
				id: deviceId,
				natType: this.enums.NatType.values.SYMMETRIC, // Browser always NAT
				licenceKey: serverKey || '',
				connType: ct,
				token: '',
				version: RDProtocol.CLIENT_VERSION,
				forceRelay: true // Browser must use relay
			}
		};
	}

	/**
	 * Build RequestRelay message
	 * @param deviceId - Target device ID
	 * @param uuid - Relay session UUID (from RelayResponse)
	 * @param relayServer - Relay server address
	 * @param serverKey - Server public key for licence_key validation (hbbr checks this)
	 */
	buildRequestRelay(
		deviceId: string,
		uuid: string,
		relayServer?: string | null,
		serverKey?: string | null,
		connType?: number | null
	): hbb.IRendezvousMessage {
		const ct = connType != null ? connType : this.enums.ConnType.values.DEFAULT_CONN;
		return {
			requestRelay: {
				id: deviceId,
				uuid: uuid,
				relayServer: relayServer || '',
				licenceKey: serverKey || '',
				// The device sends its SignedId only when asked for a secure
				// connection, and the client accepts nothing else first.
				secure: true,
				connType: ct,
				token: ''
			}
		};
	}

	/**
	 * Build PublicKey message for secure handshake
	 */
	buildPublicKey(asymPk: Uint8Array, symKey: Uint8Array): hbb.MessageInit {
		return {
			publicKey: {
				asymmetricValue: asymPk,
				symmetricValue: symKey
			}
		};
	}

	/**
	 * Build Auth2FA message for peer TOTP verification (RustDesk unattended 2FA).
	 * @param code - 6-digit TOTP code
	 * @param hwid - Optional hardware id for trusted-device bypass
	 */
	buildAuth2FA(code: string | null | undefined, hwid?: Uint8Array | null): hbb.MessageInit {
		const auth2fa: hbb.Auth2FAInit = { code: String(code || '').trim() };
		if (hwid && hwid.length) auth2fa.hwid = hwid;
		return { auth_2fa: auth2fa };
	}

	/**
	 * Build LoginRequest for a dedicated FILE_TRANSFER session (RustDesk file manager).
	 */
	buildFileTransferLoginRequest(
		passwordHash: Uint8Array,
		opts: RDFileTransferLoginOptions = {}
	): hbb.MessageInit {
		return {
			loginRequest: {
				username: opts.username || '',
				password: passwordHash,
				myId: opts.myId || 'web-client-ft',
				myName: opts.myName || 'NeoDesk Web',
				myPlatform: 'Web',
				version: RDProtocol.CLIENT_VERSION,
				sessionId: Date.now(),
				fileTransfer: {
					dir: opts.dir != null ? opts.dir : '',
					showHidden: !!opts.showHidden
				}
			}
		};
	}

	/**
	 * Build LoginRequest message
	 */
	buildLoginRequest(passwordHash: Uint8Array, opts: RDLoginRequestOptions = {}): hbb.MessageInit {
		// Dynamically detect codec support
		const hasWebCodecs = typeof VideoDecoder !== 'undefined';
		const hasJMuxer = typeof JMuxer === 'function';

		// Default to Best quality for sharpest image
		const quality = opts.imageQuality || 'Best';

		return {
			loginRequest: {
				username: opts.username || '',
				password: passwordHash,
				myId: opts.myId || 'web-client',
				myName: opts.myName || 'NeoDesk Web',
				myPlatform: 'Web',
				version: RDProtocol.SESSION_VERSION,
				sessionId: Date.now(),
				option: {
					imageQuality:
						enumLookup(this.enums.ImageQuality.values, quality) || this.enums.ImageQuality.values.Best,
					supportedDecoding: this.buildSupportedDecoding({
						abilities: opts.codecAbilities,
						prefer: opts.preferCodec,
						hasWebCodecs,
						hasJMuxer
					}),
					customFps: opts.fps || 60,
					showRemoteCursor: 2, // Yes
					disableAudio: opts.disableAudio ? 2 : 1,
					disableClipboard: 1, // No
					enableFileTransfer: 2, // Yes — required for file transfer on DEFAULT_CONN
					lockAfterSessionEnd: 1, // No
					// Watch-only guests: the device ignores keyboard and mouse
					...(opts.disableKeyboard ? { disableKeyboard: 2 } : {})
				}
			}
		};
	}

	/**
	 * Map a PreferCodec name (Auto/VP9/H264/H265/VP8/AV1) to its enum value.
	 */
	preferCodecValue(name: string | null | undefined): number | hbb.SupportedDecoding_PreferCodecName {
		if (!name) return 0;
		const v = this.enums.PreferCodec.values;
		const value = enumLookup(v, name);
		return v && value !== undefined ? value : 0;
	}

	/**
	 * Build a SupportedDecoding object advertising decode abilities + preferred codec.
	 * The preference is only ever a codec the abilities include: a device
	 * takes a VP9 preference without looking at the abilities, so a VP9
	 * choice saved over HTTPS would give a viewer without WebCodecs video it
	 * cannot play. Another codec falls back to Auto, or H.264 without
	 * WebCodecs.
	 */
	buildSupportedDecoding(o: RDSupportedDecodingOptions = {}): hbb.SupportedDecodingInit {
		const hasWebCodecs = o.hasWebCodecs !== undefined ? o.hasWebCodecs : typeof VideoDecoder !== 'undefined';
		const hasJMuxer = o.hasJMuxer !== undefined ? o.hasJMuxer : typeof JMuxer === 'function';
		const a = o.abilities || null;
		const can = (key: keyof RDCodecAbilities, fallback: boolean) => (a ? (a[key] ? 1 : 0) : fallback ? 1 : 0);
		const decoding = {
			abilityVp9: can('vp9', hasWebCodecs),
			abilityH264: can('h264', hasWebCodecs || hasJMuxer),
			abilityH265: can('h265', hasWebCodecs),
			abilityAv1: can('av1', hasWebCodecs),
			abilityVp8: can('vp8', hasWebCodecs)
		};
		const fallback = hasWebCodecs ? 'Auto' : 'H264';
		const wanted = o.prefer || fallback;
		const ability: Record<string, number | undefined> = {
			VP9: decoding.abilityVp9,
			H264: decoding.abilityH264,
			H265: decoding.abilityH265,
			AV1: decoding.abilityAv1,
			VP8: decoding.abilityVp8
		};
		const playable = wanted === 'Auto' || ability[wanted.toUpperCase()] === 1;
		return { ...decoding, prefer: this.preferCodecValue(playable ? wanted : fallback) };
	}

	/**
	 * Build SupportedEncoding for Misc.supported_encoding (RustDesk 1.4.x negotiation).
	 * @param abilities - probed { h264, h265, vp8, av1 } booleans
	 */
	buildSupportedEncoding(abilities?: RDCodecAbilities | null): hbb.SupportedEncodingInit {
		const a: RDCodecAbilities = abilities || {};
		const has = (key: keyof RDCodecAbilities, fallback: boolean) => (a[key] != null ? !!a[key] : !!fallback);
		const hasWebCodecs = typeof VideoDecoder !== 'undefined';
		const hasJMuxer = typeof JMuxer === 'function';
		return {
			h264: has('h264', hasWebCodecs || hasJMuxer),
			h265: has('h265', hasWebCodecs),
			vp8: has('vp8', hasWebCodecs),
			av1: has('av1', hasWebCodecs),
			i444: {
				vp8: false,
				vp9: false,
				av1: false,
				h264: false,
				h265: false
			}
		};
	}

	/**
	 * Build MouseEvent message
	 */
	buildMouseEvent(
		mask: number,
		x: number,
		y: number,
		modifiers: (hbb.ControlKeyName | number)[] = []
	): hbb.MessageInit {
		return {
			mouseEvent: { mask, x, y, modifiers }
		};
	}

	/**
	 * Build TestDelay message (ping)
	 */
	buildTestDelay(): hbb.MessageInit {
		return {
			testDelay: {
				time: Date.now(),
				fromClient: true,
				lastDelay: 0,
				targetBitrate: 0
			}
		};
	}

	/**
	 * Build Clipboard message (optional zstd compression for large payloads)
	 */
	async buildClipboard(text: string | null | undefined): Promise<hbb.MessageInit> {
		const encoder = new TextEncoder();
		const raw = encoder.encode(text || '');
		const packed = await RDCompress.compressZstd(raw);
		return {
			clipboard: {
				compress: packed.compress,
				content: packed.content,
				format: this.enums.ClipboardFormat.values.Text
			}
		};
	}

	/**
	 * Build a multi-format clipboard message.
	 *
	 * Entries use the decoded RDClipboard shape:
	 * { text, html, pngBlob } (at least one value is required).
	 */
	async buildMultiClipboards(entry: RDMultiClipboardEntry | null | undefined): Promise<hbb.MessageInit> {
		const value = entry || {};
		const entries: hbb.ClipboardInit[] = [];
		const addBytes = async (content: Uint8Array, format: number) => {
			if (!content || !content.length) return;
			const packed = await RDCompress.compressZstd(content);
			entries.push({
				compress: packed.compress,
				content: packed.content,
				format
			});
		};
		const formats = this.enums.ClipboardFormat.values;
		if (value.text) {
			await addBytes(new TextEncoder().encode(String(value.text)), formats.Text);
		}
		if (value.html) {
			await addBytes(new TextEncoder().encode(String(value.html)), formats.Html);
		}
		if (value.pngBlob) {
			const blob =
				value.pngBlob instanceof Blob ? value.pngBlob : new Blob([value.pngBlob], { type: 'image/png' });
			if (blob.size > RDProtocol.MAX_CLIPBOARD_IMAGE_BYTES) {
				throw new Error('clipboard_image_too_large');
			}
			await addBytes(new Uint8Array(await blob.arrayBuffer()), formats.ImagePng);
		}
		if (!entries.length) {
			throw new Error('clipboard_empty');
		}
		return { multiClipboards: { clipboards: entries } };
	}

	/**
	 * Build Cliprdr MonitorReady (initialize file clipboard channel)
	 */
	buildCliprdrMonitorReady(): hbb.MessageInit {
		return {
			cliprdr: {
				ready: {}
			}
		};
	}

	/**
	 * Build Cliprdr FormatList advertising file descriptor + contents formats
	 * @param names - optional native format id/name map from desktop bridge
	 */
	buildCliprdrFormatList(names?: RDCliprdrFormatNames | null): hbb.MessageInit {
		const fdId =
			names && (names.fileDescriptorFormatId != null || names.file_descriptor_format_id != null)
				? Number(
						names.fileDescriptorFormatId != null
							? names.fileDescriptorFormatId
							: names.file_descriptor_format_id
					)
				: 49334;
		const fdName =
			names && (names.fileDescriptorFormatName || names.file_descriptor_format_name)
				? String(names.fileDescriptorFormatName || names.file_descriptor_format_name)
				: 'FileGroupDescriptorW';
		const fcId =
			names && (names.fileContentsFormatId != null || names.file_contents_format_id != null)
				? Number(
						names.fileContentsFormatId != null ? names.fileContentsFormatId : names.file_contents_format_id
					)
				: 49267;
		const fcName =
			names && (names.fileContentsFormatName || names.file_contents_format_name)
				? String(names.fileContentsFormatName || names.file_contents_format_name)
				: 'FileContents';
		return {
			cliprdr: {
				formatList: {
					formats: [
						{ id: fdId, format: fdName },
						{ id: fcId, format: fcName }
					]
				}
			}
		};
	}

	buildCliprdrFormatDataRequest(requestedFormatId: number): hbb.MessageInit {
		return {
			cliprdr: {
				formatDataRequest: {
					requestedFormatId: requestedFormatId
				}
			}
		};
	}

	buildCliprdrFileContentsRequest(opts: RDCliprdrFileContentsRequestOptions = {}): hbb.MessageInit {
		return {
			cliprdr: {
				fileContentsRequest: {
					streamId: opts.streamId || 0,
					listIndex: opts.listIndex || 0,
					dwFlags: opts.dwFlags || 0,
					nPositionLow: opts.nPositionLow || 0,
					nPositionHigh: opts.nPositionHigh || 0,
					cbRequested: opts.cbRequested || 0,
					haveClipDataId: !!opts.haveClipDataId,
					clipDataId: opts.clipDataId || 0
				}
			}
		};
	}

	buildCliprdrFormatDataResponse(msgFlags: number, formatData: RDBinaryPayload): hbb.MessageInit {
		const bytes =
			formatData instanceof Uint8Array
				? formatData
				: typeof formatData === 'string'
					? // Callers must pass binary; string here is almost always mistaken base64.
						// `new Uint8Array(string)` is length 0 — refuse silent empty FormatData.
						LocalFiles.coerceBinaryPayload(formatData)
					: Array.isArray(formatData) || formatData instanceof ArrayBuffer
						? new Uint8Array(formatData)
						: new Uint8Array(0);
		return {
			cliprdr: {
				formatDataResponse: {
					msgFlags: msgFlags,
					formatData: bytes
				}
			}
		};
	}

	buildCliprdrFileContentsResponse(
		msgFlags: number,
		streamId: number,
		requestedData: RDBinaryPayload
	): hbb.MessageInit {
		const bytes =
			requestedData instanceof Uint8Array
				? requestedData
				: typeof requestedData === 'string'
					? LocalFiles.coerceBinaryPayload(requestedData)
					: Array.isArray(requestedData) || requestedData instanceof ArrayBuffer
						? new Uint8Array(requestedData)
						: new Uint8Array(0);
		return {
			cliprdr: {
				fileContentsResponse: {
					msgFlags: msgFlags,
					streamId: streamId,
					requestedData: bytes
				}
			}
		};
	}

	buildCliprdrFormatListResponse(msgFlags: number): hbb.MessageInit {
		return {
			cliprdr: {
				formatListResponse: {
					msgFlags: msgFlags
				}
			}
		};
	}

	buildCliprdrTryEmpty(): hbb.MessageInit {
		return {
			cliprdr: {
				tryEmpty: {}
			}
		};
	}

	/**
	 * Build a Misc message wrapping an OptionMessage for mid-session setting changes.
	 * BoolOption values: NotSet=0, No=1, Yes=2
	 * @param opts - OptionMessage fields
	 */
	buildOptionMisc(opts: RDOptionMiscOptions = {}): hbb.MessageInit {
		const optionMsg: hbb.OptionMessageInit = {};
		if (opts.imageQuality !== undefined) {
			optionMsg.imageQuality = enumLookup(this.enums.ImageQuality.values, opts.imageQuality) || 0;
		}
		if (opts.customImageQuality !== undefined) {
			optionMsg.customImageQuality = opts.customImageQuality;
		}
		if (opts.customFps !== undefined) {
			optionMsg.customFps = opts.customFps;
		}
		if (opts.lockAfterSessionEnd !== undefined) {
			optionMsg.lockAfterSessionEnd = opts.lockAfterSessionEnd ? 2 : 1;
		}
		if (opts.showRemoteCursor !== undefined) {
			optionMsg.showRemoteCursor = opts.showRemoteCursor ? 2 : 1;
		}
		if (opts.privacyMode !== undefined) {
			optionMsg.privacyMode = opts.privacyMode ? 2 : 1;
		}
		if (opts.blockInput !== undefined) {
			optionMsg.blockInput = opts.blockInput ? 2 : 1;
		}
		if (opts.disableAudio !== undefined) {
			optionMsg.disableAudio = opts.disableAudio ? 2 : 1;
		}
		if (opts.disableClipboard !== undefined) {
			optionMsg.disableClipboard = opts.disableClipboard ? 2 : 1;
		}
		if (opts.disableKeyboard !== undefined) {
			optionMsg.disableKeyboard = opts.disableKeyboard ? 2 : 1;
		}
		if (opts.supportedDecoding !== undefined) {
			optionMsg.supportedDecoding = this.buildSupportedDecoding(opts.supportedDecoding);
		}
		return {
			misc: { option: optionMsg }
		};
	}

	/**
	 * Build a Misc message for various control commands
	 * @param field - Misc oneof field name
	 * @param value - Field value
	 */
	buildMisc<K extends keyof hbb.MiscInit>(field: K, value: hbb.MiscInit[K]): hbb.MessageInit {
		const misc: hbb.MiscInit = {};
		misc[field] = value;
		return { misc: misc };
	}

	/**
	 * Build ChatMessage wrapped in Misc
	 */
	buildChatMessage(text: string): hbb.MessageInit {
		return {
			misc: {
				chatMessage: { text: text }
			}
		};
	}

	/**
	 * Build TogglePrivacyMode wrapped in Misc
	 */
	buildTogglePrivacyMode(on: boolean): hbb.MessageInit {
		return {
			misc: {
				togglePrivacyMode: {
					implKey: 'privacy_mode_impl_virtual_display',
					on: on
				}
			}
		};
	}

	// ---- File Transfer builders ----

	/**
	 * Build FileAction.read_dir message
	 * @param path - Directory path to read
	 * @returns Message object for _sendPeerMessage
	 */
	buildReadDir(path: string | null | undefined, includeHidden?: boolean): hbb.MessageInit {
		return {
			fileAction: {
				readDir: {
					path: path || '',
					includeHidden: !!includeHidden
				}
			}
		};
	}

	/**
	 * Build FileAction.receive (operator uploads to remote — remote dir + FileEntry[]).
	 * @param id - Transfer ID
	 * @param path - Remote directory path
	 * @param files - Array of { name, size, modified_time, entry_type }
	 * @param fileNum - File sequence number in transfer
	 * @param totalSize - Total bytes
	 */
	buildFileReceiveRequest(
		id: number,
		path: string,
		files: RDFileReceiveEntry[] | null | undefined,
		fileNum?: number,
		totalSize?: number
	): hbb.MessageInit {
		const normalized = (files || []).map((f) => {
			const entry = {
				entryType: f.entryType != null ? f.entryType : f.entry_type != null ? f.entry_type : 4,
				name: f.name || '',
				size: Number(f.size || 0),
				modifiedTime: Number(f.modifiedTime != null ? f.modifiedTime : f.modified_time || 0),
				isHidden: !!(f.isHidden || f.is_hidden)
			};
			if (this.types.FileEntry) {
				return this.types.FileEntry.create(entry);
			}
			return entry;
		});
		return {
			fileAction: {
				receive: {
					id: id,
					path: path,
					files: normalized,
					fileNum: fileNum || 0,
					totalSize: totalSize || 0
				}
			}
		};
	}

	/**
	 * Build FileAction.send (operator downloads from remote — full remote file path).
	 * @param id - Transfer ID
	 * @param path - Remote destination path
	 */
	buildFileSendRequest(id: number, path: string, includeHidden?: boolean, fileNum?: number): hbb.MessageInit {
		return {
			fileAction: {
				send: {
					id: id,
					path: path,
					includeHidden: !!includeHidden,
					fileNum: fileNum || 0
				}
			}
		};
	}

	/**
	 * Build FileAction.send_confirm (confirm/skip download after digest)
	 * @param id - Transfer ID
	 * @param fileNum - File number
	 * @param skip - Whether to skip this file
	 * @param offsetBlk - Block offset for resume
	 */
	buildFileSendConfirm(id: number, fileNum: number, skip?: boolean, offsetBlk?: number): hbb.MessageInit {
		const confirm: hbb.FileTransferSendConfirmRequestInit = { id: id, fileNum: fileNum };
		if (skip) {
			confirm.skip = true;
		} else {
			confirm.offsetBlk = offsetBlk || 0;
		}
		return {
			fileAction: { sendConfirm: confirm }
		};
	}

	/**
	 * Build FileAction.cancel
	 * @param id - Transfer ID to cancel
	 */
	buildFileCancel(id: number): hbb.MessageInit {
		return {
			fileAction: {
				cancel: { id: id }
			}
		};
	}

	/**
	 * Build FileAction.create (create directory)
	 */
	buildFileDirCreate(id: number, path: string): hbb.MessageInit {
		return {
			fileAction: {
				create: { id: id, path: path }
			}
		};
	}

	/**
	 * Build FileAction.remove_file
	 */
	buildFileRemove(id: number, path: string, fileNum?: number): hbb.MessageInit {
		return {
			fileAction: {
				removeFile: { id: id, path: path, fileNum: fileNum || 0 }
			}
		};
	}

	/**
	 * Build FileAction.remove_dir
	 */
	buildFileRemoveDir(id: number, path: string, recursive?: boolean): hbb.MessageInit {
		return {
			fileAction: {
				removeDir: { id: id, path: path, recursive: !!recursive }
			}
		};
	}

	/**
	 * Build FileAction.rename
	 */
	buildFileRename(id: number, path: string, newName: string): hbb.MessageInit {
		return {
			fileAction: {
				rename: { id: id, path: path, newName: newName }
			}
		};
	}

	/**
	 * Build FileResponse.digest (operator sends local file metadata before upload blocks).
	 * @param lastModified - Unix seconds
	 */
	buildFileDigest(
		id: number,
		fileNum: number | null | undefined,
		fileSize: number | null | undefined,
		lastModified: number | null | undefined,
		opts: RDFileDigestOptions = {}
	): hbb.MessageInit {
		const digest: hbb.FileTransferDigestInit = {
			id: id,
			fileNum: fileNum || 0,
			fileSize: Number(fileSize || 0),
			lastModified: Number(lastModified || 0)
		};
		if (opts.isUpload) digest.isUpload = true;
		if (opts.isIdentical) digest.isIdentical = true;
		if (opts.isResume) digest.isResume = true;
		if (opts.transferredSize != null) {
			digest.transferredSize = Number(opts.transferredSize);
		}
		return { fileResponse: { digest: digest } };
	}

	/**
	 * Build FileResponse.block (upload data block)
	 */
	buildFileBlock(
		id: number,
		fileNum: number,
		data: RDBinaryPayload,
		compressed?: boolean,
		blkId?: number
	): hbb.MessageInit {
		const bytes =
			data instanceof Uint8Array
				? data
				: typeof data === 'string'
					? LocalFiles.coerceBinaryPayload(data)
					: Array.isArray(data) || data instanceof ArrayBuffer
						? new Uint8Array(data)
						: new Uint8Array(0);
		return {
			fileResponse: {
				block: {
					id: id,
					fileNum: fileNum,
					data: bytes,
					compressed: !!compressed,
					blkId: blkId || 0
				}
			}
		};
	}

	/**
	 * Build FileResponse.done (upload complete)
	 */
	buildFileDone(id: number, fileNum: number): hbb.MessageInit {
		return {
			fileResponse: {
				done: { id: id, fileNum: fileNum }
			}
		};
	}

	// ---- Message parsing helpers ----

	/**
	 * Get the active field name from a oneof message
	 */
	getOneOfField(msg: object | null | undefined, oneofName: string): string | null {
		if (!msg) return null;
		// protobufjs stores the active field name directly
		for (const [key, value] of Object.entries(msg)) {
			if (key !== oneofName && value != null) {
				return key;
			}
		}
		return null;
	}

	/**
	 * Detect video codec from VideoFrame message
	 */
	detectVideoCodec(videoFrame: hbb.VideoFrame): RDVideoCodec | null {
		if (videoFrame.vp9s) return 'vp9';
		if (videoFrame.h264s) return 'h264';
		if (videoFrame.h265s) return 'h265';
		if (videoFrame.vp8s) return 'vp8';
		if (videoFrame.av1s) return 'av1';
		if (videoFrame.rgb) return 'rgb';
		if (videoFrame.yuv) return 'yuv';
		return null;
	}

	/**
	 * Extract encoded frames from VideoFrame
	 */
	getEncodedFrames(videoFrame: hbb.VideoFrame): RDEncodedFrame[] {
		const codec = this.detectVideoCodec(videoFrame);
		if (!codec || codec === 'rgb' || codec === 'yuv') return [];

		const key = `${codec}s` as const;
		const container = videoFrame[key];
		if (!container || !container.frames) return [];

		return container.frames.map((f) => ({
			data: f.data,
			key: f.key,
			pts: f.pts,
			codec: codec
		}));
	}

	// ---- RustDesk Variable-Length Frame Codec ----
	// hbb_common/src/bytes_codec.rs uses a variable-length header:
	//   bottom 2 bits of byte 0 = (header_length - 1)
	//   remaining bits (shifted right 2) = payload length
	// Header is 1-4 bytes, little-endian.
	//   1 byte:  payload ≤ 63 bytes
	//   2 bytes: payload ≤ 16383 bytes
	//   3 bytes: payload ≤ 4194303 bytes
	//   4 bytes: payload ≤ 1073741823 bytes

	/**
	 * Encode protobuf bytes into a RustDesk frame (variable-length header + payload)
	 * @param buf - Raw protobuf bytes
	 */
	_encodeFrame(buf: Uint8Array): Uint8Array<ArrayBuffer> {
		const len = buf.length;
		let header: Uint8Array;

		if (len <= 0x3f) {
			header = new Uint8Array(1);
			header[0] = len << 2; // bottom 2 bits = 0b00
		} else if (len <= 0x3fff) {
			header = new Uint8Array(2);
			const val = (len << 2) | 0x1; // bottom 2 bits = 0b01
			header[0] = val & 0xff;
			header[1] = (val >> 8) & 0xff;
		} else if (len <= 0x3fffff) {
			header = new Uint8Array(3);
			const val = (len << 2) | 0x2; // bottom 2 bits = 0b10
			header[0] = val & 0xff;
			header[1] = (val >> 8) & 0xff;
			header[2] = (val >> 16) & 0xff;
		} else if (len <= 0x3fffffff) {
			header = new Uint8Array(4);
			const val = (len << 2) | 0x3; // bottom 2 bits = 0b11
			header[0] = val & 0xff;
			header[1] = (val >> 8) & 0xff;
			header[2] = (val >> 16) & 0xff;
			header[3] = (val >>> 24) & 0xff;
		} else {
			throw new Error('Frame too large');
		}

		const result = new Uint8Array(header.length + buf.length);
		result.set(header);
		result.set(buf, header.length);
		return result;
	}

	/**
	 * Try to decode one frame from a byte buffer at the given offset
	 * @returns null if incomplete
	 */
	_decodeFrame(bytes: Uint8Array, offset: number): { data: Uint8Array; consumed: number } | null {
		if (offset >= bytes.length) return null;

		// A read past the end is undefined, which bitwise operators take as 0
		const firstByte = bytes[offset] ?? 0;
		const headLen = (firstByte & 0x3) + 1;

		if (offset + headLen > bytes.length) return null;

		let n = bytes[offset] ?? 0;
		if (headLen > 1) n |= (bytes[offset + 1] ?? 0) << 8;
		if (headLen > 2) n |= (bytes[offset + 2] ?? 0) << 16;
		if (headLen > 3) n |= (bytes[offset + 3] ?? 0) << 24;
		n >>>= 2; // unsigned right shift to get payload length

		if (offset + headLen + n > bytes.length) return null;

		return {
			data: bytes.slice(offset + headLen, offset + headLen + n),
			consumed: headLen + n
		};
	}

	/**
	 * Create a stateful stream decoder that handles TCP reassembly
	 * Feed it raw WebSocket binary data chunks, and it returns complete protobuf frames.
	 * Uses a pre-allocated growing buffer to avoid O(n²) copies on every chunk.
	 */
	createStreamDecoder(): RDStreamDecoder {
		let buffer = new Uint8Array(0);
		/** Valid data length within buffer (buffer.length may be larger) */
		let dataLen = 0;

		return {
			feed: (data) => {
				const incoming = data instanceof ArrayBuffer ? new Uint8Array(data) : data;

				// Append to buffer (grow capacity if needed)
				const needed = dataLen + incoming.length;
				if (needed > buffer.length) {
					// Grow to at least 2x current or needed, whichever is larger
					const newCap = Math.max(needed, buffer.length * 2, 4096);
					const newBuf = new Uint8Array(newCap);
					if (dataLen > 0) newBuf.set(buffer.subarray(0, dataLen));
					buffer = newBuf;
				}
				buffer.set(incoming, dataLen);
				dataLen += incoming.length;

				const frames: Uint8Array[] = [];
				let offset = 0;

				// Decode frames using a view of the valid portion
				const view = buffer.subarray(0, dataLen);
				while (offset < dataLen) {
					const frame = this._decodeFrame(view, offset);
					if (!frame) break; // Incomplete frame, wait for more data
					frames.push(frame.data);
					offset += frame.consumed;
				}

				// Compact: move unconsumed bytes to front of buffer
				if (offset > 0) {
					const remaining = dataLen - offset;
					if (remaining > 0) {
						buffer.copyWithin(0, offset, dataLen);
					}
					dataLen = remaining;
				}

				// Release oversized backing store after large spikes
				if (dataLen === 0 && buffer.length > 65536) {
					buffer = new Uint8Array(0);
				}

				return frames;
			},

			reset: () => {
				buffer = new Uint8Array(0);
				dataLen = 0;
			}
		};
	}

	static MAX_CLIPBOARD_IMAGE_BYTES = 8 * 1024 * 1024;
}
