// Package chat holds the console's chat: the HTTP routes (uploading a
// file that a message links to, downloading it, searching messages) and
// the relay, the websockets of devices and operators (relay.go). The
// messages, contacts, groups and read marks are kept by the API server;
// both reach them through api.go.
//
// Files are kept in <console folder>/data/chat-files as
// <32 hex digits><extension>, where the extension is the first ten
// characters of the uploaded name's extension. A message links to a file
// as /api/chat/files/<32 hex digits>. Files are never deleted.
package chat

import (
	"crypto/rand"
	"io"
	"net/http"
	"path/filepath"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/webui"
)

// maxFileSize is the largest file an upload may carry, 50 MB.
const maxFileSize = 50 << 20

type handlers struct {
	api *goapi.Client
	// dir is the folder of the uploaded files.
	dir string
	// files sends a file of dir.
	files *httpx.Static
	// random gives the bytes of a new file's id.
	random io.Reader
	// errors is the web app host, whose error handler answers what the
	// routes do not answer themselves. Without it the answer is the one
	// the handler gives in production.
	errors *webui.Host
}

// Register adds the group's routes.
func Register(d *kit.Deps) {
	register(d, filepath.Join(d.Config.ConsoleDir, "data", "chat-files"))
}

func register(d *kit.Deps, dir string) *handlers {
	h := &handlers{api: d.GoAPI, dir: dir, random: rand.Reader, errors: d.WebUI}
	h.files = httpx.NewStatic(dir, httpx.StaticOptions{
		NoIndex:       true,
		NoFallthrough: true,
		SetHeaders:    attachment,
		OnError: func(w http.ResponseWriter, r *http.Request, status int, err error) {
			h.serverError(w, r, err, status)
		},
	})
	// The chat websocket needs chat.access; its HTTP routes need the same
	chatAccess := d.RequirePermission("chat.access")
	r := d.Router
	r.Post("/api/chat/upload", h.upload, d.RequireAuth, chatAccess)
	r.Get("/api/chat/files/{fileId}", h.download, d.RequireAuth, chatAccess)
	r.Get("/api/chat/search", h.search, d.RequireAuth, chatAccess)
	return h
}

// serverError answers through the console's error handler: the status,
// and in production "Internal Server Error" in place of the message.
func (h *handlers) serverError(w http.ResponseWriter, r *http.Request, err error, status int) {
	if h.errors != nil {
		h.errors.ServerError(w, r, err, status)
		return
	}
	if status == 0 {
		status = http.StatusInternalServerError
	}
	kit.Fail(w, r, status, "Internal Server Error")
}
