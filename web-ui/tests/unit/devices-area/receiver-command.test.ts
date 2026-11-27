/**
 * The silent install of the receiver app: the properties and the command
 * the Receiver app tab shows, the same as install.cmd in the download.
 */
import { describe, expect, test } from 'vitest';
import {
	installCommand,
	msiValue,
	properties,
	tokenLooksRight
} from '../../../src/lib/devices-area/add/receiver-command.ts';

const values = {
	idServer: 'support.example.com:21116',
	key: 'abc+/=',
	api: 'https://support.example.com',
	token: '0123456789abcdef0123456789abcdef',
	group: 0
};

describe('the silent install of the receiver app', () => {
	test('names SERVER, KEY, API and TOKEN in that order, GROUP only with a folder', () => {
		expect(installCommand(values)).toBe(
			'msiexec /i neodesk-support.msi /qn SERVER=support.example.com:21116 KEY=abc+/= API=https://support.example.com TOKEN=0123456789abcdef0123456789abcdef'
		);
		expect(installCommand({ ...values, group: 12 })).toMatch(
			/ TOKEN=0123456789abcdef0123456789abcdef GROUP=12$/
		);
		expect(properties({ ...values, api: '' }).map((p) => p.name)).toEqual(['SERVER', 'KEY', 'TOKEN']);
	});

	test('quotes what msiexec would split, as install.cmd does', () => {
		expect(msiValue('plain')).toBe('plain');
		expect(msiValue('a b')).toBe('"a b"');
		expect(msiValue('say "hi"')).toBe('"say ""hi"""');
		expect(msiValue('')).toBe('""');
		expect(installCommand({ ...values, api: 'https://x/a b' })).toContain(' API="https://x/a b" ');
	});

	test('a token has the form the server takes', () => {
		expect(tokenLooksRight('0123456789abcdef0123456789abcdef')).toBe(true);
		expect(tokenLooksRight('  0123456789abcdef  ')).toBe(true);
		expect(tokenLooksRight('short')).toBe(false);
		expect(tokenLooksRight('0123456789abcdef 0123')).toBe(false);
	});
});
