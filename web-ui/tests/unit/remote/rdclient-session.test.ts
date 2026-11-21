/**
 * The wrapped RustDesk client (static/rdclient/client.js and
 * file-connection.js) against a fake device: messages go in through the
 * relay handler, what the client sends is caught at the relay socket. No
 * rendezvous, relay or device runs.
 */
import { beforeEach, describe, expect, test, vi } from 'vitest';
import { loadScripts, RD_CORE, type Loaded } from './rdclient-scripts.ts';

const g: Loaded = loadScripts(
	[
		...RD_CORE,
		'video.js',
		'audio.js',
		'renderer.js',
		'keyboard-scancode.js',
		'keyboard-encoder.js',
		'input.js',
		'compress.js',
		'clipboard.js',
		'filetransfer.js',
		'file-connection.js',
		'client.js'
	],
	['RDClient', 'RDFileConnection', 'RDCrypto', 'RDClipboard', 'nacl']
);
const nacl = g.nacl;

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
			return nacl.secretbox(proto.serializeMessage(msg), nonce, key) as Uint8Array;
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
	test('a device that proves its identity gets the session key and its encrypted password prompt shows', async () => {
		const t = await relayClient();
		const d = device(t.c.proto);
		trust(t, d);
		t.c._handleRelayMessage(d.signedIdFrame);
		// The client answers the SignedId with its key at once
		expect(t.sent).toHaveLength(1);
		expect(d.takeKey(t.sent[0]!)).not.toBeNull();
		t.c._handleRelayMessage(d.sealed(HASH));
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
