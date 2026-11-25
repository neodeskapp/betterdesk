// Package chat holds the console's chat: the HTTP routes (uploading a
// file that a message links to, downloading it, searching messages) and
// the relay, the websockets of devices and operators (relay.go). The
// messages, contacts, groups and read marks are kept by the API server;
// both reach them through api.go.
//
// Files are kept in <console folder>/data/chat-files as
// <32 hex digits><extension>, where the extension is the first ten
// characters of the uploaded name's extension. A message links to a file
// as /api/chat/files/<32 hex digits>. Who may download a file is noted
// next to it (links.go). Files are never deleted: once they hold
// maxFolderSize, the folder takes no more uploads.
package chat

import (
	"crypto/rand"
	"io"
	"net/http"
	"path/filepath"

	"github.com/neodeskapp/neodesk/neodesk-server/console/config"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/httpx"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/session"
	"github.com/neodeskapp/neodesk/neodesk-server/console/webui"
)

// maxFileSize is the largest file an upload may carry, 50 MB.
const maxFileSize = 50 << 20

// maxFolderSize is how much the chat files may hold together, 2 GiB.
const maxFolderSize = 2 << 30

type handlers struct {
	api *goapi.Client
	// dir is the folder of the uploaded files.
	dir string
	// maxFolder is how many bytes the files of dir may hold together.
	maxFolder int64
	// files sends a file of dir.
	files *httpx.Static
	// random gives the bytes of a new file's id.
	random io.Reader
	// errors is the web app host, whose error handler answers what the
	// routes do not answer themselves.
	errors *webui.Host
	// scope is the device scope a download is checked against.
	scope Scope
	// user is the signed-in account (kit.User).
	user func(r *http.Request) *session.User
}

// FilesDir is the folder of the chat files: <console folder>/data/chat-files.
func FilesDir(cfg *config.Config) string {
	return filepath.Join(cfg.ConsoleDir, "data", "chat-files")
}

// ChatPermissions open the chat: Support chat and Team chat (A242).
var ChatPermissions = []string{"chat.support", "chat.team"}

// Register adds the group's routes.
func Register(d *kit.Deps) {
	h := register(d, FilesDir(d.Config))
	h.scope = ScopeOf(d)
}

func register(d *kit.Deps, dir string) *handlers {
	h := &handlers{api: d.GoAPI, dir: dir, maxFolder: maxFolderSize, random: rand.Reader, errors: d.WebUI, user: kit.User}
	if h.errors == nil {
		// Without the web app the error handler is the one of production
		h.errors = webui.New(webui.Options{Production: true})
	}
	h.files = httpx.NewStatic(dir, httpx.StaticOptions{
		NoIndex:       true,
		NoFallthrough: true,
		SetHeaders:    attachment,
		OnError: func(w http.ResponseWriter, r *http.Request, status int, err error) {
			// The sender refused after it set the file's headers: the
			// error answer is JSON and no file to save
			w.Header().Del("Content-Type")
			w.Header().Del("Content-Disposition")
			h.serverError(w, r, err, status)
		},
	})
	// The chat websocket needs Support chat or Team chat; so do the
	// HTTP routes both use
	chatAccess := d.RequireAnyPermission(ChatPermissions...)
	r := d.Router
	r.Post("/api/chat/upload", h.upload, d.Limits.Upload.Middleware, d.RequireAuth, chatAccess)
	r.Get("/api/chat/files/{fileId}", h.download, d.RequireAuth, chatAccess)
	r.Get("/api/chat/search", h.search, d.RequireAuth, chatAccess)
	return h
}

// serverError answers through the console's error handler (webui): the
// status, and in production "Internal Server Error" in place of the
// message of a failure of the server.
func (h *handlers) serverError(w http.ResponseWriter, r *http.Request, err error, status int) {
	h.errors.ServerError(w, r, err, status)
}
