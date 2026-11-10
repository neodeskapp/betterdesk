'use strict';

// Vectors for the guest link routes: routes/guest.routes.js of the Node
// console with the real guards, the real cookie parser, the real
// middleware/guestAccess.js and the real API client in front of a scripted
// API server. Writes ../vectors.json.
//
// Three things around the routes are fakes that each vector sets or
// reads: the device scope of the account (services/deviceGroupService.js;
// "scope" in a vector is null for no limit, a list of device ids, or
// {"throws": text}), the database module it is called with, and the
// socket relay that is told about a deleted link (services/wsRelay.js;
// "revoked" lists the ids it was told).
//
// The harness records no cookies. A middleware here copies the Set-Cookie
// header of an answer into Content-Disposition, which the harness does
// record: "disposition" of a vector is "cookie: " and the header's value.
// No guest route sets a real Content-Disposition.
//
// Left out, because the Node console stops at them (BUGS.md MC1): a token
// with a toString member in ?guest=, ?t= or ?mesh_share= on the peers and
// desktop routes, and a link whose peer_ids is neither a list nor a text.

const path = require('path');
const fs = require('fs');
const Module = require('module');
const oracleDir = path.join(__dirname, '..', '..', '..', '..', 'goapipath', 'testdata', 'nodeoracle');
const { start, accounts, apiAnswers } = require(path.join(oracleDir, 'harness'));
const { random } = require(path.join(oracleDir, 'lib'));

process.env.RDCLIENT_PAGE_RATE_LIMIT_MAX = '100000000';

const json = 'application/json';
const ok = { status: 200, type: json, body: '{"ok":true,"items":[]}' };
const links = '/api/guest/access-links';
const desktop = '/api/guest/mesh/devices/dev-1/desktop';
const issuer = { userId: 7, user: { id: 7, username: 'e2e_guest_issuer', role: 'guest_issuer' } };

let scope = null;
let scopeCall = null;
let revoked = [];

function fake(load, request, exports) {
    const filename = load.resolve(request);
    const m = new Module(filename, null);
    m.filename = filename;
    m.loaded = true;
    m.exports = exports;
    require.cache[filename] = m;
}

async function main() {
    const oracle = await start((app, load) => {
        fake(load, './services/database', { marker: 'database' });
        fake(load, './services/deviceGroupService', {
            getDeviceScopeForUser: async (db, user, devices) => {
                scopeCall = { database: db && db.marker === 'database', user: user === undefined ? null : user, ids: devices.map((d) => d.id) };
                if (scope && scope.throws !== undefined) throw new Error(scope.throws);
                return scope === null ? null : new Set(scope);
            },
        });
        fake(load, './services/wsRelay', { invalidateGuestGrant: (id) => { revoked.push(id); return 0; } });
        app.use(load('cookie-parser')());
        app.use((req, res, next) => {
            const end = res.end;
            res.end = function patched(...args) {
                const cookie = res.getHeader('set-cookie');
                if (cookie && !res.headersSent) res.setHeader('Content-Disposition', `cookie: ${[].concat(cookie).join(' | ')}`);
                return end.apply(this, args);
            };
            next();
        });
        app.use('/', load('./routes/guest.routes'));
    });
    const vectors = [];
    const send = async (request, answer, withScope = null) => {
        scope = withScope;
        scopeCall = null;
        revoked = [];
        const vector = await oracle.call(request, answer);
        vector.scope = withScope;
        vector.scopeCall = scopeCall;
        vector.revoked = revoked;
        vectors.push(vector);
    };
    const everyone = [undefined, accounts.pro, accounts.viewer, issuer, accounts.operator, accounts.global_admin, accounts.server_admin, accounts.super_admin, accounts.admin];
    const link = '{"peer_ids":["dev-1","dev-2"],"ttl_minutes":5,"view_only":true,"label":"Front desk","max_uses":3}';
    const grant = { status: 200, type: json, body: '{"valid":true,"id":"g1","peer_ids":["dev-1","dev-2"],"view_only":false,"expires_at":"2099-01-01T00:00:00Z","label":"L","devices":[{"id":"dev-1"}]}' };
    const watching = { status: 200, type: json, body: '{"valid":true,"id":"g2","peer_ids":["dev-1"],"view_only":true,"expires_at":"2099-01-01T00:00:00Z"}' };

    const routes = [
        ['POST', links, link],
        ['GET', links],
        ['DELETE', `${links}/g1`],
        ['GET', `${links}/validate?token=tok&peer_id=dev-1`],
        ['GET', `${links}/peers?t=tok`],
        ['POST', `${desktop}?guest=tok`, '{}'],
        ['POST', `${desktop}?mesh_share=tok`, '{}'],
        ['POST', desktop, '{}'],
    ];

    // Who may call what
    for (const [method, p, body] of routes) {
        for (const session of everyone) {
            const request = { method, path: p, session };
            if (body) Object.assign(request, { type: json, body });
            await send(request, grant);
        }
    }

    // Every kind of answer of the API server. Answers that are an object
    // with a peer_ids that is no list are kept from the desktop route.
    const answers = apiAnswers.concat([
        grant, watching,
        { status: 200, type: json, body: '{"valid":false,"error":"invalid or expired guest link"}' },
        { status: 200, type: json, body: '{"valid":true}' },
        { status: 200, type: json, body: '{"valid":true,"peer_ids":[]}' },
        { status: 200, type: json, body: '{"valid":true,"peer_ids":["other"],"allowed_peer_ids":["dev-1"]}' },
        { status: 200, type: json, body: '{"valid":true,"allowed_peer_ids":["dev-1"],"view_only":1}' },
        { status: 200, type: json, body: '{"valid":true,"peer_ids":"xx dev-1 yy","view_only":"no"}' },
        { status: 200, type: json, body: '{"valid":true,"peer_ids":"dev","view_only":0}' },
        { status: 200, type: json, body: '{"valid":true,"peer_ids":[["dev-1"],1,null,"DEV-1"]}' },
        { status: 200, type: json, body: '{"valid":1,"peer_ids":["dev-1"],"expires_at":"2099-01-01T00:00:00.123456789+02:00"}' },
        { status: 200, type: json, body: '{"valid":"yes","peer_ids":["dev-1"],"expires_at":4102444800000}' },
        { status: 200, type: json, body: '{"valid":true,"peer_ids":["dev-1"],"expires_at":"2001-01-01T00:00:00Z"}' },
        { status: 200, type: json, body: '{"valid":true,"peer_ids":["dev-1"],"expires_at":"not a date"}' },
        { status: 200, type: json, body: '{"valid":true,"peer_ids":["dev-1"],"expires_at":"2099-01-01"}' },
        { status: 200, type: json, body: '{"valid":true,"peer_ids":["dev-1"],"expires_at":1}' },
        { status: 200, type: json, body: '{"valid":true,"peer_ids":["dev-1"],"expires_at":""}' },
        { status: 200, type: json, body: '{"valid":true,"peer_ids":["dev-1"],"expires_at":null}' },
        { status: 200, type: json, body: '{"valid":true,"peer_ids":["dev-1"],"expires_at":true}' },
        { status: 200, type: json, body: '{"valid":false,"expires_at":"2099-01-01T00:00:00Z"}' },
        { status: 201, type: json, body: '{"valid":true,"peer_ids":["dev-1"],"expires_at":"2099-01-01T00:00:00Z"}' },
        { status: 200, type: json, body: '{"revoked":true}' },
        { status: 403, type: json, body: '{"error":"forbidden"}' },
        { status: 403, type: json, body: '{"valid":true,"peer_ids":["dev-1"],"expires_at":"2099-01-01T00:00:00Z"}' },
        { status: 404, type: json, body: '{"error":"not found"}' },
        { status: 500, type: json, body: '""' },
        { status: 500, type: 'text/plain', body: 'boom' },
    ]);
    for (const [method, p, body] of routes) {
        for (const answer of answers) {
            const request = { method, path: p, session: accounts.admin };
            if (body) Object.assign(request, { type: json, body });
            await send(request, answer);
        }
    }

    // What a link is made of
    const bodies = ['{}', '[]', '["dev-1"]', '{"peer_ids":[]}', '{"peer_ids":"dev-1"}', '{"peer_ids":null}', '{"peer_ids":{"0":"dev-1","length":1}}', '{"peer_ids":["dev-1"]}',
        '{"peer_ids":[900100001,1.5,-0,1e21,1e-7,true,false,null,"",["a",["b",null]],{},{"a":1}]}', '{"peer_ids":[{"toString":1}]}', '{"peer_ids":["a",{"toString":"x"}]}', '{"peer_ids":[[{"toString":1}]]}',
        '{"peer_ids":["dev-1"],"ttl_minutes":null}', '{"peer_ids":["dev-1"],"ttl_minutes":0,"view_only":0,"label":0,"max_uses":0}', '{"peer_ids":["dev-1"],"ttl_minutes":"60","view_only":"false","label":"  ","max_uses":"3"}',
        '{"peer_ids":["dev-1"],"ttl_minutes":{"a":[1]},"view_only":[],"label":{"x":1},"max_uses":[2]}', '{"peer_ids":["dev-1"],"ttl_minutes":-0.0,"view_only":{},"label":["a","b"],"max_uses":-1}',
        '{"peer_ids":["dev-1"],"view_only":null,"label":null,"max_uses":null}', '{"peer_ids":["dev-1"],"label":"\\u00e9 <b>&\\u2028","max_uses":1.5}', '{"max_uses":9,"label":"first","view_only":true,"ttl_minutes":1,"peer_ids":["z","a"],"extra":"dropped"}',
        '{"peer_ids":["a"],"peer_ids":["b","c"]}', '{"peer_ids":["dev-1"],"label":{"__proto__":1,"constructor":2,"keep":{"prototype":3}},"ttl_minutes":{"constructor":1}}', '{"PEER_IDS":["dev-1"]}',
        '{"peer_ids":["dev-1"],"ttl_minutes":12345678901234567890,"max_uses":1e21}', ' {"peer_ids" : [ "dev-1" ] } ', `{"peer_ids":[${Array.from({ length: 300 }, (_, i) => `"d${i}"`).join(',')}]}`];
    for (const body of bodies) {
        await send({ method: 'POST', path: links, session: accounts.operator, type: json, body }, { status: 201, type: json, body: '{"id":"g1","token":"tok"}' });
    }
    await send({ method: 'POST', path: links, session: accounts.operator }, ok);
    await send({ method: 'POST', path: links, session: accounts.operator, type: 'application/x-www-form-urlencoded', body: 'peer_ids=dev-1&peer_ids=dev-2&label=form&view_only=1' }, ok);
    await send({ method: 'POST', path: links, session: accounts.operator, type: 'application/x-www-form-urlencoded', body: 'peer_ids=dev-1' }, ok);
    await send({ method: 'POST', path: links, session: accounts.operator, type: 'text/plain', body: link }, ok);

    // The device scope of the account
    const scopes = [null, [], ['dev-1'], ['dev-1', 'dev-2'], ['dev-2', 'dev-1', 'dev-3'], ['DEV-1', 'dev-2'], { throws: 'SQLITE_BUSY: database is locked' }, { throws: '' }];
    const nameless = { userId: 9, user: { id: 9, username: '', role: 'operator' } };
    const noUser = { userId: 55 };
    for (const withScope of scopes) {
        for (const session of [accounts.operator, issuer, accounts.admin, nameless]) {
            await send({ method: 'POST', path: links, session, type: json, body: link }, { status: 201, type: json, body: '{"id":"g1"}' }, withScope);
            await send({ method: 'POST', path: links, session, type: json, body: '{"peer_ids":[1,"dev-1",1]}' }, { status: 201, type: json, body: '{"id":"g1"}' }, withScope);
        }
    }
    await send({ method: 'POST', path: links, session: noUser, type: json, body: link }, ok, ['dev-1']);

    // Deleting a link
    const ids = ['g1', 'a.b', 'a..b', 'a:b@c', 'a%25b', 'a%2Bb', 'a%20b', '%20a%20', 'a%2Fb', '%2E%2E', '%2E', 'a,b', "a'b(c)!~*", 'a'.repeat(300), '%C3%A9', 'a%00', 'a%5Cb', 'a%3Fb%23c', '%E4%B8%96'];
    for (const id of ids) {
        await send({ method: 'DELETE', path: `${links}/${id}`, session: accounts.operator }, { status: 200, type: json, body: '{"revoked":true}' });
        await send({ method: 'DELETE', path: `${links}/${id}`, session: accounts.operator }, { status: 404, type: json, body: '{"error":"not found"}' });
    }
    await send({ method: 'DELETE', path: `${links}/g1`, session: accounts.operator }, { status: 204 });
    await send({ method: 'DELETE', path: `${links}/g1`, session: accounts.operator }, { hang: true });
    await send({ method: 'DELETE', path: `${links}/G1/`, session: accounts.operator }, ok);

    // The query of the three routes of a link holder
    const next = random(77);
    const tokens = ['token', 'peer_id', 'guest', 't', 'mesh_share', 'view_only', 'x', '[]', '[0]', '[k]', '=', '=', '&', '&', 'tok', 'dev-1', '1', '0', '%20', '+', '%5B', '%5D', ',', '.', 'a%26b', '%E4%B8%96', ''];
    const validateQueries = ['', 'token=tok', 'token=tok&peer_id=dev-1', 'peer_id=dev-1&token=tok', 'token=a&token=b', 'token[]=a', 'token[x]=1', 'token[toString]=x', 'x[toString]=1&token=tok', 'token=a%20b%26c%3Dd',
        'token=%E4%B8%96', 'token=%zz', 'token=a+b', 'token=a#frag', '5=five&token=tok&1=one', 'token=tok&token[x]=1', 'a=1&b=2&a=3', 'token=' + 'k'.repeat(400), 'token=&peer_id='];
    for (let i = 0; i < 250; i++) {
        let q = '';
        const n = 1 + next(8);
        for (let j = 0; j < n; j++) q += tokens[next(tokens.length)];
        validateQueries.push(q);
    }
    for (const q of validateQueries) await send({ path: `${links}/validate?${q}` }, grant);

    // Queries that keep clear of the three token parameters as objects with
    // a toString member
    const safe = (q) => !/(^|&)(guest|t|mesh_share)(\[[^\]]*\])*(\[|%5B)toString(\]|%5D)/i.test(q);
    const holderQueries = ['', 'guest=tok', 't=tok', 'guest=a&t=b', 't=b&guest=a', 'guest=&t=tok', 'guest=%20tok%20', 'guest=+', 't=%09', 'guest=a&guest=b', 'guest[]=a', 'guest[]=a&guest[]=b', 't[x]=1', 'guest[x][y]=1',
        'guest=0', 'guest=false', 'guest=tok&view_only=0', 'guest=tok&view_only=1&view_only=2', 't=tok&x=1&x=2', 't=tok&5=five&1=one', 't=tok&guest=other&mesh_share=s', 'mesh_share=s', 'mesh_share=%20s%20',
        'mesh_share=a&mesh_share=b', 'mesh_share[]=a', 'mesh_share[x]=1', 'mesh_share=s&peer_id=other&token=other', 'mesh_share=s&x[toString]=1', 't=tok&x[toString]=1', 'guest=tok&t=tok&t=again',
        'GUEST=tok', 'guest=a%26b%3Dc', 'guest=%E4%B8%96%E7%95%8C', 'guest=tok#frag', 'x=1'];
    for (let i = 0; i < 400; i++) {
        let q = '';
        const n = 1 + next(8);
        for (let j = 0; j < n; j++) q += tokens[next(tokens.length)];
        if (safe(q)) holderQueries.push(q);
    }
    for (const q of holderQueries.filter(safe)) {
        await send({ path: `${links}/peers?${q}` }, grant);
        await send({ method: 'POST', path: `${desktop}?${q}`, type: json, body: '{}' }, grant);
        await send({ method: 'POST', path: `${desktop}?${q}`, type: json, body: '{"sent":"on"}' }, watching);
    }
    await send({ method: 'POST', path: '/api/guest/mesh/devices/dev-2/desktop?guest=tok', type: json, body: '{}' }, watching);
    await send({ method: 'POST', path: '/api/guest/mesh/devices/Dev-1/desktop?guest=tok', type: json, body: '{}' }, watching);
    for (const id of ids) {
        await send({ method: 'POST', path: `/api/guest/mesh/devices/${id}/desktop?mesh_share=s`, type: json, body: '{}' }, grant);
    }

    // The guest cookie
    const cookies = ['bd.guest=tok', 'bd.guest=%20tok%20', 'bd.guest=a%20b', 'bd.guest="quoted"', 'bd.guest=', 'bd.guest', 'other=1; bd.guest=tok; more=2', 'bd.guest=first; bd.guest=second', 'betterdesk.guest=tok',
        'BD.GUEST=tok', 'bd.guest=j:"json text"', 'bd.guest=j:["a","b"]', 'bd.guest=j:{"a":1}', 'bd.guest=j:5', 'bd.guest=j:0', 'bd.guest=j:null', 'bd.guest=j:true', 'bd.guest=j:{bad', 'bd.guest=j:', 'bd.guest=j%3A%22enc%22',
        'bd.guest=s:signed.sig', 'bd.guest=%E4%B8%96', 'bd.guest=%zz', 'bd.guest=a=b', 'bd.guest=j:[["x"],null,1.5]', 'bd.guest=j:""'];
    for (const cookie of cookies) {
        await send({ path: `${links}/peers`, headers: { Cookie: cookie } }, grant);
        await send({ path: `${links}/peers?t=fromquery`, headers: { Cookie: cookie } }, grant);
        await send({ path: `${links}/peers?guest=`, headers: { Cookie: cookie } }, grant);
        await send({ method: 'POST', path: desktop, type: json, body: '{}', headers: { Cookie: cookie } }, watching);
        await send({ method: 'POST', path: `${desktop}?mesh_share=s`, type: json, body: '{}', headers: { Cookie: cookie } }, watching);
    }

    // Other spellings Express takes for the same route
    await send({ path: '/API/Guest/Access-Links/', session: accounts.admin }, ok);
    await send({ method: 'HEAD', path: `${links}/validate?token=tok` }, grant);
    await send({ path: '/api/guest/access-links/Peers/?T=tok&t=tok' }, grant);

    oracle.stop();
    fs.writeFileSync(path.join(__dirname, '..', 'vectors.json'), JSON.stringify(vectors) + '\n');
    console.log(`vectors.json: ${vectors.length} vectors`);
}

main().catch((err) => {
    console.error(err);
    process.exit(1);
});
