package branding

import (
	"context"
	"errors"
	"io"
	"net/http"
	neturl "net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	googleFontsCSSURL = "https://fonts.googleapis.com/css2"
	fontUserAgent     = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36"
	fontFetchTimeout  = 15 * time.Second
)

var (
	reSpaceRun     = regexp.MustCompile(`[` + jsSpaceClass + `]+`)
	reWeightFile   = regexp.MustCompile(`-(\d+)\.woff2$`)
	reCustomFamily = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9` + jsSpaceClass + `.'-]{1,60}$`)
	reFontBlockEnd = regexp.MustCompile(`\}[` + jsSpaceClass + `]*`)
	reFontFileURL  = regexp.MustCompile(`url\((https://fonts\.gstatic\.com/[^)]+\.woff2)\)`)
	reFontWeight   = regexp.MustCompile(`font-weight:[` + jsSpaceClass + `]*(\d+)`)
	reThreeDigits  = regexp.MustCompile(`^\d{3}$`)
)

// CuratedFonts returns the built-in font list.
func CuratedFonts() []any {
	out := make([]any, len(curatedFonts))
	for i := range curatedFonts {
		out[i] = curatedFontObject(i)
	}
	return out
}

func curatedFontObject(i int) *Object {
	f := curatedFonts[i]
	variants := []any{}
	for _, v := range strings.Fields(f.variants) {
		variants = append(variants, v)
	}
	return object("family", f.family, "category", f.category, "variants", variants)
}

// SanitizeFontName turns a font family into the name of its folder:
// letters, digits and hyphens in lower case, white space as a hyphen. A
// family that leaves nothing fails with "Invalid font family name".
func SanitizeFontName(family any) (name string, err error) {
	defer catch(&err)
	return sanitizeFontName(family), nil
}

func sanitizeFontName(family any) string {
	safe, ok := fontFolderName(family)
	if !ok {
		throw("Invalid font family name")
	}
	return safe
}

// fontFolderName is sanitizeFontName without the throw: ok is false for
// a family that leaves no folder name.
func fontFolderName(family any) (string, bool) {
	var kept strings.Builder
	for _, r := range toString(or(family, "")) {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || isJSSpace(r) {
			kept.WriteRune(r)
		}
	}
	safe := strings.ToLower(reSpaceRun.ReplaceAllLiteralString(kept.String(), "-"))
	return safe, safe != ""
}

// EscapeCSSString makes text safe inside a quoted CSS string: quotes and
// the backslash get a backslash, control characters become hex escapes.
func EscapeCSSString(value any) (out string, err error) {
	defer catch(&err)
	return escapeCSSString(value), nil
}

func escapeCSSString(value any) string {
	s := toString(or(value, ""))
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '\\':
			b.WriteString(`\\`)
		case c == '\'':
			b.WriteString(`\'`)
		case c == '"':
			b.WriteString(`\"`)
		case c < 0x20 || c == 0x7F:
			b.WriteString(`\` + strconv.FormatUint(uint64(c), 16) + " ")
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func (s *Service) ensureFontsDir() {
	if !exists(s.opts.FontsDir) {
		if err := os.MkdirAll(s.opts.FontsDir, 0o777); err != nil {
			panic(fsError(err, "mkdir", s.opts.FontsDir))
		}
	}
}

func (s *Service) fontDir(safeName string) string {
	return resolveChildPath(s.opts.FontsDir, safeName)
}

func (s *Service) fontFile(safeName, fileName string) string {
	base := posixBasename(fileName)
	if base == "" || base == "." || base == ".." {
		throw("Invalid font file name")
	}
	return resolveChildPath(s.fontDir(safeName), base)
}

// readDirNames lists a folder. It throws the error Node's readdirSync
// throws.
func readDirNames(dir string) []string {
	list, err := os.ReadDir(dir)
	if err != nil {
		panic(fsError(err, "scandir", dir))
	}
	names := make([]string, len(list))
	for i, e := range list {
		names[i] = decodeUTF8([]byte(e.Name()))
	}
	return names
}

// woff2Files lists the .woff2 files of a font folder and the weights their
// names carry ("-400.woff2").
func woff2Files(dir string) (files []string, weights []any) {
	weights = []any{}
	for _, name := range readDirNames(dir) {
		if !strings.HasSuffix(name, ".woff2") {
			continue
		}
		files = append(files, name)
		if m := reWeightFile.FindStringSubmatch(name); m != nil {
			weights = append(weights, m[1])
		}
	}
	return files, weights
}

// IsFontDownloaded reports whether a font family has a folder with at
// least one .woff2 file.
func (s *Service) IsFontDownloaded(family any) (downloaded bool, err error) {
	defer catch(&err)
	return s.isFontDownloaded(family), nil
}

func (s *Service) isFontDownloaded(family any) bool {
	dir := s.fontDir(sanitizeFontName(family))
	if !exists(dir) {
		return false
	}
	files, _ := woff2Files(dir)
	return len(files) > 0
}

// SearchFonts returns the built-in fonts whose family contains query, of
// the given category ("all" or "" for every category), each with a
// "downloaded" flag.
func (s *Service) SearchFonts(query, category string) (fonts []any, err error) {
	defer catch(&err)
	return s.searchFonts(query, category), nil
}

func (s *Service) searchFonts(query, category string) []any {
	q := ""
	if trim(query) != "" {
		q = trim(toLower(query))
	}
	var picked []int
	for i, f := range curatedFonts {
		if category != "" && category != "all" && f.category != category {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(f.family), q) {
			continue
		}
		picked = append(picked, i)
	}
	s.ensureFontsDir()
	out := []any{}
	for _, i := range picked {
		font := curatedFontObject(i)
		font.Set("downloaded", s.isFontDownloaded(curatedFonts[i].family))
		out = append(out, font)
	}
	return out
}

// familyLower is f.family.toLowerCase() for an entry of the font cache
// file. It throws, as JavaScript does, when the entry or its family is not
// what the code expects.
func familyLower(f any) string {
	family := property(f, "family")
	text, ok := family.(string)
	if !ok {
		if family == nil || family == Undefined {
			throwType("Cannot read properties of " + toString(family) + " (reading 'toLowerCase')")
		}
		throwType("f.family.toLowerCase is not a function")
	}
	return toLower(text)
}

// SearchGoogleFonts is SearchFonts plus two more sources: the typed name
// itself as a font to fetch from Google when no listed family equals it,
// and the families of FontCacheFile. At most 80 fonts are returned.
func (s *Service) SearchGoogleFonts(query, category string) (fonts []any, err error) {
	defer catch(&err)
	fonts = s.searchFonts(query, category)
	lower := func(font any) string {
		family, _ := property(font, "family").(string)
		return toLower(family)
	}
	if q := trim(query); q != "" {
		exact := false
		for _, font := range fonts {
			if lower(font) == toLower(q) {
				exact = true
				break
			}
		}
		if !exact && reCustomFamily.MatchString(q) {
			custom := object(
				"family", q,
				"category", "sans-serif",
				"variants", []any{"400", "700"},
				"downloaded", s.isFontDownloaded(q),
				"source", "google-custom",
			)
			fonts = append([]any{custom}, fonts...)
		}
	}
	if exists(s.opts.FontCacheFile) {
		// A cache file that is broken, or broken from some entry on,
		// adds what came before the broken part.
		_ = try(func() {
			raw, err := os.ReadFile(s.opts.FontCacheFile)
			if err != nil {
				return
			}
			parsed, err := parseJSON(decodeUTF8(raw))
			if err != nil {
				return
			}
			cached, ok := parsed.([]any)
			if !ok {
				return
			}
			q := trim(toLower(query))
			var extra []any
			for _, f := range cached {
				if category != "" && category != "all" && property(f, "category") != category {
					continue
				}
				if q == "" || strings.Contains(familyLower(f), q) {
					extra = append(extra, f)
				}
			}
			seen := map[string]bool{}
			for _, font := range fonts {
				seen[lower(font)] = true
			}
			for _, f := range extra {
				name := familyLower(f)
				if seen[name] {
					continue
				}
				font := spread(NewObject(), f)
				font.Set("downloaded", s.isFontDownloaded(property(f, "family")))
				fonts = append(fonts, font)
				seen[name] = true
			}
		})
	}
	if len(fonts) > 80 {
		fonts = fonts[:80]
	}
	return fonts, nil
}

// GetLocalFont describes an installed font: its stylesheet, the weights
// of its .woff2 files and their number. It answers nil when the font has
// no font.css, and for a name that gives no folder name (no ASCII letter
// or digit), which no installed font can have.
func (s *Service) GetLocalFont(family any) (font *Object, err error) {
	defer catch(&err)
	return s.getLocalFont(family), nil
}

func (s *Service) getLocalFont(family any) *Object {
	safeName, ok := fontFolderName(family)
	if !ok {
		return nil
	}
	dir := s.fontDir(safeName)
	if !exists(s.fontFile(safeName, "font.css")) {
		return nil
	}
	files, weights := woff2Files(dir)
	return object(
		"available", true,
		"cssPath", "/fonts/"+safeName+"/font.css",
		"weights", weights,
		"fileCount", float64(len(files)),
	)
}

// ListLocalFonts lists the installed fonts: every folder of FontsDir that
// has a font.css, with the family name from its meta.json or from the
// built-in list. The order is by folder name.
func (s *Service) ListLocalFonts() (fonts []any, err error) {
	defer catch(&err)
	s.ensureFontsDir()
	list, readErr := os.ReadDir(s.opts.FontsDir)
	if readErr != nil {
		return nil, fsError(readErr, "scandir", s.opts.FontsDir)
	}
	fonts = []any{}
	for _, entry := range list {
		if !entry.IsDir() {
			continue
		}
		name := decodeUTF8([]byte(entry.Name()))
		var dir string
		if try(func() { dir = s.fontDir(name) }) != nil {
			continue
		}
		if !exists(s.fontFile(name, "font.css")) {
			continue
		}
		files, weights := woff2Files(dir)
		var family any = name
		var category any = "sans-serif"
		if metaFile := s.fontFile(name, "meta.json"); exists(metaFile) {
			_ = try(func() {
				raw, err := os.ReadFile(metaFile)
				if err != nil {
					return
				}
				meta, err := parseJSON(decodeUTF8(raw))
				if err != nil {
					return
				}
				if v := property(meta, "family"); Truthy(v) {
					family = v
				}
				if v := property(meta, "category"); Truthy(v) {
					category = v
				}
			})
		} else {
			for _, f := range curatedFonts {
				if sanitizeFontName(f.family) == name {
					family, category = f.family, f.category
					break
				}
			}
		}
		fonts = append(fonts, object(
			"family", family,
			"safeName", name,
			"category", category,
			"cssPath", "/fonts/"+name+"/font.css",
			"weights", weights,
			"fileCount", float64(len(files)),
		))
	}
	return fonts, nil
}

// DeleteLocalFont removes the folder of a font. It answers false when
// there is none.
func (s *Service) DeleteLocalFont(family any) (deleted bool, err error) {
	defer catch(&err)
	dir := s.fontDir(sanitizeFontName(family))
	if !exists(dir) {
		return false, nil
	}
	if err := os.RemoveAll(dir); err != nil {
		return false, fsError(err, "rm", dir)
	}
	return true, nil
}

// fontFaceCSS is one @font-face rule of a font's font.css.
func fontFaceCSS(family, weight, url, format string) string {
	return strings.Join([]string{
		"@font-face {",
		"    font-family: '" + escapeCSSString(family) + "';",
		"    font-style: normal;",
		"    font-weight: " + weight + ";",
		"    font-display: swap;",
		"    src: url('" + url + "') format('" + format + "');",
		"}",
	}, "\n")
}

// RegisterUploadedFont installs an uploaded font file: it copies filePath
// into the font's folder as <name>.ttf (when fileName ends in .ttf) or
// <name>-400.woff2, removes filePath, and writes font.css and meta.json.
// The content of the file is not looked at. family is trimmed and cut at
// 80 characters; nothing stands for "Custom Font".
func (s *Service) RegisterUploadedFont(family any, filePath, fileName string) (font *Object, err error) {
	defer catch(&err)
	s.ensureFontsDir()
	display := substring(trim(toString(or(family, "Custom Font"))), 80)
	if display == "" {
		throw("Font family name is required")
	}
	safeName := sanitizeFontName(display)
	dir := s.fontDir(safeName)
	if !exists(dir) {
		if err := os.MkdirAll(dir, 0o777); err != nil {
			return nil, fsError(err, "mkdir", dir)
		}
	}
	destName, format := safeName+"-400.woff2", "woff2"
	if toLower(posixExtname(fileName)) == ".ttf" {
		destName, format = safeName+".ttf", "truetype"
	}
	dest := s.fontFile(safeName, destName)
	if err := copyFile(filePath, dest); err != nil {
		return nil, err
	}
	_ = os.Remove(filePath)

	css := fontFaceCSS(display, "400", "/fonts/"+safeName+"/"+destName, format)
	if err := writeFile(s.fontFile(safeName, "font.css"), []byte(wellFormed(css))); err != nil {
		return nil, err
	}
	meta := Stringify(object("family", display, "category", "custom", "source", "upload"))
	if err := writeFile(s.fontFile(safeName, "meta.json"), []byte(wellFormed(meta))); err != nil {
		return nil, err
	}
	return object(
		"family", display,
		"safeName", safeName,
		"cssPath", "/fonts/"+safeName+"/font.css",
		"downloaded", true,
	), nil
}

func writeFile(path string, data []byte) error {
	return fsError(os.WriteFile(path, data, 0o666), "open", path)
}

// copyFile copies a file. A failure reads as Node's does: "ENOENT: no such
// file or directory, copyfile '<from>' -> '<to>'".
func copyFile(from, to string) error {
	both := from + "' -> '" + to
	in, err := os.Open(from)
	if err != nil {
		return fsError(err, "copyfile", both)
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o666)
	if err != nil {
		return fsError(err, "copyfile", both)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return fsError(err, "copyfile", both)
	}
	return fsError(out.Close(), "copyfile", both)
}

// GenerateFontCSS returns the font part of the stylesheet: an @import for
// each chosen font that is installed, the --font-heading and
// --font-family properties, and the rule that gives headings the heading
// font.
func (s *Service) GenerateFontCSS(headingFont, bodyFont any) (css string, err error) {
	defer catch(&err)
	return s.generateFontCSS(headingFont, bodyFont), nil
}

// trimmedFont is font && font.trim() for a font setting.
func trimmedFont(font any, name string) string {
	if !Truthy(font) {
		return ""
	}
	text, ok := font.(string)
	if !ok {
		throwType(name + ".trim is not a function")
	}
	return trim(text)
}

func (s *Service) generateFontCSS(headingFont, bodyFont any) string {
	var imports, vars []string
	heading := trimmedFont(headingFont, "headingFont")
	if heading != "" {
		if local := s.getLocalFont(heading); local != nil {
			imports = append(imports, "@import url('"+local.Text("cssPath")+"');")
		}
		vars = append(vars, "    --font-heading: '"+escapeCSSString(heading)+"', sans-serif;")
	}
	if body := trimmedFont(bodyFont, "bodyFont"); body != "" {
		if local := s.getLocalFont(body); local != nil && body != heading {
			imports = append(imports, "@import url('"+local.Text("cssPath")+"');")
		}
		vars = append(vars, "    --font-family: '"+escapeCSSString(body)+"', -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif;")
	}
	css := ""
	if len(imports) > 0 {
		css += strings.Join(imports, "\n") + "\n\n"
	}
	if len(vars) > 0 {
		css += ":root {\n" + strings.Join(vars, "\n") + "\n}\n"
	}
	if heading != "" {
		css += "\nh1, h2, h3, h4, h5, h6,\n" +
			".page-title, .settings-section-title, .login-title,\n" +
			".sidebar-logo-text, .brand-text-logo,\n" +
			".desktop-login-brand { font-family: var(--font-heading, var(--font-family)); }\n" +
			".ux35-shell .ux35-sidebar,\n" +
			".ux35-shell .ux35-sidebar :is(h1, h2, h3, h4, h5, h6),\n" +
			".ux35-shell .ux35-topbar { font-family: var(--ux35-font-chrome, -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif); }\n"
	}
	return css
}

// fetchURL gets a URL for the font download: one fixed User-Agent,
// redirects followed by hand to whatever Location names, 200 as the only
// good status, 15 seconds per request.
func (s *Service) fetchURL(ctx context.Context, url string) ([]byte, error) {
	client := &http.Client{
		Transport:     s.opts.HTTP,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	for {
		data, next, err := fetchOnce(ctx, client, url)
		if err != nil {
			return nil, err
		}
		if next == "" {
			return data, nil
		}
		url = next
	}
}

func fetchOnce(ctx context.Context, client *http.Client, url string) (data []byte, redirect string, err error) {
	ctx, cancel := context.WithTimeout(ctx, fontFetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil || (req.URL.Scheme != "http" && req.URL.Scheme != "https") || req.URL.Host == "" {
		// also a redirect to a path without a host
		return nil, "", &Error{Name: "TypeError", Message: "Invalid URL", Err: err}
	}
	req.Header.Set("User-Agent", fontUserAgent)
	res, err := client.Do(req)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, "", &Error{Name: "Error", Message: "Request timeout", Err: err}
		}
		// the connection's own error, as Node's request reported it:
		// "getaddrinfo ENOTFOUND fonts.googleapis.com", not "Get ...: ..."
		var urlErr *neturl.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		return nil, "", err
	}
	defer res.Body.Close()
	if location := res.Header.Get("Location"); res.StatusCode >= 300 && res.StatusCode < 400 && location != "" {
		return nil, location, nil
	}
	if res.StatusCode != http.StatusOK {
		return nil, "", &Error{Name: "Error", Message: "HTTP " + strconv.Itoa(res.StatusCode)}
	}
	data, err = io.ReadAll(res.Body)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, "", &Error{Name: "Error", Message: "Request timeout", Err: err}
		}
		return nil, "", err
	}
	return data, "", nil
}

// DownloadFont fetches a font family from Google Fonts and installs its
// latin .woff2 files with a font.css. weights is the list of wanted
// weights (Undefined stands for 400, 500, 600 and 700); for a built-in font those it does not have are dropped (its
// first weight is taken when none is left), for any other font everything
// that is not three digits is dropped ("400" and "700" when none is left).
// The answer lists the stored files; success is false when no file could
// be fetched.
func (s *Service) DownloadFont(ctx context.Context, family string, weights any) (result *Object, err error) {
	defer catch(&err)
	s.ensureFontsDir()
	if weights == Undefined {
		weights = []any{"400", "500", "600", "700"}
	}
	list, ok := weights.([]any)
	if !ok {
		if weights == nil {
			throwType("Cannot read properties of null (reading 'filter')")
		}
		throwType("weights.filter is not a function")
	}
	var available []any
	curated := -1
	for i, f := range curatedFonts {
		if f.family == family {
			curated = i
			break
		}
	}
	if curated >= 0 {
		variants := strings.Fields(curatedFonts[curated].variants)
		for _, w := range list {
			for _, v := range variants {
				if w == v {
					available = append(available, w)
					break
				}
			}
		}
		if len(available) == 0 {
			available = append(available, variants[0])
		}
	} else {
		for _, w := range list {
			if reThreeDigits.MatchString(toString(w)) {
				available = append(available, w)
			}
		}
		if len(available) == 0 {
			available = []any{"400", "700"}
		}
	}

	safeName := sanitizeFontName(family)
	dir := s.fontDir(safeName)
	if !exists(dir) {
		if err := os.MkdirAll(dir, 0o777); err != nil {
			return nil, fsError(err, "mkdir", dir)
		}
	}
	cssURL := googleFontsCSSURL + "?family=" + encodeURIComponent(family) + ":wght@" + joinValues(available, ";") + "&display=swap"
	cssData, err := s.fetchURL(ctx, cssURL)
	if err != nil {
		return nil, err
	}

	files := []any{}
	var faces []string
	for _, block := range reFontBlockEnd.Split(decodeUTF8(cssData), -1) {
		urlMatch := reFontFileURL.FindStringSubmatch(block)
		if urlMatch == nil || !strings.Contains(block, "latin") || strings.Contains(block, "latin-ext") {
			continue
		}
		weight := "400"
		if m := reFontWeight.FindStringSubmatch(block); m != nil {
			weight = m[1]
		}
		fileName := safeName + "-" + weight + ".woff2"
		filePath := s.fontFile(safeName, fileName)
		data, err := s.fetchURL(ctx, urlMatch[1])
		if err == nil {
			err = writeFile(filePath, data)
		}
		if err != nil {
			s.opts.Logf("[Fonts] Failed to download %s: %v", fileName, err)
			continue
		}
		files = append(files, fileName)
		faces = append(faces, fontFaceCSS(orText(trim(family), "sans-serif"), weight, "/fonts/"+safeName+"/"+fileName, "woff2"))
	}
	if len(files) > 0 {
		if err := writeFile(s.fontFile(safeName, "font.css"), []byte(wellFormed(strings.Join(faces, "\n\n")))); err != nil {
			return nil, err
		}
	}
	return object(
		"success", len(files) > 0,
		"path", "/fonts/"+safeName,
		"files", files,
		"cssPath", "/fonts/"+safeName+"/font.css",
	), nil
}

func orText(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// joinValues is Array.prototype.join.
func joinValues(values []any, sep string) string {
	parts := make([]string, len(values))
	for i, v := range values {
		if v != nil && v != Undefined {
			parts[i] = toString(v)
		}
	}
	return strings.Join(parts, sep)
}
