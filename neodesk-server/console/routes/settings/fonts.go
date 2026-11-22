package settings

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/unitronix/betterdesk-server/console/branding"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/middleware"
)

// downloadFont answers POST /api/settings/fonts/download with {family,
// weights}: the font's latin files are fetched from Google Fonts and
// installed. weights defaults to 400, 500, 600 and 700 when it is empty in
// JavaScript's eyes. The requests go through the outbound guard of the
// branding service.
func (h *handlers) downloadFont(w http.ResponseWriter, r *http.Request) {
	body, err := requestBody(r)
	if err != nil {
		h.log.Error("Font download error:", err)
		failMessage(w, r, http.StatusInternalServerError, err, "Failed to download font")
		return
	}
	family, isText := branding.Field(body, "family").(string)
	if !isText || family == "" {
		kit.Fail(w, r, http.StatusBadRequest, "Font family is required")
		return
	}
	weights := branding.Field(body, "weights")
	if !branding.Truthy(weights) {
		weights = branding.Undefined
	}
	result, err := h.d.Branding.DownloadFont(r.Context(), family, weights)
	if err != nil {
		h.log.Error("Font download error:", err)
		failMessage(w, r, http.StatusInternalServerError, err, "Failed to download font")
		return
	}
	files, _ := branding.Field(result, "files").([]any)
	_ = h.d.LogAction(r, "font_download", "Downloaded font: "+family+" ("+strconv.Itoa(len(files))+" files)")
	kit.OK(w, r, result)
}

type deletedAnswer struct {
	Success bool `json:"success"`
	Deleted bool `json:"deleted"`
}

// deleteFont answers DELETE /api/settings/fonts/{family}: the font's
// folder goes. The path part is decoded once more after the router
// decoded it, so "Open%2520Sans" names "Open Sans".
func (h *handlers) deleteFont(w http.ResponseWriter, r *http.Request) {
	fail := func(err error) {
		h.log.Error("Font delete error:", err)
		kit.Fail(w, r, http.StatusInternalServerError, "Failed to delete font")
	}
	family, err := branding.DecodeURIComponent(r.PathValue("family"))
	if err != nil {
		fail(err)
		return
	}
	deleted, err := h.d.Branding.DeleteLocalFont(family)
	if err != nil {
		fail(err)
		return
	}
	if deleted {
		_ = h.d.LogAction(r, "font_delete", "Deleted font: "+family)
	}
	kit.JSON(w, r, http.StatusOK, deletedAnswer{Success: true, Deleted: deleted})
}

// acceptFont is the filter of the font upload: a file name that ends in
// .woff2 or .ttf, or a font content type. The bytes are not looked at
// (BUGS BR4).
func acceptFont(file *middleware.UploadedFile) error {
	if !branding.AcceptFontUpload(file.OriginalName, file.MIMEType) {
		return errors.New("Invalid font file type")
	}
	return nil
}

// uploadFont answers POST /api/settings/fonts/upload with the file in the
// field "font" (5 MB at most) and the name in the text field "family"
// (the file name without its extension when that is empty).
func (h *handlers) uploadFont(w http.ResponseWriter, r *http.Request) {
	up, ok := readUpload(w, r, "font", branding.FontMaxBytes, "File too large (max 5 MB)", acceptFont)
	if !ok {
		return
	}
	fail := func(err error) {
		h.log.Error("Font upload error:", err)
		failMessage(w, r, http.StatusBadRequest, err, "Upload failed")
	}
	form, err := fields(up)
	if err != nil {
		fail(err)
		return
	}
	familyField := branding.Field(form, "family")
	family, err := branding.FontUploadFamily(familyField, up.File.OriginalName)
	if err != nil {
		fail(err)
		return
	}
	font, err := h.d.Branding.UploadFont(familyField, up.File.OriginalName, up.File.Buffer)
	if err != nil {
		fail(err)
		return
	}
	_ = h.d.LogAction(r, "font_upload", "Uploaded font: "+family)
	kit.OK(w, r, font)
}
