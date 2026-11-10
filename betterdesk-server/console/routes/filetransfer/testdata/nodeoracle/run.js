'use strict';

// Vectors for the file transfer routes people use:
// routes/fileTransfer.routes.js of the Node console, mounted at
// /api/files and at /api/bd as routes/index.js mounts it, with the real
// guards and the real services/fileTransferService.js. Writes
// ../vectors.json.
//
// A vector is a request with the status, the headers that matter and the
// body of the answer. Two kinds of vectors are no requests:
//
//   {"feed": {"transfer": id, "index": n, "data": base64}}   a chunk, given to the service as a device would
//   {"remove": id}                                            the transfer's file is deleted from the disk
//
// Ids are those of the Node run. The Go test maps each to the id its own
// service gave the same transfer.

const path = require('path');
const fs = require('fs');
const http = require('http');
const oracleDir = path.join(__dirname, '..', '..', '..', '..', 'goapipath', 'testdata', 'nodeoracle');
const { load } = require(path.join(oracleDir, 'lib'));
const { accounts } = require(path.join(oracleDir, 'harness'));

process.env.NODE_ENV = 'development';
process.env.UPLOAD_RATE_LIMIT_MAX = '100000000';
process.env.FILE_ACCESS_RATE_LIMIT_MAX = '100000000';

const kept = ['content-type', 'content-disposition', 'content-length', 'accept-ranges', 'cache-control', 'content-range', 'etag', 'last-modified', 'x-content-type-options'];

async function main() {
    const express = load('express');
    const service = load('./services/fileTransferService');
    const app = express();
    app.use(express.json({ limit: '2mb' }));
    app.use(express.urlencoded({ extended: false, limit: '2mb' }));
    app.use((req, _res, next) => {
        const header = req.headers['x-test-session'];
        req.session = header ? JSON.parse(header) : {};
        req.session.destroy = () => {};
        next();
    });
    const router = load('./routes/fileTransfer.routes');
    app.use('/api/files', router);
    app.use('/api/bd', router);
    app.use((req, res) => res.status(404).type('text/plain').send('no route'));
    app.use((err, req, res, _next) => res.status(err.status || 500).json({ success: false, error: err.message }));
    const server = http.createServer(app);
    await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
    const { port } = server.address();

    const vectors = [];
    function send(request) {
        return new Promise((resolve, reject) => {
            const headers = { ...(request.headers || {}) };
            if (request.session) headers['x-test-session'] = JSON.stringify(request.session);
            const body = request.body === undefined ? null : Buffer.from(request.body, 'utf8');
            if (body) {
                headers['Content-Type'] = request.type || 'application/json';
                headers['Content-Length'] = body.length;
            }
            const req = http.request({ host: '127.0.0.1', port, method: request.method || 'GET', path: request.path, headers }, (res) => {
                const chunks = [];
                res.on('data', (c) => chunks.push(c));
                res.on('end', () => {
                    const got = {};
                    for (const name of kept) if (res.headers[name] !== undefined) got[name] = res.headers[name];
                    const vector = { request: { method: request.method || 'GET', ...request }, status: res.statusCode, headers: got, body: Buffer.concat(chunks).toString('base64') };
                    vectors.push(vector);
                    resolve(vector);
                });
            });
            req.on('error', reject);
            if (body) req.write(body);
            req.end();
        });
    }
    const json = (vector) => JSON.parse(Buffer.from(vector.body, 'base64').toString('utf8'));
    const start = async (session, body, mount = '/api/files') => json(await send({ method: 'POST', path: `${mount}/transfer`, session, body: JSON.stringify(body) })).id;
    const feed = (transfer, index, bytes) => {
        const data = Buffer.from(bytes).toString('base64');
        service.receiveChunk(transfer, index, data);
        vectors.push({ feed: { transfer, index, data } });
    };
    const op = accounts.operator;
    const everyone = [undefined, accounts.pro, accounts.viewer, accounts.server_admin, op, accounts.global_admin, accounts.admin];

    // Who may call what, at both mounts
    for (const mount of ['/api/files', '/api/bd']) {
        for (const session of everyone) {
            await send({ method: 'POST', path: `${mount}/transfer`, session, body: '{"direction":"download","device_id":"dev-1","filename":"who.txt","size":3}' });
            await send({ path: `${mount}/transfers`, session });
            await send({ path: `${mount}/transfers/nope`, session });
            await send({ method: 'POST', path: `${mount}/transfers/nope/cancel`, session, body: '{}' });
            await send({ path: `${mount}/transfers/nope/download`, session });
        }
    }

    // What a transfer is made of
    const bodies = ['{}', '[]', '{"device_id":"d"}', '{"filename":"f"}', '{"device_id":"d","filename":"f"}', '{"device_id":"","filename":"f","direction":"upload"}', '{"device_id":0,"filename":"f","direction":"upload"}',
        '{"device_id":"d","filename":"","direction":"upload"}', '{"device_id":"d","filename":"f","direction":"Upload"}', '{"device_id":"d","filename":"f","direction":["upload"]}', '{"device_id":"d","filename":"f","direction":""}',
        '{"device_id":"d","filename":"f","direction":null}', '{"device_id":"d","filename":"f","direction":"download"}', '{"device_id":5,"filename":7,"direction":"upload","size":"12abc","mime_type":{"x":1}}',
        '{"device_id":{"id":1},"filename":["a","b c"],"direction":"download","size":[9],"mime_type":0}', '{"device_id":{"toString":1},"filename":"f","direction":"download"}', '{"device_id":"d","filename":{"toString":1},"direction":"download"}',
        '{"device_id":"d","filename":"f","direction":"download","size":{"toString":1}}', '{"device_id":"d","filename":"f","direction":"download","size":4294967296}', '{"device_id":"d","filename":"f","direction":"download","size":4294967297}',
        '{"device_id":"d","filename":"f","direction":"download","size":"0x10"}', '{"device_id":"d","filename":"f","direction":"download","size":-5.5}', '{"device_id":"d","filename":"f","direction":"download","size":1e21}',
        '{"device_id":"d","filename":"f","direction":"download","size":null,"mime_type":null}', '{"device_id":"d","filename":"f","direction":"download","size":true,"mime_type":"text/html"}',
        '{"device_id":"d","filename":"../../etc/pass wd?.txt","direction":"download","size":262145}', '{"device_id":"d","filename":"a/b/","direction":"upload"}', '{"device_id":"d","filename":"/","direction":"upload"}',
        '{"device_id":"d","filename":"\\u00fc\\ud83d\\ude00.png","direction":"upload"}', '{"device_id":"d","filename":"..","direction":"upload"}', '{"device_id":"d\\u2028<b>","filename":"x","direction":"upload","mime_type":"\\u2028"}',
        '{"device_id":-0.0,"filename":"x","direction":"upload"}', '{"device_id":[],"filename":{},"direction":"upload"}', '{"direction":"download","device_id":"d","filename":"dup","filename":"second","size":1,"size":2}',
        ' {"device_id" : "d" , "filename" : "spaced" , "direction" : "upload" } '];
    for (const body of bodies) await send({ method: 'POST', path: '/api/files/transfer', session: op, body });
    await send({ method: 'POST', path: '/api/files/transfer', session: op });
    await send({ method: 'POST', path: '/api/files/transfer', session: op, type: 'application/x-www-form-urlencoded', body: 'direction=upload&device_id=d&filename=form.txt&size=7' });
    await send({ method: 'POST', path: '/api/files/transfer', session: op, type: 'application/x-www-form-urlencoded', body: 'direction=upload&direction=download&device_id=d&filename=f' });
    await send({ method: 'POST', path: '/api/files/transfer', session: { userId: 9, user: { id: 9, username: '', role: 'admin' } }, body: '{"device_id":"d","filename":"nameless","direction":"upload"}' });

    // The lists
    const queries = ['', 'status=pending', 'status=cancelled', 'device_id=d', 'device_id=dev-1&direction=download', 'direction=upload', 'direction=', 'device_id=5', 'device_id=d&device_id=dev-1', 'device_id[]=d',
        'device_id[x]=d', 'status[]=pending', 'status=pending&status=active', 'direction[toString]=x', 'status=PENDING', 'device_id=%20d', 'device_id=nobody', 'STATUS=pending', 'status=pending#frag', 'x=1'];
    for (const q of queries) {
        await send({ path: `/api/files/transfers?${q}`, session: op });
    }
    await send({ path: '/api/bd/transfers?device_id=d&direction=upload', session: op });

    // One transfer from start to download
    const cases = [
        ['note.txt', 'hello world'], ['doc.pdf', '%PDF-1.4 fake'], ['data.bin', '\u0000\u0001\u0002\u0003'], ['page.html', '<h1>x</h1>'], ['conf.json', '{"a":1}'], ['noext', 'plain'], ['PIC.PNG', 'png?'],
        ['.hidden', 'dot'], ['archive.tar.gz', 'gz'], ['script.js', 'alert(1)'], ['style.css', 'a{}'], ['odd.zzz', '??'], ['empty.txt', ''],
    ];
    for (const [filename, content] of cases) {
        const id = await start(op, { direction: 'download', device_id: 'dev-9', filename, size: Buffer.byteLength(content) || 1 });
        await send({ path: `/api/files/transfers/${id}/download`, session: op });
        const bytes = Buffer.from(content);
        const half = Math.floor(bytes.length / 2);
        feed(id, 0, bytes.subarray(0, half));
        await send({ path: `/api/files/transfers/${id}`, session: op });
        if (bytes.length) await send({ path: `/api/bd/transfers/${id}/download`, session: op });
        feed(id, 1, bytes.length ? bytes.subarray(half) : Buffer.from('x'));
        await send({ path: `/api/files/transfers/${id}`, session: op });
        const full = await send({ path: `/api/files/transfers/${id}/download`, session: op });
        await send({ method: 'HEAD', path: `/api/bd/transfers/${id}/download`, session: op });
        await send({ path: `/api/files/transfers/${id}/download`, session: accounts.viewer });
        await send({ path: `/api/files/transfers/${id}/download`, session: op, headers: { Range: 'bytes=0-3' } });
        await send({ path: `/api/files/transfers/${id}/download`, session: op, headers: { Range: 'bytes=2-' } });
        await send({ path: `/api/files/transfers/${id}/download`, session: op, headers: { Range: 'bytes=-2' } });
        await send({ path: `/api/files/transfers/${id}/download`, session: op, headers: { Range: 'bytes=500-600' } });
        await send({ path: `/api/files/transfers/${id}/download`, session: op, headers: { Range: 'bytes=0-0,2-2' } });
        await send({ path: `/api/files/transfers/${id}/download`, session: op, headers: { Range: 'lines=1-2' } });
        await send({ path: `/api/files/transfers/${id}/download`, session: op, headers: { 'If-None-Match': full.headers.etag } });
        await send({ path: `/api/files/transfers/${id}/download`, session: op, headers: { 'If-None-Match': '"other"' } });
        await send({ path: `/api/files/transfers/${id}/download`, session: op, headers: { 'If-Modified-Since': 'Fri, 01 Jan 2100 00:00:00 GMT' } });
        await send({ path: `/api/files/transfers/${id}/download`, session: op, headers: { 'If-Modified-Since': 'Mon, 01 Jan 2001 00:00:00 GMT' } });
        await send({ path: `/api/files/transfers/${id}/download`, session: op, headers: { 'If-Match': '"other"' } });
        await send({ path: `/api/files/transfers/${id}/download`, session: op, headers: { 'If-Unmodified-Since': 'Mon, 01 Jan 2001 00:00:00 GMT' } });
        await send({ path: `/api/files/transfers/${id}/download`, session: op, headers: { 'If-Range': '"other"', Range: 'bytes=0-1' } });
        await send({ path: `/api/files/transfers/${id}/download/`, session: op });
        await send({ path: `/API/Files/Transfers/${id}/Download`, session: op });
        await send({ path: `/api/files/transfers/${id.toUpperCase()}/download`, session: op });
        fs.rmSync(service.getTransfer(id).tempPath, { force: true });
        vectors.push({ remove: id });
        await send({ path: `/api/files/transfers/${id}/download`, session: op });
        await send({ method: 'POST', path: `/api/bd/transfers/${id}/cancel`, session: op, body: '{}' });
        await send({ path: `/api/files/transfers/${id}/download`, session: op });
        await send({ path: `/api/files/transfers/${id}`, session: op });
    }

    // Cancel takes the file of a completed transfer with it
    const gone = await start(op, { direction: 'upload', device_id: 'dev-9', filename: 'gone.txt', size: 2 }, '/api/bd');
    feed(gone, 0, 'ab');
    await send({ method: 'POST', path: `/api/files/transfers/${gone}/cancel`, session: op });
    await send({ method: 'POST', path: `/api/files/transfers/${gone}/cancel`, session: op, body: '{}' });
    await send({ method: 'POST', path: `/api/files/transfers/${gone}/cancel`, session: accounts.viewer, body: '{}' });
    await send({ path: `/api/files/transfers/${gone}/download`, session: op });
    await send({ path: '/api/files/transfers?status=cancelled&device_id=dev-9', session: op });
    await send({ path: '/api/files/transfers/%2e%2e/download', session: op });
    await send({ path: '/api/files/transfers/a%2Fb', session: op });

    server.close();
    server.closeAllConnections();
    fs.writeFileSync(path.join(__dirname, '..', 'vectors.json'), JSON.stringify(vectors) + '\n');
    console.log(`vectors.json: ${vectors.length} vectors`);
    process.exit(0);
}

main().catch((err) => {
    console.error(err);
    process.exit(1);
});
