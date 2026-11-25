/**
 * The chat's rules without state (lib/chat/logic.ts, A242): messages stay
 * in stored order as frames arrive, a sent message replaces its pending
 * copy, read marks only move forward and are per reader, "Seen" means
 * someone other than the sender read it, runs and days, the words of the
 * system's lines and of a row's last line.
 */
import { describe, expect, test } from 'vitest';
import {
	emptyThread,
	eventText,
	isMine,
	lastLine,
	linkParts,
	snippet,
	mayEdit,
	pendingMessage,
	seen,
	startsRun,
	unreadIn,
	withHistory,
	withMessage,
	withRead,
	EDIT_WINDOW,
	RUN_GAP
} from '../../../src/lib/chat/logic.ts';
import type { Message, Party } from '../../../src/lib/chat/types.ts';

const anna: Party = { kind: 'user', id: '5', name: 'Anna' };
const ben: Party = { kind: 'user', id: '6', name: 'Ben' };
const dev: Party = { kind: 'device', id: 'dev1', name: 'reception-01' };

function msg(id: string, from: Party, text = id, at = Number(id) * 1000): Message {
	return {
		id,
		conversation_id: 'dev1',
		from,
		text,
		reply_to: null,
		attachments: [],
		created_at: at,
		edited_at: null,
		deleted: false,
		reactions: {}
	};
}

const t = (key: string, vars?: Record<string, string | number>) =>
	`${key}${
		vars
			? ' ' +
				Object.entries(vars)
					.map(([k, v]) => `${k}=${v}`)
					.join(' ')
			: ''
	}`;

describe('a thread as frames arrive', () => {
	test('messages are kept in stored order, a changed one replaces its old copy', () => {
		let th = emptyThread();
		th = withMessage(th, msg('3', dev));
		th = withMessage(th, msg('1', dev));
		th = withMessage(th, msg('2', anna));
		expect(th.messages.map((m) => m.id)).toEqual(['1', '2', '3']);
		th = withMessage(th, { ...msg('2', anna), text: 'edited', edited_at: 9 });
		expect(th.messages.map((m) => m.text)).toEqual(['1', 'edited', '3']);
	});

	test('the server copy of a sent message replaces the pending one by its client id', () => {
		let th = withMessage(emptyThread(), msg('1', dev));
		th = withMessage(th, pendingMessage('dev1', { id: 5, name: 'Anna' }, 'hi', 'c1'));
		expect(th.messages.at(-1)?.pending).toBe('sending');
		th = withMessage(th, { ...msg('2', anna, 'hi'), client_id: 'c1' });
		expect(th.messages.map((m) => [m.id, m.pending])).toEqual([
			['1', undefined],
			['2', undefined]
		]);
	});

	test('history puts older pages in front and keeps the read marks of every reader', () => {
		let th = withHistory(
			emptyThread(),
			[msg('5', dev), msg('6', anna)],
			[{ reader: { kind: 'user', id: '6' }, up_to: '5' }],
			true,
			'0'
		);
		th = withHistory(th, [msg('3', dev), msg('4', dev)], [], false, '5');
		expect(th.messages.map((m) => m.id)).toEqual(['3', '4', '5', '6']);
		expect(th.more).toBe(false);
		expect(th.reads['u:6']).toBe(5);
	});
});

describe('read marks', () => {
	test('a mark only moves forward, and unread counts others after my mark', () => {
		let th = emptyThread();
		for (const m of [msg('1', dev), msg('2', anna), msg('3', dev), msg('4', dev)]) th = withMessage(th, m);
		const me = { id: 5 };
		expect(unreadIn(th, me)).toBe(3);
		th = withRead(th, { kind: 'user', id: '5' }, '3');
		expect(unreadIn(th, me)).toBe(1);
		th = withRead(th, { kind: 'user', id: '5' }, '1');
		expect(th.reads['u:5']).toBe(3);
		// Ben's mark is his own
		expect(unreadIn(th, { id: 6 })).toBe(4);
	});

	test('"Seen" is someone other than the sender reading it', () => {
		let th = withMessage(emptyThread(), msg('7', anna));
		const m = th.messages[0] as Message;
		th = withRead(th, { kind: 'user', id: '5' }, '7');
		expect(seen(th, m)).toBe(false);
		th = withRead(th, { kind: 'device', id: 'dev1' }, '7');
		expect(seen(th, m)).toBe(true);
	});

	test("in a computer's conversation only the computer's read is \"Seen\", not a colleague's (L532)", () => {
		let th = withMessage(emptyThread(), msg('7', anna));
		const m = th.messages[0] as Message;
		th = withRead(th, { kind: 'user', id: '6' }, '7');
		expect(seen(th, m)).toBe(false);
		th = withRead(th, { kind: 'device', id: 'dev1' }, '7');
		expect(seen(th, m)).toBe(true);
	});

	test('between colleagues another person\'s read is "Seen"', () => {
		let th = withMessage(emptyThread(), { ...msg('7', anna), conversation_id: 'dm:5:6' });
		const m = th.messages[0] as Message;
		th = withRead(th, { kind: 'user', id: '6' }, '7');
		expect(seen(th, m)).toBe(true);
	});
});

describe('who and when', () => {
	test('mine is a console user with my id', () => {
		expect(isMine(msg('1', anna), { id: 5 })).toBe(true);
		expect(isMine(msg('1', dev), { id: 5 })).toBe(false);
		expect(isMine(msg('1', ben), { id: 5 })).toBe(false);
	});

	test('a run is one sender within a few minutes on one day', () => {
		const a = msg('1', anna, 'a', 1_000_000);
		expect(startsRun(undefined, a)).toBe(true);
		expect(startsRun(a, msg('2', anna, 'b', 1_000_000 + 60_000))).toBe(false);
		expect(startsRun(a, msg('2', anna, 'b', 1_000_000 + RUN_GAP + 1))).toBe(true);
		expect(startsRun(a, msg('2', ben, 'b', 1_000_001))).toBe(true);
	});

	test('my message is editable for 24 hours', () => {
		const m = msg('1', anna, 'x', 0);
		expect(mayEdit(m, { id: 5 }, EDIT_WINDOW - 1)).toBe(true);
		expect(mayEdit(m, { id: 5 }, EDIT_WINDOW + 1)).toBe(false);
		expect(mayEdit(m, { id: 6 }, 1)).toBe(false);
		expect(mayEdit({ ...m, deleted: true }, { id: 5 }, 1)).toBe(false);
	});
});

describe('words', () => {
	test('lines of the system', () => {
		expect(eventText({ event: 'assigned', by: 'Anna', to: 'Ben' }, t)).toBe(
			'chat.event_assigned by=Anna to=Ben'
		);
		expect(eventText({ event: 'closed', by: 'Anna', note: 'driver' }, t)).toBe(
			'chat.event_closed_note by=Anna note=driver'
		);
		expect(eventText({ event: 'closed', by: 'Anna' }, t)).toBe('chat.event_closed by=Anna');
		expect(eventText(undefined, t)).toBe('');
	});

	test('the last line of a row', () => {
		expect(lastLine({ text: 'hi', from_kind: 'user', from_name: 'Anna', at: 1 }, t)).toBe('Anna: hi');
		expect(lastLine({ text: 'hi', from_kind: 'device', from_name: 'pc', at: 1 }, t)).toBe('hi');
		expect(lastLine({ text: '', from_kind: 'device', from_name: 'pc', at: 1, deleted: true }, t)).toBe(
			'chat.message_deleted'
		);
		expect(lastLine({ text: '', from_kind: 'device', from_name: 'pc', at: 1, files: 2 }, t)).toBe(
			'chat.files_count count=2'
		);
		expect(
			lastLine(
				{ text: '', from_kind: 'system', from_name: '', at: 1, event: '{"event":"taken","by":"Ben"}' },
				t
			)
		).toBe('chat.event_taken by=Ben');
	});

	test('links are http(s) only, without the full stop after them, and never markup', () => {
		expect(linkParts('see https://example.com/a. ok')).toEqual([
			{ text: 'see ' },
			{ text: 'https://example.com/a', href: 'https://example.com/a' },
			{ text: '. ok' }
		]);
		expect(linkParts('javascript:alert(1) <b>x</b>')).toEqual([{ text: 'javascript:alert(1) <b>x</b>' }]);
	});
});

describe('snippet: the part of a found message around the match', () => {
	test('keeps short texts whole and marks the match without regard to case', () => {
		expect(snippet('The printer blinks', 'PRINTER')).toEqual({
			before: 'The ',
			match: 'printer',
			after: ' blinks'
		});
	});
	test('cuts long texts around the first match with an ellipsis on the cut sides', () => {
		const text = `${'a'.repeat(100)} printer ${'b'.repeat(100)}`;
		const s = snippet(text, 'printer', 10);
		expect(s.match).toBe('printer');
		expect(s.before).toBe(`…${'a'.repeat(9)} `);
		expect(s.after).toBe(` ${'b'.repeat(9)}…`);
	});
	test('a text without the match starts at the beginning', () => {
		expect(snippet('hello there', 'zzz', 5)).toEqual({ before: '', match: '', after: 'hello…' });
	});
});
