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
// in the attachment folder, also when it is reached through a link.
func (h *handlers) confined(storagePath string) (string, error) {
	return safepath.ResolveWithinRoot(storagePath, h.uploads)
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
// digits and the cleaned name; its row follows.
func (h *handlers) attach(w http.ResponseWriter, r *http.Request) {
	t, err := ticketByID(r.Context(), h.db, ticketID(r, "id"))
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
	if len(file) > maxAttachmentSize {
		fail(w, r, http.StatusRequestEntityTooLarge, "Attachment too large. Max 25 MB")
		return
	}
	if err == nil && !filename.IsString() {
		err = errors.New("filename.replace is not a function")
	}
	var storagePath string
	name := safeName(filename.Str())
	if err == nil {
		random := make([]byte, 8)
		if _, err = h.random(random); err == nil {
			storagePath, err = safepath.ResolveChild(h.uploads, number(t.ID)+"_"+hex.EncodeToString(random)+"_"+name)
		}
	}
	if err == nil {
		err = os.WriteFile(storagePath, file, 0o666)
	}
	var id jsbind.Cell
	if err == nil {
		mimetype := jsbind.Of(body.Get("mimetype")).Or(jsbind.String("application/octet-stream"))
		id, err = addAttachment(r.Context(), h.db, t.id(), name, mimetype, len(file), storagePath, kit.User(r).Username)
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

// attachments answers GET /api/tickets/{id}/attachments.
func (h *handlers) attachments(w http.ResponseWriter, r *http.Request) {
	attachments, err := ticketAttachments(r.Context(), h.db, ticketID(r, "id"))
	if err != nil {
		h.serverError(w, r, "Attachments list error", err)
		return
	}
	kit.JSON(w, r, http.StatusOK, attachmentList{Attachments: attachments})
}

// download answers GET /api/tickets/attachments/{aid} with the file, as a
// download under its stored name and with its stored type. Conditional
// and range requests work as for any file the console serves.
func (h *handlers) download(w http.ResponseWriter, r *http.Request) {
	a, err := attachmentByID(r.Context(), h.db, ticketID(r, "aid"))
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
	// The two headers are set one after the other; a stored type that no
	// header can carry fails after the first is in place.
	disposition, okName := headerValue(`attachment; filename="` + a.Filename + `"`)
	contentType, okType := headerValue(a.Mimetype.String)
	if okName {
		w.Header().Set("Content-Disposition", disposition)
	}
	if !okName || !okType || !a.Mimetype.Valid {
		h.serverError(w, r, "Attachment download error", errors.New("invalid characters in a header of the stored attachment"))
		return
	}
	w.Header()["Content-Type"] = []string{contentType}

	failed := func(w http.ResponseWriter, r *http.Request, status int, err error) { h.fileError(w, r, status, err) }
	if info.IsDir() || hasDotPart(path) {
		// A folder is no file, and a path through a dot folder is hidden
		failed(w, r, http.StatusNotFound, fmt.Errorf("cannot send %s", filepath.Base(path)))
		return
	}
	files := httpx.NewStatic(filepath.Dir(path), httpx.StaticOptions{Dotfiles: "allow", NoIndex: true, NoRedirect: true, NoFallthrough: true, OnError: failed})
	files.ServeName(w, r, filepath.Base(path), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		failed(w, r, http.StatusNotFound, errors.New("file not found"))
	}))
}

// hasDotPart reports whether a part of the path starts with a dot. Such
// a file is not sent.
func hasDotPart(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if len(part) > 1 && part[0] == '.' {
			return true
		}
	}
	return false
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
// the console's error answer as JSON, under the status of the failure.
// The Content-Type of the attachment stays and gets the charset of the
// answer, as it did.
func (h *handlers) fileError(w http.ResponseWriter, r *http.Request, status int, err error) {
	message := "Internal Server Error"
	if !h.d.Config.Production {
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
	contentType := "application/json; charset=utf-8"
	if mediaType, params, err := mime.ParseMediaType(httpx.Latin1(w.Header().Get("Content-Type"))); err == nil {
		params["charset"] = "utf-8"
		if formatted := mime.FormatMediaType(mediaType, params); formatted != "" {
			contentType = formatted
		}
	}
	httpx.WriteBody(w, r, status, contentType, body)
}
