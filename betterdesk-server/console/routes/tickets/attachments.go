package tickets

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/safepath"
)

// maxAttachmentSize is the largest file a ticket takes: 25 MB.
const maxAttachmentSize = 25 * 1024 * 1024

func randomBytes(b []byte) (int, error) { return rand.Read(b) }

// makeUploads creates the folder of the attachment files.
func (h *handlers) makeUploads() {
	if err := os.MkdirAll(h.uploads, 0o777); err != nil {
		h.log.Error("Cannot create the attachment folder "+h.uploads+":", err)
	}
}

// confined returns the path of a stored file after checking that it lies
// in the attachment folder, also when it is reached through a link. A row
// holds the file's name in the folder. A row written before holds an
// absolute path, of which the name counts, so its file is found where the
// folder is now, also after the install moved.
func (h *handlers) confined(storagePath string) (string, error) {
	path, err := safepath.ResolveChild(h.uploads, storagePath[strings.LastIndexAny(storagePath, `/\`)+1:])
	if err != nil {
		return "", err
	}
	return safepath.ResolveWithinRoot(path, h.uploads)
}

// hiddenBelow reports whether a part of the path below the attachment
// folder starts with a dot. Such a file is not sent; the folders above
// it, where the console is installed, do not count.
func (h *handlers) hiddenBelow(path string) bool {
	root, err := filepath.EvalSymlinks(h.uploads)
	if err != nil {
		root = h.uploads
	}
	below, err := filepath.Rel(root, path)
	return err != nil || hasDotPart(below)
}

// removeFile deletes the file of an attachment. A file that is gone, or a
// path that is not in the attachment folder, is left alone.
func (h *handlers) removeFile(storagePath string) {
	if path, err := h.confined(storagePath); err == nil {
		_ = os.Remove(path)
	}
}

// errNotBase64 is the answer to file data that is no base64 text, or
// holds no bytes: the file goes in as base64 text, as the web app sends it.
var errNotBase64 = errors.New("the file data is no base64 text")

// fileBytes is the bytes of the uploaded file: data is standard base64
// text with its padding, read strictly; white space in it is skipped. Any
// other value, and text that decodes to nothing, is an error, before
// anything is made of it.
func fileBytes(data jsbind.Value) ([]byte, error) {
	if !data.IsString() {
		return nil, errNotBase64
	}
	compact := strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\n', '\r', '\f', '\v':
			return -1
		}
		return r
	}, data.Str())
	file, err := base64.StdEncoding.Strict().DecodeString(compact)
	if err != nil || len(file) == 0 {
		return nil, errNotBase64
	}
	return file, nil
}

// safeName replaces every character of a file name that is not a letter
// of a to z, a digit, a dot, a dash or an underscore by an underscore. A
// character outside the basic plane becomes two.
func safeName(filename string) string {
	var b strings.Builder
	for _, r := range filename {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		case r > 0xFFFF:
			b.WriteString("__")
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

type attached struct {
	Success    bool           `json:"success"`
	Attachment attachedDetail `json:"attachment"`
}

type attachedDetail struct {
	ID        jsbind.Cell `json:"id"`
	Filename  string      `json:"filename"`
	SizeBytes int         `json:"size_bytes"`
}

// attach answers POST /api/tickets/{id}/attachments with the body
// {filename, data, mimetype?}, data being the file in base64. The file is
// written to the attachment folder under the ticket's id, 16 random hex
// digits and the cleaned name; its row follows, with that name.
func (h *handlers) attach(w http.ResponseWriter, r *http.Request) {
	t, err := h.ticketOf(r)
	if err != nil {
		h.serverError(w, r, "Attachment upload error", err)
		return
	}
	if t == nil {
		fail(w, r, http.StatusNotFound, "Ticket not found")
		return
	}
	body := kit.Body(r)
	filename, data := jsbind.Of(body.Get("filename")), jsbind.Of(body.Get("data"))
	if !filename.Truthy() || !data.Truthy() {
		fail(w, r, http.StatusBadRequest, "Filename and data (base64) are required")
		return
	}
	file, err := fileBytes(data)
	if err != nil {
		fail(w, r, http.StatusBadRequest, "Filename and data (base64) are required")
		return
	}
	mimetype := jsbind.Of(body.Get("mimetype"))
	if !mimetype.IsString() && !mimetype.IsNull() && !mimetype.IsUndefined() {
		fail(w, r, http.StatusBadRequest, "Invalid mimetype")
		return
	}
	if len(file) > maxAttachmentSize {
		fail(w, r, http.StatusRequestEntityTooLarge, "Attachment too large. Max 25 MB")
		return
	}
	if err == nil && !filename.IsString() {
		err = errors.New("filename.replace is not a function")
	}
	var stored, storagePath string
	name := safeName(filename.Str())
	if err == nil {
		random := make([]byte, 8)
		if _, err = h.random(random); err == nil {
			stored = number(t.ID) + "_" + hex.EncodeToString(random) + "_" + name
			storagePath, err = safepath.ResolveChild(h.uploads, stored)
		}
	}
	if err == nil {
		err = os.WriteFile(storagePath, file, 0o666)
	}
	var id jsbind.Cell
	if err == nil {
		mediatype := "application/octet-stream"
		if mediaType(mimetype.Str()) {
			mediatype = mimetype.Str()
		}
		id, err = addAttachment(r.Context(), h.db, t.id(), name, jsbind.String(mediatype), len(file), stored, kit.User(r).Username)
		if err != nil {
			// A file without a row is reached by nothing
			_ = os.Remove(storagePath)
		}
	}
	if err != nil {
		h.serverError(w, r, "Attachment upload error", err)
		return
	}
	h.log.Info("Attachment " + name + " uploaded to ticket #" + number(t.ID))
	kit.JSON(w, r, http.StatusCreated, attached{Success: true, Attachment: attachedDetail{ID: id, Filename: name, SizeBytes: len(file)}})
}

type attachmentList struct {
	Attachments []attachment `json:"attachments"`
}

// attachments answers GET /api/tickets/{id}/attachments; a viewer gets
// none of a ticket it may not read.
func (h *handlers) attachments(w http.ResponseWriter, r *http.Request) {
	id, ok := ticketID(r, "id")
	if ok && kit.User(r).Role == "viewer" {
		t, err := h.readable(r, id)
		if err != nil {
			h.serverError(w, r, "Attachments list error", err)
			return
		}
		ok = t != nil
	}
	if !ok {
		kit.JSON(w, r, http.StatusOK, attachmentList{Attachments: []attachment{}})
		return
	}
	attachments, err := ticketAttachments(r.Context(), h.db, id)
	if err != nil {
		h.serverError(w, r, "Attachments list error", err)
		return
	}
	kit.JSON(w, r, http.StatusOK, attachmentList{Attachments: attachments})
}

// download answers GET /api/tickets/attachments/{aid} with the file, as a
// download under its stored name and with its stored type. Conditional
// and range requests work as for any file the console serves. An
// attachment of a ticket the account may not read is not found.
func (h *handlers) download(w http.ResponseWriter, r *http.Request) {
	var (
		a   *attachment
		err error
	)
	if id, ok := ticketID(r, "aid"); ok {
		a, err = attachmentByID(r.Context(), h.db, id)
	}
	if err == nil && a != nil && kit.User(r).Role == "viewer" {
		var t *ticket
		if t, err = h.readable(r, a.TicketID.Value()); t == nil {
			a = nil
		}
	}
	if err == nil && a == nil {
		fail(w, r, http.StatusNotFound, "Attachment not found")
		return
	}
	var path string
	if err == nil {
		path, err = h.confined(a.StoragePath)
	}
	if err != nil {
		h.serverError(w, r, "Attachment download error", err)
		return
	}
	info, statErr := os.Stat(path)
	if statErr != nil {
		fail(w, r, http.StatusNotFound, "Attachment file missing")
		return
	}
	// A stored type that is no media type, which rows from before the
	// check can hold, goes out as a plain download
	disposition, okName := headerValue(`attachment; filename="` + a.Filename + `"`)
	if !okName {
		h.serverError(w, r, "Attachment download error", errors.New("invalid characters in the name of the stored attachment"))
		return
	}
	w.Header().Set("Content-Disposition", disposition)
	contentType := "application/octet-stream"
	if a.Mimetype.Valid && mediaType(a.Mimetype.String) {
		contentType = a.Mimetype.String
	}
	w.Header()["Content-Type"] = []string{contentType}

	failed := func(w http.ResponseWriter, r *http.Request, status int, err error) { h.fileError(w, r, status, err) }
	if info.IsDir() || h.hiddenBelow(path) {
		// A folder is no file, and a file whose name starts with a dot is hidden
		failed(w, r, http.StatusNotFound, fmt.Errorf("cannot send %s", filepath.Base(path)))
		return
	}
	files := httpx.NewStatic(filepath.Dir(path), httpx.StaticOptions{Dotfiles: "allow", NoIndex: true, NoRedirect: true, NoFallthrough: true, OnError: failed})
	files.ServeName(w, r, filepath.Base(path), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		failed(w, r, http.StatusNotFound, errors.New("file not found"))
	}))
}

// hasDotPart reports whether a part of the path starts with a dot.
func hasDotPart(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if len(part) > 1 && part[0] == '.' {
			return true
		}
	}
	return false
}

// mediaType reports whether text is a media type a header can carry:
// type/subtype with parameters, in ASCII, without control characters.
func mediaType(text string) bool {
	if _, ok := headerValue(text); !ok {
		return false
	}
	name, _, err := mime.ParseMediaType(text)
	return err == nil && strings.Count(name, "/") == 1
}

// headerValue returns text as the bytes of a header value: one byte per
// character. ok is false for text a header cannot carry: a control
// character other than tab, or a character outside Latin-1.
func headerValue(text string) (value string, ok bool) {
	out := make([]byte, 0, len(text))
	for _, r := range text {
		if r > 0xFF || (r < 0x20 && r != '\t') || r == 0x7F {
			return "", false
		}
		out = append(out, byte(r))
	}
	return string(out), true
}

// fileError answers a download that failed after its headers were set:
// the console's error answer as JSON, under the status of the failure,
// without the attachment's Content-Disposition. In production a failure
// of the server (500 and above) says "Internal Server Error"; a refused
// request says why, as outside production (S17). A missing file says "Not
// Found", never the path the file sender looked at.
func (h *handlers) fileError(w http.ResponseWriter, r *http.Request, status int, err error) {
	message := "Internal Server Error"
	if !h.d.Config.Production || status < http.StatusInternalServerError {
		switch status {
		case http.StatusPreconditionFailed:
			message = "Precondition Failed"
		case http.StatusRequestedRangeNotSatisfiable:
			message = "Range Not Satisfiable"
		case http.StatusNotFound:
			message = "Not Found"
		default:
			if err != nil {
				message = err.Error()
			}
		}
	}
	body, _ := httpx.Marshal(struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}{Error: message})
	w.Header().Del("Content-Disposition")
	httpx.WriteBody(w, r, status, "application/json; charset=utf-8", body)
}
