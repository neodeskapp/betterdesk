# NeoDesk third-party notices

This file is the release index for third-party material distributed with
NeoDesk. It complements, but does not replace, the license text and notices
required by each dependency.

## Upstream project

NeoDesk started as a fork of BetterDesk by UNITRONIX
(<https://github.com/UNITRONIX/BetterDesk>), under AGPL-3.0. Most of the
server code is BetterDesk's, and its copyright lines and the licensing
information in `LICENSE` stay as UNITRONIX wrote them.

## Source manifests

The authoritative dependency manifests for a release are:

- `neodesk-server/go.mod` and `neodesk-server/go.sum`
- `web-ui/package.json` and `web-ui/package-lock.json`
- `sdks/nodejs/package.json` and `sdks/nodejs/package-lock.json`
- `sdks/python/pyproject.toml`
- `bridges/*/requirements.txt`

The build/release process must archive resolved dependency lists from those
manifests with the matching source revision as an SBOM.

## Desktop client

The NeoDesk desktop client is not in this repository. It is built in
[neodeskapp/neodesk-client](https://github.com/neodeskapp/neodesk-client)
under AGPL-3.0, and its dependency manifests and notices are there. The
server hands out its release packages for join and support codes
(`neodesk-server/console/clientrelease`): it downloads them from that
repository's releases, checks them against the signed checksums and serves
them unchanged. The console's Support agents page imports agent releases
from the same repository by default
(`neodesk-server/console/agentreleases`); those ship their own notices.

## Server code

The Go server in `neodesk-server/` links the modules in its `go.mod`. The
console inside it uses one of them directly that the server did not use
directly before:

- `golang.org/x/text` (its `collate` package), copyright The Go Authors,
  BSD 3-Clause License. It sorts the list of languages by name the way the
  console always has, also for names that are not in Latin script. The module
  was already part of the server through its database driver.

`neodesk-server/console/branding/idna_table.go` holds a table of how
host names outside ASCII are mapped (Unicode 17.0 IDNA mapping, as Node.js
22 applies it). It is data derived from the Unicode Character Database,
copyright Unicode, Inc., under the Unicode License v3.

The console's mail code in `neodesk-server/console/mail/` follows other
projects closely, so that mails and SMTP conversations stay what they were:

- Nodemailer 10.4.0 (address parser, MIME message builder, SMTP client),
  copyright Andris Reinman, MIT No Attribution License. The Go code is a
  port of the parts the console used.
- The domain handling in `mail/message` (`idna.go`, `host.go`) follows the
  URL library inside Node.js 22 (ada 2.9.2), copyright the Ada authors,
  Apache License 2.0 or MIT License. Its file `tables.bin.gz` is data
  derived from the Unicode Character Database (Unicode 15.0 and 17.0),
  copyright Unicode, Inc., under the Unicode License v3.

`neodesk-server/console/alerts/jsdate.go` reads dates the way
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
Playwright, ESLint, jiti and others) are not part of what the console serves.

## Web console fonts

The console self-hosts these fonts in `web-ui/static/fonts/`:

- IBM Plex Sans (Latin, Latin Extended, Cyrillic, Greek and Vietnamese
  subsets, variable weight) and IBM Plex Mono (Latin, Latin Extended and
  Cyrillic subsets, weights 400 and 600), SIL Open Font License 1.1,
  copyright IBM Corp. License text:
  `web-ui/static/fonts/IBMPlex-OFL.txt`.
- Material Symbols (subset), Apache License 2.0, see
  `web-ui/static/fonts/MATERIAL_SYMBOLS_VERSION.txt`.

Operating system marks in `web-ui/static/img/os-icons.svg` are from
Phosphor Icons (https://phosphoricons.com), MIT License, copyright Phosphor
Icons. The logos are trademarks of their owners and only name the operating
system a device runs.

Language flags in `web-ui/static/img/flags/` are from Twemoji
(https://github.com/jdecked/twemoji), taken from the `@twemoji/svg` 15.0.0
package. Graphics licensed under CC-BY 4.0
(https://creativecommons.org/licenses/by/4.0/), copyright Twitter, Inc and
other contributors. The files are renamed to the language code they stand
for; the drawings are unchanged.

## Compatibility components

Code that talks to the stock RustDesk client follows a protocol NeoDesk
did not design. The provenance register at
<https://neode.sk/docs/development/provenance> holds the whole policy.
`repotool check-provenance` checks that this section keeps its two parts.

### Clean-room workflow

The specification role records only public standards and black-box
observations of stock clients, with test vectors it writes itself. The
implementation role works from that specification and those vectors; it
receives no external source code, generated artifacts, copied comments or
test files derived from source. Every new wire feature names the revision of
the specification it implements.

### Release gate

No code, documentation or release note describes the compatibility code as a
fork, a clone, an independently licensable component or a clean-room
implementation until the register and its evidence have been reviewed.
Before a desktop-client compatibility adapter is distributed, its release
record must include:

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

