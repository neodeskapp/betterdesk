/**
 * The dashboards' rules without a DOM: which tabs an account sees and in
 * what order, moving widgets, which links may go on a dashboard, what a
 * stat widget may show, and what is sent to the server.
 */
import { describe, expect, test } from 'vitest';
import {
	addable,
	addWidget,
	defaultBoard,
	kindOf,
	linksOf,
	metricAllowed,
	METRIC_INFO,
	moveTo,
	moveWidget,
	openTab,
	ownBody,
	readBoard,
	removeWidget,
	safeUrl,
	sharedKey,
	tabsFor,
	updateWidget,
	type Board,
	type SharedBoard
} from '../../../src/lib/dashboard/model.ts';

const board = (id: string, ...types: string[]): Board => ({
	id,
	name: id,
	widgets: types.map((type, i) => ({ id: `w${i}`, type, size: 'm' }))
});
const shared = (id: string, extra: Partial<SharedBoard> = {}): SharedBoard => ({
	...board(id, 'note'),
	roles: [],
	locked: false,
	home: false,
	...extra
});
const builtIn = defaultBoard('Overview');
const keys = (tabs: { key: string }[]) => tabs.map((t) => t.key);

describe('tabs', () => {
	test('nothing saved and nothing shared: only the built-in dashboard', () => {
		expect(keys(tabsFor(null, [], builtIn))).toEqual(['overview']);
	});

	test('a shared home comes first and replaces the built-in one; other shared ones come last', () => {
		const tabs = tabsFor(null, [shared('team'), shared('ops', { home: true })], builtIn);
		expect(keys(tabs)).toEqual(['shared:ops', 'shared:team']);
	});

	test('own dashboards sit between the shared home and the other shared ones', () => {
		const own = { boards: [board('mine'), board('more')], active: '', hidden: [] };
		const tabs = tabsFor(own, [shared('team'), shared('ops', { home: true })], builtIn);
		expect(keys(tabs)).toEqual(['shared:ops', 'mine', 'more', 'shared:team']);
	});

	test('a hidden shared dashboard is left out, unless it is locked', () => {
		const own = { boards: [board('mine')], active: '', hidden: ['team', 'rules'] };
		const tabs = tabsFor(own, [shared('team'), shared('rules', { locked: true })], builtIn);
		expect(keys(tabs)).toEqual(['mine', 'shared:rules']);
	});

	test('the address wins over the saved choice, which wins over the first tab', () => {
		const tabs = tabsFor(
			{ boards: [board('a'), board('b')], active: 'b', hidden: [] },
			[shared('s')],
			builtIn
		);
		expect(openTab(tabs, sharedKey('s'), 'b')?.key).toBe('shared:s');
		expect(openTab(tabs, null, 'b')?.key).toBe('b');
		expect(openTab(tabs, 'gone', 'gone')?.key).toBe('a');
	});
});

describe('changing a board', () => {
	test('moving stays inside the list and keeps the others in order', () => {
		const b = board('x', 'attention', 'devices', 'note');
		expect(moveWidget(b, 'w2', -1).widgets.map((w) => w.id)).toEqual(['w0', 'w2', 'w1']);
		expect(moveWidget(b, 'w0', -5).widgets.map((w) => w.id)).toEqual(['w0', 'w1', 'w2']);
		expect(moveTo(b, 'w0', 2).widgets.map((w) => w.id)).toEqual(['w1', 'w2', 'w0']);
		// The original is left alone (the draft is replaced, never changed in place)
		expect(b.widgets.map((w) => w.id)).toEqual(['w0', 'w1', 'w2']);
	});

	test('a new widget gets an id no other widget has, its kind’s width, and goes last', () => {
		const b = board('x', 'attention');
		const next = addWidget(b, kindOf('stat')!);
		const added = next.widgets.at(-1)!;
		expect(added.type).toBe('stat');
		expect(added.size).toBe('s');
		expect(added.id).not.toBe('w0');
		expect(added.id).toMatch(/^[a-z0-9][a-z0-9-]{0,39}$/);
	});

	test('an empty title or settings are dropped, not saved as empty', () => {
		const b = updateWidget(board('x', 'note'), 'w0', { title: '  ', config: {} });
		expect(b.widgets[0]).toEqual({ id: 'w0', type: 'note', size: 'm' });
		expect(removeWidget(b, 'w0').widgets).toEqual([]);
	});

	test('a widget that can be there once is not offered again, nor one the account may not see', () => {
		const b = board('x', 'attention');
		const types = (can: (p: string) => boolean) => addable(b, can).map((k) => k.type);
		expect(types(() => true)).not.toContain('attention');
		expect(types(() => true)).toContain('trends');
		expect(types(() => false)).not.toContain('trends');
		expect(types(() => false)).not.toContain('devices');
		// Several notes are fine
		expect(types(() => false)).toContain('note');
	});
});

describe('links', () => {
	test('console addresses and http(s) only', () => {
		expect(safeUrl('/devices?status=offline')).toBe(true);
		expect(safeUrl('https://example.com/x')).toBe(true);
		expect(safeUrl('http://intranet/wiki')).toBe(true);
		expect(safeUrl('javascript:alert(1)')).toBe(false);
		expect(safeUrl('JAVASCRIPT:alert(1)')).toBe(false);
		expect(safeUrl('data:text/html,hi')).toBe(false);
		expect(safeUrl('//evil.example')).toBe(false);
		expect(safeUrl('/\\evil.example')).toBe(false);
		expect(safeUrl('')).toBe(false);
	});

	test('a stored link the console would refuse is not shown', () => {
		const w = {
			id: 'l',
			type: 'links',
			size: 's' as const,
			config: {
				links: [{ label: 'Wiki', url: 'https://wiki' }, { label: 'Bad', url: 'javascript:x' }, 'junk']
			}
		};
		expect(linksOf(w)).toEqual([{ label: 'Wiki', url: 'https://wiki' }]);
	});
});

test('the help request count needs device.view and chat.support', () => {
	const only =
		(...perms: string[]) =>
		(p: string) =>
			perms.includes(p);
	expect(metricAllowed(METRIC_INFO.help, only('chat.support'))).toBe(false);
	expect(metricAllowed(METRIC_INFO.help, only('device.view', 'chat.support'))).toBe(true);
	expect(metricAllowed(METRIC_INFO.online, only())).toBe(false);
});

describe('what goes to and comes from the server', () => {
	test('the body carries only the keys the server takes, names trimmed, hidden once each', () => {
		const body = ownBody({
			boards: [
				{
					id: 'a',
					name: ' Mine ',
					widgets: [{ id: 'w', type: 'note', size: 'm', title: '', config: { text: 'hi' } }]
				}
			],
			active: 'a',
			hidden: ['x', 'x']
		});
		expect(body).toEqual({
			boards: [
				{ id: 'a', name: 'Mine', widgets: [{ id: 'w', type: 'note', size: 'm', config: { text: 'hi' } }] }
			],
			active: 'a',
			hidden: ['x']
		});
	});

	test('a widget of a type this console doesn’t know is kept, one without an id is not', () => {
		const b = readBoard({
			id: 'a',
			name: 'A',
			widgets: [
				{ id: 'w1', type: 'from-the-future', size: 'l' },
				{ type: 'note' },
				{ id: 'w2', type: 'note', size: 'huge' }
			]
		});
		expect(b?.widgets).toEqual([
			{ id: 'w1', type: 'from-the-future', size: 'l' },
			{ id: 'w2', type: 'note', size: 'm' }
		]);
	});
});
