/**
 * Stands in for long.js, which protobuf.js asks for (vite.config.ts aliases
 * "long" here). The viewer reads 64-bit fields as numbers (pb.ts turns
 * protobuf.js's Long off), and long.js would start a small WebAssembly
 * module when it loads, which the console's Content-Security-Policy
 * refuses (script-src has no 'wasm-unsafe-eval'). Without isLong,
 * protobuf.js finds no Long.
 */
export {};
