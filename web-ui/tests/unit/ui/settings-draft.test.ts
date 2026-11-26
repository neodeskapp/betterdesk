import { describe, expect, test, vi } from 'vitest';
import { SettingsDraft } from '../../../src/lib/ui/settings/draft.svelte.ts';
import { anyDirty } from '../../../src/lib/settings/dirty.ts';

function draft() {
	return new SettingsDraft(
		{ 'device.view': true, 'device.delete': false, keep: 14 },
		{
			'device.view': { label: 'View devices' },
			'device.delete': { label: 'Delete devices' },
			keep: { label: 'Keep this many', format: (v) => `${String(v)} copies` }
		}
	);
}

describe('SettingsDraft', () => {
	test('starts clean and lists no changes', () => {
		const d = draft();
		expect(d.dirty).toBe(false);
		expect(d.changes).toEqual([]);
	});

	test('a change is listed with its name and the old and new value as words', () => {
		const d = draft();
		d.set('device.delete', true);
		d.set('keep', 7);
		expect(d.dirty).toBe(true);
		expect(d.changes).toEqual([
			{
				field: 'device.delete',
				label: 'Delete devices',
				from: 'off',
				to: 'on',
				fromValue: false,
				toValue: true
			},
			{ field: 'keep', label: 'Keep this many', from: '14 copies', to: '7 copies', fromValue: 14, toValue: 7 }
		]);
	});

	test('setting a value back to what was loaded is no change', () => {
		const d = draft();
		d.set('keep', 7);
		d.set('keep', 14);
		expect(d.dirty).toBe(false);
	});

	test('reset puts one field back, discard all of them', () => {
		const d = draft();
		d.set('keep', 7);
		d.set('device.view', false);
		d.reset('keep');
		expect(d.changes.map((c) => c.field)).toEqual(['device.view']);
		d.discard();
		expect(d.dirty).toBe(false);
		expect(d.values['device.view']).toBe(true);
	});

	test('save runs with the changes, then the saved values are the new start', async () => {
		const d = draft();
		d.set('keep', 7);
		const run = vi.fn().mockResolvedValue(undefined);
		const saved = await d.save(run);
		expect(run).toHaveBeenCalledWith(d.lastSaved);
		expect(saved).toBe(true);
		expect(d.dirty).toBe(false);
		expect(d.loaded.keep).toBe(7);
		expect(d.lastSaved.map((c) => c.field)).toEqual(['keep']);
	});

	test('a failed save keeps the changes and says why', async () => {
		const d = draft();
		d.set('keep', 7);
		const saved = await d.save(() => Promise.reject(new Error('Server says no')));
		expect(saved).toBe(false);
		expect(d.dirty).toBe(true);
		expect(d.error).toBe('Server says no');
		expect(d.busy).toBe(false);
	});

	test('load replaces what is shown and drops unsaved changes', () => {
		const d = draft();
		d.set('keep', 7);
		d.load({ 'device.view': false, 'device.delete': false, keep: 3 });
		expect(d.dirty).toBe(false);
		expect(d.values.keep).toBe(3);
	});

	test('a tracked draft with changes makes the page ask before it is left', () => {
		const d = draft();
		const stop = d.track();
		expect(anyDirty()).toBe(false);
		d.set('keep', 1);
		expect(anyDirty()).toBe(true);
		stop();
		expect(anyDirty()).toBe(false);
	});

	test('a field without a spec is named by its key and lists compare by content', () => {
		const d = new SettingsDraft<Record<string, unknown>>({ ranges: ['10.0.0.0/8'] });
		d.set('ranges', ['10.0.0.0/8']);
		expect(d.dirty).toBe(false);
		d.set('ranges', ['10.0.0.0/8', '192.168.0.0/16']);
		expect(d.changes[0]).toMatchObject({
			label: 'ranges',
			from: '10.0.0.0/8',
			to: '10.0.0.0/8, 192.168.0.0/16'
		});
	});
});
