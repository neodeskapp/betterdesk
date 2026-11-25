package chat

import (
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/middleware"
)

// uploaded is the answer to an upload.
type uploaded struct {
	Success  bool   `json:"success"`
	FileID   string `json:"file_id"`
	Filename string `json:"filename"`
	Size     int64  `json:"size"`
	URL      string `json:"url"`
}

// upload answers POST /api/chat/upload: a multipart form with the file in
// the field "file". The chat client encrypts the file before it sends it;
// the console stores the bytes as they come.
//
// A form the parser refuses (a file in another field, a second file, a
// file over 50 MB, a broken body) goes to the console's error handler,
// which answers 500. Once the files hold maxFolder bytes, an upload is
// refused with 507, before its body is read; a file that would take the
// folder over is removed again.
func (h *handlers) upload(w http.ResponseWriter, r *http.Request) {
	h.store(w, r, func(r *http.Request) link {
		if user := h.user(r); user != nil && user.ID != 0 {
			return link{Owner: user.ID}
		}
		return link{}
	})
}

// deviceUpload answers POST /api/chat/device/files for a device's chat
// window (A242): the same form and limits as upload, with the device's
// token; the file is the device's to send into its conversation.
func (h *handlers) deviceUpload(w http.ResponseWriter, r *http.Request) {
	h.store(w, r, func(r *http.Request) link { return link{Device: kit.Device(r).DeviceID} })
}

// deviceDownload answers GET /api/chat/device/files/{fileId} for a
// device's chat window: a file the device uploaded or one shared into its
// conversation; with ?inline=1 a picture to show.
func (h *handlers) deviceDownload(w http.ResponseWriter, r *http.Request) {
	id := lowerASCII(r.PathValue("fileId"))
	if !isFileID(id) {
		kit.Fail(w, r, http.StatusBadRequest, "Invalid file ID")
		return
	}
	device := kit.Device(r).DeviceID
	name, err := findFile(h.dir, id)
	if err != nil {
		h.serverError(w, r, err, 0)
		return
	}
	links, err := readLinks(h.dir, id)
	if err != nil {
		h.serverError(w, r, err, 0)
		return
	}
	allowed := name != "" && (links.device == device || slices.Contains(links.conversations, device))
	if !allowed {
		kit.Fail(w, r, http.StatusNotFound, "File not found")
		return
	}
	if r.URL.Query().Get("inline") == "1" && inlinePicture(name, links.meta) {
		h.pictures.ServeName(w, r, name, nil)
		return
	}
	h.files.ServeName(w, r, name, nil)
}

// store keeps an uploaded file with the link of who uploaded it.
func (h *handlers) store(w http.ResponseWriter, r *http.Request, uploader func(r *http.Request) link) {
	used, err := folderSize(h.dir)
	if err != nil {
		h.serverError(w, r, err, 0)
		return
	}
	if used >= h.maxFolder {
		kit.Fail(w, r, http.StatusInsufficientStorage, folderFull)
		return
	}
	form, err := middleware.ParseUpload(r, middleware.UploadOptions{
		Field:       "file",
		MaxFileSize: maxFileSize,
		Dir:         h.dir,
		Filename:    h.storedName,
	})
	if err != nil {
		h.serverError(w, r, err, 0)
		return
	}
	if form.File == nil {
		kit.Fail(w, r, http.StatusBadRequest, "No file provided")
		return
	}
	name := form.File.Filename
	id := strings.TrimSuffix(name, extname(name))
	if used, err = folderSize(h.dir); err != nil || used > h.maxFolder {
		_ = os.Remove(filepath.Join(h.dir, name))
		if err != nil {
			h.serverError(w, r, err, 0)
			return
		}
		kit.Fail(w, r, http.StatusInsufficientStorage, folderFull)
		return
	}
	if l := uploader(r); l.Owner != 0 || l.Device != "" {
		if err := addLink(h.dir, id, l); err != nil {
			_ = os.Remove(filepath.Join(h.dir, name))
			h.serverError(w, r, err, 0)
			return
		}
	}
	// What the file is, for the messages that carry it (A242)
	meta := readMeta(filepath.Join(h.dir, name), form.File.OriginalName, form.File.Size)
	if err := addLink(h.dir, id, link{Meta: &meta}); err != nil {
		_ = os.Remove(filepath.Join(h.dir, name))
		h.serverError(w, r, err, 0)
		return
	}
	kit.JSON(w, r, http.StatusOK, uploaded{Success: true, FileID: id, Filename: name, Size: form.File.Size, URL: "/api/chat/files/" + id})
}

// folderFull is the answer to an upload when the files hold maxFolder.
const folderFull = "Chat file storage is full"

// folderSize is how many bytes the files of dir hold together; 0 before
// the first upload made the folder.
func folderSize(dir string) (int64, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var total int64
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		info, err := entry.Info()
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return 0, err
		}
		total += info.Size()
	}
	return total, nil
}

// storedName names a new file: 16 random bytes in hex and the start of
// the uploaded name's extension. The folder is made when the first file
// arrives.
func (h *handlers) storedName(file *middleware.UploadedFile) (string, error) {
	if err := os.MkdirAll(h.dir, 0o777); err != nil {
		return "", err
	}
	id := make([]byte, 16)
	if _, err := io.ReadFull(h.random, id); err != nil {
		return "", err
	}
	return hex.EncodeToString(id) + storedExtension(file.OriginalName), nil
}

// download answers GET /api/chat/files/{fileId} with the file whose name
// is the id or starts with the id and a dot. Only a whole id finds its
// file: 32 hex digits, in either case. A file the account may not
// download (see mayDownload) is not found either. With ?inline=1 a
// picture (inlinePicture) comes to show in the page; any other file
// still comes as a download.
func (h *handlers) download(w http.ResponseWriter, r *http.Request) {
	id := lowerASCII(r.PathValue("fileId"))
	if !isFileID(id) {
		kit.Fail(w, r, http.StatusBadRequest, "Invalid file ID")
		return
	}
	name, err := findFile(h.dir, id)
	if err != nil {
		h.serverError(w, r, err, 0)
		return
	}
	allowed := false
	if name != "" {
		if allowed, err = mayDownload(r.Context(), h.dir, h.scope, h.user(r), id, h.members); err != nil {
			h.serverError(w, r, err, 0)
			return
		}
	}
	if !allowed {
		kit.Fail(w, r, http.StatusNotFound, "File not found")
		return
	}
	if r.URL.Query().Get("inline") == "1" {
		if links, err := readLinks(h.dir, id); err == nil && inlinePicture(name, links.meta) {
			h.pictures.ServeName(w, r, name, nil)
			return
		}
	}
	h.files.ServeName(w, r, name, nil)
}

// attachment sets the header that makes a browser save the file under
// its stored name.
func attachment(w http.ResponseWriter, filePath string, _ fs.FileInfo) {
	w.Header().Set("Content-Disposition", contentDisposition(filepath.Base(filePath)))
}

// isFileID reports whether id is 32 lower-case hex digits.
func isFileID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func lowerASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}
