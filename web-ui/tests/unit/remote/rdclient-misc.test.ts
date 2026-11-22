/**
 * Smaller parts of the wrapped remote client (src/lib/remote/client): the CDAP
 * download's save hook, the Linux key codes of Map mode, cancelling a
 * RustDesk connect, and the CDAP session chat. Sockets are fakes.
 */
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { CDAPFileTransfer } from '../../../src/lib/remote/client/cdap-filetransfer.ts';
import { CDAPSession } from '../../../src/lib/remote/client/cdap-adapter.ts';
import { RDClient } from '../../../src/lib/remote/client/client.ts';
import { RDKeyboardScancode } from '../../../src/lib/remote/client/keyboard-scancode.ts';

const g = { RDClient, RDKeyboardScancode, CDAPFileTransfer, CDAPSession };

/** A socket that stays connecting until the test opens it */
class FakeSocket {
	static CONNECTING = 0;
	static OPEN = 1;
	static CLOSED = 3;
	static made: FakeSocket[] = [];
	readyState = 0;
	binaryType = '';
	sent: unknown[] = [];
	closed = false;
	listeners = new Map<string, ((ev: unknown) => void)[]>();
	onopen: (() => void) | null = null;
	onerror: ((e: unknown) => void) | null = null;
	onclose: ((e: unknown) => void) | null = null;
	onmessage: ((e: { data: unknown }) => void) | null = null;
	constructor(public url: string) {
		FakeSocket.made.push(this);
	}
	addEventListener(type: string, fn: (ev: unknown) => void) {
		this.listeners.set(type, [...(this.listeners.get(type) ?? []), fn]);
	}
	send(d: unknown) {
		this.sent.push(d);
	}
	close() {
		this.closed = true;
		this.readyState = 3;
	}
	open() {
		this.readyState = 1;
		this.onopen?.();
		for (const fn of this.listeners.get('open') ?? []) fn({});
	}
	message(data: unknown) {
		this.onmessage?.({ data });
		for (const fn of this.listeners.get('message') ?? []) fn({ data });
	}
}

beforeEach(() => {
	vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(null);
	FakeSocket.made = [];
	vi.stubGlobal('WebSocket', FakeSocket);
});
afterEach(() => vi.unstubAllGlobals());

describe('CDAP file download', () => {
	test('hands the save hook the file name, then the file', async () => {
		const events: string[] = [];
		const ft = new g.CDAPFileTransfer({ deviceId: 'cdap-1', emit: (ev: string) => events.push(ev) });
		ft._ws = { readyState: FakeSocket.OPEN, send: () => {} } as unknown as WebSocket;
		const save = vi.fn();
		ft._saveDownload = save;
		ft.downloadFile('/docs', { name: 'report.txt', size: 3 });
		await vi.waitFor(() => expect(Object.keys(ft._pending)).toHaveLength(1));
		ft._pending[Object.keys(ft._pending)[0]!]?.({ data: btoa('abc') });
		expect(save).toHaveBeenCalledOnce();
		const [name, blob] = save.mock.calls[0]!;
		expect(name).toBe('report.txt');
		expect(blob).toBeInstanceOf(Blob);
	});
});

describe('Map keyboard mode on Linux peers', () => {
	test('Delete, Pause and the arrows send their own X11 key codes', () => {
		const code = (c: string) => g.RDKeyboardScancode.codeToScancode(c, 'Linux');
		expect(code('Delete')).toBe(119);
		expect(code('Pause')).toBe(127);
		expect(code('ArrowUp')).toBe(111);
		const values = Object.values(g.RDKeyboardScancode.CODE_TO_LINUX_SCAN) as number[];
		expect(new Set(values).size).toBe(values.length);
	});
});

describe('cancelling a RustDesk connect', () => {
	test('Cancel while the rendezvous socket opens closes it, and nothing goes to the device afterwards', async () => {
		const c = new g.RDClient(document.createElement('canvas'), { deviceId: '900100012', serverPubKey: '' });
		const errors: unknown[] = [];
		c.on('error', (m: unknown) => errors.push(m));
		const connecting = c.connect();
		await vi.waitFor(() => expect(FakeSocket.made).toHaveLength(1));
		const rendezvous = FakeSocket.made[0]!;
		c.disconnect();
		expect(rendezvous.closed).toBe(true);
		// The socket opens late anyway
		rendezvous.open();
		await connecting;
		expect(rendezvous.sent).toEqual([]);
		expect(FakeSocket.made).toHaveLength(1);
		expect(errors).toEqual([]);
		expect(c.state).toBe('disconnected');
	});
});

describe('CDAP session chat', () => {
	test("the operator's own messages, echoed by the relay, do not come back as received", () => {
		const s = new g.CDAPSession(document.createElement('canvas'), { deviceId: 'cdap-1' });
		const received: unknown[] = [];
		s.on('chat', (t: unknown) => received.push(t));
		s._connectChat();
		const ws = FakeSocket.made[0]!;
		ws.message(JSON.stringify({ type: 'message', text: 'on my way', from: 'operator' }));
		ws.message(JSON.stringify({ type: 'message', text: 'thanks', from: 'cdap-1' }));
		expect(received).toEqual(['thanks']);
	});
});
