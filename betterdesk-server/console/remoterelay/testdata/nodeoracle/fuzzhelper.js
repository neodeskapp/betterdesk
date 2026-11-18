'use strict';

/**
 * Answers the Go fuzz tests (fuzz_test.go) with what the old console's
 * services/remoteRelay.js did, one line per question on stdin, one JSON
 * line per answer on stdout:
 *
 *   A <hex of UTF-8 text>   an agent's text message
 *   V <hex of UTF-8 text>   a viewer's text message, with an agent there
 *   U <hex of text>         decodeURIComponent of the agent path's id
 *
 * The message handling is the code of the two message listeners, copied
 * letter for letter because the module does not export it; a throw out of
 * a listener ended the process and is answered as "drop".
 */

const readline = require('readline');

function agent(data) {
    let frame;
    try { frame = JSON.parse(data.toString()); } catch { return { v: 'ignore' }; }
    try {
        if (frame.type === 'pong' || frame.type === 'status') {
            return { v: 'forward', text: JSON.stringify(frame) };
        }
    } catch {
        return { v: 'drop' };
    }
    return { v: 'ignore' };
}

function viewer(data) {
    let frame;
    try { frame = JSON.parse(data.toString()); } catch { return { v: 'ignore' }; }
    try {
        switch (frame.type) {
            case 'stop':
                return { v: 'stop' };
            case 'input': {
                const agentEvent = { ...frame, type: frame.event_type || frame.kind || 'unknown' };
                delete agentEvent.event_type;
                return { v: 'forward', text: JSON.stringify(agentEvent) };
            }
            default:
                return { v: 'ignore' };
        }
    } catch {
        return { v: 'drop' };
    }
}

const out = (v) => process.stdout.write(`${JSON.stringify(v)}\n`);
readline.createInterface({ input: process.stdin }).on('line', (line) => {
    const kind = line[0];
    const buf = Buffer.from(line.slice(2), 'hex');
    if (kind === 'A') return out(agent(buf));
    if (kind === 'V') return out(viewer(buf));
    if (kind === 'U') {
        try {
            return out({ ok: true, value: decodeURIComponent(buf.toString('latin1')) });
        } catch {
            return out({ ok: false });
        }
    }
    return out({ error: `unknown question ${kind}` });
});
