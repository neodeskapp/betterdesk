// Package chat holds the console's chat: the HTTP routes (uploading a
// file that a message links to, downloading it), the routes of version 2
// (inbox.go, search.go: the inbox, the team, searching messages) and
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
	"context"
	"crypto/rand"
	"io"
	"io/fs"
	"net/http"
	"path/filepath"

	"github.com/neodeskapp/neodesk/neodesk-server/console/avatars"
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
	// pictures sends a picture to show in the page (inline=1).
	pictures *httpx.Static
	// members reports whether an account reads a direct or group
	// conversation (Hub.MayRead); nil: as before version 2.
	members func(ctx context.Context, user *session.User, conv string) bool
	// helper is the account that took a device's conversation, 0 for
	// none; nil: no helper pictures.
	helper func(ctx context.Context, device string) (int64, error)
	// avatarStore keeps the profile pictures.
	avatarStore *avatars.Service
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
	h.members = HubOf(d).MayRead
	registerInbox(d, HubOf(d))
}

func register(d *kit.Deps, dir string) *handlers {
	h := &handlers{api: d.GoAPI, dir: dir, maxFolder: maxFolderSize, random: rand.Reader, errors: d.WebUI, user: kit.User}
	h.avatarStore = d.Avatars
	if d.Chat != nil {
		h.helper = func(ctx context.Context, device string) (int64, error) {
			c, err := d.Chat.Conversation(ctx, device)
			return c.AssigneeID, err
		}
	}
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
	h.pictures = httpx.NewStatic(dir, httpx.StaticOptions{
		NoIndex:       true,
		NoFallthrough: true,
		SetHeaders: func(w http.ResponseWriter, _ string, _ fs.FileInfo) {
			w.Header().Set("Content-Disposition", "inline")
			w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
		},
		OnError: func(w http.ResponseWriter, r *http.Request, status int, err error) {
			w.Header().Del("Content-Type")
			w.Header().Del("Content-Disposition")
			h.serverError(w, r, err, status)
		},
	})
	// Support chat and Team chat both send files: no section
	r := d.Router.NoSection()
	r.Post("/api/chat/upload", h.upload, d.Limits.Upload.Middleware, d.RequireAuth, chatAccess)
	r.Get("/api/chat/files/{fileId}", h.download, d.RequireAuth, chatAccess)
	// A device's chat window sends and fetches files with its device
	// token (A242): device API routes, which come before the CSRF gate and
	// the JSON rule, so a request without a token gets the token's 401
	// (L526). The server's API port hands these to the console too.
	if d.Early != nil {
		d.Early.Post("/api/chat/device/files", h.deviceUpload, d.Limits.Upload.Middleware, d.RequireDeviceToken)
		d.Early.Get("/api/chat/device/files/{fileId}", h.deviceDownload, d.RequireDeviceToken)
		d.Early.Get("/api/chat/device/helper/picture", h.helperPicture, d.RequireDeviceToken)
	}
	return h
}

// serverError answers through the console's error handler (webui): the
// status, and in production "Internal Server Error" in place of the
// message of a failure of the server.
func (h *handlers) serverError(w http.ResponseWriter, r *http.Request, err error, status int) {
	h.errors.ServerError(w, r, err, status)
}
