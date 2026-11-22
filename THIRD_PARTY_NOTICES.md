# BetterDesk third-party notices

This file is the release index for third-party material distributed with
BetterDesk. It complements, but does not replace, the license text and notices
required by each dependency.

## Source manifests

The authoritative dependency manifests for a release are:

- `betterdesk-server/go.mod` and `betterdesk-server/go.sum`
- `betterdesk-agent/go.mod` and `betterdesk-agent/go.sum`
- `betterdesk-support-agent/go.mod` and `betterdesk-support-agent/go.sum`
- `web-nodejs/package.json` and `web-nodejs/package-lock.json`
- `rdclient-desktop/package.json`, Cargo manifests, and
  `rdclient-desktop/vendor/wry/LICENSE.spdx`

The build/release process must archive resolved dependency lists from those
manifests with the matching source revision as an SBOM.

## Support Agent distribution

The Support Agent is built with Go and Fyne. Its code declares the dependency
licenses in [LICENSE](LICENSE); a release must also include the notices supplied
by Fyne and all resolved Go modules when they are distributed in a form that
requires them.

External system tools such as FFmpeg, GStreamer, PipeWire, xdg-desktop-portal,
xdotool, and ydotool are not silently relicensed by BetterDesk. A package may
only bundle one of them after its license, redistributability, provenance, and
security-update path are recorded in the release SBOM.

## Server code

The Go server in `betterdesk-server/` links the modules in its `go.mod`. The
console inside it uses one of them directly that the server did not use
directly before:

- `golang.org/x/text` (its `collate` package), copyright The Go Authors,
  BSD 3-Clause License. It sorts the list of languages by name the way the
  console always has, also for names that are not in Latin script. The module
  was already part of the server through its database driver.

`betterdesk-server/console/branding/idna_table.go` holds a table of how
host names outside ASCII are mapped (Unicode 17.0 IDNA mapping, as Node.js
22 applies it). It is data derived from the Unicode Character Database,
copyright Unicode, Inc., under the Unicode License v3.

The console's mail code in `betterdesk-server/console/mail/` follows other
projects closely, so that mails and SMTP conversations stay what they were:

- Nodemailer 10.4.0 (address parser, MIME message builder, SMTP client),
  copyright Andris Reinman, MIT No Attribution License. The Go code is a
  port of the parts the console used.
- The domain handling in `mail/message` (`idna.go`, `host.go`) follows the
  URL library inside Node.js 22 (ada 2.9.2), copyright the Ada authors,
  Apache License 2.0 or MIT License. Its file `tables.bin.gz` is data
  derived from the Unicode Character Database (Unicode 15.0 and 17.0),
  copyright Unicode, Inc., under the Unicode License v3.

`betterdesk-server/console/alerts/jsdate.go` reads dates the way
JavaScript's `Date.parse` does in Node.js. It follows the date parser of the
V8 engine, copyright the V8 project authors, BSD 3-Clause License.

## Web console code

The Svelte console in `web-ui/` bundles these npm packages into the code the
browser loads. They are under the MIT License unless noted; each package ships
its license, and the resolved versions are in `web-ui/package-lock.json`.

- Svelte, copyright Svelte contributors
- SvelteKit (`@sveltejs/kit`, its client runtime), copyright SvelteKit
  contributors
- devalue (used by SvelteKit), copyright devalue contributors
- clsx (used by Svelte), copyright Luke Edwards
- Valibot, copyright Fabian Hiller
- xterm.js (`@xterm/xterm` and `@xterm/addon-fit`), copyright the xterm.js
  authors, SourceLair Private Company and Christopher Jeffrey
- qrcode-generator, copyright Kazuhiko Arase
- protobuf.js (`protobufjs` and its `@protobufjs/*` parts), BSD-3-Clause,
  copyright Daniel Wirtz (the remote desktop viewer's RustDesk messages)
- JMuxer, MIT, copyright Samir Das (the remote desktop viewer's H.264
  playback without WebCodecs)

Each build writes the full license text of every bundled package to
`licenses.txt` next to the code (served at `/app/licenses.txt`), and the build
fails when a bundled package is under a license other than MIT, ISC, 0BSD,
BSD-2-Clause, BSD-3-Clause, Apache-2.0 or the Unlicense
(`web-ui/vite-plugins/licenses.ts`). Keep this list in step with that file.

TweetNaCl-js (`tweetnacl`, the remote desktop viewer's encryption) is public
domain under the Unlicense.

Build and test tools in `web-ui/package.json` (Vite, TypeScript, Vitest,
Playwright, ESLint and others) are not part of what the console serves.

## Web console fonts

The console self-hosts these fonts in `web-nodejs/public/fonts/`, and the
Svelte console the same files in `web-ui/static/fonts/`:

- IBM Plex Sans (Latin, Latin Extended, Cyrillic, Greek and Vietnamese
  subsets, variable weight) and IBM Plex Mono (Latin, Latin Extended and
  Cyrillic subsets, weights 400 and 600), SIL Open Font License 1.1,
  copyright IBM Corp. License text:
  `web-nodejs/public/fonts/IBMPlex-OFL.txt`.
- Material Symbols (subset), Apache License 2.0, see
  `web-nodejs/public/fonts/MATERIAL_SYMBOLS_VERSION.txt`.

Operating system marks in `web-nodejs/public/img/os-icons.svg` are from
Phosphor Icons (https://phosphoricons.com), MIT License, copyright Phosphor
Icons. The logos are trademarks of their owners and only name the operating
system a device runs.

Language flags in `web-nodejs/public/img/flags/` are from Twemoji
(https://github.com/jdecked/twemoji), taken from the `@twemoji/svg` 15.0.0
package. Graphics licensed under CC-BY 4.0
(https://creativecommons.org/licenses/by/4.0/), copyright Twitter, Inc and
other contributors. The files are renamed to the language code they stand
for; the drawings are unchanged.

The Svelte console has copies of the OS marks and the flags in
`web-ui/static/img/`.

## Compatibility components

Any desktop-client compatibility adapter is subject to
[support-agent-provenance.md](docs/important/support-agent-provenance.md).
Before distribution, its release record must include:

1. the compatibility specification revision;
2. a source-provenance review result;
3. generated-schema hashes;
4. third-party notices for its direct and transitive dependencies; and
5. a signed artifact checksum.

## Maintainer checklist

- Do not remove vendor license files from release inputs.
- Do not declare a dependency's license from memory; use its resolved package
  metadata and shipped notices.
- Do not add external source, generated protocol artifacts, or binary blobs
  without an entry in the SBOM and provenance review.
- Update this index when a release begins bundling a new runtime component.

