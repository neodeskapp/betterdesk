package httpx

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

// StaticFS is how the file server reaches the disk. Names are absolute
// paths in the form of the operating system.
type StaticFS interface {
	Stat(name string) (fs.FileInfo, error)
	Open(name string) (io.ReadSeekCloser, error)
}

type osStaticFS struct{}

func (osStaticFS) Stat(name string) (fs.FileInfo, error) { return os.Stat(name) }

func (osStaticFS) Open(name string) (io.ReadSeekCloser, error) { return os.Open(name) }

// StaticOptions say how a folder is served. The zero value serves like
// express.static(root) without options.
type StaticOptions struct {
	// MaxAge is the max-age of Cache-Control. Values below zero count as
	// zero, values above one year as one year.
	MaxAge time.Duration
	// Immutable adds ", immutable" to Cache-Control.
	Immutable bool
	// SetHeaders runs before the file's own headers are set. A header it
	// sets (Cache-Control, Content-Type, ETag, Last-Modified,
	// Accept-Ranges) is kept. filePath is the file on disk.
	SetHeaders func(w http.ResponseWriter, filePath string, info fs.FileInfo)
	// Dotfiles says what a path with a part that starts with a dot gets:
	// "allow" serves it, "deny" answers 403, "ignore" answers 404. The
	// default "" looks only at the last part: a dot there is ignored, a
	// dot folder further up is allowed.
	Dotfiles string
	// Index lists the files tried for a path that ends in a slash. The
	// default is index.html. NoIndex turns the lookup off.
	Index   []string
	NoIndex bool
	// NoRedirect answers 404 for a folder without a trailing slash, in
	// place of the 301 to the path with one.
	NoRedirect bool
	// NoFallthrough makes every miss an error (OnError) and answers 405 to
	// methods other than GET and HEAD. By default those go to next.
	NoFallthrough bool
	// NoETag, NoLastModified, NoAcceptRanges and NoCacheControl leave the
	// header out. Without Accept-Ranges, Range requests are not served.
	NoETag         bool
	NoLastModified bool
	NoAcceptRanges bool
	NoCacheControl bool
	// OnError answers a failed request: 412 and 416 for a file that was
	// found, 5xx for a disk error, and every 4xx with NoFallthrough.
	// Headers of the file (ETag, Content-Range, ...) are already set.
	// Without OnError the answer is the status text as plain text.
	OnError func(w http.ResponseWriter, r *http.Request, status int, err error)
	// FS replaces the operating system's files.
	FS StaticFS
}

// Static serves the files of one folder the way express.static does:
// weak ETags from size and modification time, Last-Modified, conditional
// requests, single byte ranges, a redirect for folders, index files.
type Static struct {
	root    string
	opts    StaticOptions
	index   []string
	maxAge  int64
	windows bool
}

const maxStaticAge = 365 * 24 * time.Hour

// NewStatic returns a file server for root.
func NewStatic(root string, opts StaticOptions) *Static {
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = filepath.Clean(root)
	}
	s := &Static{root: filepath.ToSlash(abs), opts: opts, windows: runtime.GOOS == "windows"}
	if s.opts.FS == nil {
		s.opts.FS = osStaticFS{}
	}
	switch {
	case opts.NoIndex:
	case opts.Index != nil:
		s.index = opts.Index
	default:
		s.index = []string{"index.html"}
	}
	age := opts.MaxAge
	if age < 0 {
		age = 0
	}
	if age > maxStaticAge {
		age = maxStaticAge
	}
	s.maxAge = int64(age / time.Second)
	return s
}

// RawPath is the path of the request as the client wrote it, still
// percent-encoded, without the query. It is what Express calls req.path
// before any mount.
func RawPath(r *http.Request) string {
	p, _ := splitTarget(requestTarget(r))
	return p
}

func requestTarget(r *http.Request) string {
	if r.RequestURI != "" && strings.HasPrefix(r.RequestURI, "/") {
		return r.RequestURI
	}
	return r.URL.RequestURI()
}

// splitTarget cuts a request target into the path and the rest (query and
// fragment, with their ? or #).
func splitTarget(target string) (p, rest string) {
	if i := strings.IndexAny(target, "?#"); i >= 0 {
		return target[:i], target[i:]
	}
	return target, ""
}

// StripMount reports whether a raw path is at or under a mount point the
// way Express matches app.use(mount): in any letter case, and only at a
// slash. rest is the path below the mount and always starts with a slash,
// base is the mount as the client spelled it.
func StripMount(rawPath, mount string) (rest, base string, ok bool) {
	mount = strings.TrimSuffix(mount, "/")
	if mount == "" {
		return rawPath, "", true
	}
	if len(rawPath) < len(mount) || !strings.EqualFold(rawPath[:len(mount)], mount) {
		return "", "", false
	}
	tail := rawPath[len(mount):]
	if tail != "" && tail[0] != '/' {
		return "", "", false
	}
	base = rawPath[:len(mount)]
	// The mount's own slash is part of the match when another slash or
	// the end follows: /api//x leaves /x
	if strings.HasPrefix(tail, "//") {
		tail = tail[1:]
	}
	if tail == "" {
		tail = "/"
	}
	return tail, base, true
}

// Middleware serves files under mount ("" for the root) and hands
// everything else to next.
func (s *Static) Middleware(mount string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw := RawPath(r)
			sub, _, ok := StripMount(raw, mount)
			if !ok {
				next.ServeHTTP(w, r)
				return
			}
			s.Serve(w, r, sub, next)
		})
	}
}

// Serve answers r with the file at sub, the percent-encoded path below the
// folder's mount point, or calls next when there is no such file.
func (s *Static) Serve(w http.ResponseWriter, r *http.Request, sub string, next http.Handler) {
	if !s.readMethod(w, r, next) {
		return
	}
	original, _ := splitTarget(requestTarget(r))
	// The mount itself without a slash is the folder, so it redirects
	if sub == "/" && !strings.HasSuffix(original, "/") {
		sub = ""
	}
	req := &staticRequest{s: s, w: w, r: r, next: next, sub: sub, forward: s.opts.NoFallthrough}
	req.serve()
}

// ServeName is Serve for a caller that has picked the file itself: name is
// the file's path below the folder with slashes and without
// percent-encoding ("_ui/immutable/app.js.br"). Headers the caller has set
// on w are kept, so it can set Content-Type and Content-Encoding for a
// precompressed file first. The same rules apply as for Serve: a name
// that leaves the folder is not served.
func (s *Static) ServeName(w http.ResponseWriter, r *http.Request, name string, next http.Handler) {
	if !s.readMethod(w, r, next) {
		return
	}
	req := &staticRequest{s: s, w: w, r: r, next: next, sub: "/" + strings.TrimPrefix(name, "/"), plain: true, forward: s.opts.NoFallthrough}
	req.serve()
}

// readMethod lets GET and HEAD through. Other methods go to next, or get
// 405 without fall-through.
func (s *Static) readMethod(w http.ResponseWriter, r *http.Request, next http.Handler) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return true
	}
	if !s.opts.NoFallthrough {
		next.ServeHTTP(w, r)
		return false
	}
	w.Header().Set("Allow", "GET, HEAD")
	w.Header().Set("Content-Length", "0")
	w.WriteHeader(http.StatusMethodNotAllowed)
	return false
}

type staticRequest struct {
	s    *Static
	w    http.ResponseWriter
	r    *http.Request
	next http.Handler
	sub  string
	// plain says sub is not percent-encoded
	plain bool
	// forward says a failure is an error now and no longer a miss
	forward bool
}

// fail ends the request with a status: a miss goes to next, an error to
// OnError.
func (q *staticRequest) fail(status int, err error) {
	if !q.forward && status < 500 {
		q.next.ServeHTTP(q.w, q.r)
		return
	}
	if err == nil {
		err = errors.New(staticStatusText(status))
	}
	if q.s.opts.OnError != nil {
		q.s.opts.OnError(q.w, q.r, status, err)
		return
	}
	h := q.w.Header()
	h.Set("Content-Type", "text/plain; charset=utf-8")
	h.Del("Content-Length")
	q.w.WriteHeader(status)
	if q.r.Method != http.MethodHead {
		_, _ = io.WriteString(q.w, staticStatusText(status))
	}
}

// staticStatusText is the text of a status as the console's errors have
// always carried it.
func staticStatusText(status int) string {
	if status == http.StatusRequestedRangeNotSatisfiable {
		return "Range Not Satisfiable"
	}
	return http.StatusText(status)
}

func (q *staticRequest) failStat(err error) {
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) || errors.Is(err, syscall.ENAMETOOLONG) {
		q.fail(http.StatusNotFound, err)
		return
	}
	q.fail(http.StatusInternalServerError, err)
}

func (q *staticRequest) trailingSlash() bool { return strings.HasSuffix(q.sub, "/") }

func (q *staticRequest) serve() {
	s := q.s
	decoded, ok := q.sub, true
	if !q.plain {
		decoded, ok = decodeURIComponent(q.sub)
	}
	if !ok || strings.IndexByte(decoded, 0) >= 0 {
		q.fail(http.StatusBadRequest, nil)
		return
	}
	if s.windows {
		decoded = strings.ReplaceAll(decoded, `\`, "/")
	}
	rel := decoded
	if rel != "" {
		rel = nodeNormalize("./" + rel)
	}
	if hasUpPath(rel) {
		q.fail(http.StatusForbidden, nil)
		return
	}
	parts := strings.Split(rel, "/")
	if s.windows && !windowsSafe(parts) {
		q.fail(http.StatusNotFound, nil)
		return
	}
	full := s.root
	if rel != "" {
		full = nodeNormalize(s.root + "/" + rel)
	}

	if containsDotFile(parts) {
		access := s.opts.Dotfiles
		if access == "" {
			access = "allow"
			if strings.HasPrefix(parts[len(parts)-1], ".") {
				access = "ignore"
			}
		}
		switch access {
		case "allow":
		case "deny":
			q.fail(http.StatusForbidden, nil)
			return
		default:
			q.fail(http.StatusNotFound, nil)
			return
		}
	}

	if len(s.index) > 0 && q.trailingSlash() {
		q.sendIndex(full)
		return
	}
	q.sendFile(full)
}

// stat looks at a path. A path with a trailing slash names a folder: a
// file there is "not a directory".
func (q *staticRequest) stat(full string) (fs.FileInfo, error) {
	name := full
	wantDir := false
	if len(name) > 1 && strings.HasSuffix(name, "/") {
		name = strings.TrimSuffix(name, "/")
		wantDir = true
	}
	info, err := q.s.opts.FS.Stat(filepath.FromSlash(name))
	if err == nil && wantDir && !info.IsDir() {
		return nil, &fs.PathError{Op: "stat", Path: full, Err: syscall.ENOTDIR}
	}
	return info, err
}

func (q *staticRequest) sendFile(full string) {
	info, err := q.stat(full)
	if err != nil {
		q.failStat(err)
		return
	}
	if info.IsDir() {
		q.redirect()
		return
	}
	q.send(full, info)
}

func (q *staticRequest) sendIndex(dir string) {
	var last error
	for _, name := range q.s.index {
		full := nodeNormalize(dir + "/" + name)
		info, err := q.stat(full)
		if err != nil {
			last = err
			continue
		}
		if info.IsDir() {
			last = nil
			continue
		}
		q.send(full, info)
		return
	}
	if last != nil {
		q.failStat(last)
		return
	}
	q.fail(http.StatusNotFound, nil)
}

// redirect sends the client of a folder to the same address with a slash.
func (q *staticRequest) redirect() {
	if q.s.opts.NoRedirect || q.trailingSlash() {
		q.fail(http.StatusNotFound, nil)
		return
	}
	target := requestTarget(q.r)
	hash := ""
	if i := strings.IndexByte(target, '#'); i >= 0 {
		target, hash = target[:i], target[i:]
	}
	search := ""
	if i := strings.IndexByte(target, '?'); i >= 0 {
		target, search = target[:i], target[i:]
	}
	loc := encodeURL(collapseLeadingSlashes(target+"/") + search + hash)
	doc := htmlDocument("Redirecting", "Redirecting to "+escapeHTML(loc))
	h := q.w.Header()
	h.Set("Content-Type", "text/html; charset=UTF-8")
	h.Set("Content-Length", strconv.Itoa(len(doc)))
	h.Set("Content-Security-Policy", "default-src 'none'")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Location", loc)
	q.w.WriteHeader(http.StatusMovedPermanently)
	if q.r.Method != http.MethodHead {
		_, _ = io.WriteString(q.w, doc)
	}
}

func (q *staticRequest) send(full string, info fs.FileInfo) {
	s := q.s
	q.forward = true
	h := q.w.Header()
	osPath := filepath.FromSlash(full)

	if s.opts.SetHeaders != nil {
		s.opts.SetHeaders(q.w, osPath, info)
	}
	mtime := jsMillis(info.ModTime())
	if !s.opts.NoAcceptRanges && h.Get("Accept-Ranges") == "" {
		h.Set("Accept-Ranges", "bytes")
	}
	if !s.opts.NoCacheControl && h.Get("Cache-Control") == "" {
		value := "public, max-age=" + strconv.FormatInt(s.maxAge, 10)
		if s.opts.Immutable {
			value += ", immutable"
		}
		h.Set("Cache-Control", value)
	}
	if !s.opts.NoLastModified && h.Get("Last-Modified") == "" {
		h.Set("Last-Modified", time.UnixMilli(mtime).UTC().Format(http.TimeFormat))
	}
	if !s.opts.NoETag && h.Get("ETag") == "" {
		h.Set("ETag", `W/"`+strconv.FormatInt(info.Size(), 16)+"-"+strconv.FormatInt(mtime, 16)+`"`)
	}
	if h.Get("Content-Type") == "" {
		h.Set("Content-Type", StaticContentType(full))
	}

	rh := q.r.Header
	if joined(rh, "If-Match") != "" || rh.Get("If-Unmodified-Since") != "" || joined(rh, "If-None-Match") != "" || rh.Get("If-Modified-Since") != "" {
		if q.preconditionFailed() {
			q.fail(http.StatusPreconditionFailed, nil)
			return
		}
		if staticFresh(rh, h.Get("ETag"), h.Get("Last-Modified")) {
			for _, name := range []string{"Content-Encoding", "Content-Language", "Content-Length", "Content-Range", "Content-Type"} {
				h.Del(name)
			}
			q.w.WriteHeader(http.StatusNotModified)
			return
		}
	}

	size := info.Size()
	offset, length := int64(0), size
	status := http.StatusOK
	if rangeHeader := joined(rh, "Range"); !s.opts.NoAcceptRanges && isBytesRange(rangeHeader) {
		ranges, result := parseByteRanges(size, rangeHeader)
		if !q.rangeFresh() {
			result = rangeMalformed
		}
		if result == rangeUnsatisfiable {
			h.Set("Content-Range", "bytes */"+strconv.FormatInt(size, 10))
			q.fail(http.StatusRequestedRangeNotSatisfiable, nil)
			return
		}
		if result == rangeOK && len(ranges) == 1 {
			status = http.StatusPartialContent
			h.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", ranges[0].start, ranges[0].end, size))
			offset = ranges[0].start
			length = ranges[0].end - ranges[0].start + 1
		}
	}

	if q.r.Method == http.MethodHead {
		h.Set("Content-Length", strconv.FormatInt(length, 10))
		q.w.WriteHeader(status)
		return
	}
	file, err := s.opts.FS.Open(osPath)
	if err != nil {
		q.failStat(err)
		return
	}
	defer file.Close()
	if offset > 0 {
		if _, err := file.Seek(offset, io.SeekStart); err != nil {
			q.fail(http.StatusInternalServerError, err)
			return
		}
	}
	h.Set("Content-Length", strconv.FormatInt(length, 10))
	q.w.WriteHeader(status)
	_, _ = io.CopyN(q.w, file, length)
}

// preconditionFailed checks If-Match, or without it If-Unmodified-Since.
func (q *staticRequest) preconditionFailed() bool {
	h := q.w.Header()
	if match := joined(q.r.Header, "If-Match"); match != "" {
		etag := h.Get("ETag")
		if etag == "" {
			return true
		}
		if match == "*" {
			return false
		}
		for _, candidate := range tokenList(match) {
			if candidate == etag || candidate == "W/"+etag || "W/"+candidate == etag {
				return false
			}
		}
		return true
	}
	if since, ok := parseHTTPDate(q.r.Header.Get("If-Unmodified-Since")); ok {
		modified, ok := parseHTTPDate(h.Get("Last-Modified"))
		return !ok || modified > since
	}
	return false
}

// rangeFresh checks If-Range: an entity tag has to be the file's, a date
// must not be before its modification time.
func (q *staticRequest) rangeFresh() bool {
	ifRange := joined(q.r.Header, "If-Range")
	if ifRange == "" {
		return true
	}
	h := q.w.Header()
	if strings.Contains(ifRange, `"`) {
		etag := h.Get("ETag")
		return etag != "" && strings.Contains(ifRange, etag)
	}
	modified, ok1 := parseHTTPDate(h.Get("Last-Modified"))
	since, ok2 := parseHTTPDate(ifRange)
	return ok1 && ok2 && modified <= since
}

// staticFresh says whether the client's copy is still good: every
// validator it sent has to hold, and Cache-Control: no-cache never does.
func staticFresh(rh http.Header, etag, lastModified string) bool {
	modifiedSince := rh.Get("If-Modified-Since")
	noneMatch := joined(rh, "If-None-Match")
	if modifiedSince == "" && noneMatch == "" {
		return false
	}
	for _, part := range strings.Split(joined(rh, "Cache-Control"), ",") {
		if strings.Trim(part, " \t\r\n\f\v") == "no-cache" {
			return false
		}
	}
	if noneMatch != "" && noneMatch != "*" {
		if etag == "" {
			return false
		}
		stale := true
		for _, candidate := range tokenList(noneMatch) {
			if candidate == etag || candidate == "W/"+etag || "W/"+candidate == etag {
				stale = false
				break
			}
		}
		if stale {
			return false
		}
	}
	if modifiedSince != "" {
		modified, ok1 := parseHTTPDate(lastModified)
		since, ok2 := parseHTTPDate(modifiedSince)
		if !ok1 || !ok2 || modified > since {
			return false
		}
	}
	return true
}

// joined is a request header as one value: several lines with the same
// name are one comma separated list.
func joined(h http.Header, name string) string {
	return strings.Join(h.Values(name), ", ")
}

// tokenList splits a comma separated header. Spaces around a token are
// dropped.
func tokenList(s string) []string {
	var list []string
	start, end := 0, 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case ' ':
			if start == end {
				start, end = i+1, i+1
			}
		case ',':
			list = append(list, s[start:end])
			start, end = i+1, i+1
		default:
			end = i + 1
		}
	}
	return append(list, s[start:end])
}

// jsMillis is a file time in milliseconds, rounded the way Node rounds the
// time of a file.
func jsMillis(t time.Time) int64 {
	return int64(math.Floor(float64(t.Unix())*1e3 + float64(t.Nanosecond())/1e6 + 0.5))
}

var usZones = map[string]int{"EST": -5, "EDT": -4, "CST": -6, "CDT": -5, "MST": -7, "MDT": -6, "PST": -8, "PDT": -7}

var dateLayouts = []string{
	"Mon, 2 Jan 2006 15:04:05 MST",
	"Monday, 02-Jan-06 15:04:05 MST",
	"Mon Jan _2 15:04:05 2006",
	"Mon, 2 Jan 2006 15:04:05 -0700",
	"Mon, 2 Jan 2006 15:04:05",
	"2 Jan 2006 15:04:05 MST",
	"2006-01-02T15:04:05.999999999Z07:00",
	"2006-01-02T15:04:05.999999999",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02",
}

// parseHTTPDate reads a date of a request header in milliseconds. It takes
// the three HTTP formats in any letter case and ISO 8601; clients send
// nothing else, although JavaScript's Date.parse would read more.
func parseHTTPDate(value string) (int64, bool) {
	s := strings.ToUpper(strings.TrimSpace(value))
	if s == "" {
		return 0, false
	}
	for _, layout := range dateLayouts {
		loc := time.Local
		if layout == "2006-01-02" {
			loc = time.UTC
		}
		t, err := time.ParseInLocation(layout, s, loc)
		if err != nil {
			continue
		}
		if strings.HasSuffix(layout, "MST") {
			name, _ := t.Zone()
			switch name {
			case "GMT", "UTC", "UT", "Z":
				t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.UTC)
			default:
				hours, known := usZones[name]
				if !known {
					return 0, false
				}
				t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.FixedZone(name, hours*3600))
			}
		}
		return t.UnixMilli(), true
	}
	return 0, false
}

type byteRange struct {
	start, end int64
	index      int
}

const (
	rangeOK = iota
	rangeUnsatisfiable
	rangeMalformed
)

// isBytesRange reports whether a Range header asks for bytes.
func isBytesRange(header string) bool {
	return strings.HasPrefix(strings.TrimLeft(header, " "), "bytes=")
}

// parseByteRanges reads a Range header for a file of the given size. Ranges
// that overlap or touch are merged. Parts that are not numbers or lie
// outside the file are dropped; when nothing is left the range is
// unsatisfiable.
func parseByteRanges(size int64, header string) ([]byteRange, int) {
	eq := strings.IndexByte(header, '=')
	if eq < 0 {
		return nil, rangeMalformed
	}
	var ranges []byteRange
	for i, part := range strings.Split(header[eq+1:], ",") {
		bounds := strings.Split(part, "-")
		start, startOK := jsParseInt(bounds[0])
		end, endOK := float64(0), false
		if len(bounds) > 1 {
			end, endOK = jsParseInt(bounds[1])
		}
		last := float64(size - 1)
		switch {
		case !startOK:
			if !endOK {
				continue
			}
			start, end = float64(size)-end, last
		case !endOK:
			end = last
		}
		if end > last {
			end = last
		}
		if start > end || start < 0 {
			continue
		}
		ranges = append(ranges, byteRange{start: int64(start), end: int64(end), index: i})
	}
	if len(ranges) == 0 {
		return nil, rangeUnsatisfiable
	}
	sort.SliceStable(ranges, func(a, b int) bool { return ranges[a].start < ranges[b].start })
	j := 0
	for i := 1; i < len(ranges); i++ {
		next, current := ranges[i], &ranges[j]
		switch {
		case next.start > current.end+1:
			j++
			ranges[j] = next
		case next.end > current.end:
			current.end = next.end
			if next.index < current.index {
				current.index = next.index
			}
		}
	}
	ranges = ranges[:j+1]
	sort.SliceStable(ranges, func(a, b int) bool { return ranges[a].index < ranges[b].index })
	return ranges, rangeOK
}

// jsSpace is the white space parseInt skips, as far as headers carry it.
const jsSpace = " \t\n\v\f\r"

// jsParseInt reads the whole number at the start of s the way JavaScript's
// parseInt(s, 10) does: white space first, an optional sign, then digits;
// whatever follows is ignored.
func jsParseInt(s string) (float64, bool) {
	s = strings.TrimLeft(s, jsSpace)
	negative := false
	if s != "" && (s[0] == '+' || s[0] == '-') {
		negative = s[0] == '-'
		s = s[1:]
	}
	n, digits := float64(0), 0
	for digits < len(s) && s[digits] >= '0' && s[digits] <= '9' {
		n = n*10 + float64(s[digits]-'0')
		digits++
	}
	if digits == 0 {
		return 0, false
	}
	if negative {
		n = -n
	}
	return n, true
}

// decodeURIComponent undoes percent-encoding the way JavaScript does. It
// fails on a % without two hex digits and on escaped bytes that are not
// UTF-8. A byte above 0x7f that is not escaped counts as a Latin-1 letter.
func decodeURIComponent(s string) (string, bool) {
	out := make([]byte, 0, len(s))
	escaped := func(i int) (byte, bool) {
		if i+2 >= len(s) || s[i] != '%' {
			return 0, false
		}
		hi, ok1 := unhex(s[i+1])
		lo, ok2 := unhex(s[i+2])
		return hi<<4 | lo, ok1 && ok2
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '%' {
			out = utf8.AppendRune(out, rune(c))
			continue
		}
		first, ok := escaped(i)
		if !ok {
			return "", false
		}
		i += 2
		size := 1
		switch {
		case first < 0x80:
		case first >= 0xc2 && first <= 0xdf:
			size = 2
		case first >= 0xe0 && first <= 0xef:
			size = 3
		case first >= 0xf0 && first <= 0xf4:
			size = 4
		default:
			return "", false
		}
		seq := []byte{first}
		for len(seq) < size {
			next, ok := escaped(i + 1)
			if !ok {
				return "", false
			}
			seq = append(seq, next)
			i += 3
		}
		if !utf8.Valid(seq) {
			return "", false
		}
		out = append(out, seq...)
	}
	return string(out), true
}

func unhex(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// nodeNormalize cleans a slash path like path.Clean and keeps one trailing
// slash, as Node's path.normalize does.
func nodeNormalize(p string) string {
	if p == "" {
		return "."
	}
	cleaned := path.Clean(p)
	if strings.HasSuffix(p, "/") && !strings.HasSuffix(cleaned, "/") {
		cleaned += "/"
	}
	return cleaned
}

// hasUpPath reports a ".." part.
func hasUpPath(p string) bool {
	for _, part := range strings.FieldsFunc(p, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == ".." {
			return true
		}
	}
	return false
}

func containsDotFile(parts []string) bool {
	for _, part := range parts {
		if len(part) > 1 && part[0] == '.' {
			return true
		}
	}
	return false
}

// windowsSafe refuses names Windows reads as something else than the file
// they spell: drive and stream names, device names, trailing dots and
// spaces.
func windowsSafe(parts []string) bool {
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		if strings.ContainsAny(part, `:<>"|?*`) || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
			return false
		}
		base := strings.ToUpper(part)
		if i := strings.IndexByte(base, '.'); i >= 0 {
			base = base[:i]
		}
		base = strings.TrimRight(base, " ")
		switch base {
		case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
			return false
		}
		if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '0' && base[3] <= '9' {
			return false
		}
	}
	return true
}

func collapseLeadingSlashes(s string) string {
	i := 0
	for i < len(s) && s[i] == '/' {
		i++
	}
	if i > 1 {
		return "/" + s[i:]
	}
	return s
}

func htmlDocument(title, body string) string {
	return "<!DOCTYPE html>\n" +
		"<html lang=\"en\">\n" +
		"<head>\n" +
		"<meta charset=\"utf-8\">\n" +
		"<title>" + title + "</title>\n" +
		"</head>\n" +
		"<body>\n" +
		"<pre>" + body + "</pre>\n" +
		"</body>\n" +
		"</html>\n"
}

var htmlEscaper = strings.NewReplacer(`"`, "&quot;", "&", "&amp;", "'", "&#39;", "<", "&lt;", ">", "&gt;")

func escapeHTML(s string) string { return htmlEscaper.Replace(s) }

// urlSafe is true for the bytes a Location header may carry as they are.
func urlSafe(c byte) bool {
	switch {
	case c == 0x21, c >= 0x23 && c <= 0x3b, c == 0x3d, c >= 0x3f && c <= 0x5f, c >= 0x61 && c <= 0x7a, c == 0x7c, c == 0x7e:
		return true
	}
	return false
}

// uriKeeps is true for the bytes JavaScript's encodeURI leaves alone.
func uriKeeps(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	}
	return strings.IndexByte(";,/?:@&=+$-_.!~*'()#", c) >= 0
}

// encodeURL is EncodeURL for a text whose bytes above 0x7f count as Latin-1
// letters.
func encodeURL(s string) string { return EncodeURL(Latin1(s)) }

// StaticContentType is the Content-Type for a file name, taken from what
// follows its last dot or slash. Names the table does not know are
// application/octet-stream.
func StaticContentType(name string) string {
	ext := name
	if i := strings.LastIndexAny(name, `./\`); i >= 0 {
		ext = name[i+1:]
	}
	if mediaType, ok := staticTypeByExt[strings.ToLower(ext)]; ok {
		return mediaType
	}
	return "application/octet-stream"
}
