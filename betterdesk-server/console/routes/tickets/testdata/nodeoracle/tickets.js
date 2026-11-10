'use strict';

/**
 * Records what routes/tickets.routes.js answers with the real database
 * adapter, on SQLite and (with ORACLE_PG_DSN) on PostgreSQL. See README.md.
 */

const fs = require('fs');
const path = require('path');

process.env.UPLOAD_RATE_LIMIT_MAX = '1000000';
process.env.FILE_ACCESS_RATE_LIMIT_MAX = '1000000';

const { record, dialect, WEB } = require('./lib');

const J = JSON.stringify;
const pg = dialect === 'postgres';
const yes = pg ? true : 1;
const no = pg ? false : 0;
const at = (minute) => `2001-01-01 00:${String(minute).padStart(2, '0')}:00${pg ? '+00' : ''}`;
const b64 = (s) => Buffer.from(s).toString('base64');

// The folder the route writes attachments to. The Go test puts its own
// folder where the recording says <UPLOADS>.
const UPLOADS = path.join(WEB, 'data', 'attachments');
fs.mkdirSync(UPLOADS, { recursive: true });
const before = new Set(fs.readdirSync(UPLOADS));

const TICKET = 'INSERT INTO tickets (title, description, status, priority, category, device_id, created_by, assigned_to, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)';
const COMMENT = 'INSERT INTO ticket_comments (ticket_id, author, body, is_internal, created_at) VALUES (?, ?, ?, ?, ?)';
const ATTACHMENT = 'INSERT INTO ticket_attachments (ticket_id, filename, mimetype, size_bytes, storage_path, uploaded_by, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)';
const seed = [
    [TICKET, ['Printer offline', 'The printer on floor 2', 'open', 'high', 'hardware', 'D1', 'e2e_admin', 'e2e_viewer', at(1), at(1)]],
    [TICKET, ['VPN drops 100%_ of calls', 'since monday', 'in_progress', 'medium', 'network', null, 'e2e_operator', 'e2e_operator', at(2), at(2)]],
    [TICKET, ['Laptop\\slow', 'has 100% cpu_load', 'resolved', 'low', 'software', 'D2', 'e2e_admin', 'e2e_viewer', at(3), at(3)]],
    [TICKET, ['Access request', '', 'closed', 'critical', 'access', 'D1', 'agent:D1', null, at(4), at(4)]],
    [TICKET, ['Other thing', null, 'open', 'medium', 'other', null, 'e2e_admin', null, at(5), at(5)]],
    [COMMENT, [1, 'e2e_admin', 'public one', no, at(10)]],
    [COMMENT, [1, 'e2e_operator', 'internal one', yes, at(11)]],
    [COMMENT, [1, 'e2e_viewer', 'public two', no, at(12)]],
    [COMMENT, [3, 'e2e_admin', 'only internal', yes, at(13)]],
    [ATTACHMENT, [1, 'missing.txt', 'text/plain', 5, '<UPLOADS>/1_feedface00000000_missing.txt', 'e2e_admin', at(20)]],
    [ATTACHMENT, [1, 'outside.txt', 'text/plain', 5, '/etc/hostname', 'e2e_admin', at(21)]],
    [ATTACHMENT, [1, 'up.txt', 'text/plain', 5, '<UPLOADS>/../../package.json', 'e2e_admin', at(22)]],
];

const steps = [];
let n = 0;
function step(method, url, body, role = 'admin', more = {}) {
    n += 1;
    const s = { name: `${n} ${method} ${url}`, role, method, path: url, ...more };
    if (body !== undefined) s.body = typeof body === 'string' && more.raw ? body : J(body);
    delete s.raw;
    steps.push(s);
}
const sql = (name, text, params) => steps.push({ name, sql: text, params });
const audit = () => sql('the audit log', 'SELECT user_id, action, details FROM audit_log ORDER BY id');

// New tickets get the time of the run. Lists are ordered by that time,
// so it is spread out by id before a list is read.
const spread = () => sql('spread the new tickets over time', pg
    ? "UPDATE tickets SET created_at = TIMESTAMPTZ '2001-06-01 00:00:00+00' + id * INTERVAL '1 minute' WHERE id > 5"
    : "UPDATE tickets SET created_at = datetime('2001-06-01 00:00:00', '+' || id || ' minutes') WHERE id > 5");

// Who may: every role that can sign in, and nobody. Writes of the roles
// that may not write change nothing.
const ROLES = ['admin', 'super_admin', 'global_admin', 'operator', 'viewer', 'guest_issuer', 'server_admin', ''];
for (const role of ROLES) step('POST', '/api/tickets', { title: `By ${role || 'nobody'}` }, role);
spread();
for (const role of ROLES) {
    step('GET', '/api/tickets', undefined, role);
    step('GET', '/api/tickets/stats', undefined, role);
    step('GET', '/api/tickets/1', undefined, role);
    step('GET', '/api/tickets/3', undefined, role);
    step('GET', '/api/tickets/1/comments', undefined, role);
    step('GET', '/api/tickets/1/attachments', undefined, role);
    step('GET', '/api/tickets/attachments/1', undefined, role);
    step('PATCH', '/api/tickets/999', { priority: 'low' }, role);
    step('DELETE', '/api/tickets/999', undefined, role);
    if (!['viewer', 'guest_issuer', 'server_admin'].includes(role)) {
        step('POST', '/api/tickets/999/comments', { body: 'c' }, role);
    }
    step('POST', '/api/tickets/999/attachments', { filename: 'r.txt', data: 'aGk=' }, role);
}

// Lists: every filter, the search with its wildcards, and parameters that
// are lists
const queries = ['status=open', 'status=closed', 'status=', 'status=nope', 'priority=high', 'category=network', 'assigned_to=e2e_viewer', 'assigned_to=nobody',
    'device_id=D1', 'status=open&priority=high&category=hardware&assigned_to=e2e_viewer&device_id=D1', 'status=open&device_id=D2', 'created_by=e2e_admin',
    'search=printer', 'search=PRINTER', 'search=floor', 'search=100%25', 'search=%25', 'search=_', 'search=%5C', 'search=100%25_', 'search=cpu_load', 'search=', 'search=+',
    'search=since+monday', 'search=nothing+like+it', 'status=open&search=thing', 'status=open&status=closed', 'status[]=open', 'status%5B%5D=open&priority[]=high',
    'status[0]=open', 'status[1]=open&status[0]=closed', 'status[x]=open', 'status[][]=open', 'search=a&search=b', 'search[]=printer', 'search[x]=1',
    'priority=high&priority=', 'device_id[]=D1&status=open', 'assigned_to[]=e2e_viewer', 'category=network&category[]=x', 'STATUS=open', 'status=Open',
    'status=%6Fpen', 'status=open+', 'limit=1&offset=1', ''];
for (const query of queries) step('GET', `/api/tickets?${query}`);
for (const query of ['', 'status=open', 'assigned_to=e2e_operator', 'search=vpn', 'status[]=open', 'status=open&status=closed']) step('GET', `/api/tickets?${query}`, undefined, 'viewer');

// Creating: every field with values of every type
let serial = 0;
const fresh = () => `Ticket ${++serial}`;
for (const title of ['T', '  spaced out  ', '', '   ', null, 0, 5, true, false, ['x'], [], { a: 1 }]) step('POST', '/api/tickets', { title });
step('POST', '/api/tickets', {});
step('POST', '/api/tickets', '[{"title":"In a list"}]', 'admin', { raw: true });
const values = ['text', '', 0, 5, 1.5, true, false, null, ['one'], ['a', 'b'], [], [7], {}, { a: 1 }];
for (const field of ['description', 'device_id', 'assigned_to']) {
    for (const value of values) step('POST', '/api/tickets', { title: fresh(), [field]: value });
}
for (const priority of ['low', 'medium', 'high', 'critical', 'urgent', 'High', '', 0, 1, null, true, ['high'], { toString: 'high' }]) {
    step('POST', '/api/tickets', { title: fresh(), priority });
}
for (const category of ['general', 'hardware', 'software', 'network', 'access', 'other', 'Hardware', 'misc', '', 0, 7, null, ['other']]) {
    step('POST', '/api/tickets', { title: fresh(), category });
}
step('POST', '/api/tickets', { title: fresh(), description: {}, device_id: ['a', 'b'] });
step('POST', '/api/tickets', { title: fresh(), description: 'full', priority: 'critical', category: 'access', device_id: 'D9', assigned_to: 'e2e_operator' }, 'operator');
step('GET', '/api/tickets/stats');
spread();
step('GET', '/api/tickets?search=Ticket');
audit();

// Reading by id
for (const id of ['1', '01', '0000000001', '0', '999', '2147483647', '2147483648', '99999999999999999999']) {
    step('GET', `/api/tickets/${id}`);
    step('GET', `/api/tickets/${id}/comments`);
    step('GET', `/api/tickets/${id}/attachments`);
    step('GET', `/api/tickets/attachments/${id}`);
}
for (const role of ['viewer', 'operator']) {
    step('GET', '/api/tickets/1', undefined, role);
    step('GET', '/api/tickets/3', undefined, role);
    step('GET', '/api/tickets/3/comments', undefined, role);
}

// Updating ticket 2. Its times are set back first, so that an answer
// shows which of them an update moves.
const read2 = () => step('GET', '/api/tickets/2');
sql('set the times of ticket 2 back', `UPDATE tickets SET updated_at = ${pg ? "TIMESTAMPTZ '2001-02-03 04:05:06+00'" : "'2001-02-03 04:05:06'"} WHERE id = 2`);
step('PATCH', '/api/tickets/2', {});
read2();
const updates = [{ title: '  Renamed  ' }, { title: '' }, { title: null }, { title: 5 }, { title: ['x'] }, { description: 'new text' }, { description: null }, { description: 5 },
    { description: ['a'] }, { description: ['a', 'b'] }, { description: {} }, { description: true }, { status: 'open' }, { status: 'in_progress' }, { status: 'resolved' },
    { status: 'closed' }, { status: 'done' }, { status: 'Open' }, { status: '' }, { status: 0 }, { status: null }, { status: false }, { status: ['open'] }, { status: 5 },
    { status: 'resolved', resolved_at: 'given' }, { status: 'closed', closed_at: 'given' }, { priority: 'critical' }, { priority: 'x' }, { priority: null }, { priority: '' },
    { priority: 0 }, { category: 'whatever you like' }, { category: 5 }, { category: null }, { category: true }, { assigned_to: 'e2e_viewer' }, { assigned_to: null },
    { assigned_to: 7 }, { assigned_to: '' }, { sla_due_at: '2030-01-02T03:04:05.000Z' }, { sla_due_at: 'not a date' }, { sla_due_at: null }, { sla_due_at: 5 },
    { sla_due_at: '' }, { device_id: '  D2 ' }, { device_id: '' }, { device_id: '   ' }, { device_id: null }, { device_id: 5 }, { device_id: true }, { device_id: ['D1'] },
    { device_id: 'd'.repeat(64) }, { device_id: 'd'.repeat(65) }, { device_id: ` ${'d'.repeat(64)} ` }, { device_id: '💻'.repeat(32) }, { device_id: '💻'.repeat(33) },
    { unknown: 1, id: 99, created_by: 'x', created_at: 'x' },
    { title: 'All at once', description: 'd', status: 'open', priority: 'low', category: 'general', assigned_to: 'e2e_operator', sla_due_at: '2031-05-06T07:08:09.000Z', device_id: 'D3' },
    { status: 'urgent', priority: 'urgent', device_id: 5 }, { priority: 'urgent', device_id: 5 }, { title: null, device_id: 5 }];
for (const body of updates) {
    step('PATCH', '/api/tickets/2', body);
    read2();
}
step('PATCH', '/api/tickets/2', '[{"title":"In a list"}]', 'admin', { raw: true });
step('PATCH', '/api/tickets/2', { status: 'resolved' }, 'operator');
read2();
step('PATCH', '/api/tickets/999', { status: 'nope' });
step('GET', '/api/tickets/stats');
audit();

// Comments on ticket 5
for (const body of ['c', '  trimmed  ', '', '   ', null, 0, 5, true, ['x'], {}]) step('POST', '/api/tickets/5/comments', { body });
for (const internal of [true, false, 1, 0, 'yes', '', 'false', null, [], {}]) step('POST', '/api/tickets/5/comments', { body: `internal ${J(internal)}`, is_internal: internal });
step('POST', '/api/tickets/5/comments', {});
step('POST', '/api/tickets/5/comments', { body: 'by the operator', is_internal: true }, 'operator');
sql('spread the comments over time', pg
    ? "UPDATE ticket_comments SET created_at = TIMESTAMPTZ '2001-03-01 00:00:00+00' + id * INTERVAL '1 minute' WHERE ticket_id = 5"
    : "UPDATE ticket_comments SET created_at = datetime('2001-03-01 00:00:00', '+' || id || ' minutes') WHERE ticket_id = 5");
sql('assign ticket 5 to the viewer', "UPDATE tickets SET assigned_to = 'e2e_viewer' WHERE id = 5");
for (const role of ['admin', 'viewer', 'guest_issuer']) {
    step('GET', '/api/tickets/5/comments', undefined, role);
    step('GET', '/api/tickets/5', undefined, role);
}

// Attachments on ticket 5
const attach = (body, role, more) => step('POST', '/api/tickets/5/attachments', body, role, more);
attach({ filename: 'hello.txt', data: b64('hello ticket'), mimetype: 'text/plain' });
for (const filename of ['a b/c\\d.txt', '../../etc/passwd', '..', '.', 're port📎.txt', 'über.txt', 'UPPER-lower_0.9', '', 0, 5, true, ['x'], {}, null, 'n'.repeat(300)]) {
    attach({ filename, data: b64(`named ${J(filename)}`) });
}
for (const mimetype of ['image/png', '', 0, 5, null, true, ['x/y'], {}, 'text/html\r\nX-Injected: 1', 'tëxt/plain', 'π/x', 'text/plain; charset=utf-8', ' ', 'x'.repeat(300)]) {
    attach({ filename: 'mime.bin', data: b64(`mime ${J(mimetype)}`), mimetype });
}
for (const data of ['aGVsbG8=', 'aGVsbG8', 'aGVs bG8=\n', 'a-_b', 'a+/b', 'aGk=aGk=', '=', '==', 'a', 'ab', 'abc', 'abcd', '!!!!', 'a!b@c#d$', 'YQ==YQ==', 'YQ=YQ', 'éééé',
    'AAAA', '////', '', 0, 5, true, null, [104, 105], [], [300, -1, '65', null, 1.9, [66], {}, true, 'x', 255, 256], {}, { type: 'Buffer', data: [1, 2, 3] },
    { type: 'Buffer', data: 'ab' }, { length: 2, 0: 65, 1: 66 }, { length: '2', 0: 65 }, { length: -1 }, { length: 1.5, 0: 7 }, { length: 0 }, { data: [1] }]) {
    attach({ filename: 'data.bin', data });
}
attach({ data: 'aGk=' });
attach({ filename: 'only-a-name.txt' });
attach({});
attach('[{"filename":"a.txt","data":"aGk="}]', 'admin', { raw: true });
attach({ filename: 'by-the-operator.txt', data: b64('operator') }, 'operator');
sql('spread the attachments over time', pg
    ? "UPDATE ticket_attachments SET created_at = TIMESTAMPTZ '2001-04-01 00:00:00+00' + id * INTERVAL '1 minute' WHERE ticket_id = 5"
    : "UPDATE ticket_attachments SET created_at = datetime('2001-04-01 00:00:00', '+' || id || ' minutes') WHERE ticket_id = 5");
step('GET', '/api/tickets/5/attachments');
step('GET', '/api/tickets/5/attachments', undefined, 'viewer');
step('GET', '/api/tickets/5');
for (let id = 1; id <= 70; id += 1) step('GET', `/api/tickets/attachments/${id}`, undefined, 'admin', { download: true });
step('GET', '/api/tickets/attachments/4', undefined, 'viewer', { download: true });
step('HEAD', '/api/tickets/attachments/4', undefined, 'admin', { download: true });
for (const range of ['bytes=0-4', 'bytes=6-', 'bytes=-6', 'bytes=50-60', 'bytes=0-0,2-3', 'items=0-1', 'bytes=abc']) {
    step('GET', '/api/tickets/attachments/4', undefined, 'admin', { download: true, headers: { Range: range } });
}
step('GET', '/api/tickets/attachments/4', undefined, 'admin', { download: true, headers: { 'If-None-Match': '*' } });
step('GET', '/api/tickets/attachments/4', undefined, 'admin', { download: true, headers: { 'If-Modified-Since': 'Fri, 01 Jan 2100 00:00:00 GMT' } });
step('GET', '/api/tickets/attachments/4', undefined, 'admin', { download: true, headers: { 'If-Match': '"nope"' } });

// Deleting takes comments, attachments and their files along
step('DELETE', '/api/tickets/5', undefined, 'operator');
step('DELETE', '/api/tickets/5');
step('GET', '/api/tickets/5');
step('GET', '/api/tickets/5/comments');
step('GET', '/api/tickets/5/attachments');
step('GET', '/api/tickets/attachments/4', undefined, 'admin', { download: true });
step('DELETE', '/api/tickets/1');
step('GET', '/api/tickets/1/comments');
step('DELETE', '/api/tickets/0000000002');
step('DELETE', '/api/tickets/99999999999999999999');
step('GET', '/api/tickets/stats');
audit();
sql('what is left', 'SELECT id, title FROM tickets ORDER BY id');
sql('comments left', 'SELECT id, ticket_id FROM ticket_comments ORDER BY id');
sql('attachments left', 'SELECT id, ticket_id, filename, size_bytes FROM ticket_attachments ORDER BY id');

const KEPT = ['content-type', 'content-disposition', 'cache-control', 'accept-ranges', 'content-range', 'content-length'];
record({
    routes: 'routes/tickets.routes',
    mounts: ['/api/tickets'],
    seed,
    steps,
    out: 'node',
    vars: { '<UPLOADS>': UPLOADS },
    keep(step, answer) {
        if (!step.download) return null;
        const headers = {};
        for (const name of KEPT) if (answer.headers[name] !== undefined) headers[name] = answer.headers[name];
        return { base64: answer.body.toString('base64'), headers, etag: answer.headers.etag !== undefined, lastModified: answer.headers['last-modified'] !== undefined };
    },
    finish() {
        // Files of this run that no delete took along: uploads whose row could not be written
        const left = fs.readdirSync(UPLOADS).filter((name) => !before.has(name));
        for (const name of left) fs.unlinkSync(path.join(UPLOADS, name));
        return [{ name: 'files left behind', files: left.map((name) => name.replace(/^(\d+)_[0-9a-f]{16}_/, '$1_<random>_')).sort() }];
    },
}).catch((err) => {
    console.error(err);
    process.exit(1);
});
