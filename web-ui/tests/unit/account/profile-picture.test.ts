/**
 * Your account › Profile: upload a profile picture (the file goes to
 * PUT /api/users/me/avatar as it is when the browser can't shrink it) and
 * remove it. The avatar in the section, the account menu and the list of
 * pictures follow at once.
 */
import { cleanup, render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import { session, type User } from '../../../src/lib/state/session.svelte.ts';
import { avatarDirectory, resetAvatarDirectory } from '../../../src/lib/state/avatars.svelte.ts';
import Profile from '../../../src/lib/account/Profile.svelte';

const json = (body: unknown, status = 200) =>
	new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });

type Call = { method: string; path: string; body: unknown; type: string | null };
let calls: Call[];
let answer: (call: Call) => Response;

beforeEach(() => {
	calls = [];
	answer = () => json({ success: true, data: { avatar_version: null } });
	resetAvatarDirectory();
	vi.stubGlobal(
		'fetch',
		vi.fn((input: string, init: RequestInit = {}) => {
			const headers = (init.headers ?? {}) as Record<string, string>;
			const call = {
				method: init.method ?? 'GET',
				path: new URL(input, 'http://localhost').pathname,
				body: init.body,
				type: headers['Content-Type'] ?? null
			};
			calls.push(call);
			return Promise.resolve(answer(call));
		})
	);
});

afterEach(() => {
	cleanup();
	vi.unstubAllGlobals();
	session.user = null;
});

function signIn(avatarVersion: string | null) {
	session.user = { id: 7, username: 'hank', role: 'viewer', preferredLanguage: null, avatarVersion } as User;
}

test('uploading a picture sends the file and shows the new picture', async () => {
	signIn(null);
	answer = (c) =>
		c.method === 'PUT'
			? json({ success: true, data: { avatar_version: 'abc123' } })
			: json({ success: true });
	const user = userEvent.setup();
	const { container } = render(Profile);
	expect(container.querySelector('#profile .avatar img')).toBeNull();
	expect(screen.queryByRole('button', { name: 'account.picture_remove' })).toBeNull();

	const file = new File([new Uint8Array([0x89, 0x50, 0x4e, 0x47, 1, 2, 3])], 'me.png', { type: 'image/png' });
	await user.upload(container.querySelector<HTMLInputElement>('input[type="file"]')!, file);

	await vi.waitFor(() => expect(session.user?.avatarVersion).toBe('abc123'));
	const put = calls.find((c) => c.method === 'PUT')!;
	expect(put.path).toBe('/api/users/me/avatar');
	expect(put.body).toBe(file);
	expect(put.type).toBe('image/png');
	expect(container.querySelector('#profile .avatar img')).toHaveAttribute(
		'src',
		'/api/users/7/avatar?v=abc123'
	);
	expect(avatarDirectory.versions.get('7')).toBe('abc123');
	expect(screen.getByRole('status')).toHaveTextContent('account.picture_saved');
	expect(screen.getByRole('button', { name: 'account.picture_remove' })).toBeInTheDocument();
});

test('a refused file says why and keeps the old picture', async () => {
	signIn('old1');
	answer = () =>
		json(
			{ success: false, error: 'The data is not a PNG, JPEG, WebP or GIF image', code: 'not_an_image' },
			400
		);
	const user = userEvent.setup();
	const { container } = render(Profile);
	const file = new File(['<html></html>'], 'me.png', { type: 'image/png' });
	await user.upload(container.querySelector<HTMLInputElement>('input[type="file"]')!, file);

	await vi.waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('account.picture_not_image'));
	expect(session.user?.avatarVersion).toBe('old1');
	expect(container.querySelector('#profile .avatar img')).toHaveAttribute(
		'src',
		'/api/users/7/avatar?v=old1'
	);
});

test('removing the picture goes back to the initials', async () => {
	signIn('abc123');
	const user = userEvent.setup();
	const { container } = render(Profile);
	expect(container.querySelector('#profile .avatar img')).not.toBeNull();

	await user.click(screen.getByRole('button', { name: 'account.picture_remove' }));

	await vi.waitFor(() => expect(session.user?.avatarVersion).toBeNull());
	expect(calls.find((c) => c.method === 'DELETE')?.path).toBe('/api/users/me/avatar');
	expect(container.querySelector('#profile .avatar img')).toBeNull();
	expect(container.querySelector('#profile .avatar')?.textContent).toBe('HA');
	expect(screen.queryByRole('button', { name: 'account.picture_remove' })).toBeNull();
	expect(screen.getByRole('button', { name: 'account.picture_upload' })).toBeInTheDocument();
});
