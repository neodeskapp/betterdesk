package chat

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/avatars"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
)

// helperPicture answers GET /api/chat/device/helper/picture for a
// device's chat window: the profile picture of the account that took the
// device's conversation, which the helper frame names by its version.
// Without a helper, or when the helper has no picture, it is 404. The
// picture is the console's stored copy, served like
// GET /api/users/{id}/avatar: an ETag of its version, and headers that
// keep it a picture.
func (h *handlers) helperPicture(w http.ResponseWriter, r *http.Request) {
	header := w.Header()
	header.Set("Cache-Control", "private, no-cache")
	if h.helper == nil || h.avatarStore == nil {
		kit.Fail(w, r, http.StatusNotFound, "not_found")
		return
	}
	assignee, err := h.helper(r.Context(), kit.Device(r).DeviceID)
	if err != nil || assignee == 0 {
		kit.Fail(w, r, http.StatusNotFound, "not_found")
		return
	}
	pic, err := h.avatarStore.GetAvatar(r.Context(), assignee)
	if err != nil {
		h.serverError(w, r, err, http.StatusInternalServerError)
		return
	}
	if pic == nil || len(pic.Data) == 0 || !avatarType(pic.MimeType) {
		kit.Fail(w, r, http.StatusNotFound, "not_found")
		return
	}
	etag := `"` + pic.Version + `"`
	header.Set("ETag", etag)
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Content-Security-Policy", "default-src 'none'")
	header.Set("Content-Disposition", "inline")
	for _, sent := range strings.Split(r.Header.Get("If-None-Match"), ",") {
		if strings.TrimPrefix(strings.TrimSpace(sent), "W/") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	header.Set("Content-Type", pic.MimeType)
	header.Set("Content-Length", strconv.Itoa(len(pic.Data)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(pic.Data)
	}
}

func avatarType(mime string) bool {
	switch mime {
	case avatars.TypePNG, avatars.TypeJPEG, avatars.TypeWebP, avatars.TypeGIF:
		return true
	}
	return false
}
