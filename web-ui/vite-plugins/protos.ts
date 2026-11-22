/**
 * Writes the remote viewer's protobuf messages as TypeScript
 * (src/lib/remote/client/generated/protos.ts, not committed) from the
 * server's schema in betterdesk-server/protos whenever Vite loads its
 * config: the dev server, the build, Vitest and `svelte-kit sync` (so
 * svelte-check sees the file too).
 *
 * The code is what protobuf.js's static target (pbjs -t static) generates,
 * typed: each message is a class with create, encode, decode and
 * fromObject on the protobuf.js runtime, written field by field in the
 * order and with the checks of protobuf.js's own encoder, decoder and
 * converter. A field goes on the wire when it is set on the object, also
 * when it holds the default value. 64-bit fields are numbers (see
 * src/lib/remote/client/pb.ts). The schema has no maps, groups or nested
 * messages; the generator refuses a schema that has them.
 */
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import protobuf from 'protobufjs';
import type { Enum, Field, NamespaceBase, ReflectionObject, Type } from 'protobufjs';
import type { Plugin } from 'vite';

const UI = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
export const PROTO_DIR = path.resolve(UI, '../betterdesk-server/protos');
export const PROTO_FILES = ['rendezvous.proto', 'message.proto'];
export const OUT_FILE = path.join(UI, 'src/lib/remote/client/generated/protos.ts');

/** protobuf.js's wire types of the scalar types (types.basic) */
const WIRE: Record<string, number> = {
	double: 1,
	float: 5,
	int32: 0,
	uint32: 0,
	sint32: 0,
	fixed32: 5,
	sfixed32: 5,
	int64: 0,
	uint64: 0,
	sint64: 0,
	fixed64: 1,
	sfixed64: 1,
	bool: 0,
	string: 2,
	bytes: 2
};
/** Scalar types protobuf.js packs (types.packed) */
const PACKABLE = new Set(Object.keys(WIRE).filter((t) => t !== 'string' && t !== 'bytes'));
const LONG = new Set(['int64', 'uint64', 'sint64', 'fixed64', 'sfixed64']);
const UNSIGNED_LONG = new Set(['uint64', 'fixed64']);
const NUMBER = new Set(['double', 'float', 'int32', 'uint32', 'sint32', 'fixed32', 'sfixed32']);

const isEnum = (f: Field) => f.resolvedType instanceof protobuf.Enum;
const isMessage = (f: Field) => f.resolvedType instanceof protobuf.Type;
/** The type the writer and reader methods are named after: enums go as int32 */
const wireName = (f: Field) => (isEnum(f) ? 'int32' : f.type);
const q = (s: string) => `'${s.replace(/\\/g, '\\\\').replace(/'/g, "\\'")}'`;
const prop = (name: string) => (/^[A-Za-z_$][\w$]*$/.test(name) ? `.${name}` : `[${q(name)}]`);

/** The TypeScript name of a type in the schema's package: Parent_Child for nested enums */
function tsName(t: Type | Enum): string {
	const parts: string[] = [];
	let at: ReflectionObject | null = t;
	while (at && !(at instanceof protobuf.Namespace && !(at instanceof protobuf.Type))) {
		parts.unshift(at.name);
		at = at.parent;
	}
	return parts.join('_');
}

function enumNames(e: Enum): string {
	return Object.keys(e.values).map(q).join(' | ');
}

/** The field's type on a message instance and in create() */
function instanceType(f: Field): string {
	let t: string;
	if (isMessage(f)) t = `I${tsName(f.resolvedType as Type)}`;
	else if (isEnum(f) || NUMBER.has(f.type) || LONG.has(f.type)) t = 'number';
	else if (f.type === 'bool') t = 'boolean';
	else if (f.type === 'string') t = 'string';
	else if (f.type === 'bytes') t = 'Uint8Array';
	else throw new Error(`protos: field ${f.fullName} has the unsupported type ${f.type}`);
	return f.repeated ? `${t}[]` : t;
}

/** The field's type in a plain object for fromObject() */
function initType(f: Field): string {
	let t: string;
	if (isMessage(f)) t = `${tsName(f.resolvedType as Type)}Init`;
	else if (isEnum(f)) t = `${enumNames(f.resolvedType as Enum)} | number`;
	else if (LONG.has(f.type)) t = 'LongInput';
	else if (NUMBER.has(f.type)) t = 'number';
	else if (f.type === 'bool') t = 'boolean';
	else if (f.type === 'string') t = 'string';
	else if (f.type === 'bytes') t = 'Uint8Array | string';
	else throw new Error(`protos: field ${f.fullName} has the unsupported type ${f.type}`);
	return f.repeated ? `(${t})[]` : t;
}

/** The field's type on the class (what decode() and fromObject() give) */
function classType(f: Field): string {
	if (isMessage(f)) {
		const name = tsName(f.resolvedType as Type);
		return f.repeated ? `${name}[]` : `${name} | null`;
	}
	// A member of a oneof reads null until it is set
	return f.partOf ? `${instanceType(f)} | null` : instanceType(f);
}

/** The prototype default protobuf.js's static target gives a field */
function defaultValue(f: Field): string {
	if (f.repeated) return '$util.emptyArray';
	if (f.partOf) return 'null';
	if (LONG.has(f.type)) return '0';
	if (f.type === 'bytes') return '$util.newBuffer([])';
	return JSON.stringify(f.typeDefault);
}

function encodeField(f: Field, out: string[]): void {
	const ref = `m${prop(f.name)}`;
	const type = wireName(f);
	const wire = WIRE[type];
	const tag = (wireType: number) => ((f.id << 3) | wireType) >>> 0;
	if (f.repeated) {
		out.push(`\t\tif (${ref} != null && ${ref}.length) {`);
		if (f.packed && PACKABLE.has(type)) {
			out.push(`\t\t\tw.uint32(${tag(2)}).fork();`);
			out.push(`\t\t\tfor (let i = 0; i < ${ref}.length; ++i) w.${type}(${ref}[i]!);`);
			out.push('\t\t\tw.ldelim();');
		} else if (wire === undefined) {
			const name = tsName(f.resolvedType as Type);
			out.push(`\t\t\tfor (let i = 0; i < ${ref}.length; ++i)`);
			out.push(`\t\t\t\t${name}.encode(${ref}[i]!, w.uint32(${tag(2)}).fork(), q + 1).ldelim();`);
		} else {
			out.push(`\t\t\tfor (let i = 0; i < ${ref}.length; ++i) w.uint32(${tag(wire)}).${type}(${ref}[i]!);`);
		}
		out.push('\t\t}');
		return;
	}
	const guard = f.optional ? `if (${ref} != null && Object.hasOwnProperty.call(m, ${q(f.name)})) ` : '';
	if (wire === undefined) {
		const name = tsName(f.resolvedType as Type);
		out.push(
			`\t\t${guard}${name}.encode(${ref}${f.optional ? '' : '!'}, w.uint32(${tag(2)}).fork(), q + 1).ldelim();`
		);
	} else {
		out.push(`\t\t${guard}w.uint32(${tag(wire)}).${type}(${ref}${f.optional ? '' : '!'});`);
	}
}

/** What the reader gives for one value of the field */
function readValue(f: Field): string {
	if (isMessage(f)) return `${tsName(f.resolvedType as Type)}.decode(r, r.uint32(), undefined, n + 1)`;
	const type = wireName(f);
	if (LONG.has(type)) return `longNumber(r.${type}(), ${UNSIGNED_LONG.has(type)})`;
	return `r.${type}()`;
}

function decodeField(f: Field, out: string[]): void {
	const ref = `m${prop(f.name)}`;
	const type = wireName(f);
	out.push(`\t\t\t\tcase ${f.id}: {`);
	if (f.repeated) {
		out.push(`\t\t\t\t\tif (!(${ref} && ${ref}.length)) ${ref} = [];`);
		if (PACKABLE.has(type)) {
			out.push('\t\t\t\t\tif ((t & 7) === 2) {');
			out.push('\t\t\t\t\t\tconst c2 = r.uint32() + r.pos;');
			out.push("\t\t\t\t\t\tif (c2 > r.len) throw RangeError('index out of range');");
			out.push('\t\t\t\t\t\tr.len = c2;');
			out.push(`\t\t\t\t\t\twhile (r.pos < c2) ${ref}.push(${readValue(f)});`);
			out.push("\t\t\t\t\t\tif (r.pos !== c2) throw RangeError('index out of range');");
			out.push('\t\t\t\t\t\tr.len = c;');
			out.push(`\t\t\t\t\t} else ${ref}.push(${readValue(f)});`);
		} else {
			out.push(`\t\t\t\t\t${ref}.push(${readValue(f)});`);
		}
	} else {
		out.push(`\t\t\t\t\t${ref} = ${readValue(f)};`);
	}
	out.push('\t\t\t\t\tbreak;');
	out.push('\t\t\t\t}');
}

/** fromObject for one scalar value v into the target t (protobuf.js converter.fromObject) */
function scalarFromObject(f: Field, v: string, t: string, out: string[], indent: string): void {
	switch (f.type) {
		case 'double':
		case 'float':
			out.push(`${indent}${t} = Number(${v});`);
			return;
		case 'uint32':
		case 'fixed32':
			out.push(`${indent}${t} = ${v} >>> 0;`);
			return;
		case 'int32':
		case 'sint32':
		case 'sfixed32':
			out.push(`${indent}${t} = ${v} | 0;`);
			return;
		case 'string':
			out.push(`${indent}${t} = String(${v});`);
			return;
		case 'bool':
			out.push(`${indent}${t} = Boolean(${v});`);
			return;
		case 'bytes':
			out.push(`${indent}if (typeof ${v} === 'string')`);
			out.push(
				`${indent}\t$util.base64.decode(${v}, (${t} = $util.newBuffer($util.base64.length(${v}))), 0);`
			);
			out.push(`${indent}else if (${v}.length >= 0) ${t} = ${v};`);
			return;
	}
	if (LONG.has(f.type)) {
		const unsigned = UNSIGNED_LONG.has(f.type) ? 'true' : '';
		out.push(`${indent}if (typeof ${v} === 'string') ${t} = parseInt(${v}, 10);`);
		out.push(`${indent}else if (typeof ${v} === 'number') ${t} = ${v};`);
		out.push(`${indent}else if (typeof ${v} === 'object')`);
		out.push(`${indent}\t${t} = new $util.LongBits(${v}.low >>> 0, ${v}.high >>> 0).toNumber(${unsigned});`);
		return;
	}
	throw new Error(`protos: field ${f.fullName} has the unsupported type ${f.type}`);
}

/**
 * fromObject for an enum value: a name or its number. An unknown number is
 * kept; an unknown name leaves a single field unset and gives an element of
 * a repeated field the default.
 */
function enumFromObject(f: Field, v: string, t: string, out: string[], indent: string): void {
	const values = (f.resolvedType as Enum).values;
	out.push(`${indent}switch (${v}) {`);
	let defaultDone = false;
	for (const [name, id] of Object.entries(values)) {
		if (id === f.typeDefault && !defaultDone) {
			out.push(`${indent}\tdefault:`);
			out.push(`${indent}\t\tif (typeof ${v} === 'number') {`);
			out.push(`${indent}\t\t\t${t} = ${v};`);
			out.push(`${indent}\t\t\tbreak;`);
			out.push(`${indent}\t\t}`);
			if (f.repeated) out.push(`${indent}\t\t${t} = ${id};`);
			out.push(`${indent}\t\tbreak;`);
			defaultDone = true;
		}
		out.push(`${indent}\tcase ${q(name)}:`);
		out.push(`${indent}\tcase ${id}:`);
		out.push(`${indent}\t\t${t} = ${id};`);
		out.push(`${indent}\t\tbreak;`);
	}
	out.push(`${indent}}`);
}

function fromObjectField(f: Field, out: string[]): void {
	const d = `d${prop(f.name)}`;
	const m = `m${prop(f.name)}`;
	const value = (v: string, t: string, indent: string) => {
		if (isEnum(f)) enumFromObject(f, v, t, out, indent);
		else if (isMessage(f)) {
			out.push(
				`${indent}if (!$util.isObject(${v})) throw TypeError(${q(`${f.fullName}: object expected`)});`
			);
			out.push(`${indent}${t} = ${tsName(f.resolvedType as Type)}.fromObject(${v}, n + 1);`);
		} else scalarFromObject(f, v, t, out, indent);
	};
	if (f.repeated) {
		out.push(`\t\tif (${d}) {`);
		out.push(`\t\t\tif (!Array.isArray(${d})) throw TypeError(${q(`${f.fullName}: array expected`)});`);
		out.push(`\t\t\t${m} = [];`);
		out.push(`\t\t\tfor (let i = 0; i < ${d}.length; ++i) {`);
		out.push(`\t\t\t\tconst v = ${d}[i]!;`);
		value('v', `${m}[i]`, '\t\t\t\t');
		out.push('\t\t\t}');
		out.push('\t\t}');
	} else if (isEnum(f)) {
		value(d, m, '\t\t');
	} else {
		out.push(`\t\tif (${d} != null) {`);
		value(d, m, '\t\t\t');
		out.push('\t\t}');
	}
}

function messageCode(type: Type): string {
	const name = tsName(type);
	const fields = type.fieldsArray;
	for (const f of fields) {
		f.resolve();
		if (f.map) throw new Error(`protos: map field ${f.fullName} is not supported`);
		if (Reflect.get(f, 'delimited') === true)
			throw new Error(`protos: group field ${f.fullName} is not supported`);
	}
	const out: string[] = [];
	out.push(`export interface I${name} {`);
	for (const f of fields) out.push(`\t${f.name}?: ${instanceType(f)} | null;`);
	out.push('}');
	out.push('');
	out.push(`export interface ${name}Init {`);
	for (const f of fields) out.push(`\t${f.name}?: ${initType(f)} | null;`);
	out.push('}');
	out.push('');
	out.push(`export class ${name} implements I${name} {`);
	for (const f of fields) out.push(`\tdeclare ${f.name}: ${classType(f)};`);
	for (const o of type.oneofsArray) {
		out.push(`\tdeclare ${o.name}: ${o.oneof.map(q).join(' | ')} | undefined;`);
	}
	out.push('');
	out.push(`\tconstructor(p?: I${name}) {`);
	// Repeated fields start as own empty arrays, not the prototype's shared one
	for (const f of fields) if (f.repeated) out.push(`\t\tthis${prop(f.name)} = [];`);
	out.push('\t\tif (p) assignProps(this, p);');
	out.push('\t}');
	out.push('');
	out.push(`\tstatic create(p?: I${name}): ${name} {`);
	out.push(`\t\treturn new ${name}(p);`);
	out.push('\t}');
	out.push('');

	out.push(`\tstatic encode(m: I${name}, w?: Writer, q?: number): Writer {`);
	out.push('\t\tif (!w) w = $Writer.create();');
	out.push('\t\tif (q === undefined) q = 0;');
	out.push("\t\tif (q > $util.recursionLimit) throw Error('max depth exceeded');");
	for (const f of fields.slice().sort((a, b) => a.id - b.id)) encodeField(f, out);
	out.push('\t\treturn w;');
	out.push('\t}');
	out.push('');

	out.push(`\tstatic decode(r: Reader | Uint8Array, l?: number, e?: number, n?: number): ${name} {`);
	out.push('\t\tif (!(r instanceof $Reader)) r = $Reader.create(r);');
	out.push('\t\tif (n === undefined) n = 0;');
	out.push("\t\tif (n > $Reader.recursionLimit) throw Error('maximum nesting depth exceeded');");
	out.push('\t\tlet c: number;');
	out.push('\t\tif (l === undefined) c = r.len;');
	out.push('\t\telse {');
	out.push('\t\t\tc = r.pos + l;');
	out.push("\t\t\tif (c > r.len) throw RangeError('index out of range');");
	out.push('\t\t\tl = r.len;');
	out.push('\t\t\tr.len = c;');
	out.push('\t\t}');
	out.push(`\t\tconst m = new ${name}();`);
	out.push('\t\twhile (r.pos < c) {');
	out.push('\t\t\tconst t = r.uint32();');
	out.push('\t\t\tif (t === e) break;');
	out.push('\t\t\tswitch (t >>> 3) {');
	for (const f of fields) decodeField(f, out);
	out.push('\t\t\t\tdefault:');
	out.push('\t\t\t\t\tr.skipType(t & 7, n);');
	out.push('\t\t\t\t\tbreak;');
	out.push('\t\t\t}');
	out.push('\t\t}');
	out.push('\t\tif (l !== undefined) {');
	out.push("\t\t\tif (r.pos !== c) throw RangeError('index out of range');");
	out.push('\t\t\tr.len = l;');
	out.push('\t\t}');
	for (const f of fields) {
		if (f.required) {
			out.push(`\t\tif (!Object.hasOwnProperty.call(m, ${q(f.name)}))`);
			out.push(`\t\t\tthrow $util.ProtocolError(${q(`missing required '${f.name}'`)}, { instance: m });`);
		}
	}
	out.push('\t\treturn m;');
	out.push('\t}');
	out.push('');

	out.push(`\tstatic fromObject(d: ${name}Init, n?: number): ${name} {`);
	out.push(`\t\tif (d instanceof ${name}) return d;`);
	if (!fields.length) {
		out.push(`\t\treturn new ${name}();`);
	} else {
		out.push(`\t\tif (!$util.isObject(d)) throw TypeError(${q(`${type.fullName}: object expected`)});`);
		out.push('\t\tif (n === undefined) n = 0;');
		out.push("\t\tif (n > $util.recursionLimit) throw Error('maximum nesting depth exceeded');");
		out.push(`\t\tconst m = new ${name}();`);
		for (const f of fields) fromObjectField(f, out);
		out.push('\t\treturn m;');
	}
	out.push('\t}');
	out.push('}');
	out.push('');
	for (const f of fields) out.push(`${name}.prototype${prop(f.name)} = ${defaultValue(f)};`);
	for (const o of type.oneofsArray) {
		const names = `[${o.oneof.map(q).join(', ')}]`;
		out.push(`Object.defineProperty(${name}.prototype, ${q(o.name)}, {`);
		out.push(`\tget: $util.oneOfGetter(${names}),`);
		out.push(`\tset: $util.oneOfSetter(${names})`);
		out.push('});');
	}
	out.push('');
	return out.join('\n');
}

function enumCode(e: Enum): string {
	const name = tsName(e);
	const entries = Object.entries(e.values).map(([k, v]) => `\t${k}: ${v}`);
	return [
		`export const ${name} = makeEnum({`,
		entries.join(',\n'),
		'});',
		`export type ${name}Name = ${enumNames(e)};`,
		''
	].join('\n');
}

/** The TypeScript module for the schema in dir */
export function generateProtos(dir = PROTO_DIR): string {
	const root = new protobuf.Root();
	root.loadSync(
		PROTO_FILES.map((f) => path.join(dir, f)),
		{ keepCase: false }
	);
	root.resolveAll();
	const enums: string[] = [];
	const messages: string[] = [];
	const walk = (ns: NamespaceBase) => {
		for (const obj of ns.nestedArray) {
			if (obj instanceof protobuf.Type) {
				if (obj.parent instanceof protobuf.Type) {
					throw new Error(`protos: nested message ${obj.fullName} is not supported`);
				}
				messages.push(messageCode(obj));
				walk(obj);
			} else if (obj instanceof protobuf.Enum) {
				enums.push(enumCode(obj));
			} else if (obj instanceof protobuf.Namespace) {
				walk(obj);
			}
		}
	};
	walk(root);
	return [
		'// Generated by vite-plugins/protos.ts from betterdesk-server/protos when Vite loads',
		'// its config (dev server, build, Vitest, svelte-kit sync). Not committed; do not edit.',
		"import { $Reader, $Writer, $util, assignProps, longNumber, makeEnum, type LongInput, type Reader, type Writer } from '../pb.ts';",
		'',
		...enums,
		...messages
	].join('\n');
}

/** Write the module when its text changed, so watchers see no change for nothing */
export function writeProtos(dir = PROTO_DIR, file = OUT_FILE): void {
	const text = generateProtos(dir);
	if (fs.existsSync(file) && fs.readFileSync(file, 'utf8') === text) return;
	fs.mkdirSync(path.dirname(file), { recursive: true });
	fs.writeFileSync(file, text);
}

export function protos(): Plugin {
	return {
		name: 'betterdesk-protos',
		config() {
			writeProtos();
		},
		configureServer(server) {
			server.watcher.add(PROTO_FILES.map((f) => path.join(PROTO_DIR, f)));
			server.watcher.on('change', (file) => {
				if (path.dirname(path.resolve(file)) === PROTO_DIR) writeProtos();
			});
		}
	};
}
