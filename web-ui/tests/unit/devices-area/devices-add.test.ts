/**
 * Devices › Add computers (/devices/add): which tabs an account gets and
 * which one ?tab= opens.
 */
import { describe, expect, test } from 'vitest';
import { ADD_TABS, addTabs, pickAddTab } from '../../../src/lib/devices-area/add-computers.ts';

const can = (list: string[]) => (p: string) => list.includes(p);

describe('the tabs of Add computers', () => {
	test('in their order: invites, device tokens, pairing codes, support agents, manual setup', () => {
		expect([...ADD_TABS]).toEqual(['invites', 'tokens', 'pairing', 'agents', 'manual']);
	});

	test('each tab asks what its page asked before', () => {
		const all = addTabs({ can: () => true, role: 'super_admin' });
		expect(all).toEqual(['invites', 'tokens', 'pairing', 'agents', 'manual']);
		// An operator who approves joins: invites and pairing codes, no tokens, no agents (admin roles)
		expect(addTabs({ can: can(['enrollment.approve']), role: 'operator' })).toEqual([
			'invites',
			'pairing',
			'manual'
		]);
		expect(addTabs({ can: can(['enrollment.manage']), role: 'admin' })).toEqual([
			'tokens',
			'agents',
			'manual'
		]);
		// Manual setup needs a session only
		expect(addTabs({ can: can([]), role: 'viewer' })).toEqual(['manual']);
	});

	test('?tab= opens a tab the account has; anything else opens the first one', () => {
		const tabs = addTabs({ can: can(['enrollment.approve']), role: 'operator' });
		expect(pickAddTab('pairing', tabs)).toBe('pairing');
		expect(pickAddTab('PAIRING', tabs)).toBe('pairing');
		expect(pickAddTab('tokens', tabs)).toBe('invites');
		expect(pickAddTab(null, tabs)).toBe('invites');
		expect(pickAddTab('agents', ['manual'])).toBe('manual');
	});
});
