// Package clientrelease keeps the official client packages that join and
// support codes hand out. The server takes them from the releases of the
// client fork (NEODESK_CLIENT_REPO, neodeskapp/neodesk-client
// when unset; NEODESK_CLIENT_RELEASE_TAG picks a release, else the
// latest, else the newest pre-release while the fork has no stable
// release), checks the release's SHA256SUMS against its ed25519 signature
// (SHA256SUMS.sig, keys.go), downloads each package the first time it is
// asked for, checks its SHA-256 against SHA256SUMS and keeps it in the
// data folder (client-releases/<tag>/). A release without a valid
// signature is not used.
//
// NEODESK_CLIENT_RELEASE_DIR names a folder that holds the packages
// instead, for tests and for servers without access to GitHub: its files
// are used under their release names, checked against a SHA256SUMS in
// the folder when there is one and against its signature when there is a
// SHA256SUMS.sig.
package clientrelease

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/updater"
)

// DefaultRepo is the client fork.
const DefaultRepo = "neodeskapp/neodesk-client"

// The settings the service reads.
const (
	EnvRepo     = "NEODESK_CLIENT_REPO"
	EnvTag      = "NEODESK_CLIENT_RELEASE_TAG"
	EnvLocalDir = "NEODESK_CLIENT_RELEASE_DIR"
)

const (
	// refreshAfter is how long a release is used before GitHub is asked
	// again; retryAfter how long after a failed attempt.
	refreshAfter = 6 * time.Hour
	retryAfter   = 5 * time.Minute
	// The largest files read.
	maxMetadata    = 1 << 20
	maxReleaseList = 8 << 20
	maxPackage     = 1 << 30
	// downloadTimeout bounds one package download.
	downloadTimeout = 15 * time.Minute
)

// ErrUnavailable means no checked release is at hand: none was found,
// GitHub could not be reached, or its signature did not verify.
var ErrUnavailable = errors.New("release_unavailable")

// ErrNoPackage means the release has no package for that system.
var ErrNoPackage = errors.New("no_package")

// githubHosts are the hosts release downloads may go to.
var githubHosts = map[string]bool{
	"api.github.com":                       true,
	"github.com":                           true,
	"objects.githubusercontent.com":        true,
	"release-assets.githubusercontent.com": true,
}

// Options are the service's settings.
type Options struct {
	// DataDir holds the cache, in client-releases.
	DataDir string
	// Getenv reads the settings; nil reads none.
	Getenv func(string) string
	// HTTP makes the requests; nil takes a client of its own. Its
	// redirects are checked against AllowedHosts either way.
	HTTP *http.Client
	// APIBase is GitHub's API, https://api.github.com when empty.
	APIBase string
	// AllowedHosts are the hosts requests may go to, GitHub's when nil.
	AllowedHosts map[string]bool
	// Now is the clock; nil is time.Now.
	Now func() time.Time
	// Logf gets a line for each release taken and each one refused.
	Logf func(format string, args ...any)
}

// Offer is a package of the current release.
type Offer struct {
	Package
	Version string `json:"version"`
	Size    int64  `json:"size"`
}

// Status is what an admin sees of the cache.
type Status struct {
	Source   string `json:"source"`
	Repo     string `json:"repo,omitempty"`
	Folder   string `json:"folder,omitempty"`
	Tag      string `json:"tag"`
	Verified bool   `json:"verified"`
	Packages int    `json:"packages"`
	Error    string `json:"error,omitempty"`
}

// asset is one package file of a release.
type asset struct {
	pkg     Package
	name    string
	version string
	size    int64
	url     string // GitHub
	path    string // the folder
}

// release is a release whose checksums were checked.
type release struct {
	tag      string
	source   string // github or folder
	verified bool
	sums     map[string]string
	assets   map[string]asset // by Package.key
}

// Service is the cache.
type Service struct {
	opts Options
	http *http.Client

	refreshMu sync.Mutex // one look at GitHub at a time

	mu      sync.Mutex
	current *release
	// nextLook is when GitHub is asked again; looking is true while it is.
	nextLook time.Time
	looking  bool
	lastErr  string
	// hashed remembers files whose hash was checked, by path, with the
	// size and time they had then.
	hashed   map[string]fileStamp
	inFlight map[string]*sync.Mutex
}

type fileStamp struct {
	size int64
	mod  time.Time
}

// New returns the cache.
func New(opts Options) *Service {
	if opts.Getenv == nil {
		opts.Getenv = func(string) string { return "" }
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.APIBase == "" {
		opts.APIBase = "https://api.github.com"
	}
	if opts.AllowedHosts == nil {
		opts.AllowedHosts = githubHosts
	}
	if opts.Logf == nil {
		opts.Logf = func(string, ...any) {}
	}
	s := &Service{opts: opts, hashed: map[string]fileStamp{}, inFlight: map[string]*sync.Mutex{}}
	base := opts.HTTP
	if base == nil {
		base = &http.Client{}
	}
	client := *base
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		return s.checkURL(req.URL)
	}
	s.http = &client
	return s
}

// Repo is the client fork the releases come from.
func (s *Service) Repo() string {
	if repo := strings.TrimSpace(s.opts.Getenv(EnvRepo)); repo != "" {
		return repo
	}
	return DefaultRepo
}

func (s *Service) localDir() string { return strings.TrimSpace(s.opts.Getenv(EnvLocalDir)) }

func (s *Service) keys() updater.Keys { return Keys(s.opts.Getenv(EnvPublicKey)) }

func (s *Service) cacheDir() string { return filepath.Join(s.opts.DataDir, "client-releases") }

// checkURL refuses an address that is not https on an allowed host.
func (s *Service) checkURL(u *url.URL) error {
	if u.Scheme != "https" || u.User != nil || !s.opts.AllowedHosts[strings.ToLower(u.Hostname())] {
		return fmt.Errorf("release download from %s is not allowed", u.Redacted())
	}
	return nil
}

// Offers returns the packages of the current release for codes of the
// kind, in the order of Packages.
func (s *Service) Offers(ctx context.Context, kind string) ([]Offer, error) {
	rel, err := s.release(ctx)
	if err != nil {
		return nil, err
	}
	var out []Offer
	for _, p := range Packages {
		if a, ok := rel.assets[p.key()]; ok && p.For(kind) {
			out = append(out, Offer{Package: p, Version: a.version, Size: a.size})
		}
	}
	return out, nil
}

// Status reports the source and the release in use. It does not ask
// GitHub: a release shows once a page or a download asked for it. A
// folder is read.
func (s *Service) Status(ctx context.Context) Status {
	st := Status{Source: "github", Repo: s.Repo()}
	var rel *release
	if dir := s.localDir(); dir != "" {
		st = Status{Source: "folder", Folder: dir}
		rel, _ = s.release(ctx)
	} else {
		s.mu.Lock()
		rel = s.current
		s.mu.Unlock()
	}
	if rel != nil {
		st.Tag, st.Verified, st.Packages = rel.tag, rel.verified, len(rel.assets)
	}
	s.mu.Lock()
	st.Error = s.lastErr
	s.mu.Unlock()
	return st
}

// Open returns the package file for the kind and system, checked, and
// what it is. The caller closes the file.
func (s *Service) Open(ctx context.Context, kind, osName, arch, format string) (*os.File, Offer, error) {
	pkg, ok := FindPackage(kind, osName, arch, format)
	if !ok {
		return nil, Offer{}, ErrNoPackage
	}
	rel, err := s.release(ctx)
	if err != nil {
		return nil, Offer{}, err
	}
	a, ok := rel.assets[pkg.key()]
	if !ok {
		return nil, Offer{}, ErrNoPackage
	}
	path, err := s.ensureFile(ctx, rel, a)
	if err != nil {
		return nil, Offer{}, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, Offer{}, fmt.Errorf("open package: %w", err)
	}
	return f, Offer{Package: pkg, Version: a.version, Size: a.size}, nil
}

// release returns the release in use, looking at the source when the last
// look is older than refreshAfter (retryAfter after a failure). A folder
// is read again on every call.
func (s *Service) release(ctx context.Context) (*release, error) {
	if dir := s.localDir(); dir != "" {
		rel, err := s.readFolder(dir)
		s.mu.Lock()
		if err != nil {
			s.lastErr = err.Error()
		} else {
			s.lastErr = ""
		}
		s.mu.Unlock()
		if err != nil {
			return nil, ErrUnavailable
		}
		return rel, nil
	}

	s.mu.Lock()
	rel, due := s.current, !s.opts.Now().Before(s.nextLook)
	if rel == nil {
		if stored, err := s.loadStored(); err == nil {
			s.current, rel = stored, stored
		}
	}
	start := due && !s.looking
	if start {
		s.looking = true
	}
	s.mu.Unlock()

	switch {
	case rel != nil && start:
		// The release at hand serves while GitHub is asked.
		go s.look(context.WithoutCancel(ctx))
		return rel, nil
	case rel != nil:
		return rel, nil
	case start:
		if fresh := s.look(ctx); fresh != nil {
			return fresh, nil
		}
		return nil, ErrUnavailable
	}
	// Another request is asking GitHub: wait for its answer.
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current == nil {
		return nil, ErrUnavailable
	}
	return s.current, nil
}

// look asks GitHub for the release once and takes it when it checks out.
// It returns the release in use afterwards.
func (s *Service) look(ctx context.Context) *release {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	fresh, err := s.fetchGitHub(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.looking = false
	now := s.opts.Now()
	if err != nil {
		s.lastErr = err.Error()
		s.nextLook = now.Add(retryAfter)
		s.opts.Logf("WARN: client release of %s not taken: %v", s.Repo(), err)
		return s.current
	}
	if s.current == nil || s.current.tag != fresh.tag {
		s.opts.Logf("Client release %s of %s taken (%d packages)", fresh.tag, s.Repo(), len(fresh.assets))
	}
	s.current, s.nextLook, s.lastErr = fresh, now.Add(refreshAfter), ""
	return fresh
}

// githubRelease is the part of GitHub's release JSON the cache reads.
type githubRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

// tagPattern is a tag that can name a folder.
var tagPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,99}$`)

// fetchGitHub reads the release from GitHub and checks its signature.
func (s *Service) fetchGitHub(ctx context.Context) (*release, error) {
	repo := s.Repo()
	if !regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`).MatchString(repo) {
		return nil, fmt.Errorf("%s %q is no owner/name", EnvRepo, repo)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	raw, err := s.readRelease(ctx, repo)
	if err != nil {
		return nil, fmt.Errorf("read the release: %w", err)
	}
	var gh githubRelease
	if err := json.Unmarshal(raw, &gh); err != nil {
		return nil, fmt.Errorf("read the release: %w", err)
	}
	if !tagPattern.MatchString(gh.TagName) {
		return nil, fmt.Errorf("release tag %q cannot be used", gh.TagName)
	}
	urls := map[string]string{}
	sizes := map[string]int64{}
	for _, a := range gh.Assets {
		urls[a.Name], sizes[a.Name] = a.URL, a.Size
	}
	if urls[SumsFile] == "" || urls[SignatureFile] == "" {
		return nil, fmt.Errorf("release %s has no %s and %s", gh.TagName, SumsFile, SignatureFile)
	}
	sums, err := s.get(ctx, urls[SumsFile], maxMetadata, false)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", SumsFile, err)
	}
	sig, err := s.get(ctx, urls[SignatureFile], maxMetadata, false)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", SignatureFile, err)
	}
	rel, err := s.checkedRelease(gh.TagName, sums, sig, true)
	if err != nil {
		return nil, err
	}
	for name, u := range urls {
		pkg, version, ok := matchAsset(name)
		if !ok || rel.sums[name] == "" {
			continue
		}
		rel.assets[pkg.key()] = asset{pkg: pkg, name: name, version: version, size: sizes[name], url: u}
	}
	if err := s.store(rel, gh, sums, sig); err != nil {
		s.opts.Logf("WARN: client release %s not stored: %v", rel.tag, err)
	}
	return rel, nil
}

// readRelease reads the JSON of the release to use: the tag EnvTag names,
// else the latest release, else, while the fork has published no stable
// release (GitHub answers 404 for latest), the newest pre-release that is
// no draft and whose tag can name a folder.
func (s *Service) readRelease(ctx context.Context, repo string) ([]byte, error) {
	base := s.opts.APIBase + "/repos/" + repo + "/releases"
	if tag := strings.TrimSpace(s.opts.Getenv(EnvTag)); tag != "" {
		return s.get(ctx, base+"/tags/"+url.PathEscape(tag), maxMetadata, true)
	}
	raw, err := s.get(ctx, base+"/latest", maxMetadata, true)
	var status *statusError
	if !errors.As(err, &status) || status.code != http.StatusNotFound {
		return raw, err
	}
	list, err := s.get(ctx, base+"?per_page=20", maxReleaseList, true)
	if err != nil {
		return nil, err
	}
	var releases []json.RawMessage
	if err := json.Unmarshal(list, &releases); err != nil {
		return nil, fmt.Errorf("the release list: %w", err)
	}
	for _, r := range releases {
		var head struct {
			TagName    string `json:"tag_name"`
			Draft      bool   `json:"draft"`
			Prerelease bool   `json:"prerelease"`
		}
		if json.Unmarshal(r, &head) == nil && !head.Draft && head.Prerelease && tagPattern.MatchString(head.TagName) {
			return r, nil
		}
	}
	return nil, fmt.Errorf("%s has no published release or pre-release", repo)
}

// statusError is an answer other than 200.
type statusError struct {
	url  string
	code int
}

func (e *statusError) Error() string { return fmt.Sprintf("%s answered %d", e.url, e.code) }

// checkedRelease reads SHA256SUMS after its signature verified. With
// signed false a missing signature is allowed (a folder).
func (s *Service) checkedRelease(tag string, sums, sig []byte, signed bool) (*release, error) {
	rel := &release{tag: tag, assets: map[string]asset{}, source: "github"}
	if sig != nil {
		if !updater.VerifySignature(sums, sig, s.keys()) {
			return nil, fmt.Errorf("the signature of %s of release %s does not verify", SumsFile, tag)
		}
		rel.verified = true
	} else if signed {
		return nil, fmt.Errorf("release %s has no %s", tag, SignatureFile)
	}
	parsed, err := parseSums(sums)
	if err != nil {
		return nil, fmt.Errorf("%s of release %s: %w", SumsFile, tag, err)
	}
	rel.sums = parsed
	return rel, nil
}

// parseSums reads sha256sum lines: 64 hex digits, a space, a space or an
// asterisk, the file name.
func parseSums(data []byte) (map[string]string, error) {
	out := map[string]string{}
	for i, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if len(line) < 67 || (line[65] != ' ' && line[65] != '*') || line[64] != ' ' {
			return nil, fmt.Errorf("line %d is no checksum line", i+1)
		}
		sum := strings.ToLower(line[:64])
		if _, err := hex.DecodeString(sum); err != nil {
			return nil, fmt.Errorf("line %d: %w", i+1, err)
		}
		name := strings.TrimPrefix(line[66:], "./")
		if name == "" || strings.ContainsAny(name, `/\`) {
			return nil, fmt.Errorf("line %d names no plain file", i+1)
		}
		out[name] = sum
	}
	return out, nil
}

// get reads a URL of at most limit bytes. The token goes to the API only.
func (s *Service) get(ctx context.Context, rawURL string, limit int64, api bool) ([]byte, error) {
	resp, err := s.request(ctx, rawURL, api)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s is larger than %d bytes", rawURL, limit)
	}
	return data, nil
}

func (s *Service) request(ctx context.Context, rawURL string, api bool) (*http.Response, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if err := s.checkURL(u); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "neodesk-server")
	if api {
		req.Header.Set("Accept", "application/vnd.github+json")
		token := s.opts.Getenv("NEODESK_GITHUB_TOKEN")
		if token == "" {
			token = s.opts.Getenv("GITHUB_TOKEN")
		}
		if token != "" && strings.EqualFold(u.Hostname(), "api.github.com") {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, &statusError{url: u.Redacted(), code: resp.StatusCode}
	}
	return resp, nil
}

// storedRelease is what the cache writes next to the packages, so a
// restart without GitHub still serves the release.
type storedRelease struct {
	Tag    string `json:"tag"`
	Assets []struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

// store writes the release's metadata into its folder and removes the
// folders of other releases.
func (s *Service) store(rel *release, gh githubRelease, sums, sig []byte) error {
	dir := filepath.Join(s.cacheDir(), rel.tag)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	meta, err := json.Marshal(storedRelease{Tag: gh.TagName, Assets: gh.Assets})
	if err != nil {
		return err
	}
	for name, data := range map[string][]byte{"release.json": meta, SumsFile: sums, SignatureFile: sig} {
		if err := writeAtomic(filepath.Join(dir, name), data); err != nil {
			return err
		}
	}
	if err := writeAtomic(filepath.Join(s.cacheDir(), "current"), []byte(rel.tag+"\n")); err != nil {
		return err
	}
	entries, err := os.ReadDir(s.cacheDir())
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() && e.Name() != rel.tag {
			_ = os.RemoveAll(filepath.Join(s.cacheDir(), e.Name()))
		}
	}
	return nil
}

// loadStored reads the release the cache stored, checked again.
func (s *Service) loadStored() (*release, error) {
	tagText, err := os.ReadFile(filepath.Join(s.cacheDir(), "current"))
	if err != nil {
		return nil, err
	}
	tag := strings.TrimSpace(string(tagText))
	if !tagPattern.MatchString(tag) {
		return nil, fmt.Errorf("stored tag %q", tag)
	}
	dir := filepath.Join(s.cacheDir(), tag)
	meta, err := os.ReadFile(filepath.Join(dir, "release.json"))
	if err != nil {
		return nil, err
	}
	sums, err := os.ReadFile(filepath.Join(dir, SumsFile))
	if err != nil {
		return nil, err
	}
	sig, err := os.ReadFile(filepath.Join(dir, SignatureFile))
	if err != nil {
		return nil, err
	}
	var stored storedRelease
	if err := json.Unmarshal(meta, &stored); err != nil {
		return nil, err
	}
	rel, err := s.checkedRelease(tag, sums, sig, true)
	if err != nil {
		return nil, err
	}
	for _, a := range stored.Assets {
		if pkg, version, ok := matchAsset(a.Name); ok && rel.sums[a.Name] != "" {
			rel.assets[pkg.key()] = asset{pkg: pkg, name: a.Name, version: version, size: a.Size, url: a.URL}
		}
	}
	return rel, nil
}

// readFolder makes a release of the packages in dir.
func (s *Service) readFolder(dir string) (*release, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", EnvLocalDir, err)
	}
	rel := &release{tag: "local", source: "folder", assets: map[string]asset{}}
	sums, sumsErr := os.ReadFile(filepath.Join(dir, SumsFile))
	if sumsErr == nil {
		sig, sigErr := os.ReadFile(filepath.Join(dir, SignatureFile))
		if sigErr != nil {
			sig = nil
		}
		checked, err := s.checkedRelease("local", sums, sig, false)
		if err != nil {
			return nil, err
		}
		rel.sums, rel.verified = checked.sums, checked.verified
	}
	var versions []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		pkg, version, ok := matchAsset(e.Name())
		if !ok || (rel.sums != nil && rel.sums[e.Name()] == "") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		rel.assets[pkg.key()] = asset{pkg: pkg, name: e.Name(), version: version, size: info.Size(), path: filepath.Join(dir, e.Name())}
		versions = append(versions, version)
	}
	if len(rel.assets) == 0 {
		return nil, fmt.Errorf("%s holds no client package", dir)
	}
	sort.Strings(versions)
	rel.tag = versions[len(versions)-1]
	return rel, nil
}

// ensureFile returns the path of a checked copy of the asset: the
// folder's file once its hash matched, or the cache's copy, downloaded
// the first time.
func (s *Service) ensureFile(ctx context.Context, rel *release, a asset) (string, error) {
	if a.path != "" {
		if rel.sums != nil {
			if err := s.checkHash(a.path, rel.sums[a.name]); err != nil {
				return "", err
			}
		}
		return a.path, nil
	}
	path := filepath.Join(s.cacheDir(), rel.tag, a.name)
	lock := s.fileLock(path)
	lock.Lock()
	defer lock.Unlock()
	if _, err := os.Stat(path); err == nil {
		if err := s.checkHash(path, rel.sums[a.name]); err == nil {
			return path, nil
		}
		_ = os.Remove(path)
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), downloadTimeout)
	defer cancel()
	if err := s.download(ctx, a, path, rel.sums[a.name]); err != nil {
		s.opts.Logf("WARN: client package %s not downloaded: %v", a.name, err)
		return "", ErrUnavailable
	}
	return path, nil
}

func (s *Service) fileLock(path string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	lock := s.inFlight[path]
	if lock == nil {
		lock = &sync.Mutex{}
		s.inFlight[path] = lock
	}
	return lock
}

// checkHash compares a file's SHA-256 with want, once per size and time
// of the file.
func (s *Service) checkHash(path, want string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	stamp := fileStamp{size: info.Size(), mod: info.ModTime()}
	s.mu.Lock()
	known, ok := s.hashed[path]
	s.mu.Unlock()
	if ok && known == stamp {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return fmt.Errorf("%s: SHA-256 %s does not match %s", filepath.Base(path), got, want)
	}
	s.mu.Lock()
	s.hashed[path] = stamp
	s.mu.Unlock()
	return nil
}

// download fetches the asset into path through a temporary file and keeps
// it only when its SHA-256 is want.
func (s *Service) download(ctx context.Context, a asset, path, want string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	resp, err := s.request(ctx, a.url, false)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	tmp, err := os.CreateTemp(filepath.Dir(path), ".download-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(resp.Body, maxPackage+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if n > maxPackage {
		return fmt.Errorf("larger than %d bytes", int64(maxPackage))
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return fmt.Errorf("SHA-256 %s does not match %s", got, want)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	if info, err := os.Stat(path); err == nil {
		s.mu.Lock()
		s.hashed[path] = fileStamp{size: info.Size(), mod: info.ModTime()}
		s.mu.Unlock()
	}
	return nil
}

func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".write-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
