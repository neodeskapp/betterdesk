'use strict';

/**
 * Records what routes/phase4_5.routes.js answers with the real database
 * adapter, the real avatar service and the real release cache, on SQLite
 * and (with ORACLE_PG_DSN) on PostgreSQL. See README.md.
 *
 * The release cache is written by releases.json (beside this file) into
 * the scratch data folder; the Go test writes the same files.
 */

const fs = require('fs');
const path = require('path');
const { record, dialect } = require('./lib');

const J = JSON.stringify;
const pg = dialect === 'postgres';
const HOST = { Host: 'console.test' };

const seed = [
    ['UPDATE users SET first_name = ?, last_name = ?, email = ?, phone = ?, role_display = ?, avatar_url = ? WHERE id = ?',
        ['Grace', 'Hopper', 'grace@example.test', '+1 555', 'Rear admiral', '', 1]],
    ['UPDATE users SET first_name = ?, last_name = ?, email = ?, phone = ?, role_display = ?, avatar_url = ? WHERE id = ?',
        ['', 'Only', '', '', '', 'https://pictures.example.test/a.png', 2]],
    ['UPDATE users SET first_name = NULL, last_name = NULL, email = NULL, phone = NULL, role_display = NULL, avatar_url = NULL WHERE id = ?', [4]],
];
// Texts that look like numbers, "0" among them, which is no empty field
seed.push(['UPDATE users SET first_name = ?, last_name = ?, phone = ? WHERE id = ?', ['42', '1.5', '0', 5]]);

const steps = [];
let n = 0;
function step(method, p, body, role = 'admin', raw = false) {
    n += 1;
    const s = { name: `${n} ${method} ${p}`, role, method, path: p, headers: HOST };
    if (body !== undefined) s.body = raw ? body : J(body);
    steps.push(s);
}

// The profile and the consent popup's data
for (const role of ['admin', 'super_admin', 'global_admin', 'operator', '']) {
    step('GET', '/api/users/me/profile', undefined, role);
    step('GET', '/api/bd/operator-info', undefined, role);
}
step('GET', '/api/bd/operator-info?session_id=abc', undefined, 'admin');
step('GET', `/api/bd/operator-info?session_id=${'s'.repeat(200)}`, undefined, 'admin');
step('GET', '/api/bd/operator-info?session_id=a&session_id=b', undefined, 'admin');
step('GET', '/api/bd/operator-info?session_id[x]=1', undefined, 'admin');
step('GET', '/api/bd/operator-info?session_id[toString]=1', undefined, '');
step('GET', '/api/bd/operator-info?session_id=', undefined, '');

const toString = { toString: 1 };
const saves = [
    {},
    { first_name: 'Ada', last_name: 'Lovelace', email: 'ada@example.test', phone: '1', role_display: 'Eng', avatar_url: 'not-an-address' },
    { first_name: 'Ada', last_name: 'Lovelace', email: 'ada@example.test', phone: '1', role_display: 'Eng', avatar_url: 'not-an-address' },
    { phone: '555' },
    { first_name: 42, last_name: true, email: 1e21, phone: null, role_display: ['a', 1, null], avatar_url: '' },
    { first_name: { a: 1 }, role_display: [] },
    { first_name: 'x'.repeat(250), last_name: `${'ü'.repeat(199)}😀`, phone: '  padded  ', avatar_url: `  ${'u'.repeat(210)}  ` },
    { email: 'e'.repeat(201) },
    { email: 'e'.repeat(200) },
    { email: '😀'.repeat(101) },
    { email: 123456 },
    { email: toString },
    { last_name: toString },
    { avatar_url: toString },
    { email: '', avatar_url: null },
    { avatar_url: 'ftp://example.test/a.png' },
    { avatar_url: 'ftp://example.test/a.png' },
    { avatar_url: '' },
];
for (const role of ['admin', 'super_admin', 'global_admin']) {
    for (const body of saves) {
        step('PUT', '/api/users/me/profile', body, role);
        step('GET', '/api/users/me/profile', undefined, role);
        step('GET', '/api/bd/operator-info', undefined, role);
    }
}
step('PUT', '/api/users/me/profile', '["first_name"]', 'admin', true);
step('GET', '/api/users/me/profile', undefined, 'admin');
step('PUT', '/api/users/me/profile', { first_name: 'x' }, '');
steps.push({ name: 'audit', sql: 'SELECT action, details, user_id FROM audit_log ORDER BY id', params: [] });
steps.push({ name: 'pictures', sql: 'SELECT user_id, source FROM user_avatars ORDER BY user_id', params: [] });

// Agent templates and enrollment
step('GET', '/api/agent-templates');
for (const body of [
    {}, { name: '' }, { name: ' \t ' }, { name: 'Kiosk', description: 'front desk', config: { tags: ['k'] } }, { name: 7 },
    { name: 0 }, { name: ['a', 'b'] }, { name: toString }, { name: 'x', description: toString }, { name: 'x'.repeat(150) },
    { name: null }, { name: false },
]) {
    step('POST', '/api/agent-templates', body);
}
step('POST', '/api/agent-templates', '[1,2]', 'admin', true);
for (const id of ['1', '0', '-1', 'abc', '12abc', '0x10', '99999999999999999999999', '%205', '0.5', '1e3', '%2B7', 'Infinity']) {
    step('DELETE', `/api/agent-templates/${id}`);
}
for (const body of [
    {}, { enrollment_token: 't' }, { device_id: 'd' }, { enrollment_token: 't', device_id: 'd' }, { enrollment_token: 1, device_id: 2 },
    { enrollment_token: 0, device_id: 'd' }, { enrollment_token: toString, device_id: 'd' }, { enrollment_token: 't', device_id: toString },
    { enrollment_token: 'x'.repeat(100), device_id: 'y'.repeat(100) }, { enrollment_token: ['a'], device_id: [[]] },
]) {
    step('POST', '/api/bd/enroll', body, '');
}

// Releases in the cache, their downloads and the portal
const releases = JSON.parse(fs.readFileSync(path.join(__dirname, 'releases.json'), 'utf8'));
step('GET', '/api/agent-releases');
step('GET', '/api/agent-releases/signing-status');
step('GET', '/api/portal/installers', undefined, '');
for (const p of [
    '/downloads/generic-agent/support-v1.2.0/agent-windows.zip',
    '/downloads/generic-agent/support-v1.2.0/agent%2Dlinux.tar.gz',
    '/downloads/generic-agent/support-v1.2.0/missing.zip',
    '/downloads/generic-agent/support-v1.2.0/..',
    '/downloads/generic-agent/support-v1.2.0/.',
    '/downloads/generic-agent/./top.txt',
    '/downloads/generic-agent/support-v1.2.0/manifest.json',
    '/downloads/generic-agent/support-v1.2.0/a%20b.zip',
    '/downloads/generic-agent/support-v1.2.0/sub',
    '/downloads/generic-agent/no-manifest/file.bin',
]) {
    step('GET', p, undefined, '');
}

record({
    routes: 'routes/phase4_5.routes',
    mounts: ['/'],
    seed,
    steps,
    out: 'node',
    prepare: async ({ scratch, exec }) => {
        if (pg) {
            // The profile columns, as the Go console's schema has them on every database
            for (const column of ['first_name', 'last_name', 'email', 'phone', 'role_display', 'avatar_url']) {
                await exec(`ALTER TABLE users ADD COLUMN IF NOT EXISTS ${column} TEXT DEFAULT ''`);
            }
        }
        const root = path.join(scratch, 'modules', 'betterdesk-support-releases');
        for (const [rel, content] of Object.entries(releases.files)) {
            const file = path.join(root, rel);
            fs.mkdirSync(path.dirname(file), { recursive: true });
            fs.writeFileSync(file, content);
        }
        for (const dir of releases.dirs) fs.mkdirSync(path.join(root, dir), { recursive: true });
    },
    keep: (s, answer) => (s.path.startsWith('/downloads/') ? {
        headers: J({
            'content-type': answer.headers['content-type'] || null,
            'content-disposition': answer.headers['content-disposition'] || null,
            'accept-ranges': answer.headers['accept-ranges'] || null,
        }),
    } : null),
});
