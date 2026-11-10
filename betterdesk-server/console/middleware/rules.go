package middleware

import (
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/unitronix/betterdesk-server/console/httpx"
)

// multipartPaths are the API routes that take a file as
// multipart/form-data, by exact path.
var multipartPaths = map[string]bool{
	"/api/settings/restore":               true,
	"/api/generator/module/install-local": true,
}

type jsonOnlyAnswer struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
}

// RequireJSON makes POST, PUT and PATCH requests to /api/ send JSON, which
// a form on another site cannot. It passes GET, DELETE, OPTIONS and HEAD,
// paths outside /api/, paths with /upload or /import in them, and the two
// multipart routes. Everything else needs a body announced by its headers
// and the type application/json, or it gets
//
//	415 {"success":false,"error":"Content-Type must be application/json"}
//
// The path is compared as the client wrote it: /API/x is not under /api/.
func RequireJSON() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet, http.MethodDelete, http.MethodOptions, http.MethodHead:
				next.ServeHTTP(w, r)
				return
			}
			p := httpx.RawPath(r)
			if !strings.HasPrefix(p, "/api/") || strings.Contains(p, "/upload") || strings.Contains(p, "/import") || multipartPaths[p] || IsJSON(r) {
				next.ServeHTTP(w, r)
				return
			}
			httpx.WriteJSON(w, r, http.StatusUnsupportedMediaType, jsonOnlyAnswer{Error: "Content-Type must be application/json"})
		})
	}
}

var fileLikePath = regexp.MustCompile(`\.(js|css|png|jpg|jpeg|gif|svg|ico|woff2?|ttf|eot|map|proto)$`)

// NoStoreForNonFiles sets Cache-Control: no-cache, no-store,
// must-revalidate on every request whose path does not end like a file
// (.js, .css, images, fonts, .map, .proto), so nothing the API or a page
// answers is stored. A route may still set its own.
func NoStoreForNonFiles() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !fileLikePath.MatchString(httpx.RawPath(r)) {
				w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
			}
			next.ServeHTTP(w, r)
		})
	}
}

// MarkAPICompression lets the JSON answers of requests under /api/ go out
// compressed (httpx.WriteJSON does it for answers of 1 KB and more).
// exempt names the requests that stay as they are: the RustDesk client API
// routes and the device API router, whose answers are never compressed
// because they sit before this step. nil exempts nothing.
func MarkAPICompression(exempt func(*http.Request) bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, _, ok := httpx.StripMount(httpx.RawPath(r), "/api/"); ok && (exempt == nil || !exempt(r)) {
				r = r.WithContext(httpx.WithJSONCompression(r.Context()))
			}
			next.ServeHTTP(w, r)
		})
	}
}

// StaticFoldersOptions name the folders the console serves as files.
type StaticFoldersOptions struct {
	// PublicDir is served at the root: favicon.svg, fonts/, img/.
	PublicDir string
	// ProtosDir is served at /protos.
	ProtosDir string
	// UploadsDir is served at /uploads: DATA_DIR/uploads, where uploaded
	// logos and backgrounds are.
	UploadsDir string
	// WallpapersDir is served at /wallpapers. It may not exist.
	WallpapersDir string
	// Production is NODE_ENV=production. It gives the files a max-age of 7
	// days (30 for uploads and wallpapers) in place of 0.
	Production bool
	// OnError answers a file request that failed after the file was found
	// (412, 416) or on a disk error.
	OnError ErrorHandler
	// FS replaces the operating system's files.
	FS httpx.StaticFS
}

// StaticFolders serves the console's own files: the public folder at the
// root, then /protos, /uploads and /wallpapers. A request that names no
// file goes on to next; a folder without a trailing slash is redirected
// to the address with one.
//
// Cache-Control is only set when it is missing. Behind SecurityHeaders it
// never is, so the files answer no-store, or one hour under /img/ and
// /fonts/, and the max-age of this step has no effect.
func StaticFolders(opts StaticFoldersOptions) func(http.Handler) http.Handler {
	week, month := time.Duration(0), time.Duration(0)
	if opts.Production {
		week, month = 7*24*time.Hour, 30*24*time.Hour
	}
	var onError func(http.ResponseWriter, *http.Request, int, error)
	if opts.OnError != nil {
		onError = opts.OnError
	}
	mounts := []struct {
		mount, dir string
		options    httpx.StaticOptions
	}{
		{"", opts.PublicDir, httpx.StaticOptions{MaxAge: week}},
		{"/protos", opts.ProtosDir, httpx.StaticOptions{MaxAge: week}},
		{"/uploads", opts.UploadsDir, httpx.StaticOptions{MaxAge: month}},
		{"/wallpapers", opts.WallpapersDir, httpx.StaticOptions{MaxAge: month, Immutable: true}},
	}
	return func(next http.Handler) http.Handler {
		handler := next
		for i := len(mounts) - 1; i >= 0; i-- {
			m := mounts[i]
			if m.dir == "" {
				continue
			}
			m.options.OnError = onError
			m.options.FS = opts.FS
			handler = httpx.NewStatic(filepath.Clean(m.dir), m.options).Middleware(m.mount)(handler)
		}
		return handler
	}
}
