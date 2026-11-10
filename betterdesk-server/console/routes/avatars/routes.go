// Package avatars serves the profile pictures of console users: the list
// of who has one and the pictures themselves.
//
// Anyone signed in to the console may see any user's picture and the
// list. That is the reach of the names people see next to chat messages
// and in the audit log already.
package avatars

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	avatarsvc "github.com/unitronix/betterdesk-server/console/avatars"
	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/kit"
)

type handlers struct {
	avatars *avatarsvc.Service
	log     *kit.Logger
}

// Register adds the group's routes.
func Register(d *kit.Deps) {
	h := &handlers{avatars: d.Avatars, log: d.Log.Child("Avatar")}
	r := d.Router
	r.Get("/api/users/avatars", h.list, d.RequireAuth, d.Limits.PanelRead.Middleware)
	r.Get("/api/users/{id}/avatar", h.picture, d.RequireAuth, d.Limits.PanelRead.Middleware)
}

type directory struct {
	Users []avatarsvc.DirectoryEntry `json:"users"`
}

// list answers GET /api/users/avatars: id, name and picture version of
// every user with a picture.
func (h *handlers) list(w http.ResponseWriter, r *http.Request) {
	users, err := h.avatars.ListDirectory(r.Context())
	if err != nil {
		h.log.Error("list error:", err)
		kit.Fail(w, r, http.StatusInternalServerError, "server_error")
		return
	}
	w.Header().Set("Cache-Control", "private, no-cache")
	kit.OK(w, r, directory{Users: users})
}

// picture answers GET /api/users/{id}/avatar with the stored bytes of a
// user's picture.
//
// The ETag is the picture's version. An address with the current version
// as ?v= never changes, so the browser may keep its answer for a day; any
// other address is checked against the ETag each time. The headers keep a
// browser from reading the bytes as anything but a picture.
func (h *handlers) picture(w http.ResponseWriter, r *http.Request) {
	id := userID(r.PathValue("id"))
	if id == 0 {
		kit.Fail(w, r, http.StatusNotFound, "not_found")
		return
	}
	row, err := h.avatars.GetAvatar(r.Context(), id)
	if err != nil {
		h.log.Error("read error:", err)
		kit.Fail(w, r, http.StatusInternalServerError, "server_error")
		return
	}
	header := w.Header()
	// A row without bytes is served as it is: an empty picture
	if row == nil || !servable(lowerASCII(row.MimeType)) {
		header.Set("Cache-Control", "private, no-cache")
		kit.Fail(w, r, http.StatusNotFound, "not_found")
		return
	}
	etag := `"` + row.Version + `"`
	cache := "private, no-cache"
	if v, ok := queryText(r.URL.RawQuery, "v"); ok && v == row.Version {
		cache = "private, max-age=86400"
	}
	header.Set("Cache-Control", cache)
	header.Set("ETag", etag)
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Content-Security-Policy", "default-src 'none'")
	header.Set("Content-Disposition", "inline")
	header.Set("Cross-Origin-Resource-Policy", "same-origin")
	if matchesETag(strings.Join(r.Header.Values("If-None-Match"), ", "), etag) {
		// No Content-Type: the HTTP server takes it off every 304
		w.WriteHeader(http.StatusNotModified)
		return
	}
	header.Set("Content-Type", lowerASCII(row.MimeType))
	header.Set("Content-Length", strconv.Itoa(len(row.Data)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(row.Data)
	}
}

// userID reads the id of a picture address: one to ten digits. Anything
// else, and zero, is 0.
func userID(text string) int64 {
	if len(text) < 1 || len(text) > 10 {
		return 0
	}
	for i := 0; i < len(text); i++ {
		if text[i] < '0' || text[i] > '9' {
			return 0
		}
	}
	id, _ := strconv.ParseInt(text, 10, 64)
	return id
}

// servable reports the picture types the console sends.
func servable(mime string) bool {
	switch mime {
	case avatarsvc.TypePNG, avatarsvc.TypeJPEG, avatarsvc.TypeWebP, avatarsvc.TypeGIF:
		return true
	}
	return false
}

// matchesETag reports whether an If-None-Match header names the ETag:
// one of its comma-separated entries, with or without "W/" in front, is
// the ETag exactly. "*" matches nothing.
func matchesETag(header, etag string) bool {
	if header == "" {
		return false
	}
	for _, entry := range strings.Split(httpx.Latin1(header), ",") {
		if strings.TrimPrefix(strings.TrimFunc(entry, isJSSpace), "W/") == etag {
			return true
		}
	}
	return false
}

// isJSSpace reports what JavaScript's trim removes.
func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

func lowerASCII(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + 'a' - 'A'
		}
		return r
	}, s)
}

// queryText returns a parameter of the query when the query gives it once
// as plain text. ok is false when it is missing, repeated or written with
// brackets (v[]=, v[x]=), which the old console read as a list or an
// object.
func queryText(rawQuery, name string) (value string, ok bool) {
	seen := 0
	for _, pair := range strings.Split(rawQuery, "&") {
		if pair == "" {
			continue
		}
		rawKey, rawValue, _ := strings.Cut(pair, "=")
		key := queryDecode(rawKey)
		switch {
		case key == name:
			seen++
			value = queryDecode(rawValue)
		case strings.HasPrefix(key, name+"["):
			return "", false
		}
	}
	return value, seen == 1
}

// queryDecode decodes one key or value of a query: "+" is a space, and a
// text whose escapes do not decode stays as it is.
func queryDecode(s string) string {
	s = strings.ReplaceAll(s, "+", " ")
	if decoded, err := url.PathUnescape(s); err == nil {
		return decoded
	}
	return s
}
