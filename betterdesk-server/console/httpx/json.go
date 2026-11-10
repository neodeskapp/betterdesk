package httpx

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// CompressMinBytes is the size from which a JSON answer is compressed.
const CompressMinBytes = 1024

type compressKey struct{}

// WithJSONCompression marks a request whose JSON answers may be
// compressed. The console sets it for its API routes.
func WithJSONCompression(ctx context.Context) context.Context {
	return context.WithValue(ctx, compressKey{}, true)
}

// Marshal encodes v as JSON the way the console always has: no escaping of
// <, > and &, the line separators U+2028 and U+2029 as characters, no
// trailing newline.
func Marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return unescapeLineSeparators(bytes.TrimSuffix(buf.Bytes(), []byte("\n"))), nil
}

// unescapeLineSeparators puts U+2028 and U+2029 back as characters.
// encoding/json writes them as six-character escapes; JSON.stringify does
// not, and the bytes of an answer count (length, ETag).
func unescapeLineSeparators(b []byte) []byte {
	if !bytes.Contains(b, []byte(`\u202`)) {
		return b
	}
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		if b[i] != '\\' || i+1 >= len(b) {
			out = append(out, b[i])
			continue
		}
		// Inside a JSON string a backslash starts an escape. Each escape is
		// taken whole, so an escaped backslash never starts another one.
		if b[i+1] == 'u' && i+5 < len(b) {
			switch string(b[i+2 : i+6]) {
			case "2028":
				out = append(out, 0xE2, 0x80, 0xA8)
				i += 5
				continue
			case "2029":
				out = append(out, 0xE2, 0x80, 0xA9)
				i += 5
				continue
			}
		}
		out = append(out, b[i], b[i+1])
		i++
	}
	return out
}

// ETag is the weak entity tag for a body: its length in hex and the first
// 27 characters of its SHA-1 in base64.
func ETag(body []byte) string {
	if len(body) == 0 {
		return `W/"0-2jmj7l5rSw0yVb/vlWAYkK/YBwk"`
	}
	sum := sha1.Sum(body)
	return `W/"` + strconv.FormatInt(int64(len(body)), 16) + "-" + base64.StdEncoding.EncodeToString(sum[:])[:27] + `"`
}

// Fresh reports whether the client's copy is still good, for a GET or HEAD
// with If-None-Match or If-Modified-Since. Every validator the request sent
// has to hold: If-None-Match must name etag (weak comparison, or "*"), and
// If-Modified-Since must not be before lastModified. A request that says
// Cache-Control: no-cache is never fresh.
func Fresh(r *http.Request, etag string, lastModified time.Time) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	noneMatch := r.Header.Get("If-None-Match")
	modifiedSince := r.Header.Get("If-Modified-Since")
	if noneMatch == "" && modifiedSince == "" {
		return false
	}
	for _, part := range strings.Split(r.Header.Get("Cache-Control"), ",") {
		if strings.TrimSpace(part) == "no-cache" {
			return false
		}
	}
	if noneMatch != "" && noneMatch != "*" {
		if etag == "" {
			return false
		}
		matched := false
		for _, candidate := range strings.Split(noneMatch, ",") {
			candidate = strings.TrimSpace(candidate)
			if candidate == etag || candidate == "W/"+etag || "W/"+candidate == etag {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	if modifiedSince != "" {
		if lastModified.IsZero() {
			return false
		}
		since, err := http.ParseTime(modifiedSince)
		if err != nil || lastModified.Truncate(time.Second).After(since) {
			return false
		}
	}
	return true
}

// WriteJSON answers with v as JSON and the given status.
//
// The answer carries a weak ETag, and a GET or HEAD whose If-None-Match
// names it gets 304 without a body.
//
// A request marked with WithJSONCompression whose answer is at least
// CompressMinBytes long is answered by what its Accept-Encoding offers.
// When that is gzip or brotli (with a q above zero), the answer says
// Vary: Accept-Encoding and, to a write, carries no ETag. The body goes out
// gzip-compressed when gzip is offered, and plain otherwise: a client that
// offers brotli alone gets the plain body.
func WriteJSON(w http.ResponseWriter, r *http.Request, status int, v any) {
	body, err := Marshal(v)
	if err != nil {
		http.Error(w, `{"success":false,"error":"Internal Server Error"}`, http.StatusInternalServerError)
		return
	}
	WriteJSONBytes(w, r, status, body)
}

// WriteJSONBytes is WriteJSON for a body that is already JSON.
func WriteJSONBytes(w http.ResponseWriter, r *http.Request, status int, body []byte) {
	h := w.Header()
	if h.Get("Content-Type") == "" {
		h.Set("Content-Type", "application/json; charset=utf-8")
	}
	// varies: the answer depends on Accept-Encoding. compress: it is gzip.
	varies, compress := false, false
	if len(body) >= CompressMinBytes && r.Context().Value(compressKey{}) == true && h.Get("Content-Encoding") == "" {
		offered := strings.Join(r.Header.Values("Accept-Encoding"), ", ")
		compress = AcceptsGzip(offered)
		varies = compress || acceptsEncoding(offered, "br")
	}
	safe := r.Method == http.MethodGet || r.Method == http.MethodHead
	if varies {
		AddVary(h, "Accept-Encoding")
	}
	if h.Get("ETag") == "" && (safe || !varies) {
		h.Set("ETag", ETag(body))
	}
	if status >= 200 && status < 300 && Fresh(r, h.Get("ETag"), time.Time{}) {
		h.Del("Content-Type")
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if status == http.StatusNoContent || status == http.StatusNotModified {
		h.Del("Content-Type")
		w.WriteHeader(status)
		return
	}
	if !compress {
		h.Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(status)
		if r.Method != http.MethodHead {
			_, _ = w.Write(body)
		}
		return
	}
	h.Set("Content-Encoding", "gzip")
	w.WriteHeader(status)
	if r.Method == http.MethodHead {
		return
	}
	zw, _ := gzip.NewWriterLevel(w, 6)
	_, _ = zw.Write(body)
	_ = zw.Close()
}

// AcceptsGzip reports whether an Accept-Encoding header allows gzip.
func AcceptsGzip(header string) bool {
	return acceptsEncoding(header, "gzip")
}

// acceptsEncoding reads an Accept-Encoding header the way the old console
// did. An entry is a name and parameters, split at ";". The name counts
// as written in lower case, without white space around the entry. A
// parameter q= followed by digits and dots sets the quality, the last one
// wins, and one that is no number (1.2.3) refuses the encoding. Without a
// q the quality is 1. When a name comes more than once, its last entry
// counts. The encoding is accepted when its quality is above zero.
func acceptsEncoding(header, encoding string) bool {
	accepted := false
	for _, part := range strings.Split(header, ",") {
		fields := strings.Split(lowerASCII(trimHeaderSpace(part)), ";")
		if fields[0] != encoding {
			continue
		}
		accepted = true
		for _, param := range fields[1:] {
			value, ok := strings.CutPrefix(trimHeaderSpace(param), "q=")
			if !ok || value == "" || strings.Trim(value, "0123456789.") != "" {
				continue
			}
			q, err := strconv.ParseFloat(value, 64)
			accepted = q > 0 && (err == nil || math.IsInf(q, 1))
		}
	}
	return accepted
}

// lowerASCII lowers the ASCII letters of a header value and leaves every
// other byte as it is.
func lowerASCII(s string) string {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c >= 'A' && c <= 'Z' {
			b := []byte(s)
			for j := i; j < len(b); j++ {
				if b[j] >= 'A' && b[j] <= 'Z' {
					b[j] += 'a' - 'A'
				}
			}
			return string(b)
		}
	}
	return s
}

// ISOTime formats t as JavaScript's Date.toISOString does: UTC with
// milliseconds, 2026-01-02T15:04:05.000Z.
func ISOTime(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}
