/**
 * @vitest-environment-options {"url": "https://desk.example.com/join/ABCD2345"}
 *
 * The pages /join/<code> and /s/<code>: the main action for the visitor's
 * system, the warning for a console without HTTPS, and the one message for
 * a code that cannot be used. The page runs at an https address here, so
 * only the server's flag can make it warn.
 */
import { cleanup, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import type { PublicCode } from '../../../src/lib/api/onboard.ts';
import { detectComputer } from '../../../src/lib/public/detect.ts';
import { isInsecure, mainAction, otherDownloads, stepKeys } from '../../../src/lib/public/onboard.ts';

vi.mock('$app/paths', () => ({ resolve: (p: string) => p || '/', asset: (p: string) => '/' + p }));

const CodePage = (await import('../../../src/lib/public/CodePage.svelte')).default;

const WIN =
	'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130 Safari/537.36';
const WIN_ARM = 'Mozilla/5.0 (Windows NT 10.0; ARM64) AppleWebKit/537.36 Chrome/130';
const MAC = 'Mozilla/5.0 (Macintosh; Intel Mac OS X 14_5) AppleWebKit/605.1.15 Safari/605.1.15';
const LINUX = 'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 Chrome/130';
const PHONE = 'Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15';

function dl(os: 'windows' | 'macos' | 'linux', arch: string, format: string) {
	return {
		os,
		arch,
		format,
		version: '1.4.6',
		size: 1000,
		file: `NeoDesk-Setup-desk.example.com-ABCD2345.${format}`,
		url: `/api/onboard/public/ABCD2345/download?os=${os}&arch=${arch}&format=${format}`
	};
}

function code(over: Partial<PublicCode> = {}): PublicCode {
	return {
		kind: 'join',
		code: 'ABCD2345',
		display_code: 'ABCD-2345',
		expires_at: '2026-11-29T12:00:00Z',
		server: { name: 'Acme Desk', host: 'desk.example.com', https: true },
		publisher: 'Rath & Blume GbR',
		supporter: '',
		company: '',
		downloads: [
			dl('windows', 'x64', 'exe'),
			dl('windows', 'arm64', 'exe'),
			dl('macos', 'x64', 'dmg'),
			dl('macos', 'arm64', 'dmg'),
			dl('linux', 'x64', 'deb'),
			dl('linux', 'x64', 'rpm')
		],
		install_command: "curl -fsSL 'https://desk.example.com/join/ABCD2345/install.sh' | sudo sh",
		run_command: '',
		deep_link: 'neodesk://join/desk.example.com/ABCD2345',
		release_error: '',
		...over
	};
}

describe('the main action for this computer', () => {
	test('Windows gets the exe for its processor', () => {
		const main = mainAction(code(), detectComputer(WIN));
		expect(main.kind === 'download' && main.download.os + main.download.arch).toBe('windowsx64');
		const arm = mainAction(code(), detectComputer(WIN_ARM));
		expect(arm.kind === 'download' && arm.download.arch).toBe('arm64');
	});

	test('a Mac gets the disk image for Apple chips, the Intel one stays among the others', () => {
		const c = code();
		const main = mainAction(c, detectComputer(MAC));
		expect(main.kind === 'download' && main.download.os + main.download.arch).toBe('macosarm64');
		expect(otherDownloads(c, main).map((d) => d.os + d.arch + d.format)).toContain('macosx64dmg');
		expect(otherDownloads(c, main)).toHaveLength(5);
	});

	test('Linux gets the install command of an invite and the run command of a support link', () => {
		expect(mainAction(code(), detectComputer(LINUX))).toEqual({
			kind: 'command',
			command: "curl -fsSL 'https://desk.example.com/join/ABCD2345/install.sh' | sudo sh"
		});
		const support = code({
			kind: 'support',
			install_command: '',
			run_command: "curl -fsSL 'https://desk.example.com/s/ABCD2345/run.sh' | sh",
			downloads: [dl('linux', 'x64', 'tar.gz')]
		});
		expect(mainAction(support, detectComputer(LINUX))).toEqual({
			kind: 'command',
			command: support.run_command
		});
		// Without Linux packages the command could install nothing
		expect(mainAction(code({ downloads: [dl('windows', 'x64', 'exe')] }), detectComputer(LINUX))).toEqual({
			kind: 'none'
		});
	});

	test('a phone gets no main action', () => {
		expect(mainAction(code(), detectComputer(PHONE))).toEqual({ kind: 'none' });
	});

	test('plain HTTP is insecure, on the server or in the address bar', () => {
		expect(isInsecure(code(), 'https:')).toBe(false);
		expect(isInsecure(code(), 'http:')).toBe(true);
		expect(isInsecure(code({ server: { name: 'x', host: 'x', https: false } }), 'https:')).toBe(true);
	});

	test('the steps follow the kind and the system', () => {
		expect(stepKeys('join', 'windows')).toEqual([
			'onboard.public.join_steps_windows_1',
			'onboard.public.join_steps_windows_2',
			'onboard.public.join_steps_windows_3'
		]);
		expect(stepKeys('support', 'macos')).toHaveLength(4);
		expect(stepKeys('support', 'linux')).toEqual([]);
		expect(stepKeys('join', null)).toEqual([]);
	});
});

/* ── The page ── */

let answer: { status: number; body: unknown };
let ua: string;

beforeEach(() => {
	answer = { status: 200, body: { success: true, ...code() } };
	ua = WIN;
	vi.spyOn(navigator, 'userAgent', 'get').mockImplementation(() => ua);
	vi.stubGlobal(
		'fetch',
		vi.fn(
			async () =>
				new Response(JSON.stringify(answer.body), {
					status: answer.status,
					headers: { 'Content-Type': 'application/json' }
				})
		)
	);
});

afterEach(() => {
	cleanup();
	vi.unstubAllGlobals();
	vi.restoreAllMocks();
});

describe('the invite page', () => {
	test('one big download for Windows with the publisher line and the short code', async () => {
		render(CodePage, { code: 'ABCD2345', kind: 'join' });
		const button = await screen.findByRole('link', { name: 'onboard.public.download_join_windows' });
		expect(button.getAttribute('href')).toBe(
			'/api/onboard/public/ABCD2345/download?os=windows&arch=x64&format=exe'
		);
		expect(button.getAttribute('download')).toBe('NeoDesk-Setup-desk.example.com-ABCD2345.exe');
		expect(screen.getByText(/onboard.public.publisher/)).toBeInTheDocument();
		expect(screen.getByText('ABCD-2345')).toBeInTheDocument();
		expect(document.querySelector('.insecure')).toBeNull();
	});

	test('Linux shows the command instead of a download', async () => {
		ua = LINUX;
		render(CodePage, { code: 'ABCD2345', kind: 'join' });
		expect(await screen.findByText(/install\.sh' \| sudo sh/)).toBeInTheDocument();
		expect(screen.queryByRole('link', { name: /onboard\.public\.download_join/ })).toBeNull();
	});

	test('a console on plain HTTP warns', async () => {
		answer.body = { success: true, ...code({ server: { name: 'Acme', host: 'desk.lan', https: false } }) };
		render(CodePage, { code: 'ABCD2345', kind: 'join' });
		await waitFor(() => expect(document.querySelector('.insecure')).not.toBeNull());
	});

	test('a code that cannot be used shows one plain message', async () => {
		answer = { status: 404, body: { success: false, error: 'invalid_or_expired' } };
		render(CodePage, { code: 'ZZZZ2345', kind: 'join' });
		expect(await screen.findByText('onboard.public.gone_title')).toBeInTheDocument();
		expect(screen.getByText('onboard.public.gone_join')).toBeInTheDocument();
		expect(screen.queryByText('ABCD-2345')).toBeNull();
	});

	test('a support code under /join/ is not shown there', async () => {
		answer.body = { success: true, ...code({ kind: 'support' }) };
		render(CodePage, { code: 'ABCD2345', kind: 'join' });
		expect(await screen.findByText('onboard.public.gone_title')).toBeInTheDocument();
	});

	test('no release: say so and still show the code', async () => {
		answer.body = { success: true, ...code({ downloads: [], release_error: 'release_unavailable' }) };
		render(CodePage, { code: 'ABCD2345', kind: 'join' });
		expect(await screen.findByText('onboard.public.no_release')).toBeInTheDocument();
		expect(screen.getByText('ABCD-2345')).toBeInTheDocument();
	});
});
