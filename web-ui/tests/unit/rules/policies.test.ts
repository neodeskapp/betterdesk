import { describe, expect, test } from 'vitest';
import type { ResolvedPolicies } from '../../../src/lib/api/policies.ts';
import {
	auditSection,
	changedGroups,
	FIELD_IDS,
	fieldProblems,
	groupBody,
	groupFields,
	isAddressRange,
	isRelayAddress,
	setCount,
	startValue,
	undoWrites,
	valuesFromResolved,
	writesFor
} from '../../../src/lib/rules/policies.ts';

/** A resolved answer as the server gives it: every group, every field, nothing stored */
function resolved(stored: Record<string, Record<string, unknown>> = {}): ResolvedPolicies {
	const out: ResolvedPolicies = {};
	for (const id of FIELD_IDS) {
		const [group = '', name = ''] = id.split('.');
		const values = stored[group] ?? {};
		const g = (out[group] ??= {
			set: Object.keys(values).length > 0,
			applied_by: group === 'network' ? 'server' : 'device',
			device_app_applies: false,
			timing: group === 'network' ? 'server_minute' : 'device_next_check',
			fields: {}
		});
		const has = name in values;
		g.fields[name] = { value: has ? values[name] : null, source: has ? 'organization' : 'none' };
		if (id === 'network.block_direct_p2p') {
			g.fields[name] = {
				...g.fields[name],
				source: has ? 'organization' : 'server',
				server_value: true,
				server_setting: { label_key: 'settings.connection_mode_title', href: '/settings#x' }
			};
		}
	}
	return out;
}

describe('values from the policies in force', () => {
	test('a group never saved has no rule in any field: no defaults are invented', () => {
		const v = valuesFromResolved(resolved());
		expect(Object.keys(v).sort()).toEqual([...FIELD_IDS].sort());
		for (const id of FIELD_IDS) expect(v[id], id).toBeNull();
	});

	test('only what the organization stored becomes a value; 0, off and empty lists stay', () => {
		const v = valuesFromResolved(
			resolved({
				connection: { mode: 'unattended', idle_lock_timeout: 0 },
				features: { allow_audio: false },
				security: { allowed_operators: ['remote_operator', 'demo_operator'] },
				network: { allowed_relay_servers: [] }
			})
		);
		expect(v['connection.mode']).toBe('unattended');
		expect(v['connection.idle_lock_timeout']).toBe(0);
		expect(v['connection.max_session_duration']).toBeNull();
		expect(v['features.allow_audio']).toBe(false);
		expect(v['features.allow_clipboard']).toBeNull();
		expect(v['security.allowed_operators']).toEqual(['remote_operator', 'demo_operator']);
		expect(v['network.allowed_relay_servers']).toEqual([]);
		// The server's connection strategy is where the value comes from, not a rule here
		expect(v['network.block_direct_p2p']).toBeNull();
	});

	test('setting a rule starts from the server value where there is one, else from a neutral value', () => {
		const r = resolved();
		expect(startValue('network.block_direct_p2p', r)).toBe(true);
		const off = resolved();
		off.network!.fields.block_direct_p2p!.server_value = false;
		expect(startValue('network.block_direct_p2p', off)).toBe(false);
		// The server could not say: neutral
		const unknown = resolved();
		unknown.network!.fields.block_direct_p2p!.server_value = null;
		expect(startValue('network.block_direct_p2p', unknown)).toBe(false);
		expect(startValue('features.allow_audio', r)).toBe(false);
		expect(startValue('connection.mode', r)).toBe('attended');
		expect(startValue('connection.idle_lock_timeout', r)).toBe(0);
		expect(startValue('security.password_min_length', r)).toBe(6);
		expect(startValue('network.ip_allowlist', r)).toEqual([]);
	});

	test('the fields of each group in page order', () => {
		expect(groupFields('features')).toEqual([
			'features.allow_file_transfer',
			'features.allow_clipboard',
			'features.allow_audio',
			'features.allow_recording'
		]);
		expect(groupFields('update')).toEqual(['update.auto_update', 'update.channel']);
	});
});

describe('saving', () => {
	test('a group body holds only the fields with a rule; none left means no body (DELETE)', () => {
		const v = valuesFromResolved(resolved());
		expect(groupBody('features', v)).toBeNull();
		v['features.allow_audio'] = false;
		expect(groupBody('features', v)).toEqual({ allow_audio: false });
		v['network.ip_allowlist'] = ['10.0.0.0/8'];
		expect(groupBody('network', v)).toEqual({ ip_allowlist: ['10.0.0.0/8'] });
	});

	test('only changed groups are written: PUT with the rules, DELETE when the last rule went', () => {
		const loaded = valuesFromResolved(
			resolved({ connection: { mode: 'unattended' }, update: { channel: 'beta' } })
		);
		const v = { ...loaded };
		expect(changedGroups(v, loaded)).toEqual([]);
		expect(writesFor(v, loaded)).toEqual([]);
		v['connection.mode'] = 'attended';
		v['update.channel'] = null;
		expect(changedGroups(v, loaded)).toEqual(['connection', 'update']);
		expect(writesFor(v, loaded)).toEqual([
			{ group: 'connection', body: { mode: 'attended' } },
			{ group: 'update', body: null }
		]);
		// Ticking and unticking is no change
		v['update.channel'] = 'beta';
		expect(changedGroups(v, loaded)).toEqual(['connection']);
	});

	test('saving "off" where there was no rule is a change (task 5: "Nothing has changed")', () => {
		const loaded = valuesFromResolved(resolved());
		const v = { ...loaded, 'features.allow_audio': false };
		expect(writesFor(v, loaded)).toEqual([{ group: 'features', body: { allow_audio: false } }]);
	});

	test('undo writes the previous group bodies back, DELETE for a group that had no rule', () => {
		const before = valuesFromResolved(resolved({ connection: { mode: 'unattended', wallpaper: 'show' } }));
		expect(undoWrites(before, ['connection', 'features'])).toEqual([
			{ group: 'connection', body: { mode: 'unattended', wallpaper: 'show' } },
			{ group: 'features', body: null }
		]);
	});

	test('wrong numbers and wrong list entries are named per field, only where a rule is set', () => {
		const v = valuesFromResolved(resolved());
		expect(fieldProblems(v)).toEqual([]);
		v['connection.max_session_duration'] = 1441;
		v['connection.idle_lock_timeout'] = 1.5;
		v['security.password_min_length'] = 5;
		v['network.ip_allowlist'] = ['10.0.0.0/8', 'not-a-cidr'];
		v['network.allowed_relay_servers'] = ['relay one.example.com'];
		expect(fieldProblems(v)).toEqual([
			{ field: 'connection.max_session_duration', entry: '' },
			{ field: 'connection.idle_lock_timeout', entry: '' },
			{ field: 'security.password_min_length', entry: '' },
			{ field: 'network.allowed_relay_servers', entry: 'relay one.example.com' },
			{ field: 'network.ip_allowlist', entry: 'not-a-cidr' }
		]);
		v['connection.max_session_duration'] = 1440;
		v['connection.idle_lock_timeout'] = null;
		expect(fieldProblems(v).map((p) => p.field)).not.toContain('connection.max_session_duration');
		expect(fieldProblems(v).map((p) => p.field)).not.toContain('connection.idle_lock_timeout');
	});
});

describe('group states and the change log', () => {
	test('groups set counts what the organization stored', () => {
		expect(setCount(resolved({ connection: { mode: 'x' }, update: { channel: 'beta' } }))).toBe(2);
		expect(setCount(null)).toBe(0);
	});

	test('the group of a change comes from its details, then the older fields', () => {
		const base = {
			timestamp: '',
			created_at: '',
			action: '',
			actor: '',
			user: '',
			target: '',
			section: '',
			category: '',
			detail: '',
			description: ''
		};
		expect(auditSection({ ...base, details: { category: 'network' } })).toBe('network');
		expect(auditSection({ ...base, details: null, section: 'update' })).toBe('update');
		expect(auditSection({ ...base, details: null })).toBe('');
	});
});

describe('list entries (BUGS L395)', () => {
	test('IP ranges: an address or a CIDR range, IPv4 or IPv6', () => {
		for (const ok of ['192.168.1.0/24', '10.0.0.5', '0.0.0.0/0', '2001:db8::/32', '::1'])
			expect(isAddressRange(ok), ok).toBe(true);
		for (const bad of ['not-a-cidr', '999.1.1.1/77', '10.0.0.0/33', '10.0.0', '2001:db8::/129', '1.2.3.4/'])
			expect(isAddressRange(bad), bad).toBe(false);
	});

	test('relay servers: a host name or an address, with a port or without', () => {
		for (const ok of [
			'relay1.example.com:21117',
			'relay2.example.com',
			'10.0.0.1',
			'10.0.0.1:21117',
			'[2001:db8::1]:21117'
		])
			expect(isRelayAddress(ok), ok).toBe(true);
		for (const bad of [
			'relay one.example.com',
			'relay.example.com:99999',
			'relay.example.com:',
			'https://relay.example.com',
			'-x'
		])
			expect(isRelayAddress(bad), bad).toBe(false);
	});
});
