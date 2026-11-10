'use strict';

// Runs the certificate check of web-nodejs/routes/toolkit.routes.js
// against TLS servers that show the certificates of ../certs, and writes
// what it answers. See README.md.
//
//   node ssl.js                                   writes ../ssl-vectors-plain.json
//   NODE_EXTRA_CA_CERTS=../certs/root.crt node ssl.js trusted
//                                                 writes ../ssl-vectors-trusted.json
//
// The route reaches the servers at 0.0.0.0, which its guard lets through.

const fs = require('fs');
const path = require('path');
const tls = require('tls');

const repo = path.resolve(__dirname, '../../../../../..');
const web = path.join(repo, 'web-nodejs');
const certs = path.join(__dirname, '..', 'certs');
const trusted = process.argv[2] === 'trusted';
if (trusted !== !!process.env.NODE_EXTRA_CA_CERTS) throw new Error('give "trusted" together with NODE_EXTRA_CA_CERTS');

const pass = (req, res, next) => next();
const authPath = require.resolve(path.join(web, 'middleware/auth'));
require.cache[authPath] = { id: authPath, filename: authPath, loaded: true, exports: { requireAuth: pass, requirePermission: () => pass } };

const NOW = Date.UTC(2026, 10, 10, 12, 0, 0);
Date.now = () => NOW;

const router = require(path.join(web, 'routes/toolkit.routes'));

function check(port) {
    return new Promise((resolve, reject) => {
        const req = { method: 'POST', url: '/api/toolkit/ssl', headers: {}, body: { host: '0.0.0.0', port } };
        const res = {
            statusCode: 200,
            status(code) { this.statusCode = code; return this; },
            json(value) { resolve({ status: this.statusCode, answer: JSON.stringify(value) }); },
        };
        router.handle(req, res, (err) => reject(err || new Error('no route')));
    });
}

(async () => {
    const servers = JSON.parse(fs.readFileSync(path.join(certs, 'servers.json'), 'utf8'));
    const out = [];
    for (const s of servers) {
        const server = tls.createServer({
            key: fs.readFileSync(path.join(certs, s.chain[0] + '.key')),
            cert: s.chain.map((name) => fs.readFileSync(path.join(certs, name + '.crt'), 'utf8')).join(''),
        }, (socket) => { socket.on('error', () => {}); socket.end(); });
        server.on('tlsClientError', () => {});
        await new Promise((resolve) => server.listen(0, '0.0.0.0', resolve));
        const result = await check(server.address().port);
        out.push({ server: s.name, trusted, now: NOW, ...result });
        await new Promise((resolve) => server.close(resolve));
    }
    const file = path.join(__dirname, '..', trusted ? 'ssl-vectors-trusted.json' : 'ssl-vectors-plain.json');
    fs.writeFileSync(file, JSON.stringify(out, null, 1) + '\n');
    for (const v of out) console.log(v.server, v.answer.slice(0, 2000));
    process.exit(0);
})().catch((err) => { console.error(err); process.exit(1); });
