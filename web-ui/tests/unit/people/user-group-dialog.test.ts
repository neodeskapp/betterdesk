/**
 * The user group dialog (PATTERN.md rules 5 and 6): it says what a group
 * does and where its members and access are set, and the name says where
 * it shows.
 */
import { cleanup, render, screen } from '@testing-library/svelte';
import { afterEach, expect, test, vi } from 'vitest';
import UserGroupDialog from '../../../src/routes/(console)/users/UserGroupDialog.svelte';

afterEach(cleanup);

test('the dialog says what a group does and where members and access are set', () => {
	render(UserGroupDialog, { group: null, onclose: vi.fn(), onsaved: vi.fn() });
	expect(
		screen.getByText(
			'Members of a group share its console access and the device groups that allow it. Add people in their user sheet; allow the group in a device group’s access settings.'
		)
	).toBeTruthy();
});

test('the name says where it shows', () => {
	render(UserGroupDialog, { group: null, onclose: vi.fn(), onsaved: vi.fn() });
	expect(screen.getByLabelText('users.group_name')).toHaveAccessibleDescription(
		'Shown in the user list, in each member’s user sheet and in access settings.'
	);
});
