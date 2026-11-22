import { render, screen } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, test } from 'vitest';
import { setStrings } from '../../../src/lib/i18n/i18n.svelte.ts';
import { clearSession, session } from '../../../src/lib/state/session.svelte.ts';
import ReadOnlyHint from '../../../src/lib/ui/ReadOnlyHint.svelte';

const SENTENCE = 'You can view this page but not change it.';

const access = {
	sections: { devices: 'read' } as Record<string, 'none' | 'read' | 'write'>,
	pages: [{ prefix: '/devices', section: 'devices' }],
	catalog: [{ key: 'devices', read: 'device.view', write: 'device.edit' }]
};

beforeEach(() => {
	setStrings('en', 'ltr', { common: { read_only_page: SENTENCE } });
	session.isSuperAdmin = false;
	session.access = null;
	session.path = '/devices';
});

afterEach(() => clearSession());

describe('ReadOnlyHint', () => {
	test('shows the sentence to an account that holds none of the page writes', () => {
		session.permissions = ['device.view'];
		render(ReadOnlyHint, { writes: ['device.edit', 'device.delete'] });
		expect(screen.getByText(SENTENCE)).toBeInTheDocument();
	});

	test('shows it when the section is at read, though the role holds the write', () => {
		session.permissions = ['device.view', 'device.edit'];
		session.access = access;
		render(ReadOnlyHint, { writes: ['device.edit'] });
		expect(screen.getByText(SENTENCE)).toBeInTheDocument();
	});

	test('a section that reads and writes with one permission: read access alone shows it', () => {
		session.permissions = ['audit.view'];
		session.access = {
			sections: { reports: 'read' },
			pages: [{ prefix: '/reports', section: 'reports' }],
			catalog: [{ key: 'reports', read: 'audit.view', write: 'audit.view' }]
		};
		session.path = '/reports';
		render(ReadOnlyHint, { writes: ['audit.view'] });
		expect(screen.getByText(SENTENCE)).toBeInTheDocument();
	});

	test('stays away while any write of the page is left', () => {
		session.permissions = ['device.view', 'device.edit'];
		render(ReadOnlyHint, { writes: ['device.edit', 'device.delete'] });
		expect(screen.queryByText(SENTENCE)).not.toBeInTheDocument();
	});

	test('stays away when the page says it has a write of its own', () => {
		session.permissions = ['user.view'];
		render(ReadOnlyHint, { writes: ['user.edit'], writable: true });
		expect(screen.queryByText(SENTENCE)).not.toBeInTheDocument();
	});

	test('stays away for a super admin, whatever the page passes', () => {
		session.permissions = [];
		session.isSuperAdmin = true;
		session.access = access;
		render(ReadOnlyHint, { writes: [] });
		expect(screen.queryByText(SENTENCE)).not.toBeInTheDocument();
	});
});
