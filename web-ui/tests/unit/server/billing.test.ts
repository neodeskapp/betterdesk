import { afterEach, describe, expect, test } from 'vitest';
import type { Contract } from '../../../src/lib/api/billing.ts';
import {
	amountFromMinutes,
	billingTab,
	contractPatch,
	contractValues,
	datesInOrder,
	filterByDevice,
	filterContracts,
	filterPackages,
	formatMinutes,
	formatMoney,
	formatMonth,
	mergeEnded,
	minutesFromAmount,
	newContractBody,
	opensSettings,
	roundingChoice,
	roundingMinutes,
	sameDay,
	serverMonth,
	thisMonth,
	usageRows,
	type AssignValues,
	VALIDITY_DAY
} from '../../../src/lib/serverarea/billing.ts';
import { formatDate } from '../../../src/lib/i18n/format.ts';

const contract = (over: Partial<Contract> = {}): Contract => ({
	id: 'c1',
	target_type: 'device_group',
	target_key: 'g1',
	target_name: 'Harbor',
	package_id: 'p1',
	package_name: 'Basic',
	status: 'active',
	remaining_minutes: 100,
	hourly_rate: 0,
	overage_rate: null,
	currency: 'EUR',
	valid_from: '',
	valid_until: '',
	current_period_ym: '2026-11',
	...over
});

describe('billing URL', () => {
	test('tabs and the old ones mapped onto them', () => {
		expect(billingTab('sessions')).toBe('sessions');
		expect(billingTab('reports')).toBe('sessions');
		expect(billingTab('overview')).toBe('packages');
		expect(billingTab('settings')).toBe('packages');
		expect(billingTab(null)).toBe('packages');
	});
	test('?settings=1 and ?tab=settings open the billing settings', () => {
		expect(opensSettings(new URLSearchParams('settings=1'))).toBe(true);
		expect(opensSettings(new URLSearchParams('tab=settings'))).toBe(true);
		expect(opensSettings(new URLSearchParams('tab=packages'))).toBe(false);
	});
});

describe('values', () => {
	test('minutes as hours and minutes', () => {
		expect(formatMinutes(260)).toMatch(/4\s?h 20\s?m/);
		expect(formatMinutes(1680)).toMatch(/^28\s?h$/);
		expect(formatMinutes(0)).toMatch(/^0\s?m/);
		expect(formatMinutes(null)).toMatch(/^0\s?m/);
	});
	test('money and months', () => {
		expect(formatMoney(45, 'EUR', 'en-US')).toBe('€45.00');
		expect(formatMoney(1, 'NOT-A-CODE', 'en-US')).toBe('1.00 NOT-A-CODE');
		expect(formatMonth('2026-11', 'en-US')).toBe('November 2026');
		expect(formatMonth('garbage', 'en-US')).toBe('garbage');
		expect(thisMonth(new Date(2026, 0, 5))).toBe('2026-01');
	});
	test('an end on the start day', () => {
		expect(sameDay('2026-11-07T10:00:00', '2026-11-07T12:00:00')).toBe(true);
		expect(sameDay('2026-11-07T10:00:00', '2026-11-09T09:00:00')).toBe(false);
		expect(sameDay('2026-11-07T10:00:00', '')).toBe(false);
	});
});

describe('package sheet', () => {
	test('included time in hours when it divides evenly', () => {
		expect(amountFromMinutes(600)).toEqual({ amount: '10', unit: 'hours' });
		expect(amountFromMinutes(90)).toEqual({ amount: '90', unit: 'minutes' });
		expect(minutesFromAmount('1.5', 'hours')).toBe(90);
		expect(minutesFromAmount('45', 'minutes')).toBe(45);
		expect(minutesFromAmount('-3', 'hours')).toBe(0);
	});
	test('rounding: a listed step or a custom one', () => {
		expect(roundingChoice(15)).toEqual({ choice: '15', custom: '15' });
		expect(roundingChoice(7)).toEqual({ choice: 'custom', custom: '7' });
		expect(roundingChoice(null).choice).toBe('10');
		expect(roundingMinutes('custom', '7')).toBe(7);
		expect(roundingMinutes('custom', '')).toBe(15);
		expect(roundingMinutes('30', '7')).toBe(30);
	});
	test('search', () => {
		const list = [
			{ name: 'Basic', description: '5 hours' },
			{ name: 'Business', description: 'small offices' }
		];
		expect(filterPackages(list, 'OFFICE').map((p) => p.name)).toEqual(['Business']);
	});
});

describe('assignments', () => {
	const values = (over: Partial<AssignValues> = {}): AssignValues => ({
		targetKey: 'g1',
		packageId: 'p1',
		remaining: '',
		hourlyRate: '',
		overageRate: '',
		validFrom: '',
		validUntil: '',
		...over
	});

	test('a new assignment leaves out empty optional fields', () => {
		expect(newContractBody(values(), { currency: 'CHF' })).toEqual({
			target_type: 'device_group',
			target_key: 'g1',
			package_id: 'p1',
			currency: 'CHF'
		});
		expect(
			newContractBody(
				values({
					remaining: '30',
					hourlyRate: '0',
					overageRate: '12.5',
					validFrom: '2026-11-01',
					validUntil: '2026-12-31'
				}),
				undefined
			)
		).toEqual({
			target_type: 'device_group',
			target_key: 'g1',
			package_id: 'p1',
			currency: 'EUR',
			remaining_minutes: 30,
			hourly_rate: 0,
			overage_rate: 12.5,
			valid_from: '2026-11-01T00:00:00Z',
			valid_until: '2026-12-31T23:59:59Z'
		});
	});
	test('an edit sends the numbers that changed and clears empty dates', () => {
		const before = values({ remaining: '600', hourlyRate: '0', overageRate: '9' });
		expect(contractPatch(values({ remaining: '5', hourlyRate: '0', overageRate: '9' }), before)).toEqual({
			remaining_minutes: 5,
			valid_from: null,
			valid_until: null
		});
	});
	test('an edit of the end date alone leaves the remaining minutes and rates the list loaded alone', () => {
		const loaded = contractValues(contract({ remaining_minutes: 600, hourly_rate: 50, overage_rate: 9 }));
		expect(contractPatch({ ...loaded, validUntil: '2026-12-31' }, loaded)).toEqual({
			valid_from: null,
			valid_until: '2026-12-31T23:59:59Z'
		});
		// A cleared overage rate is sent as null, so the package's rate applies again
		expect(contractPatch({ ...loaded, overageRate: '' }, loaded)).toMatchObject({ overage_rate: null });
	});
	test('an assignment as sheet values', () => {
		expect(contractValues(contract({ valid_from: '2026-11-01T00:00:00Z', overage_rate: 9 }))).toMatchObject({
			remaining: '100',
			hourlyRate: '0',
			overageRate: '9',
			validFrom: '2026-11-01',
			validUntil: ''
		});
		expect(contractValues(contract()).overageRate).toBe('');
	});
	test('dates in order', () => {
		expect(datesInOrder('2026-11-02', '2026-11-01')).toBe(false);
		expect(datesInOrder('2026-11-01', '2026-11-01')).toBe(true);
		expect(datesInOrder('', '2026-11-01')).toBe(true);
	});
	test('search by target, key and package', () => {
		const list = [contract(), contract({ id: 'c2', target_name: 'Clinic', package_name: 'Care' })];
		expect(filterContracts(list, 'care').map((c) => c.id)).toEqual(['c2']);
		expect(filterContracts(list, 'g1')).toHaveLength(2);
	});
});

describe('included time this month', () => {
	const packages = [
		{ id: 'p1', name: 'Basic', included_minutes: 300 },
		{ id: 'p2', name: 'Pay as you go', included_minutes: 0 }
	];
	test('used = included minus remaining, fullest first, no included time last', () => {
		const rows = usageRows(
			[
				contract({ id: 'a', remaining_minutes: 250 }),
				contract({ id: 'b', remaining_minutes: 0 }),
				contract({ id: 'c', package_id: 'p2', package_name: '' })
			],
			packages,
			'2026-11'
		);
		expect(rows.map((r) => [r.contract.id, r.used, r.included])).toEqual([
			['b', 300, 300],
			['a', 50, 300],
			['c', 0, 0]
		]);
		expect(rows[2]?.packageName).toBe('Pay as you go');
	});
	test('a period from an earlier month counts as unused (the server resets it)', () => {
		const rows = usageRows(
			[contract({ remaining_minutes: 0, current_period_ym: '2026-10' })],
			packages,
			'2026-11'
		);
		expect(rows[0]?.used).toBe(0);
	});
});

describe('sessions', () => {
	test('device filter by ID and name', () => {
		const list = [
			{ device_id: '900100001', device_name: 'reception' },
			{ device_id: '900100017', device_name: 'sales-laptop' }
		];
		expect(filterByDevice(list, 'SALES').map((s) => s.device_id)).toEqual(['900100017']);
		expect(filterByDevice(list, '0001')).toHaveLength(2);
	});
	test('ended sessions from two lists, newest first', () => {
		const merged = mergeEnded([
			[{ started_at: '2026-11-01T10:00:00Z' }],
			[{ started_at: '2026-11-05T10:00:00Z' }, { started_at: '2026-11-03T10:00:00Z' }]
		]);
		expect(merged.map((s) => s.started_at.slice(8, 10))).toEqual(['05', '03', '01']);
	});
});

describe('validity dates', () => {
	const tz = process.env.TZ;
	afterEach(() => {
		process.env.TZ = tz;
	});

	test('an assignment from 1 to 30 Nov reads so east and west of UTC', () => {
		for (const zone of ['Europe/Berlin', 'America/New_York']) {
			process.env.TZ = zone;
			expect(formatDate('2026-11-01T00:00:00Z', VALIDITY_DAY)).toBe('Nov 1, 2026');
			expect(formatDate('2026-11-30T23:59:59Z', VALIDITY_DAY)).toBe('Nov 30, 2026');
		}
	});
});

describe('the month of included time', () => {
	test("the server's billing month counts, not the browser's", async () => {
		const browserInDecember = new Date(2026, 11, 1, 0, 30);
		const month = await serverMonth(
			[{ id: 'p1' }],
			async () => ({ period_ym: '2026-11' }),
			browserInDecember
		);
		expect(month).toBe('2026-11');
		const [row] = usageRows(
			[contract({ package_id: 'p1', remaining_minutes: 100, current_period_ym: '2026-11' })],
			[{ id: 'p1', name: 'Basic', included_minutes: 600 }],
			month
		);
		expect(row?.used).toBe(500);
	});

	test("without a package or a preview, the browser's month", async () => {
		const now = new Date(2026, 11, 1, 0, 30);
		expect(await serverMonth([], async () => ({ period_ym: '2026-11' }), now)).toBe('2026-12');
		expect(await serverMonth([{ id: 'p1' }], () => Promise.reject(new Error('x')), now)).toBe('2026-12');
	});
});
