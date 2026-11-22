/**
 * The remote client's informational log lines, at the console level each
 * line had. Warnings and errors use console.warn and console.error directly.
 */
/* eslint-disable no-console */
export function debugLog(...args: unknown[]): void {
	console.log(...args);
}

export function debugInfo(...args: unknown[]): void {
	console.info(...args);
}

export function debugDebug(...args: unknown[]): void {
	console.debug(...args);
}
