'use strict';

// What the scripts in this folder share: loading the Node console's code,
// a random source that gives the same values on every run, and writing a
// vector file.

const fs = require('fs');
const path = require('path');
const { createRequire } = require('module');

const repo = process.env.BD_REPO || '/repo';
const consoleDir = path.join(repo, 'web-nodejs');

// load is require() as a file of the Node console would call it.
const load = createRequire(path.join(consoleDir, 'server.js'));

// random returns a generator of whole numbers below n, seeded.
function random(seed) {
    let a = seed >>> 0;
    return (n) => {
        a = (a + 0x6D2B79F5) >>> 0;
        let t = a;
        t = Math.imul(t ^ (t >>> 15), t | 1);
        t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
        return (((t ^ (t >>> 14)) >>> 0) % n);
    };
}

function write(name, vectors) {
    const file = path.join(__dirname, '..', name);
    fs.writeFileSync(file, JSON.stringify(vectors) + '\n');
    console.log(`${name}: ${vectors.length} vectors`);
}

module.exports = { load, random, write, consoleDir };
