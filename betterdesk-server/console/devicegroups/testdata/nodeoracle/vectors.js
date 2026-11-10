'use strict';

/**
 * Runs services/deviceGroupService.js (the device group payload helpers),
 * services/tagManagementService.js and services/rustdeskAddressBookSync.js
 * of the Node console (web-nodejs) on generated input and records every
 * answer, for the Go package console/devicegroups to replay. See README.md
 * in this folder.
 *
 *   node vectors.js      writes ../payload.json.gz, ../tags.json.gz,
 *                        ../addressbook.json.gz and ../counts.json
 */

const fs = require('fs');
const os = require('os');
const path = require('path');
const zlib = require('zlib');

const WEB = process.env.WEB_NODEJS || '/repo/web-nodejs';
const OUT = path.join(__dirname, '..');
process.env.NODE_ENV = 'test';
process.env.DATA_DIR = fs.mkdtempSync(path.join(os.tmpdir(), 'devicegroups-oracle-'));
process.chdir(WEB);

const groups = require(path.join(WEB, 'services/deviceGroupService'));
const tagService = require(path.join(WEB, 'services/tagManagementService'));
const ab = require(path.join(WEB, 'services/rustdeskAddressBookSync'));

function rng(seed) {
    let a = seed >>> 0;
    const next = () => {
        a = (a + 0x6D2B79F5) >>> 0;
        let t = a;
        t = Math.imul(t ^ (t >>> 15), t | 1);
        t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
        return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
    };
    return { next, int: (n) => Math.floor(next() * n), pick: (l) => l[Math.floor(next() * l.length)], chance: (p) => next() < p };
}

const clone = (v) => (v === undefined ? undefined : JSON.parse(JSON.stringify(v)));

// Values a JSON body can hold where text is expected
const ODD = [null, true, false, 0, 1, -1, 2.5, 1e21, 1e-7, 123456789012345680000, 0.000001, '', ' ', 'x', ' padded ', '0', 'null',
    [], ['a'], ['a', null, 'b'], [[1, 2], 3], {}, { guid: 'g1' }, { a: 1 }, 'ä'.repeat(90), 'a'.repeat(600), '\u{1F600}'.repeat(40),
    'a'.repeat(49) + '\u{1F600}', 'a'.repeat(79) + '\u{1F600}b', ' nbsp ', 'line\nbreak', 'tab\there', 'ctl\u0001\u0008\u000b\u001fend'];
const TAGS = ['lab', 'Lab', 'LAB', ' office ', 'Büro', 'BÜRO', 'İstanbul', 'istanbul', 'i̇stanbul', 'ΟΔΟΣ', 'οδος', 'οδοσ', 'ΣΑΣ', 'σας', 'x'.repeat(60),
    'a,b', 'a b', '', ' ', 'tag\u0001x', '\ttabbed\t', 'ß', 'SS', 'ǅ', 'ǆ', 'K', 'k', '٣', '3', 'emoji\u{1F600}', 'ｆｕｌｌ', 'Z', 'a', 'A', 'é', 'e', 'E', 'z'];

function payloadVectors() {
    const r = rng(11);
    const list = [];
    const add = (body) => {
        const input = clone(body);
        const payload = groups.normalizeGroupPayload(body);
        list.push({
            body: input, payload,
            hasNote: Object.prototype.hasOwnProperty.call(payload, 'note'), hasTeam: Object.prototype.hasOwnProperty.call(payload, 'team_id'),
            create: groups.buildDeviceGroupCreateFields(payload), update: groups.buildDeviceGroupUpdateFields(payload),
        });
    };
    add({});
    add({ name: 'Lab', source_type: 'tag', tag_filter: 'lab' });
    add({ name: 'Lab', dynamic: true, tag: 'lab' });
    add({ name: 'Lab', dynamic: 'true', tag: 'lab' });
    add({ name: 'Lab', dynamic: 1, tag_filter: 'lab' });
    add({ name: 'Lab', source_type: 'TAG', tag_filter: 'lab' });
    add({ name: 'Lab', source_type: 'manual', tag_filter: 'lab', tag: 'x' });
    add({ name: 'Lab', note: null, team_id: null });
    add({ name: 'Lab', note: 'a note', team_id: 'team-1', guid: ' g-1 ' });
    add({ name: 'Lab', allowed_users: 'a, b ,a,,c', allowed_groups: 'g1,g2' });
    add({ name: 'Lab', allowed_groups: [], allowed_user_groups: ['g3'], user_group_guids: ['g4'] });
    add({ name: 'Lab', allowed_groups: '', allowed_user_groups: [{ guid: 'g3', name: 'x' }, { name: 'none' }, 'g5', null, 7] });
    add({ name: 'Lab', user_group_guids: 'g6, g7' });
    add({ name: 'Lab', allowed_users: Array.from({ length: 130 }, (_, i) => `user${i}`) });
    const keys = ['guid', 'name', 'source_type', 'dynamic', 'tag_filter', 'tag', 'allowed_users', 'allowed_groups', 'allowed_user_groups', 'user_group_guids', 'note', 'team_id'];
    for (const key of keys) for (const value of ODD) add({ name: 'Group', source_type: 'tag', [key]: value });
    for (let i = 0; i < 1500; i++) {
        const body = {};
        for (const key of keys) {
            if (r.chance(0.45)) body[key] = r.chance(0.3) ? r.pick(['tag', 'manual', true, 'lab', 'Lab group']) : r.pick(ODD);
        }
        add(body);
    }
    return list;
}

async function tagVectors() {
    const r = rng(12);
    const out = { sanitize: [], equal: [], has: [], rename: [], remove: [], runs: [] };
    for (const value of [...TAGS, ...ODD]) out.sanitize.push({ value, out: tagService.sanitizeTagName(value) });
    for (const a of TAGS) for (const b of TAGS) out.equal.push({ a, b, out: tagService.tagsEqual(a, b) });
    const tagValue = () => {
        const kind = r.int(6);
        const some = Array.from({ length: r.int(6) }, () => r.pick(TAGS));
        if (kind === 0) return some;
        if (kind === 1) return some.join(',');
        if (kind === 2) return JSON.stringify(some);
        if (kind === 3) return r.pick(ODD);
        if (kind === 4) return [...some, r.pick(ODD), ...some];
        return Array.from({ length: 25 }, (_, i) => `t${i}`).concat(some);
    };
    for (let i = 0; i < 800; i++) {
        const tags = tagValue();
        const from = r.chance(0.1) ? r.pick(ODD) : r.pick(TAGS);
        const to = r.chance(0.1) ? r.pick(ODD) : r.pick(TAGS);
        out.has.push({ device: { id: 'd', tags: clone(tags) }, tag: from, out: tagService.deviceHasTag({ id: 'd', tags }, from) });
        const renamed = tagService.rewriteTagsRename(clone(tags), from, to);
        out.rename.push({ tags: clone(tags), from, to, out: Array.isArray(renamed) ? { tags: renamed, changed: false } : renamed });
        out.remove.push({ tags: clone(tags), tag: from, out: tagService.rewriteTagsDelete(clone(tags), from) });
    }
    out.has.push({ device: null, tag: 'lab', out: tagService.deviceHasTag(null, 'lab') });
    // Whole renames and deletes over a set of devices and groups
    for (let i = 0; i < 300; i++) {
        const devices = Array.from({ length: r.int(9) }, (_, n) => ({ id: r.chance(0.1) ? n : `dev-${n}`, tags: tagValue() }));
        const groupList = Array.from({ length: r.int(5) }, (_, n) => ({
            guid: `g-${n}`, source_type: r.pick(['tag', 'manual', '', undefined]), tag_filter: r.chance(0.2) ? ` ${r.pick(TAGS)} ` : r.pick(TAGS),
        }));
        const from = r.chance(0.05) ? r.pick(ODD) : r.pick(TAGS);
        const to = r.chance(0.05) ? r.pick(ODD) : r.pick(TAGS);
        const answers = Array.from({ length: 12 }, () => r.pick([{ success: true }, { success: true }, { success: false }, null, {}, { success: 0 }]));
        for (const kind of ['rename', 'delete']) {
            const d = clone(devices);
            const g = clone(groupList);
            const calls = [];
            const updates = [];
            let n = 0;
            const setPeerTags = async (id, tags) => { calls.push({ id, tags: clone(tags) }); return answers[n++ % answers.length]; };
            const updateDeviceGroup = async (guid, data) => { updates.push({ guid, data: clone(data) }); };
            const result = kind === 'rename'
                ? await tagService.renameTagAcrossScope({ devices: d, groups: g, fromTag: from, toTag: to, setPeerTags, updateDeviceGroup })
                : await tagService.deleteTagAcrossScope({ devices: d, groups: g, tag: from, setPeerTags });
            out.runs.push({
                kind, devices: clone(devices), groups: clone(groupList), from, to, answers: answers.map((a) => !(!a || a.success === false)),
                result, calls, updates, devicesAfter: d.map((x) => x.tags), filtersAfter: g.map((x) => x.tag_filter),
            });
        }
    }
    return out;
}

function addressBookVectors() {
    const r = rng(13);
    const out = { normalize: [], parse: [], merge: [], filter: [], visible: [], updates: [] };
    const tagValue = () => {
        const some = Array.from({ length: r.int(5) }, () => r.pick(TAGS));
        return r.pick([some, some.join(','), JSON.stringify(some), ` ${JSON.stringify(some)} `, '[broken, json', '[1, {"a": 2}, [3, 4], null, "x"]', '{"a": 1}', r.pick(ODD), [...some, r.pick(ODD)]]);
    };
    for (const value of [...TAGS, ...ODD, '[]', '["a","b","a"]', ' ["a" , "b"] ', '[1,2', 'a,,b, ,c', '["' + 'y'.repeat(70) + '"]', '[ "x"]']) {
        out.normalize.push({ value, out: ab.normalizeTags(value) });
    }
    for (let i = 0; i < 400; i++) {
        const value = tagValue();
        out.normalize.push({ value: clone(value), out: ab.normalizeTags(value) });
    }
    const peer = (n) => {
        const p = {};
        if (r.chance(0.9)) p.id = r.pick([`dev-${n}`, `dev-${n}`, ` dev-${n} `, n, '', null, `typed-${n}`]);
        if (r.chance(0.3)) p.username = r.pick(['alice', '', null, 5]);
        if (r.chance(0.3)) p.hostname = r.pick(['host', '']);
        if (r.chance(0.2)) p.alias = r.pick(['An alias', 0]);
        if (r.chance(0.2)) p.platform = 'Windows';
        if (r.chance(0.7)) p.tags = tagValue();
        if (r.chance(0.1)) p.extra = { kept: true, 10: 'ten', 2: 'two' };
        return r.chance(0.07) ? r.pick([null, 'text', 7, ['list'], []]) : p;
    };
    const book = () => {
        const b = {};
        if (r.chance(0.2)) b.first = 'kept';
        if (r.chance(0.85)) b.peers = r.chance(0.1) ? r.pick(ODD) : Array.from({ length: r.int(6) }, (_, n) => peer(r.int(6)));
        if (r.chance(0.7)) b.tags = tagValue();
        if (r.chance(0.2)) b.tag_colors = '{"lab": 1}';
        return b;
    };
    const data = () => {
        const b = book();
        return r.pick([b, b, JSON.stringify(b), ` ${JSON.stringify(b)}\n`, '', '   ', 'not json', '[1,2]', 'null', '"text"', '7', null, 5, true, [b], []]);
    };
    const device = (n) => {
        const d = { id: r.pick([`dev-${n}`, `dev-${n}`, ` dev-${n} `, n, '', null]) };
        if (r.chance(0.6)) d.tags = tagValue();
        if (r.chance(0.2)) d.banned = r.pick([true, 1, false, 0, 'yes']);
        if (r.chance(0.15)) d.soft_deleted = r.pick([true, false, 1]);
        for (const key of ['username', 'user', 'hostname', 'display_name', 'note', 'platform', 'os']) {
            if (r.chance(0.3)) d[key] = r.pick(['value-' + key, '', null, 0, 42, true]);
        }
        return r.chance(0.05) ? r.pick([null, 'text', 7, []]) : d;
    };
    for (let i = 0; i < 1500; i++) {
        const input = data();
        const devices = r.chance(0.1) ? r.pick([undefined, null, 'x']) : Array.from({ length: r.int(6) }, () => device(r.int(6)));
        const options = { devices: clone(devices) };
        if (r.chance(0.3)) options.includeDevices = r.pick([false, true, undefined, 0, null]);
        out.parse.push({ data: clone(input), out: ab.parseAddressBookData(clone(input)) });
        out.merge.push({ data: clone(input), devices: clone(devices) === undefined ? null : clone(devices), knownOnly: options.includeDevices === false, out: ab.mergeAddressBookData(clone(input), options) });
        out.updates.push({ data: clone(input), out: ab.collectPeerTagUpdates(clone(input)) });
        const visible = r.chance(0.2) ? null : Array.from({ length: r.int(5) }, () => `dev-${r.int(6)}`);
        const known = r.chance(0.1) ? r.pick([null, 'x']) : Array.from({ length: r.int(7) }, () => r.pick([`dev-${r.int(6)}`, ` dev-${r.int(6)} `, r.int(6), null, '']));
        out.filter.push({
            data: clone(input), visible, known: clone(known),
            out: ab.filterAddressBookPeersByScope(clone(input), { visibleIds: visible ? new Set(visible) : null, knownDeviceIds: known }),
        });
        if (Array.isArray(devices)) out.visible.push({ devices: clone(devices), out: ab.collectVisibleTags(devices) });
    }
    return out;
}

function write(name, value) {
    fs.writeFileSync(path.join(OUT, `${name}.json.gz`), zlib.gzipSync(Buffer.from(JSON.stringify(value)), { level: 9 }));
}

async function main() {
    const payload = payloadVectors();
    const tags = await tagVectors();
    const book = addressBookVectors();
    write('payload', payload);
    write('tags', tags);
    write('addressbook', book);
    const counts = { payload: payload.length };
    for (const [name, list] of Object.entries(tags)) counts[`tags_${name}`] = list.length;
    for (const [name, list] of Object.entries(book)) counts[`addressbook_${name}`] = list.length;
    fs.writeFileSync(path.join(OUT, 'counts.json'), JSON.stringify(counts, null, 1) + '\n');
    console.log(JSON.stringify(counts));
}

main().catch((err) => { console.error(err); process.exit(1); });
