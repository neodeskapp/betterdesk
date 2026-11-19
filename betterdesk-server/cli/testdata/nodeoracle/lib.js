'use strict';

// Helpers of the oracles: temporary folders, running a script of the old
// console as a child process, writing vectors.

const fs = require('fs');
const os = require('os');
const path = require('path');
const zlib = require('zlib');
const { spawnSync } = require('child_process');

const REPO = '/repo/web-nodejs';
const OUT = path.join(__dirname, '..');

function tmpdir(prefix) {
    return fs.mkdtempSync(path.join(os.tmpdir(), prefix));
}

// run starts node with the script and arguments, in cwd, with exactly the
// environment given (plus PATH and NODE_PATH, so the modules of the old
// console load from a copy of a script), and stdin.
function run(script, args, { cwd, env = {}, input } = {}) {
    const res = spawnSync(process.execPath, [script, ...args], {
        cwd,
        env: { PATH: process.env.PATH, NODE_PATH: path.join(REPO, 'node_modules'), ...env },
        input,
        encoding: 'buffer',
        timeout: 60000,
    });
    if (res.error) throw res.error;
    return {
        status: res.status,
        stdout: res.stdout.toString('utf8'),
        stderr: res.stderr.toString('utf8'),
    };
}

// replace turns every occurrence of a temporary path into its placeholder.
function replace(text, pairs) {
    let out = text;
    for (const [from, to] of pairs) out = out.split(from).join(to);
    return out;
}

function fileState(file) {
    if (!fs.existsSync(file)) return null;
    const stat = fs.statSync(file);
    if (stat.isDirectory()) return { dir: true };
    return { base64: fs.readFileSync(file).toString('base64'), mode: stat.mode & 0o777 };
}

function writeVectors(name, vectors) {
    const file = path.join(OUT, name + '.json.gz');
    fs.writeFileSync(file, zlib.gzipSync(Buffer.from(JSON.stringify(vectors, null, 1))));
    console.log(`${name}: ${Array.isArray(vectors) ? vectors.length : Object.keys(vectors).length} vectors in ${file}`);
}

module.exports = { REPO, tmpdir, run, replace, fileState, writeVectors };
