/**
 * The bytes the RustDesk client of the remote viewer puts on the wire, pinned
 * as hex: every message the protocol layer builds, and a whole session
 * against a fake device (rendezvous, relay, handshake, login, keys, mouse,
 * clipboard, settings and the file transfer session). Randomness (session
 * keys, UUIDs) and clocks are fixed, so the encrypted frames are fixed too.
 * Any change to how the client encodes, encrypts or frames a message shows
 * up here as a changed byte.
 */
import { TextEncoder as NodeTextEncoder } from 'node:util';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { loadScripts, RD_CORE, type Loaded } from './rdclient-scripts.ts';

/** The client's scripts as the viewer page loads them (JMuxer and zstd present) */
const g: Loaded = loadScripts(
	[
		...RD_CORE,
		'vendor/jmuxer.min.js',
		'video.js',
		'audio.js',
		'renderer.js',
		'keyboard-scancode.js',
		'keyboard-encoder.js',
		'input.js',
		'file-connection.js',
		'compress.js',
		'clipboard.js',
		'cliprdr.js',
		'filetransfer.js',
		'client.js'
	],
	['RDClient', 'RDProtocol', 'nacl']
);
const { RDClient, RDProtocol, nacl } = g;

type Bytes = Uint8Array;
type KeyPair = { publicKey: Bytes; secretKey: Bytes };
// eslint-disable-next-line @typescript-eslint/no-explicit-any
type Proto = any;

const NOW = 1_700_000_000_000;
const UUID = '1b4e28ba-2fa1-41d2-883f-0016d3cca427';
const DEVICE_ID = '900100012';
const RELAY = 'relay.example.com:21117';

const hex = (b: Bytes) => Array.from(b, (x) => x.toString(16).padStart(2, '0')).join('');
const b64 = (b: Bytes) => btoa(String.fromCharCode(...b));
const seed = (n: number) => new Uint8Array(32).fill(n);
const bytes = (...v: number[]) => new Uint8Array(v);

/** A fixed byte stream for tweetnacl's randomBytes (session keys, box key pairs) */
function fixedRandom() {
	let s = 0x2545f491;
	nacl.setPRNG((out: Bytes, n: number) => {
		for (let i = 0; i < n; i++) {
			s = (Math.imul(s, 1103515245) + 12345) >>> 0;
			out[i] = s >>> 24;
		}
	});
}

/** Counter nonce of RustDesk's secretbox framing: the sequence number, little endian */
function nonce(seq: number) {
	const n = new Uint8Array(24);
	let v = seq;
	for (let i = 0; i < 8; i++) {
		n[i] = v & 0xff;
		v = Math.floor(v / 256);
	}
	return n;
}

/** The device's keys and the two signed blobs the client checks */
const server: KeyPair = nacl.sign.keyPair.fromSeed(seed(1));
const identity: KeyPair = nacl.sign.keyPair.fromSeed(seed(2));
const deviceBox: KeyPair = nacl.box.keyPair.fromSecretKey(seed(3));

function idPk(proto: Proto, id: string, pk: Bytes): Bytes {
	return proto.types.IdPk.encode(proto.types.IdPk.create({ id, pk })).finish();
}

/** One relay connection of the fake device: it learns the session key from the client's PublicKey */
function deviceEnd(proto: Proto) {
	let key: Bytes | null = null;
	let sendSeq = 0;
	let recvSeq = 0;
	return {
		relayPk: nacl.sign(idPk(proto, DEVICE_ID, identity.publicKey), server.secretKey) as Bytes,
		signedId(): Bytes {
			const id = nacl.sign(idPk(proto, DEVICE_ID, deviceBox.publicKey), identity.secretKey);
			return proto.serializeMessage({ signedId: { id } });
		},
		takeKey(frame: Bytes) {
			const pk = proto.decodeMessage(frame).publicKey;
			key = nacl.box.open(pk.symmetricValue, new Uint8Array(24), pk.asymmetricValue, deviceBox.secretKey);
		},
		sealed(msg: unknown): Bytes {
			return nacl.secretbox(proto.serializeMessage(msg), nonce(++sendSeq), key);
		},
		/** The message in the client's next encrypted frame, as its field and the fields set below it */
		open(frame: Bytes): string {
			const plain = nacl.secretbox.open(frame, nonce(++recvSeq), key) as Bytes | null;
			if (!plain) return 'undecryptable';
			const msg = proto.decodeMessage(plain);
			const outer = Object.keys(msg).find((k) => msg[k] != null && k !== 'union') ?? 'empty';
			const inner = msg[outer];
			return inner && typeof inner === 'object' && !(inner instanceof Uint8Array)
				? `${outer}: ${Object.keys(inner).join(' ')}`
				: outer;
		}
	};
}

/**
 * TextEncoder as a browser has it. The test's Uint8Array is jsdom's, Node's
 * TextEncoder returns Node's, and the client's byte checks (instanceof
 * Uint8Array) would refuse those bytes, so the encoded text is copied over.
 */
class PageTextEncoder {
	readonly encoding = 'utf-8';
	encode(text = ''): Uint8Array {
		return new Uint8Array(new NodeTextEncoder().encode(text));
	}
}

/** A socket that opens when the test says so; it keeps what the client sent */
class FakeSocket {
	static CONNECTING = 0;
	static OPEN = 1;
	static CLOSING = 2;
	static CLOSED = 3;
	static made: FakeSocket[] = [];
	readyState = 0;
	binaryType = '';
	sent: Bytes[] = [];
	onopen: (() => void) | null = null;
	onerror: ((e: unknown) => void) | null = null;
	onclose: ((e: unknown) => void) | null = null;
	onmessage: ((e: { data: unknown }) => void) | null = null;
	constructor(public url: string) {
		FakeSocket.made.push(this);
	}
	send(d: Bytes) {
		this.sent.push(new Uint8Array(d));
	}
	close() {
		this.readyState = 3;
	}
	open() {
		this.readyState = 1;
		this.onopen?.();
	}
	message(data: Bytes) {
		this.onmessage?.({ data: data.slice().buffer });
	}
}

async function socket(n: number): Promise<FakeSocket> {
	await vi.waitFor(() => expect(FakeSocket.made.length).toBeGreaterThan(n));
	return FakeSocket.made[n]!;
}

async function sentCount(s: FakeSocket, n: number) {
	await vi.waitFor(() => expect(s.sent.length).toBeGreaterThanOrEqual(n));
}

beforeEach(() => {
	vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(null);
	vi.spyOn(Date, 'now').mockReturnValue(NOW);
	vi.spyOn(globalThis.crypto, 'randomUUID').mockReturnValue(UUID);
	FakeSocket.made = [];
	vi.stubGlobal('WebSocket', FakeSocket);
	vi.stubGlobal('TextEncoder', PageTextEncoder);
	fixedRandom();
});
afterEach(() => {
	vi.useRealTimers();
	vi.unstubAllGlobals();
});

describe('messages the protocol layer builds', () => {
	test('each builder serializes to the same bytes', async () => {
		const p = new RDProtocol();
		await p.load();
		const msg = (m: unknown) => hex(p.serializeMessage(m));
		const rdv = (m: unknown) => hex(p.encodeRendezvous(m));
		const hash = bytes(...Array.from({ length: 32 }, (_, i) => i));
		const out = {
			punchHoleRequest: rdv(p.buildPunchHoleRequest(DEVICE_ID, 'c2VydmVya2V5')),
			punchHoleRequestFiles: rdv(
				p.buildPunchHoleRequest(DEVICE_ID, '', p.enums.ConnType.values.FILE_TRANSFER)
			),
			requestRelayFramed: rdv(p.buildRequestRelay(DEVICE_ID, UUID, RELAY, 'c2VydmVya2V5')),
			requestRelay: hex(p.serializeRendezvous(p.buildRequestRelay(DEVICE_ID, UUID, '', ''))),
			publicKey: msg(p.buildPublicKey(bytes(1, 2, 3), bytes(4, 5, 6))),
			auth2fa: msg(p.buildAuth2FA(' 123456 ')),
			auth2faHwid: msg(p.buildAuth2FA('654321', bytes(9, 8, 7))),
			loginRequest: msg(
				p.buildLoginRequest(hash, {
					username: DEVICE_ID,
					myId: 'web-1',
					myName: 'Operator',
					fps: 30,
					imageQuality: 'Balanced',
					codecAbilities: { vp9: true, h264: true, h265: false, av1: false, vp8: true },
					preferCodec: 'VP9'
				})
			),
			loginRequestGuest: msg(p.buildLoginRequest(hash, { disableAudio: true, disableKeyboard: true })),
			loginRequestFiles: msg(
				p.buildFileTransferLoginRequest(hash, {
					username: DEVICE_ID,
					myId: 'ft-1',
					dir: 'C:\\',
					showHidden: true
				})
			),
			mouseEvent: msg(p.buildMouseEvent(9, 640, 360, [29, 4])),
			testDelay: msg(p.buildTestDelay()),
			clipboard: msg(await p.buildClipboard('hello from the operator')),
			multiClipboards: msg(await p.buildMultiClipboards({ text: 'plain', html: '<b>rich</b>' })),
			cliprdrReady: msg(p.buildCliprdrMonitorReady()),
			cliprdrFormatList: msg(p.buildCliprdrFormatList()),
			cliprdrFormatListNamed: msg(
				p.buildCliprdrFormatList({ fileDescriptorFormatId: 50000, fileContentsFormatName: 'Contents' })
			),
			cliprdrFormatListResponse: msg(p.buildCliprdrFormatListResponse(1)),
			cliprdrFormatDataRequest: msg(p.buildCliprdrFormatDataRequest(49334)),
			cliprdrFormatDataResponse: msg(p.buildCliprdrFormatDataResponse(1, bytes(1, 2, 3, 4))),
			cliprdrFileContentsRequest: msg(
				p.buildCliprdrFileContentsRequest({
					streamId: 7,
					listIndex: 1,
					dwFlags: 2,
					nPositionLow: 4096,
					cbRequested: 65536,
					haveClipDataId: true,
					clipDataId: 3
				})
			),
			cliprdrFileContentsResponse: msg(p.buildCliprdrFileContentsResponse(1, 7, [5, 6, 7])),
			cliprdrTryEmpty: msg(p.buildCliprdrTryEmpty()),
			optionMisc: msg(
				p.buildOptionMisc({
					imageQuality: 'Low',
					customImageQuality: 50,
					customFps: 24,
					lockAfterSessionEnd: true,
					showRemoteCursor: false,
					privacyMode: true,
					blockInput: false,
					disableAudio: true,
					disableClipboard: false,
					disableKeyboard: true,
					supportedDecoding: { abilities: { h264: true }, prefer: 'H264' }
				})
			),
			miscRefresh: msg(p.buildMisc('refreshVideo', true)),
			miscSwitchDisplay: msg(p.buildMisc('switchDisplay', { display: 1, width: 0, height: 0 })),
			miscSupportedEncoding: msg(p.buildMisc('supportedEncoding', p.buildSupportedEncoding({ h264: true }))),
			chat: msg(p.buildChatMessage('on my way')),
			privacyMode: msg(p.buildTogglePrivacyMode(true)),
			readDir: msg(p.buildReadDir('C:\\Users', true)),
			fileReceive: msg(
				p.buildFileReceiveRequest(
					11,
					'C:\\dest',
					[
						{ name: 'a.txt', size: 3, modifiedTime: 1_699_999_999 },
						{ name: 'big.bin', size: 5_000_000_000, entry_type: 4, is_hidden: true }
					],
					0,
					5_000_000_003
				)
			),
			fileSend: msg(p.buildFileSendRequest(12, 'C:\\a.txt', true, 2)),
			fileSendConfirm: msg(p.buildFileSendConfirm(12, 0, false, 5)),
			fileSendSkip: msg(p.buildFileSendConfirm(12, 1, true)),
			fileCancel: msg(p.buildFileCancel(12)),
			fileDirCreate: msg(p.buildFileDirCreate(13, 'C:\\new')),
			fileRemove: msg(p.buildFileRemove(14, 'C:\\a.txt', 0)),
			fileRemoveDir: msg(p.buildFileRemoveDir(15, 'C:\\old', true)),
			fileRename: msg(p.buildFileRename(16, 'C:\\a.txt', 'b.txt')),
			fileDigest: msg(
				p.buildFileDigest(11, 0, 3, 1_699_999_999, { isUpload: true, isResume: true, transferredSize: 0 })
			),
			fileBlock: msg(p.buildFileBlock(11, 0, bytes(97, 98, 99), false, 0)),
			fileDone: msg(p.buildFileDone(11, 0)),
			keyEventSeq: msg({ keyEvent: { seq: 'Hi', down: true, press: true, modifiers: [], mode: 'Legacy' } }),
			keyEventControl: msg({
				keyEvent: { controlKey: 'Backspace', down: true, press: false, modifiers: ['Control'], mode: 'Map' }
			})
		};
		expect(out).toMatchInlineSnapshot(`
			{
			  "auth2fa": "da01080a06313233343536",
			  "auth2faHwid": "da010d0a063635343332311203090807",
			  "chat": "9a010d220b0a096f6e206d7920776179",
			  "clipboard": "82011d0800121768656c6c6f2066726f6d20746865206f70657261746f722800",
			  "cliprdrFileContentsRequest": "a20115321310071801200228802030003880800440014803",
			  "cliprdrFileContentsResponse": "a2010b3a09180120072a03050607",
			  "cliprdrFormatDataRequest": "a20106220410b68103",
			  "cliprdrFormatDataResponse": "a2010a2a0810011a0401020304",
			  "cliprdrFormatList": "a201321230121a10b681031a1446696c6547726f757044657363726970746f7257121210f380031a0c46696c65436f6e74656e7473",
			  "cliprdrFormatListNamed": "a2012e122c121a10d086031a1446696c6547726f757044657363726970746f7257120e10f380031a08436f6e74656e7473",
			  "cliprdrFormatListResponse": "a201041a021001",
			  "cliprdrReady": "a201020a00",
			  "cliprdrTryEmpty": "a201024200",
			  "fileBlock": "92010f120d080b10001a0361626320002800",
			  "fileCancel": "8a01044202080c",
			  "fileDigest": "9201142a12080b100018ffe1cfaa062003280138004001",
			  "fileDirCreate": "8a010c220a080d1206433a5c6e6577",
			  "fileDone": "9201062204080b1000",
			  "fileReceive": "8a01411a3f080b1207433a5c646573741a1308041205612e7478741800200328ffe1cfaa061a15080412076269672e62696e18012080e497d012280020002883e497d012",
			  "fileRemove": "8a0110320e080e1208433a5c612e7478741800",
			  "fileRemoveDir": "8a010e2a0c080f1206433a5c6f6c641801",
			  "fileRename": "8a0115521308101208433a5c612e7478741a05622e747874",
			  "fileSend": "8a01121210080c1208433a5c612e74787418012002",
			  "fileSendConfirm": "8a01084a06080c10002005",
			  "fileSendSkip": "8a01084a06080c10021801",
			  "keyEventControl": "7a0b0801100018024201044801",
			  "keyEventSeq": "7a0a08011001320248694800",
			  "loginRequest": "3a7e0a093930303130303031321220000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f22057765622d312a084f70657261746f72321c080310011802380140014802520c080110011800200128013000581e5080d095ffbc315a144265747465724465736b2d5765622f312e342e396a03576562",
			  "loginRequestFiles": "3a6e0a093930303130303031321220000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f220466742d312a0e4265747465724465736b205765623a070a03433a5c10015080d095ffbc315a144265747465724465736b2d5765622f312e342e396a03576562",
			  "loginRequestGuest": "3a82010a001220000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f220a7765622d636c69656e742a0e4265747465724465736b20576562321e080410011802380240014802520c080010011800200228003000583c60025080d095ffbc315a144265747465724465736b2d5765622f312e342e396a03576562",
			  "miscRefresh": "9a01025001",
			  "miscSupportedEncoding": "9a011792021408011000180020002a0a08001000180020002800",
			  "miscSwitchDisplay": "9a01082a06080120002800",
			  "mouseEvent": "520c080910800a18d00522021d04",
			  "multiClipboards": "e201200a0b08001205706c61696e28000a110800120b3c623e726963683c2f623e2802",
			  "optionMisc": "9a01243a2208021002180120022801303238024001520c08001001180020022800300058186002",
			  "privacyMode": "9a01288a02250a21707269766163795f6d6f64655f696d706c5f7669727475616c5f646973706c61791001",
			  "publicKey": "220a0a030102031203040506",
			  "punchHoleRequest": "e442370a0939303031303030313210021a0c63325679646d56796132563520002a0032144265747465724465736b2d5765622f312e342e394001",
			  "punchHoleRequestFiles": "b4422b0a0939303031303030313210021a0020012a0032144265747465724465736b2d5765622f312e342e394001",
			  "readDir": "8a010e0a0c0a08433a5c55736572731001",
			  "requestRelay": "92013b0a09393030313030303132122431623465323862612d326661312d343164322d383833662d30303136643363636134323722002800320038004200",
			  "requestRelayFramed": "850192015e0a09393030313030303132122431623465323862612d326661312d343164322d383833662d303031366433636361343237221772656c61792e6578616d706c652e636f6d3a32313131372800320c63325679646d56796132563538004200",
			  "testDelay": "2a0d0880d095ffbc31100118002000",
			}
		`);
	});
});

describe('a whole session on the wire', () => {
	test('desktop session, then the file transfer session', async () => {
		vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] });
		let perf = 1000;
		vi.spyOn(performance, 'now').mockImplementation(() => (perf += 100));

		const canvas = document.createElement('canvas');
		const c = new RDClient(canvas, {
			deviceId: DEVICE_ID,
			serverPubKey: b64(server.publicKey),
			myName: 'Operator'
		});
		const p: Proto = c.proto;
		const connecting = c.connect();

		// Rendezvous: the client asks for the device, then for a relay with its own UUID
		const rv = await socket(0);
		rv.open();
		await sentCount(rv, 1);
		const d = deviceEnd(p);
		rv.message(p.encodeRendezvous({ punchHoleResponse: { relayServer: RELAY, natType: 1 } }));
		await sentCount(rv, 2);
		rv.message(p.encodeRendezvous({ relayResponse: { uuid: UUID, relayServer: RELAY, pk: d.relayPk } }));

		// Relay: RequestRelay first, the device's SignedId, our PublicKey, then encrypted frames
		const relay = await socket(1);
		relay.open();
		await connecting;
		relay.message(d.signedId());
		await sentCount(relay, 2);
		d.takeKey(relay.sent[1]!);
		relay.message(d.sealed({ hash: { salt: 'salt', challenge: 'challenge' } }));
		await c.authenticate('secret');
		relay.message(d.sealed({ loginResponse: { error: '2FA Required' } }));
		c.submit2FA(' 123456 ');
		relay.message(
			d.sealed({
				loginResponse: {
					peerInfo: {
						username: 'user',
						hostname: 'host',
						platform: 'Windows',
						displays: [{ x: 0, y: 0, width: 1920, height: 1080, name: 'DISPLAY1', online: true }],
						currentDisplay: 0,
						version: '1.4.2'
					}
				}
			})
		);
		expect(c.state).toBe('streaming');

		// The device's QoS probe is echoed, a video frame is acknowledged
		relay.message(
			d.sealed({ testDelay: { time: 123456, fromClient: false, lastDelay: 5, targetBitrate: 1000 } })
		);
		relay.message(d.sealed({ videoFrame: {} }));

		// Keyboard and mouse
		c.renderer.remoteWidth = 1920;
		c.renderer.remoteHeight = 1080;
		vi.spyOn(c.renderer, 'canvasToRemote').mockImplementation((x: number, y: number) => ({ x, y }));
		c.setSessionActive(true);
		const key = (type: string, init: KeyboardEventInit) =>
			document.dispatchEvent(new KeyboardEvent(type, { bubbles: true, cancelable: true, ...init }));
		key('keydown', { key: 'a', code: 'KeyA' });
		key('keyup', { key: 'a', code: 'KeyA' });
		key('keydown', { key: 'Shift', code: 'ShiftLeft', shiftKey: true });
		key('keydown', { key: 'B', code: 'KeyB', shiftKey: true });
		key('keyup', { key: 'B', code: 'KeyB', shiftKey: true });
		key('keyup', { key: 'Shift', code: 'ShiftLeft' });
		key('keydown', { key: 'Enter', code: 'Enter' });
		key('keyup', { key: 'Enter', code: 'Enter' });
		key('keydown', { key: 'Control', code: 'ControlLeft', ctrlKey: true });
		key('keydown', { key: 'c', code: 'KeyC', ctrlKey: true });
		key('keyup', { key: 'c', code: 'KeyC', ctrlKey: true });
		key('keyup', { key: 'Control', code: 'ControlLeft' });
		key('keydown', { key: 'ArrowLeft', code: 'ArrowLeft' });
		key('keyup', { key: 'ArrowLeft', code: 'ArrowLeft' });
		c.setKeyboardMode('Legacy');
		key('keydown', { key: 'Backspace', code: 'Backspace' });
		key('keyup', { key: 'Backspace', code: 'Backspace' });
		key('keydown', { key: 'é', code: 'Digit2' });
		key('keyup', { key: 'é', code: 'Digit2' });
		key('keydown', { key: 'F5', code: 'F5' });
		key('keyup', { key: 'F5', code: 'F5' });
		c.setKeyboardMode('Map');
		const mouse = (type: string, init: MouseEventInit) =>
			canvas.dispatchEvent(new MouseEvent(type, { bubbles: true, cancelable: true, ...init }));
		mouse('mousemove', { clientX: 100, clientY: 200 });
		mouse('mousedown', { button: 0, clientX: 100, clientY: 200 });
		mouse('mouseup', { button: 0, clientX: 110, clientY: 210 });
		mouse('mousedown', { button: 2, clientX: 300, clientY: 400, shiftKey: true });
		mouse('mouseup', { button: 2, clientX: 300, clientY: 400 });
		canvas.dispatchEvent(new WheelEvent('wheel', { deltaY: 120, bubbles: true, cancelable: true }));
		canvas.dispatchEvent(new WheelEvent('wheel', { deltaX: -40, bubbles: true, cancelable: true }));
		c.sendCtrlAltDel();
		c.sendLockScreen();
		const typed = relay.sent.length;
		c.sendText('Hello');
		await sentCount(relay, typed + 2);
		c.resetKeyboard();

		// Clipboard
		let n = relay.sent.length;
		c.sendClipboard('hello from the operator');
		await sentCount(relay, n + 1);
		n = relay.sent.length;
		c.sendClipboard({ text: 'plain', html: '<b>rich</b>' });
		await sentCount(relay, n + 1);

		// Settings and commands
		c.sendRefreshScreen();
		c.sendChat('on my way');
		c.setQualityPreset('speed');
		c.setFpsMode('60');
		c.setCodec('VP9');
		c.setImageQuality('Low');
		c.setCustomFps(24);
		c.setShowRemoteCursor(false);
		c.setBlockInput(true);
		c.setLockAfterSession(true);
		c.setPrivacyMode(true);
		c.setDisableClipboard(true);
		c.setDisableAudio(true);
		c.switchMonitor(0);
		c.setSessionActive(false);
		c.setSessionActive(true);
		c.sendRestartRemoteDevice();
		vi.advanceTimersByTime(3000);

		// The file transfer session: its own rendezvous, relay, handshake and login
		const fd = deviceEnd(p);
		const browsing = c.fileTransfer.browseDir('C:\\Users');
		const frv = await socket(2);
		frv.open();
		await sentCount(frv, 1);
		frv.message(p.encodeRendezvous({ punchHoleResponse: { relayServer: RELAY, natType: 1 } }));
		await sentCount(frv, 2);
		frv.message(p.encodeRendezvous({ relayResponse: { uuid: UUID, relayServer: RELAY, pk: fd.relayPk } }));
		const frelay = await socket(3);
		frelay.open();
		await sentCount(frelay, 1);
		frelay.message(fd.signedId());
		await sentCount(frelay, 2);
		fd.takeKey(frelay.sent[1]!);
		frelay.message(fd.sealed({ hash: { salt: 'fsalt', challenge: 'fchallenge' } }));
		await sentCount(frelay, 3);
		frelay.message(fd.sealed({ loginResponse: { peerInfo: { platform: 'Windows' } } }));
		await browsing;
		await sentCount(frelay, 4);
		c.fileTransfer.createDir('C:\\Users\\new');
		c.fileTransfer.removeFile('C:\\Users\\a.txt');
		c.fileTransfer.removeDir('C:\\Users\\old', true);
		c.fileTransfer.rename('C:\\Users\\a.txt', 'b.txt');
		// Transfer ids count up from 1: the download is 5, the upload 6
		n = frelay.sent.length;
		c.fileTransfer.downloadFile('C:\\Users', { name: 'report.txt', size: 3, modifiedTime: 1_699_999_999 });
		await sentCount(frelay, n + 1);
		frelay.message(
			fd.sealed({ fileResponse: { digest: { id: 5, fileNum: 0, lastModified: 1_699_999_999, fileSize: 3 } } })
		);
		await sentCount(frelay, n + 2);
		n = frelay.sent.length;
		c.fileTransfer.uploadFile(new File(['abc'], 'up.txt', { lastModified: NOW }), 'C:\\Users');
		await sentCount(frelay, n + 2);
		frelay.message(fd.sealed({ fileAction: { sendConfirm: { id: 6, fileNum: 0, offsetBlk: 0 } } }));
		await sentCount(frelay, n + 4);

		const wire = (s: FakeSocket) => s.sent.map(hex);
		const opened = (end: ReturnType<typeof deviceEnd>, s: FakeSocket) =>
			s.sent.slice(2).map((f) => end.open(f));
		expect({
			rendezvous: wire(rv),
			relay: wire(relay),
			relayMessages: opened(d, relay),
			fileRendezvous: wire(frv),
			fileRelay: wire(frelay),
			fileRelayMessages: opened(fd, frelay)
		}).toMatchInlineSnapshot(`
			{
			  "fileRelay": [
			    "92017e0a09393030313030303132122431623465323862612d326661312d343164322d383833662d303031366433636361343237221772656c61792e6578616d706c652e636f6d3a32313131372800322c696f6a6a3358514a385a583955747374504c70646373706e436238646c42496238335349416251506231773d38014200",
			    "22540a20aba48698cdd20e475a57dd029bc6157c3500c20f6de69d3f6da608e33547d4401230856098ec67f4879433d198c889dd10fc5f2865fc79b0cbdcb9898da6f49f901647a3ffdb2a6c6b859830223484bf8cef",
			    "1455c1faadbacc4291967ec8642bbe3125b8d7e03e9e8c5b01c7cb61119653a38fec78fb69fdf0784bcd91ba7bf578c6ddedd168d2775752d7052d70a349e4b5c792e56c485e19bddae63602f786db28515912e27e73ec2fa8e18a8dcc9c8875fc3ed3408c8673be5e4c11458ce58b8f1a29fcbe8b978b3b8e9cce42be8f22eb87888f0bc955f4515bd4940303",
			    "001ab3be4429c9905c69c1996af139023a155fd0cb86c351e247a502d76864321f",
			    "5c07cf4eea98a6d1dbad913518bb22fdcd842579c7fc11be6687dd0a426d4f84ed6acc9b24",
			    "da98818b111ebeb4c6af9a8659cafc6362e3bdce029ea0d201b5980e47ffc1b4a4dbe1547e942975de",
			    "ee55ad8b8d091d8d2facda79cbfd5bc7431f124769a7797ba23a9f248022e783fd5e50259e2fdd",
			    "c033f9594159e12b136225371b2c419fdc3da2a19efd8c2e8d8772ed1869c642bf01dc1313d4f5a6a3d5fd567241",
			    "a3ba654721fc5c0bf003a08df6ee2554ce06e7fe1f6d52ab68c5a2495480b38c5db5ac30a6251eb58ecf5bd06e4c6062",
			    "83d38133d1e715749e6453d257cde2f9eabb29205c12cbf5c3f92a",
			    "5a44f05a3ff517a38c0ec8a453116bf32d34263e0f8b7b0442abd85745777e04ec9dfeb037918fa39513bc2f58299b1f671985c3838339391e5883",
			    "279e442a197f7ff36881002e0e626de8367407307b19f8610bd5daa1a7855092fa",
			    "02d024a0bbe7fb171a567f382613ed7612e3e1769361d79e3d7e498316d482f87766",
			    "917e45190479db39f886c97cf143271262424bb142cec449f7",
			  ],
			  "fileRelayMessages": [
			    "loginRequest: username password myId myName fileTransfer sessionId version myPlatform",
			    "fileAction: readDir",
			    "fileAction: create",
			    "fileAction: removeFile",
			    "fileAction: removeDir",
			    "fileAction: rename",
			    "fileAction: send",
			    "fileAction: sendConfirm",
			    "fileAction: receive",
			    "fileResponse: digest",
			    "fileResponse: block",
			    "fileResponse: done",
			  ],
			  "fileRendezvous": [
			    "650142570a0939303031303030313210021a2c696f6a6a3358514a385a583955747374504c70646373706e436238646c42496238335349416251506231773d20012a0032144265747465724465736b2d5765622f312e342e394001",
			    "050292017e0a09393030313030303132122431623465323862612d326661312d343164322d383833662d303031366433636361343237221772656c61792e6578616d706c652e636f6d3a32313131372800322c696f6a6a3358514a385a583955747374504c70646373706e436238646c42496238335349416251506231773d38014200",
			  ],
			  "relay": [
			    "92017e0a09393030313030303132122431623465323862612d326661312d343164322d383833662d303031366433636361343237221772656c61792e6578616d706c652e636f6d3a32313131372800322c696f6a6a3358514a385a583955747374504c70646373706e436238646c42496238335349416251506231773d38004200",
			    "22540a201bd1d3b74eee368f2e4927031e786a6fae7e8dc19aab167363291246e68e377c1230ad6ca8a8887b8ee8a1fbddc0fc04e4842d7fbb14e147e2f029c535f8f2136738ca290cab1584f86668a5c07d6f668315",
			    "0a272cea41923c7114b23bd13709573776f0832c9685268d2afe029dd3ea26dd37d9d2fe41b7e349ab657921d1d1a74ffae88076a29062e5d5392ac29d1b52e9317c1098d12379c019cb3374aef80881d23250c6d3c59cd253f662b4bbb954341192a64714b21dd016594b3a5a81d3bb22f306f797595f538eaf08069b15df58a75dc9a5002ebca25564577d221dbc52216d7f152d8f66eb7dda9c6a2dc3c273333cc8",
			    "da882f2fa11dc3c3113e916029d78af81bab59511eb44e6a2f937c",
			    "bc82dce894285c92b0af8b68ff8dc4a60cb3873ba478769e46",
			    "0c5e01f311ab43df6c4859a96cddd1e32bcb6f3c0205",
			    "a0bb36c8d61ea5e0816743cb7d9b236b83a91d6540",
			    "38e11f1091bf2b73ac4cd1cee7f4faef27636de005c040d9c9ee4de04c2050ce361a5e653b49ea2adb12",
			    "37c8326ee8846726dce847c2da7128bd1e4056f16e03aa11ebebaefee7",
			    "f046e3a737170d98b418ba8910def9583612bdb47a",
			    "e9f1e722ad2265210297b59165379317d0f8e4ccd95a5a",
			    "4624ab5c0d4bbe1434f19ba39c86e134b99f81d883",
			    "c7dbf26cf953ee85a666e770a2a272e7985af25bb3a65a73af29",
			    "7d1d7437a838682d50d039bb0fe02796d586e7733ad51e9669aa",
			    "f10d2918391703c2b9f779b2abc0ee44242d8ccea0071aa099e2",
			    "05f0c79bcf22f1e990cdd3b5c1b3f18e0e8e568cdc464405e7bf",
			    "0495cbccf8259db9ddfb08ca656488124a7632cf7ec378826c6a",
			    "72b146b15c5bfdbc1393ec41e7763b4981ead8dc3713161860d6",
			    "db039ff137827e1052f0eb8d8795c9e6a64a40dae92878e2d811",
			    "9cb412a05768cfd669edb93d9c32ce28c4d9b99b3710f4cbc690",
			    "e429c2eb85dae542c5509d56da666330a5b2744b1e70c401ae8b",
			    "50ca19909e2a2f315e6d9a32b12ec463b8389e718e7f0651d0da",
			    "94c028bba0f08c0c3f9e326fb719e6134f77464b664a0e46250a",
			    "573531c6c76ed06e2ccf0ff7667874b93bca45af9aa9655f4f35",
			    "17c1d895b23db686d25122134e56ad6498ae1a14fb96235b39a55678",
			    "1f6be4ed6e7f41e1ff7366c03226c8e13b29ff7a365dc2c8342d5565",
			    "94c7effc4ed82ba1cdb0c389b8f55f865a4a1a5c9630d4c0c6a0",
			    "6331d652be209789ca5b27c5f16c702c43cdef9f6c6dfbe9fe11",
			    "29a6754ebc0c1fe24837d68100276167ad95005bc176f38ab98f",
			    "dd68d69a00086ce13d2d908121d0ad593c145c8610ef1310e972",
			    "06bfd43852c1b2f8f9411418d10868dfac11c9fae502660df3df",
			    "5fb6c2405fb79a42f139f836df75b519c11e07d69de484d9002e",
			    "2f083bef8a8deab2098abe01537f3bc752504a3b196e7e742464",
			    "bd1898bfe78b0eab24d55ce399821d83583e7ce82423e346e24b",
			    "ad0ff26b088c6ceceb5b9288da78a582cfb6aa7f3733c889287c3b6973",
			    "e246bdf838e34f22712b30a6e70b97eae2a7c98ab55beade",
			    "11ab26b90d897c482e94c2b7153103c5698ae03a0af45bee",
			    "61bc960a149ec73985b70d40e41cd45a2e13e1c00a989628eb86",
			    "2d4dee67d1374b84a5ce357bab5b953f35542c004d6fa2cbc381",
			    "751258432eaebd37bcb68927216639516fbbf2747e41b69420d7",
			    "29fa4cf138cff58f9b2ac4b5688395452146e629ccad7937b10c",
			    "7d56be9768c642b3a84b9d5f62e09fd34633c8fc07458b98892e",
			    "3390c21f489f0f3e65a61b776ec0def7617872615ed48b06d7417661",
			    "9440204a6183b1970816ccdc565e5a0f4ee6f7d92c377e6744da",
			    "1bc944dbc9e4ce803986282bb392aa414da2a49a4af25b3a90073904",
			    "d4cc23483e91a1ff2aab7a68c8addc8e5011b017a0aa9bd2ede5a22f",
			    "757b25158deaf3d87fde124e2e78d9418bab5491ae9ae1a0ebd76ec5",
			    "f893fe7ab70214307496b117aa4f490d0424e33f47b8b884e7ef2c4805e3b8",
			    "30cb631fa024af758110060dd3a41dd98e0fd765650d2f9281a5",
			    "4006904e57226456a4222f3993746f73dab663cbfe4eda748bf1",
			    "27e4b0e61d89315ef804dd36150eb7e4d93a6800e9c40858db5a",
			    "b1c2e4ea41ec1c49bcbd5556e6edf9f85aab2d652a1629766adeecba",
			    "f9f085484b0c0f09fa1f4f1d0fad726896a30b3a8ce1d11175a4",
			    "7c90d44c67075eec7318922cd89c5bc69e31fc8c057d44ad8b92425d",
			    "f71225127dad06f316e61463d33ddd949976994015fecbe1a937d7cc",
			    "30686f85814e4ddb704d7fda7e560e7b0301ff4cebecd743519202a4",
			    "ef81f40a365aac669f4ce00ea1aad2c7b6464cadac58425b41d95279b2d31f84bb7169a1ef06af85bcdc4962e083ed48",
			    "749bd820db8173ac16a2370914bbb9952bae9cf2d65abe5d645ae7b6e0e62a4249e4921d0506461270ae161da39f96d6919c2a",
			    "b92ee2b3dad616a05492785711b7e959f31d1f9d03",
			    "ebb8a914a56fea75993f47f39a2b38ea2d876ae291a516807624528225b50033",
			    "515745430034344499a7f0238f6b306440dc14b4dce7814433",
			    "75e25db115b163fec5c8841fe14448b4eaebdac0b8d672",
			    "e40a5c1de36c169b2aedc08618128f60b271eb1c827a",
			    "76821b0dddce2a5b779ba3fdc533ba1327ed7b0c3903ef5ccc9e907629f4032f0f7903",
			    "60f871b35bb0233b0140bea190dbd7d82519e4c533",
			    "de4f89ed018f97b430115f20c16d070ac78b81231fefc1",
			    "3f57fb281a938e004709c375102f24be541ef7b1265c71",
			    "d4acc4dd67f3a109b43e9593efc690fdb9c652b526084a",
			    "4dc8a9f8389aef5b632dd593a4ec189ba0463e251bd034",
			    "6474942801718a1463237cf90a7eff36cb6c5f0a0f1852",
			    "2f9783954e9b6c0b5c146c4080aa6d4c1b784aa4cee5b4b0b7fdeccc27b833c365190b24564cf76697ab0f9d6687e5820223303003d227d10095c1",
			    "e33b226d4ce97774e47d4fc62cd1a525a9ce6b3c5239f5",
			    "544757a42aa67110a01e89e51a752bb72d6f2efa1c59ff",
			    "234c910fdf70d4fb566634ae1339da020754e57e5961e0c1669968",
			    "fc659265f824175c15979c3f5dd226fb212ad1acaa41f6a131",
			    "bf56b16dd54dc7fc68d88ce4400a225d8a2146b593",
			    "b9a23fe36f35eef0c4db12dd928facd0e84c4dc08772c7f9302b",
			    "15d22db8389b50d089cbdc279970b1cb74696327792d7b357463",
			    "301a7009bd78cd9c721812ef648b771f4ee009606b3d77",
			    "1ae8aab1bc76b4c2143a7588478b859214e908e73a776d",
			    "baf532901ae4a89b97279c42d7ef1594ba9b3966a9",
			    "278f3d7fafbe445cf049bc67b24aef607f404b327f",
			    "1c0b30a5032cfe03151ce16a6bbae2c8c06991f6fbdb6930d61c75b84c2055",
			  ],
			  "relayMessages": [
			    "loginRequest: username password myId myName option sessionId version myPlatform",
			    "auth_2fa: code",
			    "misc: option",
			    "misc: autoAdjustFps",
			    "misc: refreshVideo",
			    "misc: supportedEncoding",
			    "testDelay: time fromClient lastDelay targetBitrate",
			    "misc: videoReceived",
			    "misc: option",
			    "misc: refreshVideo",
			    "keyEvent: modifiers down press chr mode",
			    "keyEvent: modifiers down press chr mode",
			    "keyEvent: modifiers down press chr mode",
			    "keyEvent: modifiers down press chr mode",
			    "keyEvent: modifiers down press chr mode",
			    "keyEvent: modifiers down press chr mode",
			    "keyEvent: modifiers down press chr mode",
			    "keyEvent: modifiers down press chr mode",
			    "keyEvent: modifiers down press chr mode",
			    "keyEvent: modifiers down press chr mode",
			    "keyEvent: modifiers down press chr mode",
			    "keyEvent: modifiers down press chr mode",
			    "keyEvent: modifiers down press chr mode",
			    "keyEvent: modifiers down press chr mode",
			    "keyEvent: modifiers down press controlKey mode",
			    "keyEvent: modifiers down press controlKey mode",
			    "keyEvent: modifiers down press chr mode",
			    "keyEvent: modifiers down press chr mode",
			    "keyEvent: modifiers down press controlKey mode",
			    "keyEvent: modifiers down press controlKey mode",
			    "mouseEvent: modifiers mask x y",
			    "mouseEvent: modifiers mask x y",
			    "mouseEvent: modifiers mask x y",
			    "mouseEvent: modifiers mask x y",
			    "mouseEvent: modifiers mask x y",
			    "keyEvent: modifiers down press controlKey mode",
			    "keyEvent: modifiers down press controlKey mode",
			    "keyEvent: modifiers down press chr mode",
			    "keyEvent: modifiers down press chr mode",
			    "keyEvent: modifiers down press chr mode",
			    "keyEvent: modifiers down press chr mode",
			    "keyEvent: modifiers down press chr mode",
			    "keyEvent: modifiers down press chr mode",
			    "keyEvent: modifiers down press chr mode",
			    "keyEvent: modifiers down press chr mode",
			    "keyEvent: modifiers down press seq mode",
			    "keyEvent: modifiers down press chr mode",
			    "keyEvent: modifiers down press chr mode",
			    "keyEvent: modifiers down press chr mode",
			    "keyEvent: modifiers down press chr mode",
			    "keyEvent: modifiers down press chr mode",
			    "keyEvent: modifiers down press chr mode",
			    "keyEvent: modifiers down press chr mode",
			    "keyEvent: modifiers down press chr mode",
			    "clipboard: compress content format",
			    "multiClipboards: clipboards",
			    "misc: refreshVideo",
			    "misc: chatMessage",
			    "misc: option",
			    "misc: option",
			    "misc: autoAdjustFps",
			    "misc: option",
			    "misc: refreshVideo",
			    "misc: option",
			    "misc: option",
			    "misc: option",
			    "misc: option",
			    "misc: option",
			    "misc: togglePrivacyMode",
			    "misc: option",
			    "misc: option",
			    "misc: switchDisplay",
			    "misc: captureDisplays",
			    "misc: refreshVideo",
			    "mouseEvent: modifiers mask x y",
			    "mouseEvent: modifiers mask x y",
			    "misc: option",
			    "misc: option",
			    "misc: refreshVideo",
			    "misc: restartRemoteDevice",
			    "testDelay: time fromClient lastDelay targetBitrate",
			  ],
			  "rendezvous": [
			    "650142570a0939303031303030313210021a2c696f6a6a3358514a385a583955747374504c70646373706e436238646c42496238335349416251506231773d20002a0032144265747465724465736b2d5765622f312e342e394001",
			    "050292017e0a09393030313030303132122431623465323862612d326661312d343164322d383833662d303031366433636361343237221772656c61792e6578616d706c652e636f6d3a32313131372800322c696f6a6a3358514a385a583955747374504c70646373706e436238646c42496238335349416251506231773d38004200",
			  ],
			}
		`);
		c.disconnect();
	});
});
