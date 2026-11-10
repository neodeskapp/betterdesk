// Package agentreleases keeps local copies of the releases of the generic
// BetterDesk Support Agent. The console never builds that client: it
// imports a release from GitHub, checks the signed manifest and the
// checksums of every file, stores it below the data folder and serves only
// that checked copy (GET /downloads/generic-agent/{tag}/{name}, and the
// installer list of the public portal).
//
// A release is a folder named after its tag in
// <DATA_DIR>/modules/betterdesk-support-releases with the release's files
// and its manifest.json, to which the import adds release_tag and
// imported_at.
package agentreleases

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"

	"github.com/unitronix/betterdesk-server/console/goapi"
)

const (
	// DefaultRepo is the GitHub repository releases come from.
	DefaultRepo = "UNITRONIX/BetterDesk-Client"
	// DefaultPublicKey is the public half of the key the release workflow
	// signs manifests with, in base64.
	DefaultPublicKey = "eXGPOvUvFfz1FGGbYogKfYEEWVWzeJEKpmI1ngAU490="

	releasePrefix   = "support-v"
	maxReleaseBytes = 2 * 1024 * 1024 * 1024
	// idleTimeout ends a request to GitHub that has been silent this long.
	idleTimeout = 30 * time.Second
	userAgent   = "BetterDesk-Console-Support-Releases"
)

// githubHosts are the hosts a request may be redirected to: the API, the
// site and the two hosts GitHub serves release files from.
var githubHosts = map[string]string{
	"api.github.com":                       "api.github.com",
	"github.com":                           "github.com",
	"objects.githubusercontent.com":        "objects.githubusercontent.com",
	"release-assets.githubusercontent.com": "release-assets.githubusercontent.com",
}

// ResolveAllowedGithubHost returns the host when a redirect may go there,
// and "" otherwise. The host is looked up in lower case. Like the plain
// object the old console looked it up in, the table also "has" the names
// constructor and __proto__ (BUGS AG1); such a host is kept as it is.
func ResolveAllowedGithubHost(host string) string {
	lower := strings.ToLower(host)
	if allowed, ok := githubHosts[lower]; ok {
		return allowed
	}
	if lower == "constructor" || lower == "__proto__" {
		return host
	}
	return ""
}

// GithubHosts lists the hosts of ResolveAllowedGithubHost.
func GithubHosts() []string {
	out := make([]string, 0, len(githubHosts))
	for host := range githubHosts {
		out = append(out, host)
	}
	sort.Strings(out)
	return out
}

// Options are the settings of a Service.
type Options struct {
	// DataDir is the console's data folder (DATA_DIR). Releases are kept
	// in DataDir/modules/betterdesk-support-releases.
	DataDir string
	// Repo is BETTERDESK_SUPPORT_RELEASE_REPO as set: empty means
	// DefaultRepo, anything else is trimmed.
	Repo string
	// PublicKey is BETTERDESK_SUPPORT_RELEASE_PUBLIC_KEY as set: empty
	// means DefaultPublicKey, anything else is trimmed.
	PublicKey string
	// HTTP sends the requests to GitHub. Nil means a direct HTTPS
	// connection without a proxy and without transparent compression.
	HTTP http.RoundTripper
	// ListTar lists the entries of a .tar.gz file, one per line, as
	// "tar -tzf -" does. Nil runs tar.
	ListTar func(ctx context.Context, data []byte) (string, error)
	// Now is the clock. time.Now when nil.
	Now func() time.Time
	// PID names the folder an import is staged in. os.Getpid when 0.
	PID int
}

// Service reads and imports the release cache. It is safe for concurrent
// use.
type Service struct {
	opts Options
	root string
}

// New returns a Service.
func New(opts Options) *Service {
	if opts.HTTP == nil {
		opts.HTTP = &http.Transport{
			DisableCompression:    true,
			TLSNextProto:          map[string]func(string, *tls.Conn) http.RoundTripper{},
			ResponseHeaderTimeout: idleTimeout,
		}
	}
	if opts.ListTar == nil {
		opts.ListTar = listTar
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.PID == 0 {
		opts.PID = os.Getpid()
	}
	return &Service{opts: opts, root: filepath.Join(opts.DataDir, "modules", "betterdesk-support-releases")}
}

// listTar runs tar -tzf - on data.
func listTar(ctx context.Context, data []byte) (string, error) {
	cmd := exec.CommandContext(ctx, "tar", "-tzf", "-")
	cmd.Stdin = strings.NewReader(string(data))
	out, err := cmd.Output()
	return string(out), err
}

// Root is the folder the releases are kept in.
func (s *Service) Root() string { return s.root }

func (s *Service) repo() string {
	if s.opts.Repo == "" {
		return DefaultRepo
	}
	return strings.TrimSpace(s.opts.Repo)
}

func (s *Service) publicKey() string {
	if s.opts.PublicKey == "" {
		return DefaultPublicKey
	}
	return jsTrim(s.opts.PublicKey)
}

// SigningKeyFingerprint names the key manifests must be signed with: the
// first 16 hex digits of the SHA-256 of its base64 text.
func (s *Service) SigningKeyFingerprint() string {
	sum := sha256.Sum256([]byte(s.publicKey()))
	return hex.EncodeToString(sum[:])[:16]
}

// KeyFromEnvironment reports whether the signing key was set in the
// environment rather than built in.
func (s *Service) KeyFromEnvironment() bool { return s.opts.PublicKey != "" }

var reSafeName = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// safeName is a value that may be a file name: text of letters, digits,
// dots, dashes and underscores, else "".
func safeName(v any) string {
	if name, ok := v.(string); ok && reSafeName.MatchString(name) {
		return name
	}
	return ""
}

// Release is a release in the cache, newest tag first in a list.
type Release struct {
	Tag string
	// ImportedAt is the manifest's imported_at, Undefined (left out of
	// the JSON) when it has none.
	ImportedAt any
	Manifest   any
}

// MarshalJSON writes {tag, imported_at, manifest}.
func (r Release) MarshalJSON() ([]byte, error) {
	o := goapi.NewObject("tag", r.Tag)
	if r.ImportedAt != undefined {
		o.Set("imported_at", r.ImportedAt)
	}
	o.Set("manifest", r.Manifest)
	return o.MarshalJSON()
}

// undefined marks a value JavaScript left undefined.
var undefined = &struct{ undefined bool }{true}

// ListCached returns the releases in the cache: every folder with a
// manifest.json that can be read, sorted by tag as text, highest first. A
// cache that cannot be read is empty.
func (s *Service) ListCached() []Release {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return []Release{}
	}
	releases := []Release{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(s.root, entry.Name(), "manifest.json"))
		if err != nil {
			continue
		}
		manifest, err := goapi.Parse([]byte(decodeUTF8(raw)))
		if err != nil || manifest == nil {
			// a manifest of null has no imported_at to read
			continue
		}
		var importedAt any = undefined
		if o, ok := manifest.(*goapi.Object); ok && o.Has("imported_at") {
			importedAt = o.Get("imported_at")
		}
		releases = append(releases, Release{Tag: entry.Name(), ImportedAt: importedAt, Manifest: manifest})
	}
	// String.prototype.localeCompare: the default collation of the root locale
	collator := collate.New(language.Und)
	sort.SliceStable(releases, func(i, j int) bool {
		return collator.CompareString(releases[j].Tag, releases[i].Tag) < 0
	})
	return releases
}

// CacheExists reports whether the cache folder exists.
func (s *Service) CacheExists() bool {
	_, err := os.Stat(s.root)
	return err == nil
}

// ResolveAsset returns the path of a file of a cached release, or "" when
// the tag or the name is no plain file name or there is no such file.
func (s *Service) ResolveAsset(tag, name string) string {
	safeTag, safeAsset := safeName(tag), safeName(name)
	if safeTag == "" || safeAsset == "" {
		return ""
	}
	candidate := filepath.Join(s.root, safeTag, safeAsset)
	rel, err := filepath.Rel(s.root, candidate)
	if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return ""
	}
	info, err := os.Stat(candidate)
	if err != nil || !info.Mode().IsRegular() {
		return ""
	}
	return candidate
}
