/**
 * The settings pattern's components (lib/ui/settings, PATTERN.md): the
 * page template, rows with source, dependency and key, the filter, the
 * save bar with the list of changes, the list editor, keep-count, the
 * inherited value and the record picker.
 */
import { cleanup, render, screen, within } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { createRawSnippet, flushSync } from 'svelte';
import { afterEach, describe, expect, test, vi } from 'vitest';

vi.mock('$app/state', () => ({ page: { url: new URL('http://localhost/fx'), state: {} } }));
vi.mock('$app/paths', () => ({ resolve: (p: string) => p || '/', asset: (p: string) => '/' + p }));
vi.mock('$app/navigation', () => ({ goto: vi.fn(), beforeNavigate: vi.fn(), replaceState: vi.fn() }));

import SettingsFixture from './fixtures/SettingsFixture.svelte';
import KeepRowFixture from './fixtures/KeepRowFixture.svelte';
import SummaryPageFixture from './fixtures/SummaryPageFixture.svelte';
import ListEditor from '../../../src/lib/ui/settings/ListEditor.svelte';
import KeepCount from '../../../src/lib/ui/settings/KeepCount.svelte';
import InheritedValue from '../../../src/lib/ui/settings/InheritedValue.svelte';
import RecordPicker from '../../../src/lib/ui/settings/RecordPicker.svelte';
import SettingRow from '../../../src/lib/ui/settings/SettingRow.svelte';
import { relayOptions, roleOptions } from '../../../src/lib/ui/settings/pickers.ts';

afterEach(() => cleanup());

describe('a page with a summary', () => {
	test('without a scope picker no empty scope row sits above the groups', () => {
		const { container } = render(SummaryPageFixture);
		const body = container.querySelector('.settings-body');
		expect(body).toHaveClass('has-summary');
		expect(body).not.toHaveClass('has-scope');
		cleanup();
		const scoped = render(SummaryPageFixture, { withScope: true });
		expect(scoped.container.querySelector('.settings-body')).toHaveClass('has-scope');
	});

	test('summaryLast puts the summary after the groups on narrow screens', () => {
		const { container } = render(SummaryPageFixture, { summaryLast: true });
		expect(container.querySelector('.settings-body')).toHaveClass('summary-last');
		expect(screen.getByRole('complementary', { name: 'Summary' })).toHaveTextContent('What a backup holds');
	});
});

describe('a keep count in a setting row', () => {
	test('the row names the switch and number as one group, its help describes it', () => {
		render(KeepRowFixture);
		const group = screen.getByRole('group', { name: 'Keep update backups' });
		expect(group).toHaveAccessibleDescription('Older ones are deleted after each update.');
		expect(within(group).getByRole('checkbox', { name: 'Keep all update backups' })).not.toBeChecked();
		// The row's name is no <label> for a control that is not there
		expect(document.querySelector('label[for="r-keep-control"]')).toBeNull();
	});

	test('a refused number is marked and said next to it', () => {
		render(KeepRowFixture, { error: 'Enter a whole number from 1 to 1000.' });
		const number = screen.getByRole('spinbutton', { name: 'Keep this many' });
		expect(number).toHaveAttribute('aria-invalid', 'true');
		expect(number).toHaveAccessibleDescription('Enter a whole number from 1 to 1000.');
	});
});

describe('the settings page template', () => {
	test('title, purpose and save model come first, the scope picker before the groups', () => {
		render(SettingsFixture);
		expect(screen.getByRole('heading', { level: 1, name: 'Roles' })).toBeInTheDocument();
		expect(screen.getByText(/Changes are saved when you press Save/)).toBeInTheDocument();
		const role = screen.getByLabelText('Role');
		const firstGroup = screen.getByRole('heading', { level: 2, name: 'Devices' });
		expect(role.compareDocumentPosition(firstGroup) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
	});

	test('an account that may not see the page gets the no-access view and no settings', () => {
		render(SettingsFixture, { allowed: false });
		expect(screen.queryByRole('heading', { level: 2, name: 'Devices' })).toBeNull();
	});

	test('the consequence is said once for the group', () => {
		render(SettingsFixture);
		expect(screen.getAllByText('Applies on the next page these people open.')).toHaveLength(1);
	});
});

describe('a setting row', () => {
	test('the name labels the switch; help and source describe it; the key is there but secondary', () => {
		render(SettingsFixture);
		const view = screen.getByRole('checkbox', { name: 'View devices' });
		expect(view).toBeChecked();
		expect(view).toHaveAccessibleDescription(/See the device list\..*Built-in default for this role/);
		const row = view.closest('.setting-row') as HTMLElement;
		expect(within(row).getByText('device.view').closest('.setting-key')).not.toBeNull();
		expect((view as HTMLInputElement).labels?.[0]?.textContent).not.toContain('device.view');
	});

	test('a switch that shows other fields points at them and says whether they show', () => {
		const { rerender } = render(SettingRow, {
			id: 'auth',
			label: 'The mail server needs a sign-in',
			checked: false,
			controls: 'auth-fields'
		});
		const box = screen.getByRole('checkbox', { name: 'The mail server needs a sign-in' });
		expect(box).toHaveAttribute('aria-controls', 'auth-fields');
		expect(box).toHaveAttribute('aria-expanded', 'false');
		rerender({
			id: 'auth',
			label: 'The mail server needs a sign-in',
			checked: true,
			controls: 'auth-fields'
		});
		expect(box).toHaveAttribute('aria-expanded', 'true');
	});

	test('a dependency says what is needed and its button turns it on', async () => {
		render(SettingsFixture);
		await userEvent.click(screen.getByRole('checkbox', { name: 'View devices' }));
		expect(screen.getByText('Needs View devices.')).toBeInTheDocument();
		await userEvent.click(screen.getByRole('button', { name: 'Turn it on' }));
		expect(screen.getByRole('checkbox', { name: 'View devices' })).toBeChecked();
		expect(screen.queryByText('Needs View devices.')).toBeNull();
	});
});

describe('the filter', () => {
	test('hides rows and groups that do not match, also by key, and says when nothing does', async () => {
		render(SettingsFixture);
		const field = screen.getByRole('searchbox', { name: 'Filter permissions' });
		await userEvent.type(field, 'device.delete');
		expect(screen.getByRole('checkbox', { name: 'Delete devices' })).toBeVisible();
		expect(screen.queryByRole('checkbox', { name: 'View devices' })).toBeNull();
		expect(screen.queryByRole('heading', { level: 2, name: 'Server' })).toBeNull();
		expect(screen.getByText('1 setting matches.')).toBeInTheDocument();
		await userEvent.clear(field);
		await userEvent.type(field, 'xyz');
		expect(screen.getByText(/Nothing matches/)).toBeInTheDocument();
	});

	test('is a search landmark named like its field, so it is no setting of the page', () => {
		render(SettingsFixture);
		const region = screen.getByRole('search', { name: 'Filter permissions' });
		expect(within(region).getByRole('searchbox', { name: 'Filter permissions' })).toBeInTheDocument();
	});
});

describe('the changes bar', () => {
	test('appears with a change, names it with old and new value, and Discard puts it back', async () => {
		render(SettingsFixture);
		expect(screen.queryByRole('region', { name: 'Unsaved changes' })).toBeNull();
		await userEvent.click(screen.getByRole('checkbox', { name: 'Delete devices' }));
		const bar = screen.getByRole('region', { name: 'Unsaved changes' });
		expect(within(bar).getByText('1 unsaved change')).toBeInTheDocument();
		expect(bar).toHaveTextContent('Delete devices: off → on');
		expect(screen.getByRole('checkbox', { name: 'Delete devices' }).closest('.setting-row')).toHaveClass(
			'is-changed'
		);
		await userEvent.click(within(bar).getByRole('button', { name: 'Discard' }));
		expect(screen.getByRole('checkbox', { name: 'Delete devices' })).not.toBeChecked();
		expect(screen.queryByRole('region', { name: 'Unsaved changes' })).toBeNull();
	});

	test('Save and Ctrl+S both save', async () => {
		const onsave = vi.fn();
		render(SettingsFixture, { onsave });
		await userEvent.click(screen.getByRole('checkbox', { name: 'Delete devices' }));
		await userEvent.click(screen.getByRole('button', { name: 'Save changes' }));
		expect(onsave).toHaveBeenCalledTimes(1);
		await userEvent.keyboard('{Control>}s{/Control}');
		expect(onsave).toHaveBeenCalledTimes(2);
	});
});

describe('the list editor', () => {
	test('one row per entry, a message under a wrong one, Add focuses the new row, empty rows are dropped', async () => {
		const onchange = vi.fn();
		render(ListEditor, {
			id: 'ranges',
			items: ['10.0.0.0/8', 'nope'],
			onchange,
			itemLabel: (n: number) => `Range ${n}`,
			check: (s: string) => (s.includes('/') ? '' : 'Write a range like 10.0.0.0/8.'),
			addLabel: 'Add a range'
		});
		expect(screen.getByRole('textbox', { name: 'Range 1' })).toHaveValue('10.0.0.0/8');
		const bad = screen.getByRole('textbox', { name: 'Range 2' });
		expect(bad).toHaveAttribute('aria-invalid', 'true');
		expect(bad).toHaveAccessibleDescription('Write a range like 10.0.0.0/8.');
		await userEvent.click(screen.getByRole('button', { name: 'Add a range' }));
		expect(screen.getByRole('textbox', { name: 'Range 3' })).toHaveFocus();
		await userEvent.click(screen.getByRole('button', { name: 'Remove: Range 2' }));
		expect(onchange).toHaveBeenLastCalledWith(['10.0.0.0/8']);
	});

	test('offers existing records with one click until they are in the list', async () => {
		const onchange = vi.fn();
		render(ListEditor, {
			id: 'relays',
			items: [],
			onchange,
			itemLabel: (n: number) => `Relay ${n}`,
			addLabel: 'Add an address',
			quick: relayOptions([{ name: 'Berlin', address: 'relay1.example.com:21117' }, { address: '' }])
		});
		await userEvent.click(screen.getByRole('button', { name: 'Berlin (relay1.example.com:21117)' }));
		expect(onchange).toHaveBeenLastCalledWith(['relay1.example.com:21117']);
		expect(screen.queryByRole('button', { name: /Berlin/ })).toBeNull();
	});
});

describe('keep count', () => {
	test('a switch for all and a number only while it is off; no 0 code', async () => {
		const onchange = vi.fn();
		const { rerender } = render(KeepCount, {
			id: 'keep',
			all: false,
			count: 14,
			onchange,
			allLabel: 'Keep all backups',
			countLabel: 'Keep this many'
		});
		expect(screen.getByRole('spinbutton', { name: 'Keep this many' })).toHaveValue(14);
		await userEvent.click(screen.getByRole('checkbox', { name: 'Keep all backups' }));
		expect(onchange).toHaveBeenLastCalledWith({ all: true, count: 14 });
		await rerender({ all: true });
		expect(screen.queryByRole('spinbutton', { name: 'Keep this many' })).toBeNull();
	});
});

describe('an inherited value', () => {
	const control = createRawSnippet(() => ({
		render: () => '<input aria-label="Relay only" type="checkbox">'
	}));

	test('says what applies and where; setting it here and going back are buttons', async () => {
		const onoverride = vi.fn();
		const oninherit = vi.fn();
		const props = {
			id: 'iv',
			overridden: false,
			inherited: 'From the server: Direct first.',
			href: '/settings#connection',
			linkLabel: 'Settings, General',
			overrideLabel: 'Set for this organization',
			inheritLabel: "Use the server's value",
			onoverride,
			oninherit,
			children: control
		};
		const { rerender } = render(InheritedValue, props);
		expect(screen.getByText(/From the server: Direct first\./)).toBeInTheDocument();
		expect(screen.getByRole('link', { name: 'Settings, General' })).toHaveAttribute(
			'href',
			'/settings#connection'
		);
		expect(screen.queryByRole('checkbox', { name: 'Relay only' })).toBeNull();
		await userEvent.click(screen.getByRole('button', { name: 'Set for this organization' }));
		expect(onoverride).toHaveBeenCalledOnce();
		await rerender({ ...props, overridden: true });
		flushSync();
		expect(screen.getByRole('checkbox', { name: 'Relay only' })).toBeInTheDocument();
		await userEvent.click(screen.getByRole('button', { name: "Use the server's value" }));
		expect(oninherit).toHaveBeenCalledOnce();
	});
});

describe('the record picker', () => {
	test('one box per role with its sentence; a stored role that is gone stays visible until unticked', async () => {
		const onchange = vi.fn();
		const t = (k: string) =>
			({ 'users.role_operator': 'Operator', 'users.role_desc_operator': 'Connects to devices.' })[k] ?? k;
		render(RecordPicker, {
			id: 'ops',
			legend: 'Allowed operator roles',
			options: roleOptions([{ name: 'operator' }, { name: 'viewer' }], t),
			value: ['operator', 'helpdesk'],
			onchange
		});
		expect(screen.getByRole('checkbox', { name: 'Operator' })).toBeChecked();
		expect(screen.getByRole('checkbox', { name: 'Operator' })).toHaveAccessibleDescription(
			'Connects to devices.'
		);
		expect(screen.getByRole('checkbox', { name: 'viewer' })).not.toBeChecked();
		const gone = screen.getByRole('checkbox', { name: /helpdesk/ });
		expect(gone).toBeChecked();
		await userEvent.click(gone);
		expect(onchange).toHaveBeenLastCalledWith(['operator']);
	});
});
