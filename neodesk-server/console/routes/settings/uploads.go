package settings

import (
	"bytes"
	"errors"
	"net/http"

	"github.com/neodeskapp/neodesk/neodesk-server/console/branding"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/middleware"
)

// The uploads of the branding: a logo, a background picture and a font,
// each one file in its own form field, read as multer read them
// (middleware.ParseUpload). An upload that breaks a rule is 400 with the
// reason; nothing of it stays on disk.

// readUpload reads the one file of an upload in field, at most limit
// bytes, that accept lets through. The error is the answer of the route:
// tooLarge for a file over the limit, the reason for anything else.
func readUpload(w http.ResponseWriter, r *http.Request, field string, limit int64, tooLarge string, accept func(*middleware.UploadedFile) error) (*middleware.Upload, bool) {
	up, err := middleware.ParseUpload(r, middleware.UploadOptions{
		Field: field, MaxFileSize: limit,
		Filter: func(file *middleware.UploadedFile) (bool, error) {
			if err := accept(file); err != nil {
				return false, err
			}
			return true, nil
		},
	})
	if err != nil {
		message := err.Error()
		if middleware.UploadErrorCode(err) == middleware.UploadFileTooLarge {
			message = tooLarge
		}
		if message == "" {
			message = "Upload failed"
		}
		kit.Fail(w, r, http.StatusBadRequest, inCallersLanguage(r, message))
		return nil, false
	}
	if up.File == nil {
		kit.Fail(w, r, http.StatusBadRequest, "No file provided")
		return nil, false
	}
	return up, true
}

// acceptImage is the filter of the logo and background uploads: the
// content type the client named must be a picture type.
func acceptImage(file *middleware.UploadedFile) error {
	if !branding.AcceptImageUpload(file.MIMEType) {
		return errors.New("Invalid file type")
	}
	return nil
}

// fields is req.body of an upload: its text fields as JavaScript values.
func fields(up *middleware.Upload) (any, error) {
	raw, err := up.Fields.MarshalJSON()
	if err != nil {
		return nil, err
	}
	return branding.ParseJSON(raw)
}

type logoAnswer struct {
	Success bool   `json:"success"`
	URL     string `json:"url"`
}

// uploadLogo answers POST /api/settings/branding/upload-logo with the
// picture in the field "logo" (2 MB at most). The logo this service stored
// before goes when the settings point to it; the settings themselves are
// not changed.
func (h *handlers) uploadLogo(w http.ResponseWriter, r *http.Request) {
	up, ok := readUpload(w, r, "logo", branding.LogoMaxBytes, "File too large (max 2 MB)", acceptImage)
	if !ok {
		return
	}
	b := h.d.Branding
	stored, err := b.StoreLogo(up.File.MIMEType, bytes.NewReader(up.File.Buffer))
	if err != nil {
		failMessage(w, r, http.StatusBadRequest, err, "Upload failed")
		return
	}
	b.RemovePreviousLogo(stored)
	_ = h.d.LogAction(r, "branding_logo_upload", "Uploaded logo: "+stored)
	kit.JSON(w, r, http.StatusOK, logoAnswer{Success: true, URL: "/uploads/" + stored})
}

type backgroundAnswer struct {
	Success  bool             `json:"success"`
	URL      string           `json:"url"`
	Applied  bool             `json:"applied"`
	Data     *branding.Object `json:"data"`
	Revision string           `json:"revision,omitempty"`
}

// uploadBackground answers POST /api/settings/branding/upload-background
// with the picture in the field "background" (8 MB at most) and, in the
// text field "target", the setting that takes it (bgImageUrl,
// loginBgImageUrl, agentBgImageUrl or rdclientBgImageUrl). With a known
// target the picture becomes that background at once and the answer
// carries the saved settings; old pictures stay, as one may serve several
// backgrounds.
func (h *handlers) uploadBackground(w http.ResponseWriter, r *http.Request) {
	up, ok := readUpload(w, r, "background", branding.BackgroundMaxBytes, "File too large (max 8 MB)", acceptImage)
	if !ok {
		return
	}
	b := h.d.Branding
	stored, err := b.StoreBackground(up.File.MIMEType, bytes.NewReader(up.File.Buffer))
	if err != nil {
		failMessage(w, r, http.StatusBadRequest, err, "Upload failed")
		return
	}
	url := "/uploads/" + stored
	// A target that cannot be made text and a failed save left the old
	// console's request without an answer (BUGS BR15): 500 here.
	form, err := fields(up)
	if err != nil {
		h.d.WebUI.ServerError(w, r, err, http.StatusInternalServerError)
		return
	}
	target, err := jsText(branding.Field(form, "target"))
	if err != nil {
		h.d.WebUI.ServerError(w, r, err, http.StatusInternalServerError)
		return
	}
	saved, applied, err := b.ApplyBackground(r.Context(), target, url)
	if err != nil {
		h.d.WebUI.ServerError(w, r, err, http.StatusInternalServerError)
		return
	}
	details := "Uploaded background: " + stored
	answer := backgroundAnswer{Success: true, URL: url, Applied: applied}
	if applied {
		details = "Uploaded and applied background: " + stored + " (" + target + ")"
		answer.Data, answer.Revision = saved, b.Revision()
	}
	_ = h.d.LogAction(r, "branding_bg_upload", details)
	kit.JSON(w, r, http.StatusOK, answer)
}
