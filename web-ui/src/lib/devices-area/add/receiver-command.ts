/**
 * The silent install of the NeoDesk app (the receiver) as the Receiver
 * app tab shows it: the MSI properties and the msiexec command. The same
 * rules as console/routes/receiverapp/msi.go, which writes install.cmd;
 * tested in tests/unit/devices-area/receiver-command.test.ts.
 *
 * Property contract with the client's MSI:
 *   SERVER  ID server, host[:port]
 *   KEY     the server's public key
 *   API     API address (https://SERVER when left out)
 *   TOKEN   enrollment token
 *   GROUP   folder id or name; the token's own folder wins
 */

export const MSI_NAME = 'neodesk-support.msi';

export interface ReceiverValues {
	idServer: string;
	key: string;
	api: string;
	token: string;
	/** A folder id; 0 for none */
	group: number;
}

/** A value as msiexec takes it: as it is without a space or quote, else quoted with quotes doubled */
export function msiValue(value: string): string {
	if (value !== '' && !/[\s"]/.test(value)) return value;
	return `"${value.replaceAll('"', '""')}"`;
}

/** The properties in the contract's order, empty ones left out */
export function properties(v: ReceiverValues): { name: string; value: string }[] {
	const list = [
		{ name: 'SERVER', value: v.idServer.trim() },
		{ name: 'KEY', value: v.key.trim() },
		{ name: 'API', value: v.api.trim() },
		{ name: 'TOKEN', value: v.token.trim() },
		{ name: 'GROUP', value: v.group > 0 ? String(v.group) : '' }
	];
	return list.filter((p) => p.value !== '');
}

/** msiexec /i neodesk-support.msi /qn SERVER=... KEY=... API=... TOKEN=... [GROUP=...] */
export function installCommand(v: ReceiverValues): string {
	return [
		'msiexec',
		'/i',
		MSI_NAME,
		'/qn',
		...properties(v).map((p) => `${p.name}=${msiValue(p.value)}`)
	].join(' ');
}

/** A token as the server takes one: 16 to 128 letters, digits, - and _ */
export function tokenLooksRight(token: string): boolean {
	return /^[A-Za-z0-9_-]{16,128}$/.test(token.trim());
}

/** The processors the release has MSIs for, as words: x86_64 is "64-bit (x64)", aarch64 "ARM64" */
export const ARCH_LABEL: Readonly<Record<string, string>> = {
	x86_64: 'receiver_app.arch_x64',
	aarch64: 'receiver_app.arch_arm64'
};
