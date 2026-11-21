package agentreleases

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/unitronix/betterdesk-server/console/branding"
	"github.com/unitronix/betterdesk-server/console/goapi"
)

// Imported is a release ImportLatest stored.
type Imported struct {
	Tag string
	// Manifest is the release's manifest as it was signed.
	Manifest any
	Path     string
}

// ImportLatest imports the newest release of the repository whose tag
// starts with support-v and that is no draft and no pre-release. Before
// anything is downloaded it checks the tag (checkReleaseTag). It checks
// the manifest's signature with the signing key, the manifest itself, and
// the size and SHA-256 of every file against the manifest and SHA256SUMS,
// and refuses archives that carry a custom.txt. The files go into a
// staging folder that is renamed to the tag's folder at the end. The
// errors carry the codes the console's answer shows
// (generic_agent_release_...).
func (s *Service) ImportLatest(ctx context.Context) (Imported, error) {
	release, err := s.fetchLatestRelease(ctx)
	if err != nil {
		return Imported{}, err
	}
	tag, err := s.checkReleaseTag(goapi.Dig(release, "tag_name"))
	if err != nil {
		return Imported{}, err
	}
	assets := goapi.Dig(release, "assets")
	var list []any
	if assets != nil {
		var ok bool
		if list, ok = assets.([]any); !ok {
			return Imported{}, errors.New("release.assets?.find is not a function")
		}
	}
	find := func(name string) (any, error) {
		for _, asset := range list {
			if asset == nil {
				return nil, errors.New("Cannot read properties of null (reading 'name')")
			}
			if n, ok := goapi.Dig(asset, "name").(string); ok && n == name {
				return asset, nil
			}
		}
		return nil, nil
	}
	var manifestAsset, sumsAsset, signatureAsset any
	for _, pick := range []struct {
		name string
		into *any
	}{{"manifest.json", &manifestAsset}, {"SHA256SUMS", &sumsAsset}, {"manifest.sig", &signatureAsset}} {
		if *pick.into, err = find(pick.name); err != nil {
			return Imported{}, err
		}
	}
	if manifestAsset == nil || sumsAsset == nil || signatureAsset == nil {
		return Imported{}, errors.New("generic_agent_release_manifest_missing")
	}

	manifestFile, err := s.downloadAsset(ctx, manifestAsset)
	if err != nil {
		return Imported{}, err
	}
	sumsFile, err := s.downloadAsset(ctx, sumsAsset)
	if err != nil {
		return Imported{}, err
	}
	signatureFile, err := s.downloadAsset(ctx, signatureAsset)
	if err != nil {
		return Imported{}, err
	}
	keyText := s.publicKey()
	if keyText == "" {
		return Imported{}, errors.New("generic_agent_release_public_key_missing")
	}
	signature := decodeBase64(jsTrim(decodeUTF8(signatureFile.body)))
	key := decodeBase64(keyText)
	if len(key) != ed25519.PublicKeySize || len(signature) != ed25519.SignatureSize ||
		!ed25519.Verify(ed25519.PublicKey(key), manifestFile.body, signature) {
		return Imported{}, errors.New("generic_agent_release_signature_invalid")
	}
	manifest, err := parseJSON(manifestFile.body)
	if err != nil {
		return Imported{}, err
	}
	if manifest == nil {
		return Imported{}, errors.New("Cannot read properties of null (reading 'schema_version')")
	}
	manifestAssets, _ := goapi.Dig(manifest, "assets").([]any)
	if goapi.Dig(manifest, "schema_version") != float64(1) || goapi.Dig(manifest, "sku") != "betterdesk-support" ||
		goapi.Dig(manifest, "generic") != true || goapi.Dig(manifest, "external_config") != false ||
		len(manifestAssets) == 0 {
		return Imported{}, errors.New("generic_agent_release_manifest_invalid")
	}

	checksums := map[string]string{}
	for _, line := range reLineBreak.Split(decodeUTF8(sumsFile.body), -1) {
		if m := reChecksumLine.FindStringSubmatch(jsTrim(line)); m != nil {
			checksums[m[2]] = m[1]
		}
	}
	var files []downloaded
	for _, asset := range manifestAssets {
		if asset == nil {
			return Imported{}, errors.New("Cannot read properties of null (reading 'name')")
		}
		name := safeName(goapi.Dig(asset, "name"))
		sha := jsString(goapi.Dig(asset, "sha256"), "")
		if name == "" || !reSHA256.MatchString(sha) {
			return Imported{}, errors.New("generic_agent_release_asset_invalid")
		}
		source, err := find(name)
		if err != nil {
			return Imported{}, err
		}
		file, err := s.downloadAsset(ctx, source)
		if err != nil {
			return Imported{}, err
		}
		sum := sha256.Sum256(file.body)
		digest := hex.EncodeToString(sum[:])
		if digest != strings.ToLower(sha) || checksums[name] != digest || goapi.Dig(asset, "size") != float64(len(file.body)) {
			return Imported{}, errors.New("generic_agent_release_checksum_mismatch:" + name)
		}
		if err := s.assertNoExternalConfig(ctx, name, file.body); err != nil {
			return Imported{}, err
		}
		files = append(files, file)
	}

	destination := filepath.Join(s.root, tag)
	now := s.opts.Now()
	staging := destination + ".tmp-" + strconv.Itoa(s.opts.PID) + "-" + strconv.FormatInt(now.UnixMilli(), 10)
	if err := os.RemoveAll(staging); err != nil {
		return Imported{}, fsError(err, "rm", staging, "")
	}
	if err := os.MkdirAll(staging, 0o750); err != nil {
		return Imported{}, fsError(err, "mkdir", staging, "")
	}
	for _, file := range files {
		target := filepath.Join(staging, file.name)
		if err := os.WriteFile(target, file.body, 0o640); err != nil {
			return Imported{}, fsError(err, "open", target, "")
		}
	}
	stored := goapi.NewObject()
	if o, ok := manifest.(*goapi.Object); ok {
		stored = o.Clone()
	}
	stored.Set("release_tag", tag)
	stored.Set("imported_at", now.UTC().Format("2006-01-02T15:04:05.000Z"))
	compact, err := stored.MarshalJSON()
	if err != nil {
		return Imported{}, err
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, compact, "", "  "); err != nil {
		return Imported{}, err
	}
	pretty.WriteByte('\n')
	manifestPath := filepath.Join(staging, "manifest.json")
	if err := os.WriteFile(manifestPath, pretty.Bytes(), 0o640); err != nil {
		return Imported{}, fsError(err, "open", manifestPath, "")
	}
	if err := os.Rename(staging, destination); err != nil {
		return Imported{}, fsError(err, "rename", staging, destination)
	}
	return Imported{Tag: tag, Manifest: manifest, Path: destination}, nil
}

var (
	reLineBreak    = regexp.MustCompile(`\r?\n`)
	reChecksumLine = regexp.MustCompile(`^([a-f0-9]{64})\s+\*?([A-Za-z0-9._-]+)$`)
	reSHA256       = regexp.MustCompile(`^(?i)[a-f0-9]{64}$`)
	reDownloadURL  = regexp.MustCompile(`^https://api\.github\.com/|^https://github\.com/`)
)

// fetchLatestRelease asks GitHub for the repository's last 30 releases
// and returns the first that is a support release.
func (s *Service) fetchLatestRelease(ctx context.Context) (any, error) {
	res, err := s.get(ctx, "https://api.github.com/repos/"+s.repo()+"/releases?per_page=30", "", 0)
	if err != nil {
		return nil, err
	}
	if res.status != http.StatusOK || res.tooLarge {
		return nil, fmt.Errorf("generic_agent_release_list_failed:%d", res.status)
	}
	releases, err := parseJSON(res.body)
	if err != nil {
		return nil, err
	}
	list, ok := releases.([]any)
	if !ok {
		if releases == nil {
			return nil, errors.New("Cannot read properties of null (reading 'find')")
		}
		return nil, errors.New("releases.find is not a function")
	}
	for _, item := range list {
		if item == nil {
			return nil, errors.New("Cannot read properties of null (reading 'draft')")
		}
		if goapi.Truthy(goapi.Dig(item, "draft")) || goapi.Truthy(goapi.Dig(item, "prerelease")) {
			continue
		}
		if strings.HasPrefix(jsString(goapi.Dig(item, "tag_name"), ""), releasePrefix) {
			return item, nil
		}
	}
	return nil, errors.New("generic_agent_release_not_found")
}

// ErrNotNewer refuses a release whose tag is not higher than every
// release in the cache. Its error text is the code, a colon and the tag.
var ErrNotNewer = errors.New("generic_agent_release_not_newer")

// checkReleaseTag takes a release's tag as the name of its cache folder:
// a plain file name that starts with support-v and is higher than the
// tag of every release in the cache (compareTags). Only manifest.json is
// signed and it does not name its release, so the files of an older
// signed release published under a new tag would otherwise be offered
// as the newest agent.
func (s *Service) checkReleaseTag(v any) (string, error) {
	tag, ok := v.(string)
	if !ok {
		return "", errors.New(`The "path" argument must be of type string. ` + received(v))
	}
	if safeName(tag) == "" || !strings.HasPrefix(tag, releasePrefix) || tag == releasePrefix {
		return "", errors.New("generic_agent_release_tag_invalid")
	}
	for _, cached := range s.ListCached() {
		if strings.HasPrefix(cached.Tag, releasePrefix) && compareTags(tag, cached.Tag) <= 0 {
			return "", fmt.Errorf("%w:%s", ErrNotNewer, tag)
		}
	}
	return tag, nil
}

// compareTags orders two support-v tags by their versions as semantic
// versioning does: the dotted parts before the first "-" one by one
// (numbers by value, a missing part as 0), then a version without a
// suffix after "-" above one with it, then the suffixes' dotted parts.
// It returns -1, 0 or 1.
func compareTags(a, b string) int {
	coreA, preA, hasPreA := strings.Cut(strings.TrimPrefix(a, releasePrefix), "-")
	coreB, preB, hasPreB := strings.Cut(strings.TrimPrefix(b, releasePrefix), "-")
	partsA, partsB := strings.Split(coreA, "."), strings.Split(coreB, ".")
	for i := 0; i < max(len(partsA), len(partsB)); i++ {
		x, y := "0", "0"
		if i < len(partsA) {
			x = partsA[i]
		}
		if i < len(partsB) {
			y = partsB[i]
		}
		if c := comparePart(x, y); c != 0 {
			return c
		}
	}
	switch {
	case hasPreA != hasPreB:
		if hasPreA {
			return -1
		}
		return 1
	case !hasPreA:
		return 0
	}
	partsA, partsB = strings.Split(preA, "."), strings.Split(preB, ".")
	for i := 0; i < min(len(partsA), len(partsB)); i++ {
		if c := comparePart(partsA[i], partsB[i]); c != 0 {
			return c
		}
	}
	switch {
	case len(partsA) < len(partsB):
		return -1
	case len(partsA) > len(partsB):
		return 1
	}
	return 0
}

// comparePart orders two version parts: numbers by value and below
// text, text by its bytes.
func comparePart(x, y string) int {
	numX, numY := isDigits(x), isDigits(y)
	switch {
	case numX && numY:
		x, y = strings.TrimLeft(x, "0"), strings.TrimLeft(y, "0")
		if len(x) != len(y) {
			if len(x) < len(y) {
				return -1
			}
			return 1
		}
	case numX:
		return -1
	case numY:
		return 1
	}
	return strings.Compare(x, y)
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

type downloaded struct {
	name string
	body []byte
}

// downloadAsset fetches one file of a release. Its name must be a plain
// file name and its address one of GitHub's.
func (s *Service) downloadAsset(ctx context.Context, asset any) (downloaded, error) {
	name := safeName(goapi.Dig(asset, "name"))
	address := jsString(goapi.Dig(asset, "browser_download_url"), "")
	if name == "" || !reDownloadURL.MatchString(address) {
		return downloaded{}, errors.New("generic_agent_release_asset_invalid")
	}
	res, err := s.get(ctx, address, "application/octet-stream", 0)
	if err != nil {
		return downloaded{}, err
	}
	if res.status != http.StatusOK || res.tooLarge {
		return downloaded{}, errors.New("generic_agent_release_asset_download_failed:" + name)
	}
	return downloaded{name: name, body: res.body}, nil
}

// assertNoExternalConfig refuses an archive that carries a custom.txt (or
// the marker that one belongs there): a generic agent must take no
// configuration from its files.
func (s *Service) assertNoExternalConfig(ctx context.Context, name string, body []byte) error {
	carries := func(entry string) bool {
		entry = strings.ToLower(strings.ReplaceAll(entry, `\`, "/"))
		return strings.HasSuffix(entry, "/custom.txt") || strings.HasSuffix(entry, "/.custom-txt-here")
	}
	switch {
	case strings.HasSuffix(name, ".zip"):
		archive, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
		if err != nil {
			return errors.New("ADM-ZIP: Invalid or unsupported zip format. No END header found")
		}
		seen := map[string]bool{}
		for _, f := range archive.File {
			if seen[f.Name] {
				return errors.New(`ADM-ZIP: Duplicate entry name "` + f.Name + `"`)
			}
			seen[f.Name] = true
		}
		for _, f := range archive.File {
			if carries(f.Name) {
				return errors.New("generic_agent_release_external_config")
			}
		}
	case strings.HasSuffix(name, ".tar.gz"):
		listed, err := s.opts.ListTar(ctx, body)
		if err != nil {
			return errors.New("generic_agent_release_archive_invalid")
		}
		for _, line := range reLineBreak.Split(listed, -1) {
			if carries(line) {
				return errors.New("generic_agent_release_external_config")
			}
		}
	}
	return nil
}

// response is what a request to GitHub got: the status and up to 2 GB of
// the body.
type response struct {
	status   int
	body     []byte
	tooLarge bool
}

// get fetches address over HTTPS and follows up to three redirects
// (301, 302, 307, 308) to GitHub's hosts. accept replaces the Accept
// header of the API when it is not empty. A request that has been silent
// for 30 seconds ends with generic_agent_release_timeout.
func (s *Service) get(ctx context.Context, address, accept string, redirects int) (response, error) {
	target, err := url.Parse(address)
	if err != nil {
		return response{}, errors.New("Invalid URL")
	}
	if target.Scheme != "https" {
		return response{}, fmt.Errorf("Protocol %q not supported. Expected \"https:\"", target.Scheme+":")
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return response{}, errors.New("Invalid URL")
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/vnd.github+json")
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	res, err := s.opts.HTTP.RoundTrip(req)
	if err != nil {
		return response{}, requestError(err, target)
	}
	defer res.Body.Close()
	switch res.StatusCode {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		if location := res.Header.Get("Location"); location != "" {
			if redirects >= 3 {
				return response{}, errors.New("generic_agent_release_redirect_limit")
			}
			next, err := target.Parse(location)
			if err != nil {
				return response{}, errors.New("Invalid URL")
			}
			allowed := ResolveAllowedGithubHost(next.Hostname())
			if allowed == "" {
				return response{}, errors.New("generic_agent_release_redirect_host_invalid")
			}
			if port := next.Port(); port != "" {
				next.Host = allowed + ":" + port
			} else {
				next.Host = allowed
			}
			return s.get(ctx, next.String(), accept, redirects+1)
		}
	}
	timer := time.AfterFunc(idleTimeout, func() { cancel(errTimeout) })
	defer timer.Stop()
	var body bytes.Buffer
	size := int64(0)
	buf := make([]byte, 64*1024)
	for {
		n, err := res.Body.Read(buf)
		if n > 0 {
			timer.Reset(idleTimeout)
			size += int64(n)
			if size <= maxReleaseBytes {
				body.Write(buf[:n])
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if errors.Is(context.Cause(ctx), errTimeout) {
				return response{}, errTimeout
			}
			return response{}, requestError(err, target)
		}
	}
	return response{status: res.StatusCode, body: body.Bytes(), tooLarge: size > maxReleaseBytes}, nil
}

var errTimeout = errors.New("generic_agent_release_timeout")

// requestError words a failed request the way Node's https module did
// where the cause is common: a name that does not resolve and a refused
// connection. Other errors keep Go's text.
func requestError(err error, target *url.URL) error {
	var dnsErr *net.DNSError
	switch {
	case errors.As(err, &dnsErr) && dnsErr.IsNotFound:
		return fmt.Errorf("getaddrinfo ENOTFOUND %s", target.Hostname())
	case errors.Is(err, syscall.ECONNREFUSED):
		var opErr *net.OpError
		if errors.As(err, &opErr) && opErr.Addr != nil {
			return fmt.Errorf("connect ECONNREFUSED %s", opErr.Addr.String())
		}
		return errors.New("connect ECONNREFUSED")
	case errors.Is(err, context.DeadlineExceeded):
		return errTimeout
	}
	return err
}

// parseJSON is JSON.parse of a downloaded file, with V8's message for
// text that is no JSON.
func parseJSON(body []byte) (any, error) {
	text := []byte(decodeUTF8(body))
	v, err := goapi.Parse(text)
	if err != nil {
		if _, jsErr := branding.ParseJSON(text); jsErr != nil {
			return nil, errors.New(branding.Message(jsErr))
		}
		return nil, err
	}
	return v, nil
}

// jsString is String(v || fallback).
func jsString(v any, fallback string) string {
	if !goapi.Truthy(v) {
		return fallback
	}
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	case []any:
		parts := make([]string, len(t))
		for i, el := range t {
			if el != nil {
				parts[i] = jsString(el, "")
			}
		}
		return strings.Join(parts, ",")
	}
	return "[object Object]"
}

// received is the end of Node's ERR_INVALID_ARG_TYPE message for a value.
func received(v any) string {
	switch t := v.(type) {
	case nil:
		return "Received null"
	case bool:
		return "Received type boolean (" + strconv.FormatBool(t) + ")"
	case float64:
		return "Received type number (" + strconv.FormatFloat(t, 'f', -1, 64) + ")"
	case []any:
		return "Received an instance of Array"
	}
	return "Received an instance of Object"
}

// jsTrim is String.prototype.trim.
func jsTrim(s string) string {
	return strings.TrimFunc(s, func(r rune) bool {
		switch r {
		case '\t', '\n', '\v', '\f', '\r', ' ', 0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
			return true
		}
		return r >= 0x2000 && r <= 0x200A
	})
}

// decodeUTF8 reads bytes as UTF-8 text, each byte that is no part of a
// character becoming U+FFFD.
func decodeUTF8(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	var out strings.Builder
	for len(b) > 0 {
		r, size := utf8.DecodeRune(b)
		out.WriteRune(r)
		b = b[size:]
	}
	return out.String()
}

// decodeBase64 is Buffer.from(text, 'base64'): both alphabets, everything
// that is no base64 character skipped, the end at the first "=", and a
// last group of two or three characters giving one or two bytes.
func decodeBase64(text string) []byte {
	var out []byte
	var group [4]byte
	n := 0
	for i := 0; i < len(text) && text[i] != '='; i++ {
		v, ok := base64Value(text[i])
		if !ok {
			continue
		}
		group[n] = v
		n++
		if n == 4 {
			out = append(out, group[0]<<2|group[1]>>4, group[1]<<4|group[2]>>2, group[2]<<6|group[3])
			n = 0
		}
	}
	switch n {
	case 2:
		out = append(out, group[0]<<2|group[1]>>4)
	case 3:
		out = append(out, group[0]<<2|group[1]>>4, group[1]<<4|group[2]>>2)
	}
	return out
}

func base64Value(c byte) (byte, bool) {
	switch {
	case c >= 'A' && c <= 'Z':
		return c - 'A', true
	case c >= 'a' && c <= 'z':
		return c - 'a' + 26, true
	case c >= '0' && c <= '9':
		return c - '0' + 52, true
	case c == '+' || c == '-':
		return 62, true
	case c == '/' || c == '_':
		return 63, true
	}
	return 0, false
}

// fsError words a file error as Node does: "EACCES: permission denied,
// mkdir '/x'".
func fsError(err error, call, path, dest string) error {
	code, text := "", ""
	switch {
	case errors.Is(err, os.ErrNotExist):
		code, text = "ENOENT", "no such file or directory"
	case errors.Is(err, syscall.ENOTEMPTY), errors.Is(err, syscall.EEXIST):
		code, text = "ENOTEMPTY", "directory not empty"
	case errors.Is(err, os.ErrPermission):
		code, text = "EACCES", "permission denied"
	case errors.Is(err, syscall.ENOSPC):
		code, text = "ENOSPC", "no space left on device"
	default:
		return err
	}
	message := code + ": " + text + ", " + call + " '" + path + "'"
	if dest != "" {
		message += " -> '" + dest + "'"
	}
	return errors.New(message)
}
