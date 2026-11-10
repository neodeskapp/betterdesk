package httpx

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
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
// <, > and &, no trailing newline.
func Marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
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
// names it gets 304 without a body. When the request is marked with
// WithJSONCompression, the client accepts gzip and the body is at least
// CompressMinBytes long, the body goes out gzip-compressed.
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
	compress := len(body) >= CompressMinBytes && r.Context().Value(compressKey{}) == true &&
		h.Get("Content-Encoding") == "" && AcceptsGzip(r.Header.Get("Accept-Encoding"))
	safe := r.Method == http.MethodGet || r.Method == http.MethodHead
	if compress {
		h.Add("Vary", "Accept-Encoding")
	}
	if h.Get("ETag") == "" && (safe || !compress) {
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

func acceptsEncoding(header, encoding string) bool {
	for _, part := range strings.Split(header, ",") {
		fields := strings.Split(strings.ToLower(strings.TrimSpace(part)), ";")
		if strings.TrimSpace(fields[0]) != encoding {
			continue
		}
		q := 1.0
		for _, param := range fields[1:] {
			if value, ok := strings.CutPrefix(strings.TrimSpace(param), "q="); ok {
				if parsed, err := strconv.ParseFloat(value, 64); err == nil {
					q = parsed
				}
			}
		}
		return q > 0
	}
	return false
}

// ISOTime formats t as JavaScript's Date.toISOString does: UTC with
// milliseconds, 2026-01-02T15:04:05.000Z.
func ISOTime(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}
