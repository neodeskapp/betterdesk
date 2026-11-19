package settings_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/unitronix/betterdesk-server/console/branding"
	"github.com/unitronix/betterdesk-server/console/routes/internal/routetest"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// part is one part of a multipart body: a text field when name is empty.
type part struct {
	field, name, contentType string
	content                  []byte
}

// form builds a multipart/form-data body. File names go out as bytes,
// each letter below 256 as one byte, the way a browser sends a name that
// the reader then takes as Latin-1.
func form(t *testing.T, parts ...part) (body, contentType string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for _, p := range parts {
		header := textproto.MIMEHeader{}
		disposition := `form-data; name="` + p.field + `"`
		if p.name != "" {
			raw := make([]byte, 0, len(p.name))
			for _, r := range p.name {
				if r > 0xff {
					t.Fatalf("file name %q has a letter beyond Latin-1", p.name)
				}
				raw = append(raw, byte(r))
			}
			escaped := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(string(raw))
			disposition += `; filename="` + escaped + `"`
		}
		header.Set("Content-Disposition", disposition)
		if p.contentType != "" {
			header.Set("Content-Type", p.contentType)
		}
		pw, err := w.CreatePart(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pw.Write(p.content); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.String(), w.FormDataContentType()
}

func upload(t *testing.T, a *routetest.Agent, path string, parts ...part) routetest.Answer {
	t.Helper()
	body, contentType := form(t, parts...)
	return a.Do("POST", path, body, "Content-Type", contentType)
}

var pngBytes, _ = base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==")

func files(t *testing.T, dir string) []string {
	t.Helper()
	list, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var names []string
	for _, e := range list {
		names = append(names, e.Name())
	}
	return names
}

var reLogoName = regexp.MustCompile(`^/uploads/(logo-[0-9a-f]{16}\.png)$`)

func TestUploadLogo(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := world(t, db)
		admin := w.As("admin")
		uploads := filepath.Join(w.DataDir, "uploads")
		res := upload(t, admin, "/api/settings/branding/upload-logo", part{field: "logo", name: "Logo.PNG", contentType: "image/png", content: pngBytes})
		m := reLogoName.FindStringSubmatch(decode(t, res.Body)["url"].(string))
		if res.Status != http.StatusOK || m == nil || keyOrder(t, res.Body) != "success,url" {
			t.Fatalf("upload: %d %s", res.Status, res.Body)
		}
		first := m[1]
		if stored, err := os.ReadFile(filepath.Join(uploads, first)); err != nil || !bytes.Equal(stored, pngBytes) {
			t.Errorf("stored file: %v", err)
		}
		if got := lastAudit(t, w); got != "branding_logo_upload|Uploaded logo: "+first+"|127.0.0.1|1" {
			t.Errorf("audit: %s", got)
		}
		// the settings are not changed by the upload; once they point to
		// the logo, the next upload removes it
		if got := w.Server.Deps().Branding.GetBranding().Text("logoUrl"); got != "" {
			t.Errorf("logoUrl after the upload: %q", got)
		}
		admin.Do("POST", "/api/settings/branding", `{"logoUrl":"/uploads/`+first+`"}`)
		res = upload(t, admin, "/api/settings/branding/upload-logo", part{field: "logo", name: "second.gif", contentType: "image/gif", content: pngBytes})
		if res.Status != http.StatusOK || !strings.HasSuffix(decode(t, res.Body)["url"].(string), ".gif") {
			t.Fatalf("second upload: %d %s", res.Status, res.Body)
		}
		if _, err := os.Stat(filepath.Join(uploads, first)); !os.IsNotExist(err) {
			t.Errorf("the logo before is still there: %v", err)
		}
		before := files(t, uploads)
		auditBefore := len(w.AuditRows())
		refusals := []struct {
			name  string
			parts []part
			want  string
		}{
			{"wrong type", []part{{field: "logo", name: "page.png", contentType: "text/html", content: pngBytes}}, "Invalid file type"},
			{"too large", []part{{field: "logo", name: "big.png", contentType: "image/png", content: bytes.Repeat([]byte("a"), branding.LogoMaxBytes+1)}}, "File too large (max 2 MB)"},
			{"no file", []part{{field: "note", content: []byte("x")}}, "No file provided"},
			{"other field", []part{{field: "picture", name: "a.png", contentType: "image/png", content: pngBytes}}, "Unexpected file field"},
			{"two files", []part{{field: "logo", name: "a.png", contentType: "image/png", content: pngBytes}, {field: "logo", name: "b.png", contentType: "image/png", content: pngBytes}}, "Unexpected file field"},
			{"dots", []part{{field: "logo", name: "..", contentType: "image/png", content: pngBytes}}, "No file provided"},
		}
		for _, c := range refusals {
			res := upload(t, admin, "/api/settings/branding/upload-logo", c.parts...)
			if res.Status != http.StatusBadRequest || res.Body != `{"success":false,"error":"`+c.want+`"}` {
				t.Errorf("%s: %d %s", c.name, res.Status, res.Body)
			}
		}
		if res := admin.Do("POST", "/api/settings/branding/upload-logo", "{}"); res.Body != `{"success":false,"error":"No file provided"}` {
			t.Errorf("JSON: %s", res.Body)
		}
		if res := admin.Do("POST", "/api/settings/branding/upload-logo", "x", "Content-Type", "multipart/form-data"); res.Status != http.StatusBadRequest || res.Body != `{"success":false,"error":"Multipart: Boundary not found"}` {
			t.Errorf("no boundary: %d %s", res.Status, res.Body)
		}
		if got := files(t, uploads); strings.Join(got, ",") != strings.Join(before, ",") {
			t.Errorf("refused uploads left files: %v, before %v", got, before)
		}
		if rows := w.AuditRows(); len(rows) != auditBefore {
			t.Errorf("refused uploads were audited: %v", rows[auditBefore:])
		}
		// exactly the limit is taken
		res = upload(t, admin, "/api/settings/branding/upload-logo", part{field: "logo", name: "noext", contentType: "image/webp", content: bytes.Repeat([]byte("a"), branding.LogoMaxBytes)})
		if res.Status != http.StatusOK || !strings.HasSuffix(decode(t, res.Body)["url"].(string), ".webp") {
			t.Errorf("exact size: %d %s", res.Status, res.Body)
		}
	})
}

// TestUploadsTakeTheExtensionOfTheirType: a logo or background is stored
// with the extension of the picture type it was accepted as, never with
// the one of the client's file name, so a page or a script sent as a
// picture is not served as one from /uploads/ (BUGS I9).
func TestUploadsTakeTheExtensionOfTheirType(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := world(t, db)
		admin := w.As("admin")
		uploads := filepath.Join(w.DataDir, "uploads")
		for _, c := range []struct {
			path, field, name, contentType, ext string
		}{
			{"upload-logo", "logo", "x.html", "image/png", ".png"},
			{"upload-logo", "logo", "x.js", "image/svg+xml", ".svg"},
			{"upload-logo", "logo", "photo.JPEG", "image/jpeg", ".jpg"},
			{"upload-logo", "logo", "noext", "image/gif", ".gif"},
			{"upload-background", "background", "x.html", "image/jpeg", ".jpg"},
			{"upload-background", "background", "wall.php", "image/webp", ".webp"},
			{"upload-background", "background", "wall", "image/png", ".png"},
		} {
			res := upload(t, admin, "/api/settings/branding/"+c.path, part{field: c.field, name: c.name, contentType: c.contentType, content: pngBytes})
			url, _ := decode(t, res.Body)["url"].(string)
			stored := strings.TrimPrefix(url, "/uploads/")
			if res.Status != http.StatusOK || !regexp.MustCompile(`^(logo|bg)-[0-9a-f]{16}\`+c.ext+`$`).MatchString(stored) {
				t.Errorf("%s %q as %s: %d %s, want a name ending in %s", c.path, c.name, c.contentType, res.Status, res.Body, c.ext)
				continue
			}
			if data, err := os.ReadFile(filepath.Join(uploads, stored)); err != nil || !bytes.Equal(data, pngBytes) {
				t.Errorf("%s %q: the stored file %s: %v", c.path, c.name, stored, err)
			}
		}
		for _, name := range files(t, uploads) {
			if ext := filepath.Ext(name); ext == ".html" || ext == ".js" || ext == ".php" || ext == "" {
				t.Errorf("an upload is stored as %s", name)
			}
		}
	})
}

func TestUploadBackground(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := world(t, db)
		admin := w.As("admin")
		res := upload(t, admin, "/api/settings/branding/upload-background",
			part{field: "background", name: "wall.JPEG", contentType: "image/jpeg", content: pngBytes}, part{field: "target", content: []byte(" loginBgImageUrl ")})
		answer := decode(t, res.Body)
		url, _ := answer["url"].(string)
		if res.Status != http.StatusOK || keyOrder(t, res.Body) != "success,url,applied,data,revision" || answer["applied"] != true ||
			!regexp.MustCompile(`^/uploads/bg-[0-9a-f]{16}\.jpg$`).MatchString(url) {
			t.Fatalf("applied: %d %s", res.Status, res.Body)
		}
		data := answer["data"].(map[string]any)
		if data["loginBgImageUrl"] != url || data["loginBgType"] != "image" || answer["revision"] != w.Server.Deps().Branding.Revision() {
			t.Errorf("settings after the upload: %v %v", data["loginBgImageUrl"], data["loginBgType"])
		}
		name := strings.TrimPrefix(url, "/uploads/")
		if got := lastAudit(t, w); got != "branding_bg_upload|Uploaded and applied background: "+name+" (loginBgImageUrl)|127.0.0.1|1" {
			t.Errorf("audit: %s", got)
		}
		for _, target := range []string{"", "logoUrl", "bgType"} {
			parts := []part{{field: "background", name: "wall", contentType: "image/png", content: pngBytes}}
			if target != "" {
				parts = append(parts, part{field: "target", content: []byte(target)})
			}
			res := upload(t, admin, "/api/settings/branding/upload-background", parts...)
			answer := decode(t, res.Body)
			if res.Status != http.StatusOK || keyOrder(t, res.Body) != "success,url,applied,data" || answer["applied"] != false || answer["data"] != nil ||
				!strings.HasSuffix(answer["url"].(string), ".png") {
				t.Errorf("target %q: %d %s", target, res.Status, res.Body)
			}
			if got := lastAudit(t, w); !strings.HasPrefix(got, "branding_bg_upload|Uploaded background: bg-") {
				t.Errorf("target %q: audit %s", target, got)
			}
		}
		// a name every object has counts as a target and stores nothing
		// about the picture (BUGS BR10)
		res = upload(t, admin, "/api/settings/branding/upload-background",
			part{field: "target", content: []byte("constructor")}, part{field: "background", name: "w.png", contentType: "image/png", content: pngBytes})
		if answer := decode(t, res.Body); answer["applied"] != true || answer["data"].(map[string]any)["bgImageUrl"] != "" {
			t.Errorf("constructor: %s", res.Body)
		}
		if res := upload(t, admin, "/api/settings/branding/upload-background", part{field: "background", name: "big.jpg", contentType: "image/jpeg", content: bytes.Repeat([]byte("a"), branding.BackgroundMaxBytes+1)}); res.Body != `{"success":false,"error":"File too large (max 8 MB)"}` {
			t.Errorf("too large: %d %s", res.Status, res.Body)
		}
		if res := upload(t, admin, "/api/settings/branding/upload-background", part{field: "background", name: "a.bmp", contentType: "image/bmp", content: pngBytes}); res.Body != `{"success":false,"error":"Invalid file type"}` {
			t.Errorf("bmp: %d %s", res.Status, res.Body)
		}
	})
}

// A target that cannot be made text leaves the old console's request
// without an answer (BUGS BR15). Go answers 500; the picture is stored.
func TestUploadBackgroundTargetWithoutText(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := world(t, db)
		admin := w.As("admin")
		res := upload(t, admin, "/api/settings/branding/upload-background",
			part{field: "target[toString]", content: []byte("x")}, part{field: "background", name: "w.png", contentType: "image/png", content: pngBytes})
		if res.Status != http.StatusInternalServerError {
			t.Errorf("target without text: %d %s", res.Status, res.Body)
		}
		if got := files(t, filepath.Join(w.DataDir, "uploads")); len(got) != 1 || !strings.HasPrefix(got[0], "bg-") {
			t.Errorf("uploads: %v", got)
		}
		// a list is its items joined by commas, which is no target
		res = upload(t, admin, "/api/settings/branding/upload-background",
			part{field: "target", content: []byte("bgImageUrl")}, part{field: "target", content: []byte("x")}, part{field: "background", name: "w.png", contentType: "image/png", content: pngBytes})
		if answer := decode(t, res.Body); res.Status != http.StatusOK || answer["applied"] != false {
			t.Errorf("a list: %d %s", res.Status, res.Body)
		}
	})
}

func TestUploadFont(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := world(t, db)
		admin := w.As("admin")
		fonts, tmp := w.Server.Deps().Config.FontsDir, w.Server.Deps().Config.TempDir
		font := []byte("not a font at all")
		res := upload(t, admin, "/api/settings/fonts/upload", part{field: "family", content: []byte("  My Font  ")}, part{field: "font", name: "file.TTF", contentType: "application/octet-stream", content: font})
		if res.Body != `{"success":true,"data":{"family":"My Font","safeName":"my-font","cssPath":"/fonts/my-font/font.css","downloaded":true}}` {
			t.Fatalf("upload: %d %s", res.Status, res.Body)
		}
		if got := strings.Join(files(t, filepath.Join(fonts, "my-font")), ","); got != "font.css,meta.json,my-font.ttf" {
			t.Errorf("font folder: %s", got)
		}
		if stored, _ := os.ReadFile(filepath.Join(fonts, "my-font", "my-font.ttf")); !bytes.Equal(stored, font) {
			t.Errorf("stored font: %q", stored)
		}
		if got := lastAudit(t, w); got != "font_upload|Uploaded font: My Font|127.0.0.1|1" {
			t.Errorf("audit: %s", got)
		}
		if left := files(t, tmp); len(left) != 0 {
			t.Errorf("temporary files: %v", left)
		}
		// without a family the file name names the font; a woff2 by type
		res = upload(t, admin, "/api/settings/fonts/upload", part{field: "font", name: "Other Face.bin", contentType: "font/woff2", content: font})
		if !strings.Contains(res.Body, `"family":"Other Face","safeName":"other-face"`) {
			t.Errorf("by type: %d %s", res.Status, res.Body)
		}
		if got := lastAudit(t, w); got != "font_upload|Uploaded font: Other Face|127.0.0.1|1" {
			t.Errorf("audit: %s", got)
		}
		if got := strings.Join(files(t, filepath.Join(fonts, "other-face")), ","); got != "font.css,meta.json,other-face-400.woff2" {
			t.Errorf("woff2 folder: %s", got)
		}
		auditBefore := len(w.AuditRows())
		for _, c := range []struct {
			name  string
			parts []part
			want  string
		}{
			{"wrong type", []part{{field: "font", name: "x.png", contentType: "image/png", content: font}}, "Invalid font file type"},
			{"too large", []part{{field: "font", name: "x.ttf", contentType: "font/ttf", content: bytes.Repeat([]byte("a"), branding.FontMaxBytes+1)}}, "File too large (max 5 MB)"},
			{"no file", []part{{field: "family", content: []byte("x")}}, "No file provided"},
			{"family without text", []part{{field: "family[toString]", content: []byte("x")}, {field: "font", name: "x.ttf", contentType: "font/ttf", content: font}}, "Cannot convert object to primitive value"},
			{"family without letters", []part{{field: "family", content: []byte("你好")}, {field: "font", name: "x.ttf", contentType: "font/ttf", content: font}}, "Invalid font family name"},
		} {
			res := upload(t, admin, "/api/settings/fonts/upload", c.parts...)
			if res.Status != http.StatusBadRequest || res.Body != `{"success":false,"error":"`+c.want+`"}` {
				t.Errorf("%s: %d %s", c.name, res.Status, res.Body)
			}
		}
		if rows := w.AuditRows(); len(rows) != auditBefore {
			t.Errorf("refused uploads were audited: %v", rows[auditBefore:])
		}
		// the refusal of the name comes after the file was written to the
		// temporary folder, and the file stays there (BUGS BR3)
		if left := files(t, tmp); len(left) != 1 || !strings.HasPrefix(left[0], "font-upload-") {
			t.Errorf("temporary files after a refused name: %v", left)
		}
	})
}

// testdata/nodeoracle/files.js of console/branding sent uploads through
// the Node routes and recorded multer's view of each request (the file
// name and type it handed the route), the answers, the audit entries and
// the uploads folder after each step. Here the same requests go through
// the Go routes, with the file name and type multer saw.
func TestUploadsLikeNode(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		// about 180 requests: more than the API limit of a window
		w := worldWith(t, db, map[string]string{"RATE_LIMIT_MAX": "100000"})
		admin := w.As("admin")
		ctx := context.Background()
		uploads := filepath.Join(w.DataDir, "uploads")
		fontsDir := w.Server.Deps().Config.FontsDir
		recorded := loadFiles(t)
		steps, _ := branding.Field(recorded, "uploads").([]any)
		if len(steps) < 150 {
			t.Fatalf("%d upload steps recorded", len(steps))
		}
		// the fonts the font steps of the recording left behind, which this
		// console does not have
		fontSteps, _ := branding.Field(recorded, "fonts").([]any)
		earlier := map[string]bool{}
		if len(fontSteps) > 0 {
			for _, line := range lines(branding.Field(fontSteps[len(fontSteps)-1], "fonts")) {
				folder, _, _ := strings.Cut(line, "/")
				earlier[folder] = true
			}
		}
		last := ""
		renames := 0
		defer func() {
			if renames < 10 {
				t.Errorf("%d uploads stored under another name than Node's, the recording has more (BUGS I9)", renames)
			}
		}()
		for i, entry := range steps {
			step := branding.Field(entry, "step")
			get := func(key string) string { s, _ := branding.ToString(branding.Field(step, key)); return s }
			what := "step " + strconv.Itoa(i) + " " + clipText(branding.Stringify(step))
			auditBefore := len(w.AuditRows())
			status, body := 0, ""
			var renamed []string
			switch get("op") {
			case "upload":
				var content []byte
				described := branding.Field(step, "content")
				if text, ok := branding.Field(described, "base64").(string); ok {
					content, _ = base64.StdEncoding.DecodeString(text)
				} else {
					size, _ := branding.Field(described, "size").(float64)
					fill, _ := branding.Field(described, "fill").(float64)
					content = bytes.Repeat([]byte{byte(fill)}, int(size))
				}
				route, field := map[string][2]string{
					"logo":       {"/api/settings/branding/upload-logo", "logo"},
					"background": {"/api/settings/branding/upload-background", "background"},
					"font":       {"/api/settings/fonts/upload", "font"},
				}[get("kind")][0], get("kind")
				var parts []part
				if fieldsObj, ok := branding.Field(step, "fields").(*branding.Object); ok {
					for _, key := range fieldsObj.Keys() {
						value, _ := fieldsObj.Get(key)
						text, _ := branding.ToString(value)
						parts = append(parts, part{field: key, content: []byte(text)})
					}
				}
				probe := branding.Field(entry, "probe")
				name, mimeType := "..", "image/png"
				if probe != branding.Undefined {
					name, _ = branding.ToString(branding.Field(probe, "originalname"))
					mimeType, _ = branding.ToString(branding.Field(probe, "mimetype"))
				}
				parts = append(parts, part{field: field, name: name, contentType: mimeType, content: content})
				res := upload(t, admin, route, parts...)
				status, body = res.Status, res.Body
				if from, to := nodeUploadName(t, body, branding.Field(entry, "body"), mimeType); from != to {
					// stored under the extension of its type, where Node took
					// the one of the file name (BUGS I9): the file gets Node's
					// name, so the later steps see the folder Node saw
					renamed = append(renamed, from, to)
					renames++
					if err := os.Rename(filepath.Join(uploads, from), filepath.Join(uploads, to)); err != nil {
						t.Fatal(err)
					}
					body = strings.ReplaceAll(body, from, to)
				}
				if url := regexp.MustCompile(`"url":"([^"]*)"`).FindStringSubmatch(body); url != nil {
					last = url[1]
				}
			case "save":
				updates, err := branding.ParseJSON([]byte(strings.Replace(branding.Stringify(branding.Field(step, "updates")), "$last", last, 1)))
				if err != nil {
					t.Fatal(err)
				}
				if err := w.Server.Deps().Branding.SaveBranding(ctx, updates); err != nil {
					t.Fatalf("%s: %v", what, err)
				}
			case "file":
				if err := os.WriteFile(filepath.Join(uploads, get("name")), []byte(get("text")), 0o644); err != nil {
					t.Fatal(err)
				}
			case "dir":
				if err := os.Mkdir(filepath.Join(uploads, get("name")), 0o755); err != nil {
					t.Fatal(err)
				}
			case "link":
				if err := os.Symlink(filepath.Join(uploads, get("target")), filepath.Join(uploads, get("name"))); err != nil {
					t.Fatal(err)
				}
			case "mtime":
				seconds, _ := branding.Field(step, "seconds").(float64)
				whole := math.Floor(seconds)
				when := time.Unix(int64(whole), int64((seconds-whole)*1e9)/1000*1000)
				if err := os.Chtimes(filepath.Join(uploads, get("name")), when, when); err != nil {
					t.Fatal(err)
				}
			case "backgrounds":
				res := admin.Do("GET", "/api/settings/branding/backgrounds", "")
				status, body = res.Status, backgroundList(t, res.Body)
			default:
				t.Fatalf("%s: unknown op", what)
			}
			if want, ok := branding.Field(entry, "status").(float64); ok && int(want) != status {
				t.Errorf("%s: status %d, want %d (%s)", what, status, int(want), clipText(body))
			}
			if want, ok := branding.Field(entry, "body").(string); ok {
				if got := maskUploadNames(reRevisionField.ReplaceAllLiteralString(body, `"revision":"<REV>"`)); got != want {
					t.Errorf("%s: body\n got  %s\n want %s", what, clipText(got), clipText(want))
				}
			}
			var audit []string
			for _, row := range w.AuditRows()[auditBefore:] {
				parts := strings.SplitN(row, "|", 3)
				audit = append(audit, branding.Stringify([]any{parts[0], maskUploadNames(strings.NewReplacer(renamed...).Replace(parts[1]))}))
			}
			var wantAudit []string
			for _, a := range lines2(branding.Field(entry, "audit")) {
				wantAudit = append(wantAudit, a)
			}
			if strings.Join(audit, ",") != strings.Join(wantAudit, ",") {
				t.Errorf("%s: audit %v, want %v", what, audit, wantAudit)
			}
			if got, want := strings.Join(tree(t, uploads), "\n"), strings.Join(lines(branding.Field(entry, "uploads")), "\n"); got != want {
				t.Errorf("%s: uploads folder\n got  %s\n want %s", what, clipText(got), clipText(want))
			}
			if got, want := maskUploadNames(w.Server.Deps().Branding.GetBranding().Text("logoUrl")), branding.Field(entry, "logoUrl"); got != want {
				t.Errorf("%s: logoUrl %q, want %v", what, got, want)
			}
			// the font folder the upload wrote, when the recording's font
			// steps had not made one of that name before
			if safe := regexp.MustCompile(`"safeName":"([^"]+)"`).FindStringSubmatch(body); safe != nil && !earlier[safe[1]] {
				var want []string
				for _, line := range lines(branding.Field(entry, "fonts")) {
					if strings.HasPrefix(line, safe[1]+"/") {
						want = append(want, line)
					}
				}
				var got []string
				for _, line := range tree(t, fontsDir) {
					if strings.HasPrefix(line, safe[1]+"/") {
						got = append(got, line)
					}
				}
				if strings.Join(got, "\n") != strings.Join(want, "\n") {
					t.Errorf("%s: font folder\n got  %v\n want %v", what, got, want)
				}
			}
		}
	})
}

var (
	reRevisionField = regexp.MustCompile(`"revision":"[^"]*"`)
	reRandomUpload  = regexp.MustCompile(`\b(logo|bg)-[0-9a-f]{16}`)
	reTempUpload    = regexp.MustCompile(`font-upload-\d+`)
	reStoredURL     = regexp.MustCompile(`"url":"/uploads/((?:logo|bg)-[0-9a-f]{16})([^"]*)"`)
	reRecordedURL   = regexp.MustCompile(`"url":"/uploads/(?:logo|bg)-<HEX>([^"]*)"`)
)

// uploadExtensions are the extensions a logo or background gets by its
// content type (BUGS I9).
var uploadExtensions = map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif", "image/webp": ".webp", "image/svg+xml": ".svg"}

// nodeUploadName takes the name of a stored logo or background from the
// console's answer and the extension Node gave it from its recorded
// answer, and checks that the console's extension is the one of the
// type. It returns the console's name and the name Node gave the same
// file, both empty when nothing was stored.
func nodeUploadName(t *testing.T, body string, recorded any, mimeType string) (string, string) {
	t.Helper()
	got := reStoredURL.FindStringSubmatch(body)
	text, _ := recorded.(string)
	want := reRecordedURL.FindStringSubmatch(text)
	if got == nil || want == nil {
		return "", ""
	}
	if got[2] != uploadExtensions[mimeType] {
		t.Errorf("stored as %s%s, want the extension %s of the type %s", got[1], got[2], uploadExtensions[mimeType], mimeType)
	}
	return got[1] + got[2], got[1] + want[1]
}

func maskUploadNames(text string) string {
	return reTempUpload.ReplaceAllLiteralString(reRandomUpload.ReplaceAllString(text, "$1-<HEX>"), "font-upload-<MS>")
}

func loadFiles(t *testing.T) any {
	t.Helper()
	file, err := os.Open(filepath.Join("..", "..", "branding", "testdata", "files.json.gz"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	unzipped, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(unzipped)
	if err != nil {
		t.Fatal(err)
	}
	data, err := branding.ParseJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// lines is a recorded list of texts.
func lines(v any) []string {
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, item := range list {
		text, _ := branding.ToString(item)
		out = append(out, text)
	}
	return out
}

// lines2 is a recorded list of lists, each written as JSON.
func lines2(v any) []string {
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, item := range list {
		out = append(out, branding.Stringify(item))
	}
	return out
}

func clipText(s string) string {
	if len(s) > 1200 {
		return s[:600] + " ... " + s[len(s)-500:]
	}
	return s
}

// tree lists every entry below dir as the recording did: "rel/path =
// content", content as JSON text for small text files, "<n> bytes
// <sha256>" otherwise, "<dir>" and "<link>", sorted by UTF-16 code units.
func tree(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	var walk func(string)
	walk = func(current string) {
		list, err := os.ReadDir(current)
		if err != nil {
			return
		}
		for _, entry := range list {
			full := filepath.Join(current, entry.Name())
			rel, _ := filepath.Rel(dir, full)
			rel = maskUploadNames(filepath.ToSlash(rel))
			info, err := os.Lstat(full)
			if err != nil {
				t.Fatal(err)
			}
			switch {
			case info.Mode()&os.ModeSymlink != 0:
				out = append(out, rel+" = <link>")
			case info.IsDir():
				out = append(out, rel+"/ = <dir>")
				walk(full)
			default:
				data, err := os.ReadFile(full)
				if err != nil {
					t.Fatal(err)
				}
				if len(data) <= 300 && !bytes.Contains(data, []byte{0}) {
					out = append(out, rel+" = "+branding.Stringify(string(data)))
				} else {
					sum := sha256.Sum256(data)
					out = append(out, rel+" = "+strconv.Itoa(len(data))+" bytes "+hex.EncodeToString(sum[:]))
				}
			}
		}
	}
	walk(dir)
	sort.Slice(out, func(i, j int) bool {
		a, b := utf16.Encode([]rune(out[i])), utf16.Encode([]rune(out[j]))
		for k := 0; k < len(a) && k < len(b); k++ {
			if a[k] != b[k] {
				return a[k] < b[k]
			}
		}
		return len(a) < len(b)
	})
	return out
}

// backgroundList is the background list as the recording wrote it: sorted
// by time, newest first, then by name, with the random names masked and
// their times left out.
func backgroundList(t *testing.T, body string) string {
	t.Helper()
	parsed, err := branding.ParseJSON([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	list, _ := branding.Field(parsed, "data").([]any)
	type item struct {
		o          *branding.Object
		name, time string
	}
	var items []item
	for _, v := range list {
		o := v.(*branding.Object)
		name := maskUploadNames(o.Text("name"))
		o.Set("name", name)
		o.Set("url", maskUploadNames(o.Text("url")))
		items = append(items, item{o, name, o.Text("updatedAt")})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].time != items[j].time {
			return items[i].time > items[j].time
		}
		return items[i].name < items[j].name
	})
	out := make([]any, len(items))
	for i, it := range items {
		if strings.Contains(it.name, "<HEX>") {
			it.o.Set("updatedAt", "<T>")
		}
		out[i] = it.o
	}
	return branding.Stringify(out)
}
