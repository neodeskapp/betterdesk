package middleware

import (
	"io"
	"io/fs"
	"net/http"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/httpx"
)

// multipartPaths are the API routes that take a file as
// multipart/form-data, by exact path.
var multipartPaths = map[string]bool{
	"/api/settings/restore":               true,
	"/api/generator/module/install-local": true,
}

// picturePaths are the API routes that take a picture file as the body
// itself, by exact path. They pass with the type of a picture
// (image/...) or application/octet-stream, which a form on another site
// cannot send either.
var picturePaths = map[string]bool{
	"/api/users/me/avatar": true,
}

// pictureBody reports whether the request announces a picture file.
func pictureBody(r *http.Request) bool {
	media, _, _ := strings.Cut(r.Header.Get("Content-Type"), ";")
	media = strings.ToLower(strings.TrimSpace(media))
	return strings.HasPrefix(media, "image/") && len(media) > len("image/") || media == "application/octet-stream"
}

type jsonOnlyAnswer struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
}

// RequireJSON makes POST, PUT and PATCH requests to /api/ send JSON, which
// a form on another site cannot. It passes GET, DELETE, OPTIONS and HEAD,
// paths outside /api/, paths with /upload or /import in them, the two
// multipart routes, and a picture sent to a route of picturePaths.
// Everything else needs a body announced by its headers
// and the type application/json, or it gets
//
//	415 {"success":false,"error":"Content-Type must be application/json"}
//
// The path is compared in any letter case, as the router matches routes:
// /API/x is under /api/, and /API/chat/UPLOAD is an upload.
func RequireJSON() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet, http.MethodDelete, http.MethodOptions, http.MethodHead:
				next.ServeHTTP(w, r)
				return
			}
			p := strings.ToLower(httpx.RawPath(r))
			if !strings.HasPrefix(p, "/api/") || strings.Contains(p, "/upload") || strings.Contains(p, "/import") || multipartPaths[p] || picturePaths[p] && pictureBody(r) || IsJSON(r) {
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
	// PublicDir is served at the root: favicon.svg, img/.
	PublicDir string
	// FontsDir is served at /fonts: the installed fonts.
	FontsDir string
	// ProtosDir is served at /protos.
	ProtosDir string
	// UploadsDir is served at /uploads: DATA_DIR/uploads, where uploaded
	// logos and backgrounds are.
	UploadsDir string
	// WallpapersDir is served at /wallpapers. It may not exist.
	WallpapersDir string
	// Static is the tree of the console's own files (package static):
	// its public and protos folders are served where PublicDir or
	// ProtosDir is empty.
	Static fs.FS
	// Production is NODE_ENV=production. It gives the files a max-age of 7
	// days (30 for uploads and wallpapers) in place of 0.
	Production bool
	// OnError answers a file request that failed on a disk error. A
	// precondition that does not hold (412) and a range outside the file
	// (416) are answered by the step itself, without a body.
	OnError ErrorHandler
	// FS replaces the operating system's files.
	FS httpx.StaticFS
}

// StaticFolders serves the console's own files: the public folder at the
// root, then /fonts, /protos, /uploads and /wallpapers. A request that
// names no file goes on to next; a folder without a trailing slash is
// redirected to the address with one.
//
// A file's Cache-Control is the folder's max-age; it replaces the one
// SecurityHeaders set before. A file of /uploads gets sandbox in its
// Content-Security-Policy.
func StaticFolders(opts StaticFoldersOptions) func(http.Handler) http.Handler {
	week, month := time.Duration(0), time.Duration(0)
	if opts.Production {
		week, month = 7*24*time.Hour, 30*24*time.Hour
	}
	var onError func(http.ResponseWriter, *http.Request, int, error)
	if opts.OnError != nil {
		onError = opts.OnError
	}
	type mount struct {
		mount, dir string
		static     string
		options    httpx.StaticOptions
	}
	mounts := []mount{
		{"", opts.PublicDir, "public", httpx.StaticOptions{MaxAge: week}},
		{"/fonts", opts.FontsDir, "", httpx.StaticOptions{MaxAge: week}},
		{"/protos", opts.ProtosDir, "protos", httpx.StaticOptions{MaxAge: week}},
		{"/uploads", opts.UploadsDir, "", httpx.StaticOptions{MaxAge: month, SetHeaders: sandboxed}},
		{"/wallpapers", opts.WallpapersDir, "", httpx.StaticOptions{MaxAge: month, Immutable: true}},
	}
	return func(next http.Handler) http.Handler {
		handler := next
		for i := len(mounts) - 1; i >= 0; i-- {
			m := mounts[i]
			m.options.OnError = onError
			m.options.AnswerClientErrors = true
			m.options.FS = opts.FS
			m.options.OwnCacheControl = true
			root := filepath.Clean(m.dir)
			switch {
			case m.dir != "":
			case m.static != "" && opts.Static != nil:
				root = staticRoot(m.static)
				m.options.FS = staticTree{root: root, fsys: opts.Static, dir: m.static}
			default:
				continue
			}
			handler = httpx.NewStatic(root, m.options).Middleware(m.mount)(handler)
		}
		return handler
	}
}

// sandboxed adds sandbox to the Content-Security-Policy of a file of the
// uploads folder, where people's logos and backgrounds are: markup in an
// uploaded SVG does not run when someone opens the file. Pictures shown in
// a page are not affected.
func sandboxed(w http.ResponseWriter, _ string, _ fs.FileInfo) {
	policy := "sandbox"
	if before := w.Header().Get("Content-Security-Policy"); before != "" {
		policy = before + ";sandbox"
	}
	w.Header().Set("Content-Security-Policy", policy)
}

// staticRoot is the name a folder of the console's tree goes by in a
// file server: the server makes file names of it as of a folder on disk,
// and staticTree turns them back.
func staticRoot(dir string) string {
	root, err := filepath.Abs(filepath.FromSlash("/neodesk-static/" + dir))
	if err != nil {
		return filepath.FromSlash("/neodesk-static/" + dir)
	}
	return root
}

// staticTree is the folder dir of the console's tree as the file server
// reaches it: by the names it makes of root.
type staticTree struct {
	root string
	fsys fs.FS
	dir  string
}

// name is the slash-separated name in fsys of a file name made of root.
func (t staticTree) name(op, file string) (string, error) {
	rel := "."
	if file != t.root {
		rest, ok := strings.CutPrefix(file, t.root+string(filepath.Separator))
		if !ok || !fs.ValidPath(filepath.ToSlash(rest)) {
			return "", &fs.PathError{Op: op, Path: file, Err: fs.ErrNotExist}
		}
		rel = filepath.ToSlash(rest)
	}
	return path.Join(t.dir, rel), nil
}

func (t staticTree) Stat(file string) (fs.FileInfo, error) {
	name, err := t.name("stat", file)
	if err != nil {
		return nil, err
	}
	return fs.Stat(t.fsys, name)
}

func (t staticTree) Open(file string) (io.ReadSeekCloser, error) {
	name, err := t.name("open", file)
	if err != nil {
		return nil, err
	}
	f, err := t.fsys.Open(name)
	if err != nil {
		return nil, err
	}
	seekable, ok := f.(io.ReadSeekCloser)
	if !ok {
		_ = f.Close()
		return nil, &fs.PathError{Op: "open", Path: file, Err: fs.ErrInvalid}
	}
	return seekable, nil
}
