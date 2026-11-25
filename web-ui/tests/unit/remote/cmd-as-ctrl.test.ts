/**
 * Cmd as Ctrl for common shortcuts (viewer/cmd-as-ctrl.ts): on a Mac, when
 * the remote computer isn't one, Cmd with C, V, X, A, Z, S or F reaches it
 * as Ctrl with the same key; Cmd alone is the Windows key, and Cmd with
 * anything else stays Cmd. Off, everything passes as it is.
 */
import { describe, expect, test } from 'vitest';
import { CmdAsCtrl, type KeyOut } from '../../../src/lib/remote/viewer/cmd-as-ctrl.ts';

function key(
	type: 'keydown' | 'keyup',
	code: string,
	mods: { meta?: boolean; ctrl?: boolean; alt?: boolean } = {}
) {
	const k = code.startsWith('Key') ? code.slice(3).toLowerCase() : code.startsWith('Meta') ? 'Meta' : code;
	return new KeyboardEvent(type, {
		code,
		key: k,
		metaKey: !!mods.meta,
		ctrlKey: !!mods.ctrl,
		altKey: !!mods.alt
	});
}

/** What reaches the remote: each event as "down Code+mods" */
function run(t: CmdAsCtrl, events: KeyboardEvent[]): string[] {
	const out: string[] = [];
	const show = (e: KeyOut) =>
		`${e.type === 'keydown' ? 'down' : 'up'} ${e.code}${e.ctrlKey ? ' ctrl' : ''}${e.metaKey ? ' meta' : ''}`;
	for (const e of events) {
		const got = t.handle(e);
		if (got === null)
			out.push(
				show({ type: e.type as KeyOut['type'], code: e.code, ctrlKey: e.ctrlKey, metaKey: e.metaKey })
			);
		else out.push(...got.map(show));
	}
	return out;
}

const on = () => new CmdAsCtrl(() => true);

describe('Cmd as Ctrl', () => {
	test('Cmd+C goes over as Ctrl+C, and Cmd let go lets Ctrl go', () => {
		expect(
			run(on(), [
				key('keydown', 'MetaLeft', { meta: true }),
				key('keydown', 'KeyC', { meta: true }),
				key('keyup', 'KeyC', { meta: true }),
				key('keyup', 'MetaLeft')
			])
		).toEqual(['down ControlLeft ctrl', 'down KeyC ctrl', 'up KeyC ctrl', 'up ControlLeft']);
	});

	test('every one of C, V, X, A, Z, S and F, and Cmd held across several', () => {
		const out = run(on(), [
			key('keydown', 'MetaRight', { meta: true }),
			...['KeyV', 'KeyX', 'KeyA', 'KeyZ', 'KeyS', 'KeyF'].flatMap((c) => [
				key('keydown', c, { meta: true }),
				key('keyup', c, { meta: true })
			]),
			key('keyup', 'MetaRight')
		]);
		expect(out.filter((l) => l.includes('Meta') || l.includes('meta'))).toEqual([]);
		expect(out[0]).toBe('down ControlLeft ctrl');
		expect(out.at(-1)).toBe('up ControlLeft');
		expect(out.filter((l) => l.startsWith('down Key')).every((l) => l.endsWith('ctrl'))).toBe(true);
	});

	test('Cmd with another key stays Cmd, and Cmd alone is the Windows key', () => {
		expect(
			run(on(), [
				key('keydown', 'MetaLeft', { meta: true }),
				key('keydown', 'KeyL', { meta: true }),
				key('keyup', 'KeyL', { meta: true }),
				key('keyup', 'MetaLeft')
			])
		).toEqual(['down MetaLeft meta', 'down KeyL meta', 'up KeyL meta', 'up MetaLeft']);
		expect(run(on(), [key('keydown', 'MetaLeft', { meta: true }), key('keyup', 'MetaLeft')])).toEqual([
			'down MetaLeft meta',
			'up MetaLeft'
		]);
	});

	test('after Cmd+C, Cmd with another key turns back into Cmd', () => {
		expect(
			run(on(), [
				key('keydown', 'MetaLeft', { meta: true }),
				key('keydown', 'KeyC', { meta: true }),
				key('keyup', 'KeyC', { meta: true }),
				key('keydown', 'ArrowLeft', { meta: true }),
				key('keyup', 'MetaLeft')
			])
		).toEqual([
			'down ControlLeft ctrl',
			'down KeyC ctrl',
			'up KeyC ctrl',
			'up ControlLeft',
			'down MetaLeft meta',
			'down ArrowLeft meta',
			'up MetaLeft'
		]);
	});

	test('Cmd+Alt+C and Ctrl+C stay as they are', () => {
		expect(
			run(on(), [
				key('keydown', 'MetaLeft', { meta: true }),
				key('keydown', 'KeyC', { meta: true, alt: true })
			])
		).toEqual(['down MetaLeft meta', 'down KeyC meta']);
		expect(run(on(), [key('keydown', 'KeyC', { ctrl: true })])).toEqual(['down KeyC ctrl']);
	});

	test('off (not a Mac, a Mac remote, or the toggle off): nothing changes', () => {
		const off = new CmdAsCtrl(() => false);
		expect(
			run(off, [key('keydown', 'MetaLeft', { meta: true }), key('keydown', 'KeyC', { meta: true })])
		).toEqual(['down MetaLeft meta', 'down KeyC meta']);
	});

	test('losing the focus lets a Ctrl it sent go', () => {
		const t = on();
		run(t, [key('keydown', 'MetaLeft', { meta: true }), key('keydown', 'KeyS', { meta: true })]);
		expect(t.reset().map((e) => `${e.type} ${e.code}`)).toEqual(['keyup KeyS', 'keyup ControlLeft']);
		expect(t.reset()).toEqual([]);
	});
});
