/**
 * The CDAP desktop widget with a fake desktop session: keys and mouse
 * buttons held on the device are released when the screen loses focus or
 * the button is let go outside the screen.
 */
import { fireEvent, render, screen } from '@testing-library/svelte';
import { describe, expect, test, vi } from 'vitest';

const made = vi.hoisted(() => [] as FakeSession[]);
interface FakeSession {
	connected: boolean;
	keys: [Record<string, unknown>, boolean][];
	mice: [Record<string, unknown>, number][];
}

vi.mock('../../../src/lib/devices-area/cdap/desktop.ts', async (importOriginal) => {
	const real = await importOriginal<typeof import('../../../src/lib/devices-area/cdap/desktop.ts')>();
	class DesktopSession implements FakeSession {
		connected = false;
		keys: [Record<string, unknown>, boolean][] = [];
		mice: [Record<string, unknown>, number][] = [];
		constructor(private o: { events: { onstate(s: string): void } }) {
			made.push(this);
		}
		open() {
			this.connected = true;
			this.o.events.onstate('open');
		}
		close() {
			this.connected = false;
		}
		key(e: Record<string, unknown>, down: boolean) {
			this.keys.push([{ key: e.key, code: e.code, altKey: e.altKey }, down]);
		}
		mouse(e: Record<string, unknown>, type: number) {
			this.mice.push([{ button: e.button }, type]);
		}
		wheel() {
			return false;
		}
		paste() {}
		selectMonitor() {}
	}
	return { ...real, DesktopSession, hasVideoDecoder: () => true };
});
vi.mock('../../../src/lib/devices-area/cdap/streams.ts', () => ({
	cdapSocketUrl: () => 'ws://test/desktop'
}));

const { default: DesktopWidget } =
	await import('../../../src/routes/(console)/cdap/devices/[id]/DesktopWidget.svelte');
const { MOUSE } = await import('../../../src/lib/devices-area/cdap/desktop.ts');

async function connected() {
	made.length = 0;
	render(DesktopWidget, { deviceId: 'cdap-1', deviceName: 'Printer', canConnect: true });
	await fireEvent.click(screen.getByRole('button'));
	const stage = await screen.findByRole('application');
	return { stage, s: made[0]!, canvas: stage.querySelector('canvas')! };
}

describe('CDAP desktop widget input', () => {
	test('keys still held when the screen loses focus are released on the device', async () => {
		const { stage, s } = await connected();
		stage.focus();
		await fireEvent.keyDown(stage, { key: 'Alt', code: 'AltLeft', altKey: true });
		await fireEvent.keyDown(stage, { key: 'Tab', code: 'Tab', altKey: true });
		await fireEvent.blur(stage);
		expect(s.keys.filter(([, down]) => !down).map(([k]) => k.code)).toEqual(['AltLeft', 'Tab']);
		// A later key up sends nothing more
		await fireEvent.keyUp(stage, { key: 'Alt', code: 'AltLeft' });
		expect(s.keys.filter(([, down]) => !down)).toHaveLength(2);
	});

	test('a mouse button let go outside the screen is released on the device', async () => {
		const { s, canvas } = await connected();
		await fireEvent.mouseDown(canvas, { button: 0, clientX: 10, clientY: 10 });
		await fireEvent.mouseUp(window, { button: 0, clientX: 900, clientY: 900 });
		expect(s.mice.map(([e, type]) => [e.button, type])).toEqual([
			[0, MOUSE.down],
			[0, MOUSE.up]
		]);
		// A release with no press before it sends nothing
		await fireEvent.mouseUp(window, { button: 0 });
		expect(s.mice).toHaveLength(2);
	});
});
