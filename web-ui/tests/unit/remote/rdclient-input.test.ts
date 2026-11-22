/**
 * The RustDesk client's mouse and touch input (src/lib/remote/client/input.ts and
 * touch.ts): a button pressed on the remote screen is released on the
 * device, wherever the pointer or finger lets go and when input stops.
 */
import { describe, expect, test, vi } from 'vitest';
import type { MessageInit } from '../../../src/lib/remote/client/generated/protos.ts';
import { RDCliprdr, type RDCliprdrClient } from '../../../src/lib/remote/client/cliprdr.ts';
import { RDInput } from '../../../src/lib/remote/client/input.ts';
import { RDProtocol } from '../../../src/lib/remote/client/protocol.ts';
import { RDTouch } from '../../../src/lib/remote/client/touch.ts';

const g = { RDInput, RDTouch };

/** A 100 x 100 remote screen drawn 1:1 at the canvas' top left; jsdom's canvas sits at 0,0 */
const renderer = {
	remoteWidth: 100,
	remoteHeight: 100,
	canvasToRemote: (x: number, y: number) => ({ x, y })
};

type Sent = MessageInit;
const UP_LEFT = 2 | (1 << 3);
const DOWN_LEFT = 1 | (1 << 3);
const masks = (sent: Sent[]) => sent.map((m) => m.mouseEvent?.mask);

describe('mouse buttons', () => {
	test('a drag released outside the screen still releases the button on the device', () => {
		const sent: Sent[] = [];
		const canvas = document.createElement('canvas');
		document.body.append(canvas);
		const input = new g.RDInput(canvas, renderer, (m: Sent) => sent.push(m));
		input.start();
		canvas.dispatchEvent(new MouseEvent('mousedown', { button: 0, clientX: 10, clientY: 10, bubbles: true }));
		window.dispatchEvent(new MouseEvent('mouseup', { button: 0, clientX: 500, clientY: 40, bubbles: true }));
		expect(masks(sent)).toEqual([DOWN_LEFT, UP_LEFT]);
		expect(sent[1]!.mouseEvent).toMatchObject({ x: 100, y: 40 });
		// A release with no press before it sends nothing
		window.dispatchEvent(new MouseEvent('mouseup', { button: 0, clientX: 5, clientY: 5, bubbles: true }));
		expect(sent).toHaveLength(2);
		input.stop();
		canvas.remove();
	});

	test('stopping input (tab switch, view only, end) releases a held button', () => {
		const sent: Sent[] = [];
		const canvas = document.createElement('canvas');
		const input = new g.RDInput(canvas, renderer, (m: Sent) => sent.push(m));
		input.start();
		canvas.dispatchEvent(new MouseEvent('mousedown', { button: 0, clientX: 20, clientY: 30 }));
		input.stop();
		expect(masks(sent)).toEqual([DOWN_LEFT, UP_LEFT]);
		expect(sent[1]!.mouseEvent).toMatchObject({ x: 20, y: 30 });
	});
});

describe('touch drag', () => {
	const touch = (x: number, y: number) => ({ identifier: 1, clientX: x, clientY: y });
	type Point = ReturnType<typeof touch>;
	const ev = (touches: Point[], changed: Point[]) => ({
		preventDefault: () => {},
		touches,
		changedTouches: changed
	});

	function dragging(sent: Sent[]) {
		vi.spyOn(performance, 'now').mockReturnValue(1e6);
		const t = new g.RDTouch(document.createElement('canvas'), renderer, (m: Sent) => sent.push(m));
		t.start();
		t._handleTouchStart(ev([touch(10, 10)], [touch(10, 10)]));
		t._handleTouchMove(ev([touch(40, 40)], [touch(40, 40)]));
		return t;
	}

	test('a drag that ends outside the screen releases the button where it last was', () => {
		const sent: Sent[] = [];
		const t = dragging(sent);
		t._handleTouchEnd(ev([], [touch(500, 500)]));
		expect(masks(sent).filter((m) => m !== 0)).toEqual([DOWN_LEFT, UP_LEFT]);
		expect(sent.at(-1)!.mouseEvent).toMatchObject({ x: 40, y: 40 });
	});

	test('stopping touch input in the middle of a drag releases the button', () => {
		const sent: Sent[] = [];
		const t = dragging(sent);
		t.stop();
		expect(masks(sent).filter((m) => m !== 0)).toEqual([DOWN_LEFT, UP_LEFT]);
	});
});

describe('the file clipboard of the desktop shell', () => {
	/** The desktop shell's bridge: answers what the file clipboard asks */
	function desktop(answers: Record<string, unknown>) {
		const w = window as unknown as Record<string, unknown>;
		w.__BETTERDESK_RDCLIENT_DESKTOP__ = true;
		w.__TAURI__ = { core: { invoke: async (cmd: string) => answers[cmd] ?? null } };
		return () => {
			delete w.__BETTERDESK_RDCLIENT_DESKTOP__;
			delete w.__TAURI__;
		};
	}

	async function session() {
		const proto = new RDProtocol();
		await proto.load();
		const sent: Sent[] = [];
		const canvas = document.createElement('canvas');
		canvas.getBoundingClientRect = () => new DOMRect(0, 0, 100, 100);
		const input = new g.RDInput(canvas, renderer, (m: Sent) => sent.push(m));
		input.start();
		const client: RDCliprdrClient = {
			_state: 'streaming',
			viewOnly: false,
			proto,
			input,
			_sendPeerMessage: (m: Sent) => sent.push(m),
			_emit: () => {}
		};
		return { sent, canvas, input, client };
	}
	const keys = (sent: Sent[]) =>
		sent.filter((m) => m.keyEvent).map((m) => [m.keyEvent!.controlKey ?? m.keyEvent!.chr, m.keyEvent!.down]);
	/** Mouse presses and releases (moves left out) */
	const presses = (sent: Sent[]) => masks(sent.filter((m) => m.mouseEvent && m.mouseEvent.mask));
	const CTRL = (key: string) => [
		['Control', true],
		[key.charCodeAt(0), true],
		[key.charCodeAt(0), false],
		['Control', false]
	];

	test('a drag to the edge of the screen and beyond sends no key to the device and keeps the button held', async () => {
		vi.useFakeTimers();
		const done = desktop({});
		try {
			const t = await session();
			await RDCliprdr.initClient(t.client);
			t.sent.length = 0;
			t.canvas.dispatchEvent(new MouseEvent('mousedown', { button: 0, clientX: 50, clientY: 50 }));
			t.canvas.dispatchEvent(new MouseEvent('mousemove', { clientX: 99, clientY: 50 }));
			t.canvas.dispatchEvent(new MouseEvent('mouseleave', { clientX: 105, clientY: 50 }));
			t.canvas.dispatchEvent(new MouseEvent('mousemove', { clientX: 120, clientY: 50 }));
			await vi.advanceTimersByTimeAsync(3000);
			expect(keys(t.sent)).toEqual([]);
			expect(presses(t.sent)).toEqual([DOWN_LEFT]);
			// The device lets go of the button when the operator does
			window.dispatchEvent(new MouseEvent('mouseup', { button: 0, clientX: 120, clientY: 50 }));
			expect(presses(t.sent)).toEqual([DOWN_LEFT, UP_LEFT]);
			RDCliprdr.stopPolling(t.client);
			t.input.stop();
		} finally {
			done();
			vi.useRealTimers();
		}
	});

	test('files dropped on the viewer are pasted where they were dropped', async () => {
		const done = desktop({ desktop_clipboard_sync_paths: { hasFiles: true, signature: 'sig' } });
		try {
			const t = await session();
			t.client._state = 'streaming';
			const dropped = RDCliprdr.syncPaths(t.client, ['/home/op/a.txt'], { x: 40, y: 30 });
			await dropped;
			expect(t.sent.some((m) => m.cliprdr?.formatList)).toBe(true);
			t.client._cliprdrFormatListAckResolve?.();
			await vi.waitFor(() => expect(keys(t.sent)).toEqual(CTRL('v')));
			const clicks = t.sent.filter((m) => m.mouseEvent);
			expect(masks(clicks)).toEqual([DOWN_LEFT, UP_LEFT]);
			expect(clicks[0]!.mouseEvent).toMatchObject({ x: 40, y: 30 });
			t.input.stop();
		} finally {
			done();
		}
	});

	test('while a file drag leaves the viewer, the mouse goes nowhere', async () => {
		const t = await session();
		t.input.setMouseSuppressed(true);
		t.canvas.dispatchEvent(new MouseEvent('mousedown', { button: 0, clientX: 50, clientY: 50 }));
		t.canvas.dispatchEvent(new MouseEvent('mousemove', { clientX: 60, clientY: 50 }));
		expect(t.sent).toEqual([]);
		t.input.setMouseSuppressed(false);
		t.canvas.dispatchEvent(new MouseEvent('mousedown', { button: 0, clientX: 50, clientY: 50 }));
		expect(masks(t.sent)).toEqual([DOWN_LEFT]);
		t.input.stop();
	});
});
