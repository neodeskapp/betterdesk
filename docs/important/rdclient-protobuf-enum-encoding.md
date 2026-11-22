# rdclient protobuf enum encoding pitfall

- web-ui/src/lib/remote/client/protocol.ts `serializeMessage` MUST use
  `Message.fromObject(obj)` (NOT `Message.create(obj)`) before `.encode()`.
- protobuf.js `create()` does NOT convert string enum names -> numeric values.
  So `keyEvent.controlKey: 'Backspace'` / `mode: 'Legacy'` encoded as 0 (Unknown)
  -> all non-character keys silently dropped by peer; only numeric `chr` letters worked.
- `fromObject()` converts enum name strings to numbers AND preserves Uint8Array
  byte fields (clipboard content, file blocks, password, pk). Verified round-trip.
- Fixed in commit 1411719 (main).
- keyboard-encoder.ts KEY_MAP sends ControlKey NAME strings (Backspace/Return/Space/...).
  ControlKey enum values: Backspace=2, Return=27, Space=30, Tab=31, Alt=1,
  Control=4, Shift=29, Meta=23, Escape=8 (see betterdesk-server/protos/message.proto).
- The message classes are generated at build time from betterdesk-server/protos
  (web-ui/vite-plugins/protos.ts writes src/lib/remote/client/generated/, not
  committed) on the protobufjs npm runtime.
  web-ui/tests/unit/remote/wire-bytes.test.ts pins the bytes of every message
  the viewer sends.
