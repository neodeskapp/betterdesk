#!/usr/bin/env bash
# Build a release of neodesk-server as the release workflow does, with a
# throwaway signing key, and check it: the binaries for every platform,
# manifest.json, manifest.sig and SHA256SUMS, "sha256sum -c", the
# signature as the Go updater checks it, the -version line the updater
# reads, and, when web-nodejs/lib/updateManifestSignature.js (upstream's
# verifier) is at hand, the signature as upstream's Node code checks it.
#
#   scripts/release-go-check.sh [output folder] [path of updateManifestSignature.js]
#
# Needs Go, sha256sum, openssl and, for the last check, node. Runs from
# the repository root.
set -euo pipefail
out="${1:-/tmp/release-check}"
node_verifier="${2:-web-nodejs/lib/updateManifestSignature.js}"
root="$(pwd)"
version="$(tr -d '[:space:]' < neodesk-server/internal/productversion/VERSION)"
commit="$(printf 'release-check-%s' "$version" | sha1sum | cut -c1-40)"

rm -rf "$out" && mkdir -p "$out"
echo "[release-check] building $version ($commit) into $out"
for target in linux/amd64 linux/arm64 linux/arm/7 windows/amd64; do
    IFS=/ read -r goos goarch goarm <<<"$target"
    name="neodesk-server-$goos-$goarch"
    [ "$goarch" = arm ] && name="neodesk-server-$goos-armv7"
    [ "$goos" = windows ] && name="$name.exe"
    (cd neodesk-server && CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" GOARM="${goarm:-}" go build -trimpath \
        -ldflags "-s -w -X main.Version=$version -X main.Commit=$commit -X main.BuildDate=2026-11-18" -o "$out/$name" .)
done

# A throwaway key: the seed is made here and thrown away with the folder.
seed="$(head -c 32 /dev/urandom | base64 -w0)"
public="$(cd neodesk-server && NEODESK_RELEASE_SIGNING_KEY="$seed" go run ./cmd/release-manifest \
    -dir "$out" -version "$version" -commit "$commit" -branch main | sed -n 's/^signed with the key //p')"
[ -n "$public" ] || { echo "[release-check] no signature was written"; exit 1; }
(cd neodesk-server && NEODESK_RELEASE_SIGNING_KEY="$seed" go run ./cmd/release-manifest \
    -dir "$out" -version "$version" -commit "$commit" -branch main -verify-key "$public" >/dev/null)
unset seed

echo "[release-check] sha256sum -c"
(cd "$out" && sha256sum -c SHA256SUMS)
echo "[release-check] the release as the updater checks it"
(cd neodesk-server && go run ./cmd/release-manifest -check -dir "$out" -branch main -verify-key "$public")
if (cd neodesk-server && go run ./cmd/release-manifest -check -dir "$out" -branch dev -verify-key "$public" >/dev/null 2>&1); then
    echo "[release-check] a stable release passed as a development one"; exit 1
fi
cp "$out/manifest.json" "$out/manifest.json.orig"
sed -i "s/\"version\": \"$version\"/\"version\": \"99.0.0\"/" "$out/manifest.json"
(cd neodesk-server && go run ./cmd/release-manifest -check -dir "$out" -branch main -verify-key "$public" >/dev/null) \
    || { echo "[release-check] the unsigned version field broke the signature check"; exit 1; }
mv "$out/manifest.json.orig" "$out/manifest.json"
printf 'x' >> "$out/neodesk-server-linux-arm64"
if (cd neodesk-server && go run ./cmd/release-manifest -check -dir "$out" -branch main -verify-key "$public" >/dev/null 2>&1); then
    echo "[release-check] a changed binary passed"; exit 1
fi
truncate -s -1 "$out/neodesk-server-linux-arm64"

line="$("$out/neodesk-server-linux-amd64" -version)"
echo "[release-check] -version: $line"
[ "$line" = "neodesk-server $version (built 2026-11-18, commit $commit)" ] || { echo "[release-check] unexpected -version line"; exit 1; }

if [ -f "$node_verifier" ] && command -v node >/dev/null; then
    echo "[release-check] the signature as upstream's Node verifier checks it"
    node -e '
        const fs = require("fs");
        const v = require(process.argv[1]);
        const m = JSON.parse(fs.readFileSync(process.argv[2] + "/manifest.json", "utf8"));
        const sig = fs.readFileSync(process.argv[2] + "/manifest.sig", "utf8");
        if (!v.verifyManifestSignature(m, sig, Buffer.from(process.argv[3], "base64"))) { console.error("does not verify"); process.exit(1); }
        console.log("verifies");
    ' "$(realpath "$node_verifier")" "$out" "$public"
else
    echo "[release-check] upstream's verifier is not at $node_verifier; skipped"
fi
echo "[release-check] ok: $(ls "$out" | tr '\n' ' ')"
