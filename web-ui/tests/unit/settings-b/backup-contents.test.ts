/**
 * Server › Backups: "Create a backup" and its warning name what the
 * server's contents answer says a backup holds, so they cannot disagree
 * with "In a backup now". On Docker the text said the file holds the
 * environment file while the panel said it does not (BUGS L557).
 */
import { describe, expect, test } from 'vitest';
import * as v from 'valibot';

import { BackupStats } from '../../../src/lib/api/settings-system.ts';
import { createNote, partState, securityWarning } from '../../../src/lib/settings-b/backup.ts';

/** Keys as <key>, parameters filled in: the text built from the parts is visible */
const t = (key: string, params?: Record<string, string | number>) => {
	let out = `<${key}>`;
	for (const [name, value] of Object.entries(params ?? {})) out += `[${name}=${value}]`;
	return out;
};

/** The server's answer on a Docker install (shape of GET /api/settings/backup/stats) */
const docker = v.parse(BackupStats, {
	users: 1,
	settings: 3,
	backend: 'neodesk',
	dbType: 'sqlite',
	components: { env: false, goPrivKey: true, goApiKey: true, uploads: 2 },
	parts: [
		{ id: 'database', included: true, files: 1, secret: true },
		{ id: 'server_data', included: true, files: 1, secret: false },
		{ id: 'database_file', included: true, files: 2, secret: true },
		{ id: 'env', included: false, reason: 'docker', files: 0, secret: true },
		{ id: 'session_secret', included: true, files: 1, secret: true },
		{ id: 'server_keys', included: true, files: 3, secret: true },
		{ id: 'mesh_cert', included: false, reason: 'missing', files: 0, secret: true },
		{ id: 'uploads', included: true, files: 2, secret: false },
		{ id: 'chat_files', included: false, reason: 'empty', files: 0, secret: false },
		{ id: 'attachments', included: false, reason: 'empty', files: 0, secret: false },
		{ id: 'fonts', included: false, reason: 'empty', files: 0, secret: false },
		{ id: 'public_endpoints', included: false, reason: 'missing', files: 0, secret: false },
		{ id: 'tls', included: false, reason: 'not_configured', files: 0, secret: true }
	]
});

const part = (id: string) => `<settings.backup_tab.part_${id}>`;
const secret = (id: string) => `<settings.backup_tab.secret_${id}>`;

describe('backup contents', () => {
	test('the description names the included parts and nothing else', () => {
		const note = createNote(docker, t, 'en');
		expect(note).toBe(
			`<settings.backup_tab.create_note_parts>[parts=${[
				part('database'),
				part('server_data'),
				part('database_file'),
				part('session_secret'),
				part('server_keys')
			].join(', ')}, and ${part('uploads')}]`
		);
		expect(note).not.toContain(part('env'));
	});

	test('the warning names the included secrets, not the environment file on Docker', () => {
		const warning = securityWarning(docker, t, 'en');
		expect(warning).toContain('<settings.backup_tab.warning_parts>');
		for (const id of ['database', 'database_file', 'session_secret', 'server_keys']) {
			expect(warning).toContain(secret(id));
		}
		expect(warning).not.toContain(secret('env'));
		expect(warning).not.toContain(secret('tls'));
		expect(warning).not.toContain(part('uploads'));
	});

	test('a native install with the environment file names it in both', () => {
		const native = {
			...docker,
			parts: docker.parts.map((p) => (p.id === 'env' ? { ...p, included: true, reason: '', files: 1 } : p))
		};
		expect(createNote(native, t, 'en')).toContain(part('env'));
		expect(securityWarning(native, t, 'en')).toContain(secret('env'));
	});

	test('before the answer is in, neither text claims any part', () => {
		expect(createNote(null, t)).toBe('<settings.backup_tab.create_note_plain>');
		expect(securityWarning(null, t)).toBe('<settings.backup_tab.warning_plain>');
	});

	test('the panel says why a part is left out', () => {
		expect(partState(docker, 'env', t)).toEqual({
			included: false,
			reason: '<settings.backup_tab.reason_docker>'
		});
		expect(partState(docker, 'server_keys', t)).toEqual({ included: true, reason: '' });
		expect(partState(docker, 'no_such_part', t)).toBeUndefined();
		expect(partState(null, 'env', t)).toBeUndefined();
	});
});
