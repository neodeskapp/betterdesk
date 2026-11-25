/**
 * What the pages /join/<code> and /s/<code> offer the visitor's computer:
 * one main action (a download, or a command to paste on Linux), the steps
 * that go with it, and the other downloads folded away below.
 *
 * - Windows: the exe for the processor (x64 when it can't tell).
 * - macOS: the disk image for Apple chips first; browsers on a Mac do not
 *   tell the processor, so the Intel one is in the other downloads.
 * - Linux, invite: the install command (it picks deb or rpm itself).
 * - Linux, quick support: the run command (portable package, no root).
 * - A phone, a tablet or an unknown system: no main action, every
 *   download listed.
 */

import type { PublicCode, PublicDownload } from '#lib/api/onboard.ts';
import type { ThisComputer } from './detect.ts';

export type MainAction =
	{ kind: 'download'; download: PublicDownload } | { kind: 'command'; command: string } | { kind: 'none' };

/** The main action for this computer */
export function mainAction(code: PublicCode, mine: ThisComputer | null): MainAction {
	if (!mine) return { kind: 'none' };
	if (mine.platform === 'linux') {
		const command = code.kind === 'join' ? code.install_command : code.run_command;
		// Without packages there is nothing the command could install
		if (command && code.downloads.some((d) => d.os === 'linux')) return { kind: 'command', command };
		return { kind: 'none' };
	}
	const forOs = code.downloads.filter((d) => d.os === mine.platform);
	const pick =
		mine.platform === 'macos'
			? (forOs.find((d) => d.arch === 'arm64') ?? forOs[0])
			: (forOs.find((d) => d.arch === (mine.arch ?? 'x64')) ?? forOs.find((d) => d.arch === 'x64'));
	return pick ? { kind: 'download', download: pick } : { kind: 'none' };
}

/** The downloads that are not the main action */
export function otherDownloads(code: PublicCode, main: MainAction): PublicDownload[] {
	return code.downloads.filter((d) => !(main.kind === 'download' && main.download.url === d.url));
}

/** The console is reached over plain HTTP: the page and the client warn */
export function isInsecure(code: PublicCode, protocol: string): boolean {
	return !code.server.https || protocol === 'http:';
}

/**
 * The language keys of the steps for the kind and system (below
 * onboard.public): three on Windows, four on a Mac. Linux has its command
 * with one line, and a phone gets no steps. format is the download's: the
 * Windows exe's permission prompt shows Windows Command Processor by
 * Microsoft Windows, only the MSI's names the publisher, so an MSI gets
 * the step that says to check for it.
 */
export function stepKeys(
	kind: PublicCode['kind'],
	platform: ThisComputer['platform'] | null,
	format?: string
): string[] {
	const count = platform === 'windows' ? 3 : platform === 'macos' ? 4 : 0;
	const prefix = kind === 'join' ? 'onboard.public.join_steps_' : 'onboard.public.support_steps_';
	return Array.from({ length: count }, (_, i) => {
		if (kind === 'join' && platform === 'windows' && format === 'msi' && i === 2)
			return `${prefix}windows_msi_3`;
		return `${prefix}${platform}_${i + 1}`;
	});
}

/**
 * The three steps of an invite, as the welcome mail has them: download and
 * open NeoDesk, click Install, done.
 */
export const WELCOME_STEPS = [
	'onboard.public.welcome_step_1',
	'onboard.public.welcome_step_2',
	'onboard.public.welcome_step_3'
] as const;

/** The label of the main download button */
export function downloadLabel(kind: PublicCode['kind'], platform: ThisComputer['platform'] | null): string {
	if (kind === 'support') return 'onboard.public.download_support';
	if (platform === 'windows') return 'onboard.public.download_join_windows';
	if (platform === 'macos') return 'onboard.public.download_join_macos';
	return 'onboard.public.download_join';
}

/** A mailto: link that sends this page's address to oneself */
export function mailMyself(subject: string, url: string): string {
	return `mailto:?subject=${encodeURIComponent(subject)}&body=${encodeURIComponent(url)}`;
}

/** "Windows x64", "macOS Apple chip", "Linux arm64 (rpm)" */
export function downloadTitle(d: PublicDownload, words: { apple: string; intel: string }): string {
	if (d.os === 'macos') return `macOS ${d.arch === 'arm64' ? words.apple : words.intel}`;
	const os = d.os === 'windows' ? 'Windows' : 'Linux';
	return d.os === 'linux' ? `${os} ${d.arch} (${d.format})` : `${os} ${d.arch}`;
}
