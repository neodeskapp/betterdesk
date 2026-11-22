/**
 * The protobuf.js runtime under the remote viewer's generated messages
 * (generated/protos.ts, written by vite-plugins/protos.ts).
 *
 * 64-bit fields read as plain numbers and byte fields as Uint8Array in
 * every environment, as protobuf.js does in a browser without long.js or
 * Node's Buffer. The bundle carries long.js (protobuf.js requires it), and
 * Node tests have Buffer, so both are switched off here before the
 * generated code runs.
 */
import * as $protobuf from 'protobufjs/minimal.js';
import type { Long } from 'protobufjs';

Object.assign($protobuf.util, { Long: null, Buffer: null });
$protobuf.configure();

export const $Reader = $protobuf.Reader;
export const $Writer = $protobuf.Writer;
export const $util = $protobuf.util;
export type Reader = $protobuf.Reader;
export type Writer = $protobuf.Writer;

/** A 64-bit field as read: a number here (see above), a Long only if long.js were on */
export function longNumber(value: number | Long, unsigned: boolean): number {
	return typeof value === 'number'
		? value
		: new $util.LongBits(value.low >>> 0, value.high >>> 0).toNumber(unsigned);
}

/** A 64-bit field in a plain object: a number, a decimal string or the bits of a Long */
export type LongInput = number | string | { low: number; high: number };

/** Copies the set properties of a plain object onto a new message, as protobuf.js constructors do */
export function assignProps(target: object, props: object): void {
	for (const [key, value] of Object.entries(props)) {
		if (value != null && key !== '__proto__') Reflect.set(target, key, value);
	}
}

/**
 * An enum as protobuf.js builds it: the names are own properties with their
 * numbers, the numbers map back to names through the prototype.
 */
export function makeEnum<const T extends Record<string, number>>(
	values: T
): Readonly<T> & { readonly [id: number]: string | undefined } {
	const byId: Record<number, string> = {};
	const out: Record<string, number> = Object.create(byId);
	for (const [name, id] of Object.entries(values)) {
		byId[id] = name;
		out[name] = id;
	}
	return out as Readonly<T> & { readonly [id: number]: string | undefined };
}
