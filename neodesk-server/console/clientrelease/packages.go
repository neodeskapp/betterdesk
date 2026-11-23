package clientrelease

import (
	"regexp"
	"strings"
)

// The kinds of code a package is handed out for (console/onboard).
const (
	KindJoin    = "join"
	KindSupport = "support"
)

// Publisher is the name Windows shows for the signed client.
const Publisher = "Rath & Blume GbR"

// The release files that carry the checksums and their signature: the
// sha256sum lines of every package, and the ed25519 signature of those
// bytes in base64.
const (
	SumsFile      = "SHA256SUMS"
	SignatureFile = "SHA256SUMS.sig"
)

// Package is one client package a code can be downloaded as.
type Package struct {
	// OS is windows, macos or linux; Arch x64 or arm64.
	OS   string `json:"os"`
	Arch string `json:"arch"`
	// Format is what the visitor gets: exe, dmg, deb, rpm or tar.gz.
	Format string `json:"format"`
	// Kinds are the codes it is for.
	Kinds []string `json:"-"`
	// asset matches the file's name in a release of the client fork; its
	// first group is the version.
	asset *regexp.Regexp
}

// For reports whether the package is handed out for codes of the kind.
func (p Package) For(kind string) bool {
	for _, k := range p.Kinds {
		if k == kind {
			return true
		}
	}
	return false
}

// Ext is the extension of the file name the package is served under.
func (p Package) Ext() string { return "." + p.Format }

// Packages are the packages of the client fork's releases
// (scripts/collect_desktop_dist.sh in neodeskapp/neodesk-client),
// in the order the public pages offer them. The Windows exe runs without
// an install and installs itself, so it serves both kinds; the client
// tells the two apart by its file name. The Linux packages install, the
// portable tar.gz runs from its folder.
var Packages = []Package{
	{OS: "windows", Arch: "x64", Format: "exe", Kinds: []string{KindJoin, KindSupport}, asset: assetPattern("windows-x86_64", "exe")},
	{OS: "windows", Arch: "arm64", Format: "exe", Kinds: []string{KindJoin, KindSupport}, asset: assetPattern("windows-aarch64", "exe")},
	{OS: "macos", Arch: "arm64", Format: "dmg", Kinds: []string{KindJoin, KindSupport}, asset: assetPattern("macos-aarch64", "dmg")},
	{OS: "macos", Arch: "x64", Format: "dmg", Kinds: []string{KindJoin, KindSupport}, asset: assetPattern("macos-x86_64", "dmg")},
	{OS: "linux", Arch: "x64", Format: "deb", Kinds: []string{KindJoin}, asset: assetPattern("linux-x86_64", "deb")},
	{OS: "linux", Arch: "x64", Format: "rpm", Kinds: []string{KindJoin}, asset: assetPattern("linux-x86_64", "rpm")},
	{OS: "linux", Arch: "x64", Format: "tar.gz", Kinds: []string{KindSupport}, asset: assetPattern("linux-x86_64-portable", "tar.gz")},
	{OS: "linux", Arch: "arm64", Format: "deb", Kinds: []string{KindJoin}, asset: assetPattern("linux-aarch64", "deb")},
	{OS: "linux", Arch: "arm64", Format: "rpm", Kinds: []string{KindJoin}, asset: assetPattern("linux-aarch64", "rpm")},
	{OS: "linux", Arch: "arm64", Format: "tar.gz", Kinds: []string{KindSupport}, asset: assetPattern("linux-aarch64-portable", "tar.gz")},
}

// assetPattern matches neodesk-<version>-<platform>.<ext>.
func assetPattern(platform, ext string) *regexp.Regexp {
	return regexp.MustCompile(`^neodesk-([0-9][0-9A-Za-z.+_-]*)-` + regexp.QuoteMeta(platform) + `\.` + regexp.QuoteMeta(ext) + `$`)
}

// FindPackage returns the package for a system, processor and format; an
// empty format takes the first package of the kind for that system and
// processor.
func FindPackage(kind, os, arch, format string) (Package, bool) {
	for _, p := range Packages {
		if p.OS == os && p.Arch == arch && p.For(kind) && (format == "" || p.Format == format) {
			return p, true
		}
	}
	return Package{}, false
}

// matchAsset returns the package a release file is and its version.
func matchAsset(name string) (Package, string, bool) {
	for _, p := range Packages {
		if m := p.asset.FindStringSubmatch(name); m != nil {
			return p, m[1], true
		}
	}
	return Package{}, "", false
}

// key names a package in maps.
func (p Package) key() string { return strings.Join([]string{p.OS, p.Arch, p.Format}, "/") }
