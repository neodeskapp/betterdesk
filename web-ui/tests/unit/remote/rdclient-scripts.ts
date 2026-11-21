/**
 * Runs the wrapped remote client's classic scripts (static/rdclient) in the
 * test's jsdom window, the way the viewer page loads them: as one script
 * text, so the top-level classes see each other. Returns the bindings asked
 * for, also the classes a script keeps as top-level bindings only.
 */
import fs from 'node:fs';
import path from 'node:path';

const dir = path.resolve(__dirname, '../../../static/rdclient');

/** The scripts the RustDesk client needs, in the page's load order */
export const RD_CORE = [
	'vendor/protobuf.min.js',
	'generated/protos-static.js',
	'vendor/nacl.min.js',
	'vendor/nacl-util.min.js',
	'connection.js',
	'connection-params.js',
	'punchhole-response.js',
	'crypto.js',
	'protocol.js'
];

// eslint-disable-next-line @typescript-eslint/no-explicit-any
export type Loaded = Record<string, any>;

export function loadScripts(files: string[], names: string[]): Loaded {
	const code = files.map((f) => fs.readFileSync(path.join(dir, f), 'utf8')).join('\n;\n');
	const out = names
		.map((n) => `${JSON.stringify(n)}: typeof ${n} === 'undefined' ? undefined : ${n}`)
		.join(',');
	// Indirect eval runs the text in the global scope, as a classic script would
	return (0, eval)(`${code}\n;({${out}})`) as Loaded;
}
