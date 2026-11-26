/**
 * Settings › General as one draft (lib/settings/general.ts): which parts a
 * save sends, the checks of the addresses per field, and the connection
 * rules that do nothing in the current state.
 */
import { describe, expect, test, vi } from 'vitest';
import {
	GENERAL_DEFAULTS,
	apiUrlProblem,
	changedParts,
	connectionNotes,
	generalProblems,
	hostProblem,
	partFields,
	partsWithChanges,
	saveInParts
} from '../../../src/lib/settings/general.ts';
import { PartlySaved } from '../../../src/lib/ui/settings/draft.svelte.ts';

const change = (field: string) => ({ field, label: field, from: '', to: '', fromValue: '', toValue: '' });

describe('the parts of one save', () => {
	test('only the parts with changes are saved, in the order of the page', () => {
		expect(changedParts([change('same_nat_relay'), change('public_api_url')])).toEqual([
			'endpoints',
			'connection'
		]);
		expect(changedParts([change('scope')])).toEqual(['scope']);
		expect(changedParts([change('availability_days'), change('enabled_unknown')])).toEqual(['availability']);
		expect(changedParts([])).toEqual([]);
	});

	test('another tab names its own parts', () => {
		const table = { mode: ['join_mode'], approvals: ['rich_approve', 'tag_picker'] } as const;
		expect(partsWithChanges([change('tag_picker')], table, ['mode', 'approvals'])).toEqual(['approvals']);
	});

	test('a part that fails keeps its fields unsaved; the message names what was saved and what not', async () => {
		const table = { a: ['x'], b: ['y', 'z'], c: ['w'] };
		const run = vi.fn(async (p: string) => {
			if (p === 'b') throw new Error('refused');
		});
		const err = await saveInParts(['a', 'b', 'c'], table, (p) => `Part ${p}`, run).catch((e: unknown) => e);
		expect(run.mock.calls.map((c) => c[0])).toEqual(['a', 'b', 'c']);
		expect(err).toBeInstanceOf(PartlySaved);
		expect((err as PartlySaved).saved).toEqual(['x', 'w']);
		expect((err as PartlySaved).message).toMatch(/Part a, Part c/);
		expect((err as PartlySaved).message).toMatch(/Part b.*refused/);
		await expect(
			saveInParts(
				['a'],
				table,
				(p) => p,
				async () => undefined
			)
		).resolves.toBeUndefined();
	});

	test('every field belongs to one part', () => {
		const all = Object.values(partFields).flat();
		expect(new Set(all).size).toBe(all.length);
		expect(partFields.endpoints).toEqual(['public_server_id', 'public_relay_server', 'public_api_url']);
	});
});

describe('the addresses, checked per field', () => {
	test('the ID and relay server are a host name or an IP address alone', () => {
		for (const ok of ['', 'remote.example.com', '203.0.113.10', 'desk-1', '[2001:db8::1]', '2001:db8::1'])
			expect(hostProblem(ok), ok).toBe('');
		for (const extra of ['https://remote.example.com', 'remote.example.com:21116', 'remote.example.com/x'])
			expect(hostProblem(extra), extra).toMatch(/without http/);
		for (const bad of ['remote example.com', '-x.example.com', 'ex_ample.com', '300.1.1.1'])
			expect(hostProblem(bad), bad).toMatch(/Not a host name/);
	});

	test('the API address is a full http or https address', () => {
		for (const ok of [
			'',
			'https://api.example.com',
			'http://203.0.113.10:21114',
			'https://x.example.com/api'
		])
			expect(apiUrlProblem(ok), ok).toBe('');
		for (const bad of ['api.example.com', 'ftp://api.example.com', 'https://', 'https://a b.com'])
			expect(apiUrlProblem(bad), bad).toMatch(/https:\/\//);
	});

	test('the problems of the form, by field, with the wait in range', () => {
		expect(generalProblems(GENERAL_DEFAULTS)).toEqual({});
		const p = generalProblems({
			...GENERAL_DEFAULTS,
			public_server_id: 'https://x.example.com',
			public_api_url: 'x',
			p2p_fallback_ms: 40000
		});
		expect(Object.keys(p).sort()).toEqual(['p2p_fallback_ms', 'public_api_url', 'public_server_id']);
		expect(generalProblems({ ...GENERAL_DEFAULTS, p2p_fallback_ms: null }).p2p_fallback_ms).toBeTruthy();
		// While every session uses the relay the wait is not used, so it is not checked
		expect(generalProblems({ ...GENERAL_DEFAULTS, mode: 'relay_only', p2p_fallback_ms: -1 })).toEqual({});
	});
});

describe('connection rules that do nothing now', () => {
	test('relay only: the wait and the same-address rule have no effect', () => {
		const n = connectionNotes({ ...GENERAL_DEFAULTS, mode: 'relay_only' });
		expect(n.p2p_fallback_ms).toMatch(/relay/);
		expect(n.same_nat_relay).toMatch(/relay/);
		expect(connectionNotes(GENERAL_DEFAULTS)).toEqual({});
	});

	test('only accounts that may connect already needs a signed-in app; both rule out the shared address', () => {
		const n = connectionNotes({ ...GENERAL_DEFAULTS, operator_only_outbound: true });
		expect(n.logged_in_only_initiator).toMatch(/Only accounts that may connect/);
		expect(n.allow_shared_nat_initiator).toBeTruthy();
		const m = connectionNotes({ ...GENERAL_DEFAULTS, logged_in_only_initiator: true });
		expect(m.allow_shared_nat_initiator).toBeTruthy();
		expect(m.logged_in_only_initiator).toBeUndefined();
	});
});
