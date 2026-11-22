package branding

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// testdata/files.json.gz recorded everything that touches files:
// the font service on a scratch folder, uploads through the Node routes,
// the background list, the built-in themes and the supporters list. The
// tests here do the same with this package and compare answers and
// folders.

var (
	reRandomName = regexp.MustCompile(`\b(logo|bg)-[0-9a-f]{16}`)
	reTempName   = regexp.MustCompile(`font-upload-\d+`)
	reCopyPaths  = regexp.MustCompile(`'[^']*' -> '[^']*'`)
)

// maskNames hides the random part of upload names and the time in the
// name of a temporary font file.
func maskNames(text string) string {
	return reTempName.ReplaceAllLiteralString(reRandomName.ReplaceAllString(text, "$1-<HEX>"), "font-upload-<MS>")
}

// tree lists every file below dir the way the oracle does: one line per
// entry, "rel/path = content", sorted.
func tree(t testing.TB, dir string) []any {
	t.Helper()
	var lines []string
	var walk func(string)
	walk = func(current string) {
		list, err := os.ReadDir(current)
		if err != nil {
			return
		}
		for _, entry := range list {
			full := filepath.Join(current, entry.Name())
			rel, _ := filepath.Rel(dir, full)
			rel = maskNames(filepath.ToSlash(rel))
			info, err := os.Lstat(full)
			if err != nil {
				t.Fatal(err)
			}
			switch {
			case info.Mode()&os.ModeSymlink != 0:
				lines = append(lines, rel+" = <link>")
			case info.IsDir():
				lines = append(lines, rel+"/ = <dir>")
				walk(full)
			default:
				data, err := os.ReadFile(full)
				if err != nil {
					t.Fatal(err)
				}
				if len(data) <= 300 && !bytes.Contains(data, []byte{0}) {
					lines = append(lines, rel+" = "+Stringify(decodeUTF8(data)))
				} else {
					sum := sha256.Sum256(data)
					lines = append(lines, rel+" = "+strconv.Itoa(len(data))+" bytes "+hex.EncodeToString(sum[:]))
				}
			}
		}
	}
	walk(dir)
	// the oracle sorts as JavaScript does, by UTF-16 code units
	sort.Slice(lines, func(i, j int) bool { return lessUTF16(lines[i], lines[j]) })
	out := make([]any, len(lines))
	for i, line := range lines {
		out[i] = line
	}
	return out
}

func lessUTF16(a, b string) bool {
	ua, ub := units(a), units(b)
	for i := 0; i < len(ua) && i < len(ub); i++ {
		if ua[i] != ub[i] {
			return ua[i] < ub[i]
		}
	}
	return len(ua) < len(ub)
}

// fakeGoogle answers font downloads from a table and records what was
// asked.
type fakeGoogle struct {
	answers  map[string]*Object
	requests []any
}

func (f *fakeGoogle) RoundTrip(req *http.Request) (*http.Response, error) {
	url := req.URL.String()
	f.requests = append(f.requests, object("url", url, "protocol", req.URL.Scheme, "userAgent", req.Header.Get("User-Agent")))
	if req.Header.Get("Accept-Encoding") != "" {
		return nil, errors.New("the font download must not ask for compression")
	}
	answer, known := f.answers[url]
	if !known {
		answer = object("status", float64(404), "body", "not found")
	}
	if message, ok := property(answer, "error").(string); ok {
		return nil, errors.New(message)
	}
	status := 200
	if n, ok := property(answer, "status").(float64); ok {
		status = int(n)
	}
	body := []byte(answer.Text("body"))
	if property(answer, "base64") == true {
		body, _ = base64.StdEncoding.DecodeString(answer.Text("body"))
	}
	header := http.Header{"Content-Type": {"text/css"}}
	if location := answer.Text("location"); location != "" {
		header.Set("Location", location)
	}
	return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(bytes.NewReader(body)), Request: req}, nil
}

// sortFonts orders what the oracle orders: the list of installed fonts by
// folder, and every list of weights. Node lists them in the order of the
// folder on disk, this package by name.
func sortFonts(value any) any {
	sortWeights := func(font any) {
		o, ok := font.(*Object)
		if !ok {
			return
		}
		if weights, ok := property(o, "weights").([]any); ok {
			sort.Slice(weights, func(i, j int) bool { return toString(weights[i]) < toString(weights[j]) })
		}
	}
	list, ok := value.([]any)
	if !ok {
		sortWeights(value)
		return value
	}
	named := true
	for _, font := range list {
		sortWeights(font)
		o, isObject := font.(*Object)
		if !isObject {
			named = false
			continue
		}
		if _, hasName := property(o, "safeName").(string); !hasName {
			named = false
		}
	}
	if named {
		sort.SliceStable(list, func(i, j int) bool {
			return lessUTF16(list[i].(*Object).Text("safeName"), list[j].(*Object).Text("safeName"))
		})
	}
	return list
}

type filesReplay struct {
	t      *testing.T
	dir    string
	s      *Service
	google *fakeGoogle
	data   any
	clock  time.Time
	// renamed counts the uploads stored under another name than Node's
	// (BUGS I9, see renameAsNode)
	renamed int
	// fixed counts the steps whose answer a fix changes, by bug
	// (fixedFontStep)
	fixed map[string]int
}

func newFilesReplay(t *testing.T) *filesReplay {
	t.Helper()
	r := &filesReplay{t: t, dir: t.TempDir(), google: &fakeGoogle{answers: map[string]*Object{}}, data: loadVectors(t, "files"), clock: fixedNow, fixed: map[string]int{}}
	opts := fixtureOptions(r.dir)
	opts.DB = storetest.Migrated(t)
	opts.HTTP = r.google
	opts.Now = func() time.Time {
		r.clock = r.clock.Add(time.Millisecond)
		return r.clock
	}
	// the themes of the recording: the real ones and some made up
	opts.ThemesDir = filepath.Join(r.dir, "themes")
	if err := os.MkdirAll(opts.ThemesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	themes := property(r.data, "themes")
	entries(property(themes, "real"), func(name string, _ any) {
		raw, err := os.ReadFile(filepath.Join("testdata", "console", "themes", name))
		if err != nil {
			t.Fatalf("the recording used the theme %s: %v", name, err)
		}
		r.write(filepath.Join(opts.ThemesDir, name), raw)
	})
	entries(property(themes, "files"), func(name string, text any) {
		r.write(filepath.Join(opts.ThemesDir, name), []byte(toString(text)))
	})
	if err := os.Mkdir(filepath.Join(opts.ThemesDir, "folder.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	opts.SupportersFile = filepath.Join(r.dir, "config", "supporters.json")
	s, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	r.s = s
	return r
}

func (r *filesReplay) write(file string, data []byte) {
	r.t.Helper()
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(file, data, 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func (r *filesReplay) fixture(name string) ([]byte, bool) {
	text, ok := property(property(r.data, "fixtures"), name).(string)
	if !ok {
		return nil, false
	}
	data, err := base64.StdEncoding.DecodeString(text)
	if err != nil {
		r.t.Fatal(err)
	}
	return data, true
}

// result is the outcome of a call with names and folders masked as the
// oracle masks them.
func (r *filesReplay) result(value any, err error) string {
	text := maskNames(outcome(value, err))
	text = strings.ReplaceAll(text, filepath.ToSlash(r.dir), "<ROOT>")
	return reCopyPaths.ReplaceAllLiteralString(text, "'<FROM>' -> '<TO>'")
}

func (r *filesReplay) fontsDir() string   { return filepath.Join(r.dir, "public", "fonts") }
func (r *filesReplay) uploadsDir() string { return filepath.Join(r.dir, "data", "uploads") }
func (r *filesReplay) tempDir() string    { return filepath.Join(r.dir, "data", "tmp") }

// fontSteps replays the steps on the font service.
func (r *filesReplay) fontSteps() {
	t, s := r.t, r.s
	ctx := context.Background()
	steps, _ := property(r.data, "fonts").([]any)
	if len(steps) < 200 {
		t.Errorf("%d font steps recorded, expected more than 200", len(steps))
	}
	registered := 0
	for i, entry := range steps {
		step := property(entry, "step")
		a := args(object("a", or(property(step, "args"), []any{})))
		r.google.requests = nil
		var value any = Undefined
		var err error
		extra := ""
		switch op := toString(property(step, "op")); op {
		case "call":
			switch fn := toString(property(step, "fn")); fn {
			case "listLocalFonts":
				value, err = s.ListLocalFonts()
			case "searchFonts":
				value, err = s.SearchFonts(textArg(a, 0), textArg(a, 1))
			case "searchGoogleFonts":
				value, err = s.SearchGoogleFonts(textArg(a, 0), textArg(a, 1))
			case "isFontDownloaded":
				value, err = s.IsFontDownloaded(argAt(a, 0))
			case "getLocalFont":
				var font *Object
				font, err = s.GetLocalFont(argAt(a, 0))
				value = nil
				if font != nil {
					value = font
				}
			case "downloadFont":
				value, err = s.DownloadFont(ctx, textArg(a, 0), argAt(a, 1))
			case "generateFontCss":
				value, err = s.GenerateFontCSS(argAt(a, 0), argAt(a, 1))
			case "deleteLocalFont":
				value, err = s.DeleteLocalFont(argAt(a, 0))
			default:
				t.Fatalf("step %d: unknown function %s", i, fn)
			}
		case "remote":
			r.google.answers[toString(property(step, "url"))] = property(step, "answer").(*Object)
		case "register":
			registered++
			tmp := filepath.Join(r.tempDir(), "in-"+strconv.Itoa(registered))
			data, ok := r.fixture(toString(property(step, "fixture")))
			if ok {
				r.write(tmp, data)
			} else if err := os.MkdirAll(r.tempDir(), 0o755); err != nil {
				t.Fatal(err)
			}
			before := tree(t, r.fontsDir())
			var font *Object
			font, err = s.RegisterUploadedFont(property(step, "family"), tmp, toString(property(step, "fileName")))
			if ok && property(property(entry, "result"), "r") != Undefined {
				if nodeFont, fixed := r.fixedFontFile(property(step, "family"), data, toString(property(step, "fileName")), before, font, err); fixed {
					r.fixed["BR4"]++
					if err != nil {
						// Node installed what the console refused, and
						// removed the file it came from
						font, err = nodeFont, nil
						_ = os.Remove(tmp)
					}
				}
			}
			if err == nil {
				value = font
			}
			extra = " tmpLeft=" + strconv.FormatBool(exists(tmp))
		case "write":
			r.write(filepath.Join(r.fontsDir(), filepath.FromSlash(toString(property(step, "file")))), []byte(toString(property(step, "text"))))
		case "link":
			if err := os.Symlink(filepath.Join(r.fontsDir(), toString(property(step, "target"))), filepath.Join(r.fontsDir(), toString(property(step, "file")))); err != nil {
				t.Fatal(err)
			}
		case "cache":
			file := filepath.Join(r.dir, "data", "google-fonts-cache.json")
			if text, ok := property(step, "text").(string); ok {
				r.write(file, []byte(wellFormed(text)))
			} else {
				_ = os.Remove(file)
			}
		default:
			t.Fatalf("step %d: unknown op %s", i, op)
		}
		if err == nil {
			value = sortFonts(value)
		}

		recorded := property(entry, "result")
		want, _ := expected(recorded)
		if left := property(recorded, "tmpLeft"); left != Undefined {
			want += " tmpLeft=" + toString(left)
		}
		if r.refusedRedirect(step, entry, value, err) {
			r.fixed["BR16"]++
		} else {
			if got := r.result(value, err) + extra; got != want {
				if bug := fixedFontStep(step, want, value, err); bug != "" {
					r.fixed[bug]++
				} else {
					t.Errorf("font step %d %s\n got  %s\n want %s", i, clip(Stringify(step)), clip(got), clip(want))
				}
			}
			asked := r.google.requests
			if asked == nil {
				asked = []any{}
			}
			if got, want := Stringify(asked), Stringify(or(property(entry, "requests"), []any{})); got != want {
				t.Errorf("font step %d %s: requests\n got  %s\n want %s", i, clip(Stringify(step)), got, want)
			}
		}
		if got, want := Stringify(tree(t, r.fontsDir())), Stringify(property(entry, "fonts")); got != want {
			t.Errorf("font step %d %s: the fonts folder\n got  %s\n want %s", i, clip(Stringify(step)), clip(got), clip(want))
		}
	}
}

// failure is the JSON answer of a route that refuses a request.
func failure(status int, message string) (int, string) {
	return status, Stringify(object("success", false, "error", message))
}

// uploadLogo does what POST /api/settings/branding/upload-logo does with
// one file in the field "logo".
func (r *filesReplay) uploadLogo(mimeType string, content []byte, audit *[]any) (int, string) {
	if !AcceptImageUpload(mimeType) {
		return failure(400, "Invalid file type")
	}
	stored, err := r.s.StoreLogo(mimeType, bytes.NewReader(content))
	if errors.Is(err, ErrFileTooLarge) {
		return failure(400, "File too large (max 2 MB)")
	}
	if err != nil {
		return failure(400, orText(Message(err), "Upload failed"))
	}
	r.s.RemovePreviousLogo(stored)
	*audit = append(*audit, []any{"branding_logo_upload", "Uploaded logo: " + stored})
	return 200, Stringify(object("success", true, "url", "/uploads/"+stored))
}

// uploadBackground does what POST
// /api/settings/branding/upload-background does.
func (r *filesReplay) uploadBackground(mimeType string, content []byte, target any, audit *[]any) (int, string) {
	if !AcceptImageUpload(mimeType) {
		return failure(400, "Invalid file type")
	}
	stored, err := r.s.StoreBackground(mimeType, bytes.NewReader(content))
	if errors.Is(err, ErrFileTooLarge) {
		return failure(400, "File too large (max 8 MB)")
	}
	if err != nil {
		return failure(400, orText(Message(err), "Upload failed"))
	}
	url := "/uploads/" + stored
	wanted := trim(toString(or(target, "")))
	branding, applied, err := r.s.ApplyBackground(context.Background(), wanted, url)
	if err != nil {
		r.t.Fatalf("apply background: %v", err)
	}
	answer := object("success", true, "url", url, "applied", applied)
	if applied {
		*audit = append(*audit, []any{"branding_bg_upload", "Uploaded and applied background: " + stored + " (" + wanted + ")"})
		answer.Set("data", branding)
		answer.Set("revision", r.s.Revision())
	} else {
		*audit = append(*audit, []any{"branding_bg_upload", "Uploaded background: " + stored})
		answer.Set("data", nil)
	}
	return 200, Stringify(answer)
}

// uploadFont does what POST /api/settings/fonts/upload does. nodeStored
// is whether Node installed the font.
func (r *filesReplay) uploadFont(name, mimeType string, content []byte, family any, audit *[]any, nodeStored bool) (int, string) {
	if !AcceptFontUpload(name, mimeType) {
		return failure(400, "Invalid font file type")
	}
	if len(content) > FontMaxBytes {
		return failure(400, "File too large (max 5 MB)")
	}
	before := tree(r.t, r.fontsDir())
	font, err := r.s.UploadFont(family, name, content)
	if nodeStored {
		if nodeFont, fixed := r.fixedFontFile(fontUploadFamily(family, name), content, name, before, font, err); fixed {
			r.fixed["BR4"]++
			if err != nil {
				font, err = nodeFont, nil
			}
		}
	}
	if err != nil {
		return failure(400, orText(Message(err), "Upload failed"))
	}
	logged := trim(toString(or(family, basenameWithout(name, posixExtname(name)))))
	*audit = append(*audit, []any{"font_upload", "Uploaded font: " + logged})
	return 200, Stringify(object("success", true, "data", font))
}

func (r *filesReplay) uploadSteps() {
	t, s := r.t, r.s
	ctx := context.Background()
	steps, _ := property(r.data, "uploads").([]any)
	if len(steps) < 150 {
		t.Errorf("%d upload steps recorded, expected more than 150", len(steps))
	}
	last := ""
	for i, entry := range steps {
		step := property(entry, "step")
		get := func(key string) string { return toString(property(step, key)) }
		var audit []any
		status, body := 0, ""
		switch op := get("op"); op {
		case "upload":
			var content []byte
			described := property(step, "content")
			if text, ok := property(described, "base64").(string); ok {
				content, _ = base64.StdEncoding.DecodeString(text)
			} else {
				size, _ := property(described, "size").(float64)
				fill, _ := property(described, "fill").(float64)
				content = bytes.Repeat([]byte{byte(fill)}, int(size))
			}
			probe := property(entry, "probe")
			if probe == Undefined {
				// The upload reader of the Node routes found no file in
				// the request (a file named ".." is none for it). That
				// is the route's business, not this package's.
				status, body = failure(400, "No file provided")
				break
			}
			name, mimeType := toString(property(probe, "originalname")), toString(property(probe, "mimetype"))
			fields := property(step, "fields")
			switch kind := get("kind"); kind {
			case "logo":
				status, body = r.uploadLogo(mimeType, content, &audit)
			case "background":
				status, body = r.uploadBackground(mimeType, content, property(fields, "target"), &audit)
			case "font":
				status, body = r.uploadFont(name, mimeType, content, property(fields, "family"), &audit, property(entry, "status") == float64(200))
			default:
				t.Fatalf("step %d: unknown kind %s", i, kind)
			}
			body = r.renameAsNode(body, audit, property(entry, "body"), mimeType)
			if parsed, err := parseJSON(body); err == nil {
				if url, ok := property(parsed, "url").(string); ok {
					last = url
				}
			}
		case "save":
			updates, err := parseJSON(strings.Replace(Stringify(property(step, "updates")), "$last", last, 1))
			if err != nil {
				t.Fatal(err)
			}
			if err := s.SaveBranding(ctx, updates); err != nil {
				t.Fatalf("step %d: save: %v", i, err)
			}
		case "file":
			r.write(filepath.Join(r.uploadsDir(), get("name")), []byte(get("text")))
		case "dir":
			if err := os.Mkdir(filepath.Join(r.uploadsDir(), get("name")), 0o755); err != nil {
				t.Fatal(err)
			}
		case "link":
			if err := os.Symlink(filepath.Join(r.uploadsDir(), get("target")), filepath.Join(r.uploadsDir(), get("name"))); err != nil {
				t.Fatal(err)
			}
		case "mtime":
			// Node sets file times in whole microseconds
			seconds, _ := property(step, "seconds").(float64)
			whole := math.Floor(seconds)
			when := time.Unix(int64(whole), int64((seconds-whole)*1e9)/1000*1000)
			if err := os.Chtimes(filepath.Join(r.uploadsDir(), get("name")), when, when); err != nil {
				t.Fatal(err)
			}
		case "backgrounds":
			list, err := s.ListBackgrounds()
			if err != nil {
				t.Fatalf("step %d: list backgrounds: %v", i, err)
			}
			status = 200
			for j := 1; j < len(list); j++ {
				if list[j-1].(*Object).Text("updatedAt") < list[j].(*Object).Text("updatedAt") {
					t.Errorf("step %d: the background list is not newest first: %s", i, Stringify(list))
				}
			}
			for _, item := range list {
				o := item.(*Object)
				o.Set("name", maskNames(o.Text("name")))
				o.Set("url", maskNames(o.Text("url")))
			}
			sort.SliceStable(list, func(a, b int) bool {
				x, y := list[a].(*Object), list[b].(*Object)
				if x.Text("updatedAt") != y.Text("updatedAt") {
					return x.Text("updatedAt") > y.Text("updatedAt")
				}
				return lessUTF16(x.Text("name"), y.Text("name"))
			})
			for _, item := range list {
				if o := item.(*Object); strings.Contains(o.Text("name"), "<HEX>") {
					o.Set("updatedAt", "<T>")
				}
			}
			body = Stringify(list)
			if property(entry, "inTimeOrder") != true {
				t.Errorf("step %d: the recorded list was not in time order", i)
			}
		default:
			t.Fatalf("step %d: unknown op %s", i, op)
		}

		what := "upload step " + strconv.Itoa(i) + " " + clip(Stringify(step))
		if recorded, ok := property(entry, "status").(float64); ok && int(recorded) != status {
			t.Errorf("%s: status %d, want %d (body %s)", what, status, int(recorded), clip(body))
		}
		wantBody, wantAudit, fixed := fixedUploadAnswer(entry)
		if fixed != "" {
			r.fixed[fixed]++
		}
		if recorded, ok := wantBody.(string); ok {
			if got := maskNames(maskRevision(body)); got != recorded {
				t.Errorf("%s: body\n got  %s\n want %s", what, clip(got), clip(recorded))
			}
		}
		if got, want := maskNames(Stringify(or(audit, []any{}))), Stringify(or(wantAudit, []any{})); got != want {
			t.Errorf("%s: audit entries\n got  %s\n want %s", what, got, want)
		}
		if got, want := Stringify(tree(t, r.uploadsDir())), Stringify(property(entry, "uploads")); got != want {
			t.Errorf("%s: the uploads folder\n got  %s\n want %s", what, clip(got), clip(want))
		}
		if got, want := maskNames(s.GetBranding().Text("logoUrl")), toString(property(entry, "logoUrl")); got != want {
			t.Errorf("%s: logoUrl %q, want %q", what, got, want)
		}
		if recorded := property(entry, "fonts"); recorded != Undefined {
			if got, want := Stringify(tree(t, r.fontsDir())), Stringify(recorded); got != want {
				t.Errorf("%s: the fonts folder\n got  %s\n want %s", what, clip(got), clip(want))
			}
			seen := map[string]bool{}
			names := []any{}
			for _, line := range tree(t, r.tempDir()) {
				name, _, _ := strings.Cut(line.(string), " = ")
				if !seen[name] {
					seen[name] = true
					names = append(names, name)
				}
			}
			wantTmp, fixed := tmpAfterUploads(property(entry, "tmp"))
			if fixed {
				r.fixed["BR3"]++
			}
			if got, want := Stringify(names), Stringify(wantTmp); got != want {
				t.Errorf("%s: the temporary folder\n got  %s\n want %s", what, got, want)
			}
		}
	}
}

func (r *filesReplay) themes() {
	t, s := r.t, r.s
	ctx := context.Background()
	themes := property(r.data, "themes")
	list, err := s.ListThemes()
	if err != nil {
		t.Fatal(err)
	}
	sort.SliceStable(list, func(i, j int) bool { return lessUTF16(list[i].(*Object).Text("id"), list[j].(*Object).Text("id")) })
	if got, want := Stringify(object("success", true, "data", list)), toString(property(property(themes, "list"), "body")); got != want {
		t.Errorf("theme list\n got  %s\n want %s", clip(got), clip(want))
	}
	steps, _ := property(themes, "steps").([]any)
	if len(steps) < 20 {
		t.Errorf("%d theme steps recorded, expected more than 20", len(steps))
	}
	for _, step := range steps {
		id := toString(property(step, "id"))
		if err := s.ResetBranding(ctx); err != nil {
			t.Fatal(err)
		}
		result, err := s.ApplyTheme(ctx, id)
		status, body := 200, Stringify(object("success", true, "message", `Theme "`+id+`" applied`))
		switch {
		case err != nil:
			status, body = failure(500, "Failed to apply theme")
		case result == ThemeInvalidID:
			status, body = failure(400, "Invalid theme id")
		case result == ThemeNotFound:
			status, body = failure(404, "Theme not found")
		case result == ThemeInvalidFormat:
			status, body = failure(400, "Invalid theme format")
		}
		wantStatus, _ := property(step, "status").(float64)
		if status != int(wantStatus) || body != toString(property(step, "body")) {
			t.Errorf("apply theme %q: %d %s (error %v), want %d %s", id, status, body, err, int(wantStatus), toString(property(step, "body")))
		}
		rows, err := getBrandingConfigSorted(ctx, s.opts.DB)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := Stringify(rows), toString(property(step, "rows")); got != want {
			t.Errorf("apply theme %q: stored rows\n got  %s\n want %s", id, clip(got), clip(want))
		}
	}
}

func (r *filesReplay) supporters() {
	t, s := r.t, r.s
	steps, _ := property(r.data, "supporters").([]any)
	if len(steps) < 10 {
		t.Errorf("%d supporter files recorded, expected more than 10", len(steps))
	}
	file := s.opts.SupportersFile
	for i, step := range steps {
		if text, ok := property(step, "text").(string); ok {
			r.write(file, []byte(text))
		} else if err := os.Remove(file); err != nil {
			t.Fatal(err)
		}
		s.ResetSupportersCache()
		first := Stringify(s.LoadSupporters())
		if want := toString(property(step, "r")); first != want {
			t.Errorf("supporters file %d %s\n got  %s\n want %s", i, clip(Stringify(property(step, "text"))), clip(first), clip(want))
		}
		// the answer is kept until the cache is reset
		if _, ok := property(step, "text").(string); ok {
			r.write(file, []byte(`{"honorary":[{"name":"changed"}]}`))
		}
		if again := Stringify(s.LoadSupporters()); again != first || property(step, "cached") != true {
			t.Errorf("supporters file %d: a second call answered %s after %s", i, clip(again), clip(first))
		}
	}
}

func TestOracleFiles(t *testing.T) {
	r := newFilesReplay(t)
	t.Run("fonts", func(t *testing.T) {
		r.t = t
		r.fontSteps()
		if r.fixed["BR2"] == 0 {
			t.Error("no font step looks up a name without ASCII letters, the recording has some (BUGS BR2)")
		}
		if r.fixed["BR16"] == 0 {
			t.Error("no font download was sent away from Google Fonts, the recording has one (BUGS BR16)")
		}
		if r.fixed["BR4"] < 6 {
			t.Errorf("%d installed fonts are no font or named as another kind, the recording has more (BUGS BR4)", r.fixed["BR4"])
		}
	})
	t.Run("uploads", func(t *testing.T) {
		r.t = t
		registered := r.fixed["BR4"]
		r.uploadSteps()
		if uploaded := r.fixed["BR4"] - registered; uploaded < 10 {
			t.Errorf("%d uploaded fonts are no font or named as another kind, the recording has more (BUGS BR4)", uploaded)
		}
		if r.renamed < 10 {
			t.Errorf("%d uploads stored under another name than Node's, the recording has more (BUGS I9)", r.renamed)
		}
		if r.fixed["BR10"] < 3 {
			t.Errorf("%d background uploads name a target every object has, the recording has more (BUGS BR10)", r.fixed["BR10"])
		}
		if r.fixed["BR3"] == 0 {
			t.Error("no refused font upload left a temporary file in the recording, it has some (BUGS BR3)")
		}
	})
	t.Run("themes", func(t *testing.T) { r.t = t; r.themes() })
	t.Run("supporters", func(t *testing.T) { r.t = t; r.supporters() })
}
