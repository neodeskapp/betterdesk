'use strict';

// Vectors for proxyToGo and proxyBinaryToGo of lib/goApiProxy.js, called
// from three small routes with the real API client.

const { start, apiAnswers } = require('./harness');
const { write } = require('./lib');

const b64 = (text) => Buffer.from(text, 'utf8').toString('base64');

async function main() {
    const oracle = await start((app, load) => {
        const { apiClient } = load('./services/betterdeskApi');
        const { proxyToGo, proxyBinaryToGo, safeSegment } = load('./lib/goApiProxy');
        const sendsBody = (req) => ['POST', 'PUT', 'PATCH'].includes(req.method);
        app.all('/json/:a', (req, res) => {
            proxyToGo(apiClient, req, res, req.method, () => `/x/${safeSegment(req.params.a, 'a')}`, sendsBody(req) ? req.body : undefined);
        });
        app.all('/json/:a/:b', (req, res) => {
            proxyToGo(apiClient, req, res, req.method, () => `/x/${safeSegment(req.params.a, 'a')}/${safeSegment(req.params.b, 'b')}`, sendsBody(req) ? req.body : undefined);
        });
        app.get('/fixed', (req, res) => {
            proxyToGo(apiClient, req, res, 'GET', req.query.path);
        });
        app.get('/binary/:a', (req, res) => {
            proxyBinaryToGo(apiClient, req, res, 'GET', () => `/files/${safeSegment(req.params.a, 'a')}`);
        });
    });

    const vectors = [];
    const json = 'application/json';

    // Every kind of API answer, for a read and for a write
    for (const answer of apiAnswers) {
        vectors.push(await oracle.call({ path: '/json/one' }, answer));
        vectors.push(await oracle.call({ method: 'POST', path: '/json/one/two', type: json, body: '{"a":1}' }, answer));
    }

    // Line and paragraph separators, which JSON.stringify writes as they are
    vectors.push(await oracle.call({ path: '/json/one' }, { status: 200, type: json, body: '{"t":"a\u2028b\u2029c"}' }));

    vectors.push(await oracle.call({ method: 'POST', path: '/json/one', type: json, body: '{"t":"a\u2028b\u2029c \\\\u2028 \\u00e9"}' }, { status: 200, type: json, body: '{"t":"\\\\u2028 \\\\\\u2029 \\u00e9 \\\\\\\\"}' }));

    // Path parameters
    const params = ['abc', 'a.b', 'a..b', '..a', 'a:b@c', 'a%25b', 'a+b', 'a%2Bb', 'a%20b', '%20a%20', 'a%2Fb', '%2E%2E', '%2E', 'a,b', 'a*b', 'a~b',
        'a'.repeat(256), 'a'.repeat(257), 'A-Z_0.9', '%C3%A9', 'a%00', '%09abc%0A', '%EF%BB%BFabc', '%C2%A0abc'];
    for (const p of params) {
        vectors.push(await oracle.call({ path: `/json/${p}` }));
        vectors.push(await oracle.call({ method: 'DELETE', path: `/json/ok/${p}` }));
        vectors.push(await oracle.call({ path: `/binary/${p}` }, { status: 200, type: 'text/csv', base64: b64('a,b\n1,2\n') }));
    }
    vectors.push(await oracle.call({ path: '/json/first%20bad/second%20bad' }));

    // Bodies
    const bodies = ['{}', '{"name":"x","n":1.0,"big":12345678901234567890,"e":1e400,"neg":-0.0,"list":[1,"2",null,true],"o":{"b":1,"a":2}}', '[1,2,3]', '[]',
        '{"a":1,"a":2}', '{"2":"b","1":"a","x":"c"}', '{"t":"\\u00e9\\ud83d\\ude00 <b>&</b>"}', ' {"spaced" : true } ', '{"__proto__":{"x":1},"constructor":2}',
        '{"keep":{"prototype":1,"inner":{"constructor":2,"ok":-0}},"list":[{"__proto__":3,"constructor":4,"deep":{"prototype":5}}],"prototype":6}', '[{"__proto__":1},-0.0]'];
    for (const method of ['POST', 'PUT', 'PATCH']) {
        for (const body of bodies) {
            vectors.push(await oracle.call({ method, path: '/json/one', type: json, body }, { status: 200, type: json, body: '{"ok":true}' }));
        }
        vectors.push(await oracle.call({ method, path: '/json/one' }, { status: 200, type: json, body: '{"ok":true}' }));
        vectors.push(await oracle.call({ method, path: '/json/one', type: 'application/x-www-form-urlencoded', body: 'a=1&b=two+words&a=3' }, { status: 200, type: json, body: '{"ok":true}' }));
    }
    vectors.push(await oracle.call({ method: 'DELETE', path: '/json/one', type: json, body: '{"ignored":true}' }));
    vectors.push(await oracle.call({ method: 'HEAD', path: '/json/one' }, { status: 200, type: json, body: '{"ok":true}' }));

    // Paths handed to the client as they are: its own check answers
    const fixed = ['/peers', '/peers?id=1&x=a%20b', '/a/../b', '/a/./b', '/a\\b', 'peers', 'http://evil.example/x', '//evil.example/x', '/a?b=%0A', '/a b', '/a?q="x"<y>',
        '/a%2Fb', '/a..b', '/a#frag', '/ä'];
    for (const p of fixed) {
        vectors.push(await oracle.call({ path: `/fixed?path=${encodeURIComponent(p)}` }, { status: 200, type: json, body: '{"ok":true}' }));
    }

    // Files
    const files = [
        { status: 200, type: 'text/csv', disposition: 'attachment; filename="sessions.csv"', base64: b64('id,minutes\n1,30\n') },
        { status: 200, type: 'text/csv; charset=utf-8', base64: b64('ä,ö\n') },
        { status: 200, type: 'text/plain; Charset = iso-8859-1', base64: Buffer.from([0xe4, 0xf6, 0xfc]).toString('base64') },
        { status: 200, type: 'TEXT/CSV', base64: b64('x') },
        { status: 200, type: 'application/pdf', disposition: 'inline', base64: Buffer.from([0x25, 0x50, 0x44, 0x46, 0x00, 0xff, 0xfe]).toString('base64') },
        { status: 200, type: 'application/json', base64: b64('{"a":1}') },
        { status: 200, type: 'application/javascript', base64: b64('x()') },
        { status: 200, type: 'application/vnd.ms-excel', base64: b64('x') },
        { status: 200, base64: Buffer.from([0x00, 0x01, 0x02]).toString('base64') },
        { status: 200, type: 'text/csv' },
        { status: 200 },
        { status: 204 },
        { status: 404, type: 'application/json', body: '{"error":"no such export"}' },
        { status: 404, type: 'text/plain', body: 'gone' },
        { status: 404, type: 'application/json', body: ' [1, 2] ' },
        { status: 404, type: 'application/json', body: 'null' },
        { status: 404, type: 'application/json', body: '5' },
        { status: 404, type: 'application/json', body: '\ufeff{"error":"with a byte order mark"}' },
        { status: 500 },
        { status: 403, type: 'application/json', body: '{"error":"forbidden","b":2,"a":1}' },
        { hang: true },
    ];
    for (const file of files) {
        vectors.push(await oracle.call({ path: '/binary/report', binary: true }, file));
    }

    oracle.stop();
    write('proxy.json', vectors);
}

main().catch((err) => {
    console.error(err);
    process.exit(1);
});
