package updater

import (
	"bufio"
	"bytes"
	"strings"
)

// A release's SHA256SUMS lists every file of the release: the binaries,
// manifest.json and manifest.sig, and the installers and compose files the
// scripts take from it (install.sh, neodesk.sh, neodesk.ps1,
// neodesk-docker.sh, docker-compose*.yml). SHA256SUMS.sig is the release
// key's signature of SumsPayload(SHA256SUMS), so the scripts can check an
// installer they update themselves with as the binary is checked. The
// manifest stays the binaries' own: its form is what installed updaters
// read.

// sumsPayloadHeader comes before SHA256SUMS in what SHA256SUMS.sig signs,
// so the signature of a file list never passes for a manifest's.
const sumsPayloadHeader = "neodesk-release-files-v1\n"

// SumsPayload is what SHA256SUMS.sig signs: SHA256SUMS behind a line that
// names it.
func SumsPayload(sums []byte) []byte {
	return append([]byte(sumsPayloadHeader), sums...)
}

// SumOf returns the SHA-256 that SHA256SUMS lists for name, in lower case,
// or "" when the list has no such file. Lines are "<sha256>  <name>" or
// "<sha256> *<name>", as sha256sum writes them.
func SumOf(sums []byte, name string) string {
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		sum, rest, ok := strings.Cut(strings.TrimRight(sc.Text(), "\r"), " ")
		if !ok || len(sum) != 64 {
			continue
		}
		if strings.TrimPrefix(strings.TrimPrefix(rest, " "), "*") == name {
			return strings.ToLower(sum)
		}
	}
	return ""
}
