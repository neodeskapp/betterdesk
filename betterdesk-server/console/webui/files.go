package webui

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
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/unitronix/betterdesk-server/console/httpx"
)

// fileSystem is the file access a Host needs. Every file it reads or sends
// goes through it.
type fileSystem interface {
	Stat(name string) (fs.FileInfo, error)
	Open(name string) (io.ReadSeekCloser, error)
	ReadFile(name string) ([]byte, error)
}

type osFiles struct{}

func (osFiles) Stat(name string) (fs.FileInfo, error)       { return os.Stat(name) }
func (osFiles) Open(name string) (io.ReadSeekCloser, error) { return os.Open(name) }
func (osFiles) ReadFile(name string) ([]byte, error)        { return os.ReadFile(name) }

// noFiles is the file system of a Host without a build directory.
type noFiles struct{}

func (noFiles) Stat(string) (fs.FileInfo, error)       { return nil, fs.ErrNotExist }
func (noFiles) Open(string) (io.ReadSeekCloser, error) { return nil, fs.ErrNotExist }
func (noFiles) ReadFile(string) ([]byte, error)        { return nil, fs.ErrNotExist }

// httpError is a request that Handle could not serve and that is answered
// like a failure of the server (ServerError) with its own status.
type httpError struct {
	status  int
	message string
}

func (e *httpError) Error() string { return e.message }

func decodeFailure(param string) *httpError {
	return &httpError{http.StatusBadRequest, "Failed to decode param '" + param + "'"}
}

// hashedDir is the folder of the build's hashed files.
const hashedDir = "_ui/immutable"

// immutableTypes are the content types of hashed build output.
var immutableTypes = map[string]string{
	".js":    "application/javascript; charset=utf-8",
	".css":   "text/css; charset=utf-8",
	".json":  "application/json; charset=utf-8",
	".svg":   "image/svg+xml",
	".woff2": "font/woff2",
	".png":   "image/png",
	".wasm":  "application/wasm",
}

// staticTypes are the content types of the other build files, by the
// extension in lower case. An extension that is not listed is sent as
// application/octet-stream.
var staticTypes = map[string]string{
	"js":          "application/javascript; charset=UTF-8",
	"mjs":         "application/javascript; charset=UTF-8",
	"css":         "text/css; charset=UTF-8",
	"json":        "application/json; charset=UTF-8",
	"map":         "application/json; charset=UTF-8",
	"svg":         "image/svg+xml",
	"svgz":        "image/svg+xml",
	"png":         "image/png",
	"jpg":         "image/jpeg",
	"jpeg":        "image/jpeg",
	"gif":         "image/gif",
	"webp":        "image/webp",
	"apng":        "image/apng",
	"bmp":         "image/bmp",
	"ico":         "image/x-icon",
	"woff":        "font/woff",
	"woff2":       "font/woff2",
	"ttf":         "font/ttf",
	"otf":         "font/otf",
	"eot":         "application/vnd.ms-fontobject",
	"txt":         "text/plain; charset=UTF-8",
	"text":        "text/plain; charset=UTF-8",
	"log":         "text/plain; charset=UTF-8",
	"conf":        "text/plain; charset=UTF-8",
	"ini":         "text/plain; charset=UTF-8",
	"md":          "text/markdown; charset=UTF-8",
	"markdown":    "text/markdown; charset=UTF-8",
	"xml":         "application/xml",
	"wasm":        "application/wasm",
	"pdf":         "application/pdf",
	"webmanifest": "application/manifest+json",
	"manifest":    "text/cache-manifest; charset=UTF-8",
	"appcache":    "text/cache-manifest; charset=UTF-8",
	"mp4":         "video/mp4",
	"m4v":         "video/x-m4v",
	"webm":        "video/webm",
	"mp3":         "audio/mpeg",
	"m4a":         "audio/mp4",
	"wav":         "audio/wav",
	"ogg":         "audio/ogg",
	"oga":         "audio/ogg",
	"ogv":         "video/ogg",
	"weba":        "audio/webm",
	"aac":         "audio/x-aac",
	"flac":        "audio/x-flac",
	"html":        "text/html; charset=UTF-8",
	"htm":         "text/html; charset=UTF-8",
	"xhtml":       "application/xhtml+xml",
	"zip":         "application/zip",
	"gz":          "application/gzip",
	"tar":         "application/x-tar",
	"7z":          "application/x-7z-compressed",
	"rar":         "application/x-rar-compressed",
	"bz2":         "application/x-bzip2",
	"csv":         "text/csv; charset=UTF-8",
	"tsv":         "text/tab-separated-values; charset=UTF-8",
	"yaml":        "text/yaml; charset=UTF-8",
	"yml":         "text/yaml; charset=UTF-8",
	"ics":         "text/calendar; charset=UTF-8",
	"vtt":         "text/vtt; charset=UTF-8",
	"srt":         "application/x-subrip",
	"rtf":         "application/rtf",
	"doc":         "application/msword",
	"docx":        "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	"xls":         "application/vnd.ms-excel",
	"xlsx":        "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	"ppt":         "application/vnd.ms-powerpoint",
	"pptx":        "application/vnd.openxmlformats-officedocument.presentationml.presentation",
	"rpm":         "application/x-redhat-package-manager",
	"apk":         "application/vnd.android.package-archive",
	"jar":         "application/java-archive",
	"ts":          "video/mp2t",
	"jsx":         "text/jsx; charset=UTF-8",
	"sh":          "application/x-sh",
	"jsonld":      "application/ld+json",
	"geojson":     "application/geo+json",
	"atom":        "application/atom+xml",
	"rss":         "application/rss+xml",
}

// staticType is the content type of a build file: by what follows the last
// dot or slash of its name.
func staticType(name string) string {
	ext := strings.ToLower(name[strings.LastIndexAny(name, `./\`)+1:])
	if t, ok := staticTypes[ext]; ok {
		return t
	}
	return "application/octet-stream"
}

var (
	wantsBrotli = regexp.MustCompile(`\bbr\b`)
	wantsGzip   = regexp.MustCompile(`\bgzip\b`)
	upPath      = regexp.MustCompile(`(?:^|[\\/])\.\.(?:[\\/]|$)`)
	bytesRange  = regexp.MustCompile(`^ *bytes=`)
)

// inBuild turns a slash-separated path into the name of a file in the
// build directory. It refuses what could name something else on any system
// this runs on: a backslash, a NUL, a path that leaves the directory, and
// on Windows a device or a drive.
func (h *Host) inBuild(rel string) (string, bool) {
	if rel == "" || strings.ContainsAny(rel, "\\\x00") || !filepath.IsLocal(filepath.FromSlash(rel)) {
		return "", false
	}
	return filepath.Join(h.dir, filepath.FromSlash(rel)), true
}

// isMissing reports whether a file system error means "no such file".
func isMissing(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) || errors.Is(err, syscall.ENAMETOOLONG)
}

// serveImmutable answers a GET for hashed build output. rel is the request
// path without the base, not decoded: these files have plain names. The
// precompressed copy of the file (.br, then .gz) is sent when the browser
// names its encoding in Accept-Encoding and the copy exists.
//
// It answers false, with no header set, when there is no such file; a
// directory is no file, also with a slash at the end. A path whose dot
// segments lead out of the hashed files gets an empty 404: the build's
// other files are not sent with the year-long caching, and index.html
// only comes from the page handler.
func (h *Host) serveImmutable(w http.ResponseWriter, r *http.Request, rel string) (bool, *httpError) {
	root := strings.TrimSuffix(filepath.ToSlash(h.dir), "/")
	full := path.Clean(root + "/" + rel)
	inside, ok := strings.CutPrefix(full, root+"/")
	if !ok || !(asciiEqualFold(inside, hashedDir) || hasPrefixFold(inside, hashedDir+"/")) {
		endEmpty(w, r, http.StatusNotFound)
		return true, nil
	}
	if strings.HasSuffix(rel, "/") {
		return false, nil
	}

	type candidate struct{ suffix, encoding string }
	var candidates []candidate
	accept := strings.Join(r.Header.Values("Accept-Encoding"), ", ")
	if wantsBrotli.MatchString(accept) {
		candidates = append(candidates, candidate{".br", "br"})
	}
	if wantsGzip.MatchString(accept) {
		candidates = append(candidates, candidate{".gz", "gzip"})
	}
	candidates = append(candidates, candidate{"", ""})

	contentType, ok := immutableTypes[extname(full)]
	if !ok {
		contentType = "application/octet-stream"
	}
	for _, c := range candidates {
		name, ok := h.inBuild(inside + c.suffix)
		if !ok {
			continue
		}
		info, err := h.files.Stat(name)
		if err != nil {
			continue
		}
		if info.IsDir() {
			return false, nil
		}
		header := w.Header()
		header.Set("Content-Type", contentType)
		header.Set("Cache-Control", "public, max-age=31536000, immutable")
		header.Set("Vary", "Accept-Encoding")
		if c.encoding != "" {
			header.Set("Content-Encoding", c.encoding)
		}
		if base := filepath.Base(name); strings.HasPrefix(base, ".") && len(base) > 1 {
			return true, &httpError{http.StatusNotFound, "Not Found"}
		}
		if err := h.sendFile(w, r, name, info, "public, max-age=0"); err != nil {
			return true, err
		}
		return true, nil
	}
	return false, nil
}

// serveStatic answers a GET for one of the other files of the build. decoded
// is the request path without the base, percent-decoded. It answers false
// when there is no such file, for a directory, for a name that starts with
// a dot, and for a path that leaves the build.
func (h *Host) serveStatic(w http.ResponseWriter, r *http.Request, decoded string) (bool, *httpError) {
	if strings.ContainsRune(decoded, 0) {
		return false, nil
	}
	rel := path.Clean("./" + decoded)
	if upPath.MatchString(rel) || strings.HasSuffix(decoded, "/") {
		return false, nil
	}
	if base := path.Base(rel); len(base) > 1 && base[0] == '.' {
		return false, nil
	}
	name, ok := h.inBuild(rel)
	if !ok {
		return false, nil
	}
	info, err := h.files.Stat(name)
	if err != nil {
		if isMissing(err) {
			return false, nil
		}
		return true, &httpError{http.StatusInternalServerError, err.Error()}
	}
	if info.IsDir() {
		return false, nil
	}
	if strings.HasSuffix(name, "version.json") {
		w.Header().Set("Cache-Control", "no-cache")
	}
	if err := h.sendFile(w, r, name, info, "public, max-age=3600"); err != nil {
		return true, err
	}
	return true, nil
}

// modTimeMillis is a file's modification time in whole milliseconds,
// rounded.
func modTimeMillis(info fs.FileInfo) int64 {
	nanos := info.ModTime().UnixNano()
	millis := nanos / 1e6
	if nanos%1e6 >= 5e5 {
		millis++
	}
	return millis
}

// sendFile sends a file with validators and byte ranges. Headers that are
// set already stay as they are; cacheControl is the Cache-Control for an
// answer that has none.
//
// It answers an error, with the file's headers left on the response, when
// a precondition of the request fails (412), the range lies outside the
// file (416) or the file cannot be opened.
func (h *Host) sendFile(w http.ResponseWriter, r *http.Request, name string, info fs.FileInfo, cacheControl string) *httpError {
	header := w.Header()
	modified := time.UnixMilli(modTimeMillis(info)).UTC()
	if header.Get("Accept-Ranges") == "" {
		header.Set("Accept-Ranges", "bytes")
	}
	if header.Get("Cache-Control") == "" {
		header.Set("Cache-Control", cacheControl)
	}
	if header.Get("Last-Modified") == "" {
		header.Set("Last-Modified", modified.Format(http.TimeFormat))
	}
	if header.Get("ETag") == "" {
		header.Set("ETag", fmt.Sprintf(`W/"%x-%x"`, info.Size(), modTimeMillis(info)))
	}
	if header.Get("Content-Type") == "" {
		header.Set("Content-Type", staticType(name))
	}
	etag := header.Get("ETag")
	lastModified, lastModifiedErr := http.ParseTime(header.Get("Last-Modified"))

	in := r.Header
	if in.Get("If-Match") != "" || in.Get("If-Unmodified-Since") != "" || in.Get("If-None-Match") != "" || in.Get("If-Modified-Since") != "" {
		if preconditionFailed(in, etag, lastModified, lastModifiedErr == nil) {
			return &httpError{http.StatusPreconditionFailed, "Precondition Failed"}
		}
		if httpx.Fresh(r, etag, lastModified) {
			for _, name := range []string{"Content-Encoding", "Content-Language", "Content-Length", "Content-Range", "Content-Type"} {
				header.Del(name)
			}
			w.WriteHeader(http.StatusNotModified)
			return nil
		}
	}

	size := info.Size()
	offset, length, status := int64(0), size, http.StatusOK
	if requested := in.Get("Range"); bytesRange.MatchString(requested) {
		ranges, satisfiable := parseRange(size, requested)
		if !rangeFresh(in.Get("If-Range"), etag, lastModified, lastModifiedErr == nil) {
			ranges, satisfiable = nil, true
		}
		if !satisfiable {
			header.Set("Content-Range", fmt.Sprintf("bytes */%d", size))
			return &httpError{http.StatusRequestedRangeNotSatisfiable, "Range Not Satisfiable"}
		}
		if len(ranges) == 1 {
			status = http.StatusPartialContent
			header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", ranges[0].start, ranges[0].end, size))
			offset, length = ranges[0].start, ranges[0].end-ranges[0].start+1
		}
	}
	header.Set("Content-Length", strconv.FormatInt(length, 10))
	if r.Method == http.MethodHead {
		w.WriteHeader(status)
		return nil
	}

	file, err := h.files.Open(name)
	if err != nil {
		if isMissing(err) {
			return &httpError{http.StatusNotFound, err.Error()}
		}
		return &httpError{http.StatusInternalServerError, err.Error()}
	}
	defer file.Close()
	if offset > 0 {
		if _, err := file.Seek(offset, io.SeekStart); err != nil {
			return &httpError{http.StatusInternalServerError, err.Error()}
		}
	}
	w.WriteHeader(status)
	_, _ = io.CopyN(w, file, length)
	return nil
}

// tokens splits a header that lists values with commas.
func tokens(header string) []string {
	var list []string
	for _, part := range strings.Split(header, ",") {
		if part = strings.Trim(part, " "); part != "" {
			list = append(list, part)
		}
	}
	return list
}

// preconditionFailed checks If-Match, and If-Unmodified-Since when there is
// no If-Match.
func preconditionFailed(in http.Header, etag string, lastModified time.Time, hasLastModified bool) bool {
	if match := in.Get("If-Match"); match != "" {
		if etag == "" {
			return true
		}
		if match == "*" {
			return false
		}
		for _, candidate := range tokens(match) {
			if candidate == etag || candidate == "W/"+etag || "W/"+candidate == etag {
				return false
			}
		}
		return true
	}
	if since, err := http.ParseTime(in.Get("If-Unmodified-Since")); err == nil {
		return !hasLastModified || lastModified.After(since)
	}
	return false
}

// rangeFresh checks If-Range: a range counts only while the client's copy
// is still the file.
func rangeFresh(ifRange, etag string, lastModified time.Time, hasLastModified bool) bool {
	if ifRange == "" {
		return true
	}
	if strings.Contains(ifRange, `"`) {
		return etag != "" && strings.Contains(ifRange, etag)
	}
	since, err := http.ParseTime(ifRange)
	return err == nil && hasLastModified && !lastModified.After(since)
}

type byteRange struct{ start, end int64 }

// parseRange reads a Range header for a file of the given size. It answers
// the ranges with overlapping and adjacent ones joined, in the order the
// client gave them, and false when none of them lies in the file. A header
// without "=" gives no ranges and true: it is ignored.
func parseRange(size int64, header string) (ranges []byteRange, satisfiable bool) {
	eq := strings.IndexByte(header, '=')
	if eq < 0 {
		return nil, true
	}
	type indexed struct {
		byteRange
		index int
	}
	var list []indexed
	for _, spec := range strings.Split(header[eq+1:], ",") {
		parts := strings.Split(spec, "-")
		start := parseInt(parts[0])
		end := math.NaN()
		if len(parts) > 1 {
			end = parseInt(parts[1])
		}
		last := float64(size - 1)
		switch {
		case math.IsNaN(start):
			start = float64(size) - end
			end = last
		case math.IsNaN(end):
			end = last
		}
		if end > last {
			end = last
		}
		if math.IsNaN(start) || math.IsNaN(end) || start > end || start < 0 {
			continue
		}
		list = append(list, indexed{byteRange{int64(start), int64(end)}, len(list)})
	}
	if len(list) == 0 {
		return nil, false
	}
	sort.SliceStable(list, func(a, b int) bool { return list[a].start < list[b].start })
	joined := list[:1]
	for _, next := range list[1:] {
		current := &joined[len(joined)-1]
		switch {
		case next.start > current.end+1:
			joined = append(joined, next)
		case next.end > current.end:
			current.end = next.end
			if next.index < current.index {
				current.index = next.index
			}
		}
	}
	sort.SliceStable(joined, func(a, b int) bool { return joined[a].index < joined[b].index })
	for _, r := range joined {
		ranges = append(ranges, r.byteRange)
	}
	return ranges, true
}

var intPrefix = regexp.MustCompile(`^[+-]?[0-9]+`)

// parseInt reads the whole number a string starts with, as JavaScript's
// parseInt does; NaN when it starts with none.
func parseInt(s string) float64 {
	m := intPrefix.FindString(strings.TrimLeftFunc(s, isJSSpace))
	if m == "" {
		return math.NaN()
	}
	v, err := strconv.ParseFloat(m, 64)
	if err != nil && !math.IsInf(v, 0) {
		return math.NaN()
	}
	return v
}
