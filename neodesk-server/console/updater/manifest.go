package updater

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

// Product is the product name a manifest and the binary's -version name.
const Product = "neodesk-server"

// maxManifestBytes and maxSignatureBytes cap what a download of the two
// small release files may be.
const (
	maxManifestBytes  = 1 << 20
	maxSignatureBytes = 4 << 10
	maxManifestFiles  = 64
)

// Manifest is manifest.json of a release. The fields owner, repo, branch,
// sha and files are the ones upstream's signature covers
// (web-nodejs/lib/updateManifestSignature.js on upstream dev): files
// lists every binary of the release by its asset name with its SHA-256,
// sha is the commit it was built from, branch the branch of its channel.
// The other fields are not signed; the updater only trusts them where the
// signed binary confirms them (its -version names the version and the
// commit).
type Manifest struct {
	SchemaVersion      int            `json:"schema_version"`
	Product            string         `json:"product"`
	Version            string         `json:"version"`
	Channel            string         `json:"channel"`
	Created            string         `json:"created"`
	MinRollbackVersion string         `json:"min_rollback_version,omitempty"`
	Owner              string         `json:"owner"`
	Repo               string         `json:"repo"`
	Branch             string         `json:"branch"`
	SHA                string         `json:"sha"`
	Files              []ManifestFile `json:"files"`
}

// ManifestFile is one binary of a release.
type ManifestFile struct {
	// Path is the asset's file name, neodesk-server-<os>-<arch>[.exe].
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	// Size, OS and Arch are not signed: Size only limits the download.
	Size int64  `json:"size,omitempty"`
	OS   string `json:"os,omitempty"`
	Arch string `json:"arch,omitempty"`
}

var (
	reVersion   = regexp.MustCompile(`^[0-9]{1,6}\.[0-9]{1,6}\.[0-9]{1,6}(-[0-9A-Za-z][0-9A-Za-z.-]{0,63})?$`)
	reCommit    = regexp.MustCompile(`^[0-9a-f]{40}$`)
	reSHA256    = regexp.MustCompile(`^[0-9a-f]{64}$`)
	reAssetName = regexp.MustCompile(`^neodesk-server-[a-z0-9]{1,16}-[a-z0-9]{1,16}(\.exe)?$`)
	reOwnerRepo = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}$`)
)

// ErrManifest is a manifest the updater refuses. The message says why.
type ErrManifest struct{ Reason string }

func (e *ErrManifest) Error() string { return "release manifest refused: " + e.Reason }

func refuseManifest(format string, args ...any) error {
	return &ErrManifest{Reason: fmt.Sprintf(format, args...)}
}

// ManifestRules is what a manifest has to match besides its own form.
type ManifestRules struct {
	Owner, Repo, Branch string
}

// ParseManifest reads manifest.json and checks its form and that it is a
// release of the configured repository and branch: schema 1, the
// product, a version X.Y.Z with an optional suffix, a commit of 40
// lower-case hex digits, between one and 64 binaries with plain names and
// SHA-256 sums, none named twice. Nothing of the manifest is used before
// it passed.
func ParseManifest(data []byte, rules ManifestRules) (*Manifest, error) {
	if len(data) > maxManifestBytes {
		return nil, refuseManifest("larger than %d bytes", maxManifestBytes)
	}
	var m Manifest
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&m); err != nil {
		return nil, refuseManifest("not JSON of the expected form: %v", err)
	}
	if dec.More() {
		return nil, refuseManifest("text after the JSON object")
	}
	switch {
	case m.SchemaVersion != 1:
		return nil, refuseManifest("schema_version %d is not 1", m.SchemaVersion)
	case m.Product != Product:
		return nil, refuseManifest("product %q is not %s", m.Product, Product)
	case !reVersion.MatchString(m.Version):
		return nil, refuseManifest("version %q is not X.Y.Z", m.Version)
	case m.MinRollbackVersion != "" && !reVersion.MatchString(m.MinRollbackVersion):
		return nil, refuseManifest("min_rollback_version %q is not X.Y.Z", m.MinRollbackVersion)
	case !reCommit.MatchString(m.SHA):
		return nil, refuseManifest("sha is not a commit of 40 hex digits")
	case !reOwnerRepo.MatchString(m.Owner) || !strings.EqualFold(m.Owner, rules.Owner):
		return nil, refuseManifest("owner %q is not %s", m.Owner, rules.Owner)
	case !reOwnerRepo.MatchString(m.Repo) || !strings.EqualFold(m.Repo, rules.Repo):
		return nil, refuseManifest("repo %q is not %s", m.Repo, rules.Repo)
	case m.Branch != rules.Branch:
		return nil, refuseManifest("branch %q is not %s", m.Branch, rules.Branch)
	case m.Channel != ChannelOf(m.Branch):
		return nil, refuseManifest("channel %q does not belong to branch %s", m.Channel, m.Branch)
	case len(m.Files) == 0 || len(m.Files) > maxManifestFiles:
		return nil, refuseManifest("%d files, expected 1 to %d", len(m.Files), maxManifestFiles)
	}
	seen := map[string]bool{}
	for _, f := range m.Files {
		if !reAssetName.MatchString(f.Path) {
			return nil, refuseManifest("file name %q is not a server binary", f.Path)
		}
		if !reSHA256.MatchString(f.SHA256) {
			return nil, refuseManifest("sha256 of %s is not 64 lower-case hex digits", f.Path)
		}
		if f.Size < 0 {
			return nil, refuseManifest("size of %s is negative", f.Path)
		}
		if seen[f.Path] {
			return nil, refuseManifest("%s is listed twice", f.Path)
		}
		seen[f.Path] = true
	}
	return &m, nil
}

// File is the entry for one asset name, nil when the release has none.
func (m *Manifest) File(name string) *ManifestFile {
	for i := range m.Files {
		if m.Files[i].Path == name {
			return &m.Files[i]
		}
	}
	return nil
}

// SignedFile is one entry of the signed file list.
type SignedFile struct {
	Path, SHA256 string
}

// SignedFields are the parts of a manifest upstream's signature covers.
type SignedFields struct {
	Owner, Repo, Branch, SHA string
	Files                    []SignedFile
}

// Signed returns the signed fields of a parsed manifest.
func (m *Manifest) Signed() SignedFields {
	files := make([]SignedFile, len(m.Files))
	for i, f := range m.Files {
		files[i] = SignedFile{Path: f.Path, SHA256: f.SHA256}
	}
	return SignedFields{Owner: m.Owner, Repo: m.Repo, Branch: m.Branch, SHA: m.SHA, Files: files}
}

// manifestDomain is the first line of the signed text, so that a
// signature made for something else never passes as a release signature.
const manifestDomain = "neodesk-update-manifest-v1"

// CanonicalPayload is the text the signature is made over, built the way
// upstream's canonicalPayload builds it, with NeoDesk's first line:
//
//	neodesk-update-manifest-v1
//	owner=<owner>
//	repo=<repo>
//	branch=<branch>
//	sha=<sha>
//	files=<count>
//	<sha256>  <path>
//	...
//
// Each line ends with "\n". In a path every backslash becomes "/", a
// sha256 is written in lower case, a file without path or sha256 is left
// out, and the files are sorted by path as JavaScript compares strings
// (by UTF-16 code units), files with equal paths in their given order.
// The file lines have the form sha256sum writes.
func CanonicalPayload(f SignedFields) []byte {
	type line struct{ path, sum string }
	files := make([]line, 0, len(f.Files))
	for _, file := range f.Files {
		l := line{path: strings.ReplaceAll(file.Path, `\`, "/"), sum: strings.ToLower(file.SHA256)}
		if l.path != "" && l.sum != "" {
			files = append(files, l)
		}
	}
	sort.SliceStable(files, func(i, j int) bool { return lessUTF16(files[i].path, files[j].path) })
	var b strings.Builder
	b.WriteString(manifestDomain + "\n")
	b.WriteString("owner=" + f.Owner + "\n")
	b.WriteString("repo=" + f.Repo + "\n")
	b.WriteString("branch=" + f.Branch + "\n")
	b.WriteString("sha=" + f.SHA + "\n")
	b.WriteString("files=" + strconv.Itoa(len(files)) + "\n")
	for _, l := range files {
		b.WriteString(l.sum + "  " + l.path + "\n")
	}
	return []byte(b.String())
}

// lessUTF16 is a < b for JavaScript strings.
func lessUTF16(a, b string) bool {
	if a == b {
		return false
	}
	ua, ub := utf16.Encode([]rune(a)), utf16.Encode([]rune(b))
	for i := 0; i < len(ua) && i < len(ub); i++ {
		if ua[i] != ub[i] {
			return ua[i] < ub[i]
		}
	}
	return len(ua) < len(ub)
}

// errNoManifest is a release without manifest.json.
var errNoManifest = errors.New("the release has no manifest.json")
