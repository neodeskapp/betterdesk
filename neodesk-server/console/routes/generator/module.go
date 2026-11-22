package generator

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	gen "github.com/unitronix/betterdesk-server/console/generator"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/middleware"
)

// The JavaScript helpers of package generator, by their short names.
var (
	prop     = gen.Prop
	or       = gen.Or
	jsString = gen.String
	jsTrim   = gen.Trim
	jsSlice  = gen.Slice
)

// errorsAs is errors.As.
func errorsAs(err error, target any) bool { return errors.As(err, target) }

// requestBody is req.body as JSON.parse made it.
func requestBody(r *http.Request) any {
	v, err := gen.Parse(string(middleware.BodyValue(r).Raw()))
	if err != nil {
		return goapi.NewObject()
	}
	return v
}

// moduleStatus answers GET /api/generator/module/status: the client
// builder's state in embedded mode, the template module's otherwise.
func (h *handlers) moduleStatus(w http.ResponseWriter, r *http.Request) {
	var status *goapi.Object
	var err error
	if h.gen.Embedded() {
		status, err = h.gen.BuilderStatus(r.Context())
	} else {
		status, err = h.gen.ModuleStatus()
	}
	if err != nil {
		h.serverError(w, r, "module status", err)
		return
	}
	ok(w, r, status)
}

// acceptTerms answers POST /api/generator/module/accept-terms.
func (h *handlers) acceptTerms(w http.ResponseWriter, r *http.Request) {
	var state *goapi.Object
	var err error
	if h.gen.Embedded() {
		state, err = h.gen.AcceptBuilderTerms()
	} else {
		state, err = h.gen.AcceptModuleTerms()
	}
	if err != nil {
		h.serverError(w, r, "accept-terms", err)
		return
	}
	ok(w, r, state)
}

// codedFailure is an error answer with the error's code.
type codedFailure struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
	Code    string `json:"code"`
}

// installDeadline bounds a template install or a source sync.
const installDeadline = 30 * time.Minute

// installContext is the context of a template install or a source sync:
// the work goes on when the browser or a proxy gives up on the request,
// and the page sees the result at its next status poll.
func installContext(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(r.Context()), installDeadline)
}

// install answers POST /api/generator/module/install. Download mode
// fetches the templates of the newest client release (or body.repo,
// body.tag); embedded mode syncs the client source (body.repo, body.ref
// or body.tag) after making sure a signing key exists.
func (h *handlers) install(w http.ResponseWriter, r *http.Request) {
	body := requestBody(r)
	ctx, cancel := installContext(r)
	defer cancel()
	var data any
	var err error
	if h.gen.Embedded() {
		if _, err = h.gen.EnsureSigningKey(); err == nil {
			if !h.gen.SigningSeedValid() {
				kit.JSON(w, r, http.StatusBadRequest, failure{Error: "custom_client_signing_seed_required"})
				return
			}
			var result gen.SyncResult
			if result, err = h.gen.SyncSource(ctx, prop(body, "repo"), or(prop(body, "ref"), prop(body, "tag")), true); err == nil {
				data = result.Object()
			}
		}
	} else {
		repo, tag := "", ""
		if v := prop(body, "repo"); gen.Or(v, nil) != nil {
			repo = jsTrim(jsString(v))
		}
		if v := prop(body, "tag"); gen.Or(v, nil) != nil {
			tag = jsTrim(jsString(v))
		}
		var state *goapi.Object
		if state, err = h.gen.InstallFromGitHub(ctx, repo, tag); err == nil {
			data = state
		}
	}
	if err == nil {
		ok(w, r, data)
		return
	}
	code := gen.ErrorCode(err)
	switch code {
	case "terms_not_accepted":
		kit.JSON(w, r, http.StatusBadRequest, codedFailure{Error: kit.T(r, "generator.module_terms_required", nil), Code: code})
		return
	case "no_release", "no_template_asset":
		message := err.Error()
		if message == "" {
			message = kit.T(r, "generator.module_no_release", nil)
		}
		kit.JSON(w, r, http.StatusNotFound, codedFailure{Error: message, Code: code})
		return
	}
	h.log.Error("[generator] module install error:", err.Error())
	status := http.StatusInternalServerError
	if gen.StatusCodeOf(err) == http.StatusForbidden {
		status = http.StatusBadGateway
	}
	message := err.Error()
	if message == "" {
		message = kit.T(r, "errors.server_error", nil)
	}
	if code == "" {
		code = "install_failed"
	}
	kit.JSON(w, r, status, codedFailure{Error: message, Code: code})
}

// maxModuleUpload is the largest template archive an upload may carry.
const maxModuleUpload = 2 * 1024 * 1024 * 1024

// uploadName is the stored name of an uploaded archive: the time in
// milliseconds, a dash, and the client's name with every character but
// letters, digits, dot, dash and underscore made "_" (one per UTF-16
// code unit).
func uploadName(now time.Time, original string) string {
	if original == "" {
		original = "generator.tar.gz"
	}
	var b strings.Builder
	for _, c := range original {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '_', c == '-':
			b.WriteRune(c)
		case c >= 0x10000:
			b.WriteString("__")
		default:
			b.WriteByte('_')
		}
	}
	return strconv.FormatInt(now.UnixMilli(), 10) + "-" + b.String()
}

// installLocal answers POST /api/generator/module/install-local: a
// template archive uploaded as the multipart field "archive" (up to
// 2 GB), written to DATA_DIR/.generator-uploads and removed again
// whatever happens. A broken form goes to the console's error handler.
func (h *handlers) installLocal(w http.ResponseWriter, r *http.Request) {
	dir := filepath.Join(h.dataDir, ".generator-uploads")
	form, err := middleware.ParseUpload(r, middleware.UploadOptions{
		Field:       "archive",
		MaxFileSize: maxModuleUpload,
		Dir:         dir,
		Filename: func(file *middleware.UploadedFile) (string, error) {
			if err := os.MkdirAll(dir, 0o777); err != nil {
				return "", err
			}
			return uploadName(time.Now(), file.OriginalName), nil
		},
	})
	if err != nil {
		h.errors.ServerError(w, r, err, 0)
		return
	}
	if form.File != nil {
		defer middleware.RemoveUploadedFile(form.File, h.dataDir)
	}
	if h.gen.Embedded() {
		kit.JSON(w, r, http.StatusBadRequest, failure{Error: "embedded_builder_requires_client_source_sync"})
		return
	}
	if form.File == nil || form.File.Path == "" {
		kit.JSON(w, r, http.StatusBadRequest, failure{Error: "generator_archive_required"})
		return
	}
	fields, _ := gen.Parse(string(raw(form.Fields)))
	version := jsSlice(jsTrim(jsString(or(prop(fields, "version"), "local"))), 100)
	ctx, cancel := installContext(r)
	defer cancel()
	state, err := h.gen.InstallFromLocalArchive(ctx, form.File.Path, version)
	if err != nil {
		h.log.Error("[generator] local module install error:", err.Error())
		code := gen.ErrorCode(err)
		if code == "" {
			code = err.Error()
		}
		if code == "" {
			code = "install_failed"
		}
		kit.JSON(w, r, http.StatusBadRequest, codedFailure{Error: code, Code: code})
		return
	}
	ok(w, r, state)
}
