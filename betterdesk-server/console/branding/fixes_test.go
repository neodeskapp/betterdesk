package branding

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// The bug fixes of BUGS.md that change what this package answers where
// the Node recordings in testdata say otherwise. The replays take the
// fixed answers from the helpers here.

var (
	reStoredUpload   = regexp.MustCompile(`"url":"/uploads/((?:logo|bg)-[0-9a-f]{16})([^"]*)"`)
	reRecordedUpload = regexp.MustCompile(`"url":"/uploads/(?:logo|bg)-<HEX>([^"]*)"`)
)

// The rules of the CSS cleaners, in the order Node ran each of them once:
// those of the custom CSS, and those of a colour or gradient.
var (
	customCSSWords = []*regexp.Regexp{reCSSImport, reCSSExpression, reCSSBehavior, reCSSMozBinding, reCSSScriptURL}
	colorWords     = []*regexp.Regexp{reCSSExpression, reCSSURL}
)

// hasCSSWord reports text in which one of the rules finds something.
func hasCSSWord(text string, rules []*regexp.Regexp) bool {
	for _, re := range rules {
		if re.MatchString(text) {
			return true
		}
	}
	return false
}

// nodeCustomCSS and nodeColor are the CSS cleaners as Node ran them: the
// rules for words once each, after the tags.
func nodeCustomCSS(text string) string {
	css := stripUntilStable(text, remover(reCSSStyleTag, ""), remover(reCSSAnyTag, ""), scriptTag.strip, styleTag.strip, remover(reEventAttr, " "))
	for i, re := range customCSSWords {
		with := ""
		if i == len(customCSSWords)-1 {
			with = "none"
		}
		css = re.ReplaceAllLiteralString(css, with)
	}
	return substring(css, 20000)
}

func nodeColor(text string) string {
	var kept []rune
	for _, r := range trim(text) {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("#%.,()/-", r) || isJSSpace(r) {
			kept = append(kept, r)
		}
	}
	out := string(kept)
	for _, re := range colorWords {
		out = re.ReplaceAllLiteralString(out, "")
	}
	return substring(out, 400)
}

// The rules of Node's SVG cleaner, which worked on the text.
var (
	anyCh         = `(?s:.)`
	svgPairedTags = []string{"script", "foreignobject", "iframe", "embed", "object", "applet", "animate", "set", "style", "use", "image"}
	svgSingleTags = []string{"script", "foreignobject", "iframe", "embed", "object", "applet", "style", "use", "image"}

	reXMLInstruction = regexp.MustCompile(`<\?` + anyCh + `*?\?>`)
	reDoctype        = regexp.MustCompile(`<!` + ci("doctype") + anyCh + `*?>`)
	// an element that can run script or load something, with its content
	// up to the closing tag of the same name (Go has no back references,
	// so every tag is its own alternative)
	reSVGDangerousTags = regexp.MustCompile(func() string {
		parts := make([]string, len(svgPairedTags))
		for i, tag := range svgPairedTags {
			parts[i] = `<` + sp + `*` + ci(tag) + `\b[^>]*>` + anyCh + `*?<` + sp + `*/` + sp + `*` + ci(tag) + sp + `*>`
		}
		return strings.Join(parts, `|`)
	}())
	reSVGDangerousSelfClosing = regexp.MustCompile(`<` + sp + `*` + ciAny(svgSingleTags...) + `\b[^>]*/>`)
	// an event handler or a script link, with the rest of the tag
	reSVGDangerousAttrs = regexp.MustCompile(sp + `(?:` + ci("on") + `\w+|` + ci("xlink:href") + sp + `*=` + sp + `*["']` + sp + `*` + urlSchemes + `:)[^>]*`)
	reSVGScriptHref     = regexp.MustCompile(`\b(?:` + ci("xlink:") + `)?` + ci("href") + sp + `*=` + sp + `*["']` + sp + `*` + urlSchemes + `:[^"']*`)
	reSVGCSSExpression  = regexp.MustCompile(`\b(?:` + ci("expression") + `|` + ci("@import") + `|` + ci("url") + sp + `*\(` + sp + `*["']?` + sp + `*` + urlSchemes + `:)`)
)

// nodeSVG is Node's SVG cleaner (sanitizeSvg), which the recordings hold.
func nodeSVG(s string) string {
	if s == "" {
		return ""
	}
	s = reXMLInstruction.ReplaceAllLiteralString(s, "")
	s = reDoctype.ReplaceAllLiteralString(s, "")
	s = stripUntilStable(s, remover(reSVGDangerousTags, ""), remover(reSVGDangerousSelfClosing, ""))
	s = stripUntilStable(s, remover(reSVGDangerousAttrs, ""), remover(reSVGScriptHref, ` href="#"`), remover(reSVGCSSExpression, "blocked-"))
	return stripUntilStable(s, scriptTag.strip, styleTag.strip, remover(reEventAttr, " "))
}

// fixedSVG checks a recorded call of sanitizeSvg whose answer differs
// (BR13): the console's answer must be SVG without problems that cleaning
// again leaves as it is, and when the argument and Node's answer are both
// SVG documents, it must draw the same shapes as Node's answer.
func fixedSVG(t testing.TB, a []any, got, want string) bool {
	t.Helper()
	answer, err := parseJSON(got)
	out, isText := answer.(string)
	if err != nil || !isText || len(svgProblems(out)) > 0 || SanitizeSVG(out) != out {
		return false
	}
	in, _ := argAt(a, 0).(string)
	node, err := parseJSON(want)
	nodeText, nodeIsText := node.(string)
	if _, isDoc := svgDrawing(in); !isDoc || err != nil || !nodeIsText {
		return true
	}
	nodeShapes, nodeIsDoc := svgDrawing(nodeText)
	shapes, _ := svgDrawing(out)
	return !nodeIsDoc || shapes == nodeShapes
}

// wrappedCSSWord reports a text that a CSS cleaner now cleans otherwise
// than Node did: one with a word wrapped around itself (BR7).
func wrappedCSSWord(v any) bool {
	text, ok := v.(string)
	if !ok {
		return false
	}
	return nodeCustomCSS(text) != sanitizeCustomCSS(text) || nodeColor(text) != sanitizeCSSColorValue(text)
}

// fixedPure checks a recorded call of TestOraclePure whose answer a fix
// changes on purpose (got is the console's answer, want Node's). It
// returns the bug id when Go answered as the fix says, and "" for a call
// no fix changes or one Go answers otherwise (which is then reported
// against Node's answer).
func fixedPure(t testing.TB, name string, a []any, got, want string) string {
	// BR13: SVG is read with a parser and cleaned with allowlists
	if name == "sanitizeSvg" {
		if fixedSVG(t, a, got, want) {
			return "BR13"
		}
		return ""
	}
	// BR7: the CSS cleaners run until nothing is left to remove
	rules := colorWords
	if name == "sanitizeCustomCss" {
		rules = customCSSWords
	}
	if anyValue(a, wrappedCSSWord) && !strings.HasPrefix(got, "throws ") && !hasCSSWord(got, rules) {
		return "BR7"
	}
	// BR1: an address on this server with a control character is refused,
	// so it reaches no answer
	if anyValue(a, isControlAddress) {
		switch name {
		case "validateBrandingUrl":
			if got == "null" {
				return "BR1"
			}
		case "buildBackgroundValue":
			if got == `""` {
				return "BR1"
			}
		default:
			if answer, err := parseJSON(got); err == nil && !anyValue(answer, isControlAddress) {
				return "BR1"
			}
		}
	}
	return ""
}

var reAppliedUpload = regexp.MustCompile(`^\{"success":true,"url":"(/uploads/(bg-<HEX>[^"]*))","applied":true,`)

// fixedUploadAnswer is the answer and the audit entries Node recorded for
// a step of the uploads, with the fixes applied, and the bug that changed
// them. BR10: a background whose target is named like something every
// object has ("constructor") is not applied.
func fixedUploadAnswer(entry any) (body, audit any, bug string) {
	body, audit = property(entry, "body"), property(entry, "audit")
	fields, _ := property(property(entry, "step"), "fields").(*Object)
	target, isText := "", false
	if fields != nil {
		target, isText = property(fields, "target").(string)
	}
	m := reAppliedUpload.FindStringSubmatch(toString(or(body, "")))
	if _, own := backgroundTargets[trim(target)]; !isText || m == nil || own {
		return body, audit, ""
	}
	return `{"success":true,"url":"` + m[1] + `","applied":false,"data":null}`,
		[]any{[]any{"branding_bg_upload", "Uploaded background: " + m[2]}}, "BR10"
}

// nodeFontKind is how Node stored an installed font: by the name the file
// came with, as <name>.ttf when it ends in .ttf and as <name>-400.woff2
// otherwise, whatever the bytes were.
func nodeFontKind(fileName string) fontKind {
	if toLower(posixExtname(fileName)) == ".ttf" {
		return fontKind{".ttf", "truetype"}
	}
	return fontKind{"-400.woff2", "woff2"}
}

// fixedFontFile is BR4 in the replays of a font that Node installed from
// content under fileName (an upload, or RegisterUploadedFont): the
// console stores a font by what its first bytes are and refuses a file
// that is no font. Where that differs from what Node did, it checks the
// console's outcome (font and err): the refusal for no font, with the
// fonts folder as it was before the step; for a font of another kind
// than its name says, the file and its font.css under the console's
// name. It then installs the font as Node had, so the fonts folder
// (compared next) and the steps after it are Node's again, and returns
// what Node answered. It reports whether the step was one of these.
func (r *filesReplay) fixedFontFile(family any, content []byte, fileName string, before []any, font *Object, err error) (nodeFont *Object, fixed bool) {
	t := r.t
	t.Helper()
	node := nodeFontKind(fileName)
	kind, isFont := fontKindOf(content)
	if isFont && kind == node {
		return nil, false
	}
	display, safeName := uploadedFontName(family)
	if !isFont {
		if Message(err) != NotAFont || font != nil {
			t.Errorf("a file that is no font (%q) was installed as %s: %v", clip(string(content)), clip(Stringify(font)), err)
		}
		if got := Stringify(tree(t, r.fontsDir())); got != Stringify(before) {
			t.Errorf("a file that is no font changed the fonts folder: %s", clip(got))
		}
	} else {
		stored := r.s.fontFile(safeName, safeName+kind.suffix)
		data, readErr := os.ReadFile(stored)
		css, _ := os.ReadFile(r.s.fontFile(safeName, "font.css"))
		face := "src: url('/fonts/" + safeName + "/" + safeName + kind.suffix + "') format('" + kind.format + "');"
		if err != nil || readErr != nil || string(data) != string(content) || !strings.Contains(string(css), face) {
			t.Errorf("a %s font named %q: %v %v, font.css %q", kind.format, fileName, err, readErr, css)
		}
		_ = os.Remove(stored)
	}
	url := "/fonts/" + safeName + "/" + safeName + node.suffix
	r.write(r.s.fontFile(safeName, safeName+node.suffix), content)
	r.write(r.s.fontFile(safeName, "font.css"), []byte(wellFormed(fontFaceCSS(display, "400", url, node.format))))
	r.write(r.s.fontFile(safeName, "meta.json"), []byte(wellFormed(Stringify(object("family", display, "category", "custom", "source", "upload")))))
	return object("family", display, "safeName", safeName, "cssPath", "/fonts/"+safeName+"/font.css", "downloaded", true), true
}

// refusedRedirect is BR16 in the replay of the font steps: a download
// that Node followed to another host than Google Fonts. The console
// refuses that redirect, so the file behind it is missing. It checks that
// the console asked for everything Node asked for but the other host and
// stored every file but that one, then writes the file and font.css as
// Node had them, so the fonts folder (compared next) and the steps after
// it are Node's again. It reports whether the step was one of these.
func (r *filesReplay) refusedRedirect(step, entry, value any, err error) bool {
	if property(step, "op") != "call" || property(step, "fn") != "downloadFont" {
		return false
	}
	var allowed, refused []any
	requests, _ := property(entry, "requests").([]any)
	for _, req := range requests {
		if strings.HasPrefix(toString(property(req, "url")), "https://fonts.") {
			allowed = append(allowed, req)
		} else {
			refused = append(refused, req)
		}
	}
	if len(refused) == 0 {
		return false
	}
	t := r.t
	t.Helper()
	if got, want := Stringify(or(r.google.requests, []any{})), Stringify(or(allowed, []any{})); got != want {
		t.Errorf("a download sent away from Google Fonts: requests\n got  %s\n want %s", got, want)
	}
	want, _ := expected(property(entry, "result"))
	node, parseErr := parseJSON(want)
	stored, isResult := value.(*Object)
	if err != nil || parseErr != nil || !isResult {
		t.Fatalf("a download sent away from Google Fonts: %v, Node %s", err, want)
	}
	nodeFiles, _ := property(node, "files").([]any)
	files, _ := property(stored, "files").([]any)
	have := map[string]bool{}
	for _, f := range files {
		have[toString(f)] = true
	}
	family := toString(argAt(args(object("a", property(step, "args"))), 0))
	safeName := sanitizeFontName(family)
	var faces []string
	missing := 0
	for _, f := range nodeFiles {
		name := toString(f)
		weight := reWeightFile.FindStringSubmatch(name)
		if weight == nil {
			t.Fatalf("a stored file without a weight: %s", name)
		}
		faces = append(faces, fontFaceCSS(orText(trim(family), "sans-serif"), weight[1], "/fonts/"+safeName+"/"+name, "woff2"))
		if have[name] {
			continue
		}
		if missing == len(refused) {
			t.Fatalf("the console is missing more files than Node fetched elsewhere: %s for %s", Stringify(files), Stringify(nodeFiles))
		}
		answer := r.google.answers[toString(property(refused[missing], "url"))]
		r.write(filepath.Join(r.fontsDir(), safeName, name), []byte(answer.Text("body")))
		missing++
	}
	if missing != len(refused) {
		t.Errorf("a download sent away from Google Fonts: the console stored %s, Node %s", Stringify(files), Stringify(nodeFiles))
	}
	r.write(filepath.Join(r.fontsDir(), safeName, "font.css"), []byte(strings.Join(faces, "\n\n")))
	return true
}

// tmpAfterUploads is BR3 in the replay of the uploads: the temporary
// folder Node left, without the files of refused font uploads, which the
// console removes. fixed says that there were such files.
func tmpAfterUploads(recorded any) (names []any, fixed bool) {
	list, _ := recorded.([]any)
	names = []any{}
	for _, name := range list {
		if strings.HasPrefix(toString(name), "font-upload-<MS>") {
			fixed = true
			continue
		}
		names = append(names, name)
	}
	return names, fixed
}

// noFontFolder is the answer of Node for a font name without an ASCII
// letter or digit, where the console finds no installed font (BR2).
const noFontFolder = "throws Error: Invalid font family name"

// fixedFontStep checks a font step of TestOracleFiles whose answer a fix
// changes on purpose, and returns the bug id when Go answered as the fix
// says.
func fixedFontStep(step any, want string, value any, err error) string {
	if want != noFontFolder || err != nil || property(step, "op") != "call" {
		return ""
	}
	switch property(step, "fn") {
	case "getLocalFont":
		if value == nil {
			return "BR2"
		}
	case "generateFontCss":
		if css, ok := value.(string); ok && strings.Contains(css, ":root {") {
			return "BR2"
		}
	}
	return ""
}

// fixedCSS reports BR2 for the stylesheet of TestOracleStore (got is its
// outcome, the stylesheet as JSON text): where Node threw for a font
// without an ASCII letter or digit, the console writes the stylesheet
// with that font named.
func fixedCSS(branding *Object, got string, recorded any) bool {
	want, _ := expected(recorded)
	parsed, err := parseJSON(got)
	css, isText := parsed.(string)
	if want != noFontFolder || err != nil || !isText {
		return false
	}
	for _, key := range []string{"fontHeading", "fontBody"} {
		if font := trim(branding.Text(key)); font != "" {
			asciiName := strings.ContainsFunc(font, func(r rune) bool { return r < 0x80 && (r == '-' || isASCIIAlpha(byte(r)) || isASCIIDigit(byte(r))) })
			if !asciiName && strings.Contains(css, "'"+escapeCSSString(font)+"'") {
				return true
			}
		}
	}
	return false
}

// fixedProfileAnswer reports BR11 for the answer of a profile step of
// TestOracleStore: a refusal has a fixed text where Node passed on the
// database's or JavaScript's own message.
func fixedProfileAnswer(step any, got, want string) bool {
	switch property(step, "op") {
	case "profileCreate", "profileUpdate", "profileDuplicate":
	default:
		return false
	}
	switch got {
	case "throws Error: Profile name already exists":
		return strings.Contains(want, "UNIQUE constraint failed") || strings.Contains(want, "duplicate key value")
	case "throws Error: Invalid profile name", "throws Error: Invalid profile description":
		return strings.HasPrefix(want, "throws TypeError: ")
	case "throws Error: The stored settings of the profile cannot be read":
		return strings.HasPrefix(want, "throws SyntaxError: ")
	}
	return false
}

// storeFixes follows TestOracleStore through the steps a fix changes on
// purpose. Such a step leaves another state than Node's, which stays until
// a later step makes it Node's again (a reset, rows written straight, the
// setting saved anew); until then diverged names the fix and the state is
// not compared. A profile made of the settings meanwhile holds them as
// they were in the console, so it is left out of the comparison of the
// profile rows, and applying it diverges again.
type storeFixes struct {
	t        testing.TB
	ctx      context.Context
	db       *store.DB
	diverged string
	fixed    map[string]int
	steps    map[string]int
	profiles map[int64]string
}

func newStoreFixes(t testing.TB, ctx context.Context, db *store.DB) *storeFixes {
	return &storeFixes{t: t, ctx: ctx, db: db, fixed: map[string]int{}, steps: map[string]int{}, profiles: map[int64]string{}}
}

// before is called before a step runs.
func (f *storeFixes) before(step any) {
	bug := ""
	id := f.profileID(step)
	switch {
	// BR1: a picture address with a control character in what the step
	// saves, writes or imports, or in the profile it applies
	case anyValue(step, isControlAddress):
		bug = "BR1"
	case property(step, "op") == "profileApply" && anyValue(f.profileData(id), isControlAddress):
		bug = "BR1"
	case property(step, "op") == "profileApply" && f.profiles[id] != "":
		bug = f.profiles[id]
	// BR7: a word wrapped around itself in a colour or the custom CSS
	case anyValue(step, wrappedCSSWord):
		bug = "BR7"
	// BR12: rows written straight that the console reads otherwise
	case property(step, "op") == "rows" && oddRows(property(step, "rows")):
		bug = "BR12"
	// BR5: a theme with both parts, whose flat part now counts
	case property(step, "op") == "import" && bothParts(property(step, "preset")):
		bug = "BR5"
	case property(step, "op") == "profileApply" && bothParts(f.profileData(id)):
		bug = "BR5"
	case property(step, "op") == "profileApply" && anyValue(f.profileData(id), wrappedCSSWord):
		bug = "BR7"
	// BR13: an SVG setting the console cleans otherwise than Node, on the
	// way in, and in rows written straight on the way out
	case property(step, "op") == "rows" && svgSettingValue(property(step, "rows"), cleanedOnRead):
		bug = "BR13"
	case property(step, "op") == "save" && svgSettingValue(property(step, "updates"), cleanedOtherwise(0)):
		bug = "BR13"
	case property(step, "op") == "import" && svgSettingValue(property(step, "preset"), cleanedOtherwise(50000)):
		bug = "BR13"
	case property(step, "op") == "profileApply" && svgSettingValue(f.profileData(id), cleanedOtherwise(50000)):
		bug = "BR13"
	}
	if bug != "" {
		f.diverged = bug
		f.fixed[bug]++
	}
}

// after is called with what a step answered.
func (f *storeFixes) after(step, value any) {
	created, _ := value.(float64)
	switch op := property(step, "op"); {
	case op == "profileDelete":
		delete(f.profiles, f.profileID(step))
	case (op == "profileCreate" || op == "profileDuplicate") && created > 0 && f.diverged != "":
		f.profiles[int64(created)] = f.diverged
	case op == "profileDuplicate" && created > 0 && f.profiles[f.profileID(step)] != "":
		f.profiles[int64(created)] = f.profiles[f.profileID(step)]
	}
}

// compared reports whether the state after a step is compared: not while
// it differs on purpose. A state that is Node's again ends that, unless
// the stored rows still hold what the fix treats otherwise (BR1: a row
// written straight with such an address shows only once its background
// is switched on).
func (f *storeFixes) compared(same bool, rows []any) bool {
	if f.diverged == "" {
		return true
	}
	stays := f.diverged == "BR1" && anyValue(rows, isControlAddress) || f.diverged == "BR12" && oddRows(rows)
	if same && !stays {
		f.diverged = ""
		return true
	}
	f.steps[f.diverged]++
	return false
}

func (f *storeFixes) profileID(step any) int64 {
	n, _ := property(step, "id").(float64)
	return int64(n)
}

// profileData is the stored settings of a profile, nil when there is none.
func (f *storeFixes) profileData(id int64) any {
	if id < 1 || id > 1<<31-1 {
		return nil
	}
	var data string
	err := f.db.QueryRow(f.ctx, "SELECT data FROM branding_profiles WHERE id = ?", id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		f.t.Fatal(err)
	}
	preset, _ := parseJSON(data)
	return preset
}

// recordedProfiles is the recorded profile list without the profiles the
// comparison leaves out.
func (f *storeFixes) recordedProfiles(recorded any) any {
	list, ok := recorded.([]any)
	if !ok {
		return recorded
	}
	kept := []any{}
	for _, p := range list {
		if n, _ := property(p, "id").(float64); f.profiles[int64(n)] == "" {
			kept = append(kept, p)
		}
	}
	return kept
}

// check checks that each fix changed some steps of the scenario, and
// that the state was Node's again soon after each.
func (f *storeFixes) check() {
	f.t.Helper()
	for bug, atLeast := range map[string]int{"BR1": 5, "BR2": 1, "BR5": 1, "BR7": 1, "BR11": 1, "BR12": 1, "BR13": 1} {
		if f.fixed[bug] < atLeast {
			f.t.Errorf("%d steps of the scenario are changed by the fix of %s, the recording has more", f.fixed[bug], bug)
		}
	}
	for bug, steps := range f.steps {
		f.t.Logf("%s: %d steps changed, the state differed from Node's for %d steps", bug, f.fixed[bug], steps)
		if steps > 40*f.fixed[bug] {
			f.t.Errorf("after the steps the fix of %s changes, the state stayed other than Node's for %d steps", bug, steps)
		}
	}
}

// fixedImportAnswer reports BR5 for the answer of an import of
// TestOracleStore with both parts: the console now reads the branding
// part too, so a value there that cannot be turned into text throws, and
// one PostgreSQL cannot store (a zero byte) fails the save, where Node,
// reading the appearance part alone, answered true.
func fixedImportAnswer(step any, got string) bool {
	return property(step, "op") == "import" && bothParts(property(step, "preset")) &&
		(got == "true" || strings.HasPrefix(got, "throws TypeError: ") || strings.HasPrefix(got, "throws Error: save the branding settings: "))
}

// fixedSVGAnswer reports BR13 for the answer of a save, an import or a
// profile applied in TestOracleStore that PostgreSQL refused in Node for a
// zero byte in the SVG markup of the logo or favicon: the console cleans
// such markup to "", as it is no XML, so the write goes through.
func (f *storeFixes) fixedSVGAnswer(step any, got, want string) bool {
	if got != "undefined" && got != "true" || !strings.Contains(want, `invalid byte sequence for encoding "UTF8": 0x00`) {
		return false
	}
	zeroByte := func(v any) bool {
		text, ok := v.(string)
		return ok && strings.ContainsRune(text, 0)
	}
	return svgSettingValue(step, zeroByte) || property(step, "op") == "profileApply" && svgSettingValue(f.profileData(f.profileID(step)), zeroByte)
}

// bothParts reports a theme with an appearance and a branding part, which
// an import now takes both of, where Node took the appearance part alone
// (BR5).
func bothParts(preset any) bool {
	if !isObjectLike(preset) {
		return false
	}
	return Truthy(property(preset, "appearance")) && Truthy(property(preset, "branding"))
}

// oddRows reports stored rows that the console reads otherwise than Node
// (BR12): a key every JavaScript object has, which Node took as a
// setting, or a slide list with a value that cannot be turned into text,
// which made Node forget every setting.
func oddRows(rows any) bool {
	list, _ := rows.([]any)
	for _, row := range list {
		pair, _ := row.([]any)
		if len(pair) != 2 {
			continue
		}
		key := toString(pair[0])
		if _, inherited := objectPrototype[key]; inherited && key != "__proto__" && !DefaultBranding().Has(key) {
			return true
		}
		if key == "loginSlides" && try(func() { sanitizeLoginSlides(pair[1]) }) != nil {
			return true
		}
	}
	return false
}

// svgSettingValue reports whether test holds for a value of logoSvg or
// faviconSvg anywhere in v: under that key in an object (also one handed
// over as JSON text), or as the value of a stored row [key, value].
func svgSettingValue(v any, test func(any) bool) bool {
	switch x := v.(type) {
	case []any:
		if len(x) == 2 && (x[0] == "logoSvg" || x[0] == "faviconSvg") && test(x[1]) {
			return true
		}
		for _, item := range x {
			if svgSettingValue(item, test) {
				return true
			}
		}
	case *Object:
		found := false
		entries(x, func(key string, value any) {
			found = found || (key == "logoSvg" || key == "faviconSvg") && test(value) || svgSettingValue(value, test)
		})
		return found
	case string:
		if text := trim(x); strings.HasPrefix(text, "[") || strings.HasPrefix(text, "{") {
			if parsed, err := parseJSON(x); err == nil {
				return svgSettingValue(parsed, test)
			}
		}
	}
	return false
}

// cleanedOtherwise is a test for svgSettingValue: a value that the
// console cleans otherwise than Node did when it is saved, after it is
// cut at limit characters (0: not cut).
func cleanedOtherwise(limit int) func(any) bool {
	return func(v any) bool {
		var text string
		if err := try(func() { text = toString(v) }); err != nil {
			return false
		}
		if limit > 0 {
			text = substring(text, limit)
		}
		return SanitizeSVG(text) != nodeSVG(text)
	}
}

// cleanedOnRead is a test for svgSettingValue: a stored value that the
// console cleans when it reads it, where Node took it as it was.
func cleanedOnRead(v any) bool {
	text, ok := v.(string)
	return ok && SanitizeSVG(text) != text
}

// isControlAddress reports a picture address on this server with a
// control character, which Node took and the console refuses (BR1).
func isControlAddress(v any) bool {
	text, ok := v.(string)
	if !ok {
		return false
	}
	trimmed := trim(text)
	return strings.HasPrefix(trimmed, "/") && !strings.HasPrefix(trimmed, "//") && !strings.Contains(trimmed, "..") && strings.ContainsFunc(trimmed, func(r rune) bool { return r < 0x20 || r == 0x7f })
}

// anyValue reports whether test holds for v or anything inside it.
func anyValue(v any, test func(any) bool) bool {
	if test(v) {
		return true
	}
	switch x := v.(type) {
	case []any:
		for _, item := range x {
			if anyValue(item, test) {
				return true
			}
		}
	case *Object:
		found := false
		entries(x, func(_ string, value any) { found = found || anyValue(value, test) })
		return found
	case string:
		// a list or object handed over as JSON text
		if text := trim(x); strings.HasPrefix(text, "[") || strings.HasPrefix(text, "{") {
			if parsed, err := parseJSON(x); err == nil {
				return anyValue(parsed, test)
			}
		}
	}
	return false
}

// renameAsNode is BUGS I9 in the replay of the uploads. The console
// stores a logo or background under the extension of its content type,
// where Node took the one of the client's file name. When the two
// differ, the stored file gets Node's name, so the later steps see the
// folder Node saw, and the answer and the audit entries name it as Node
// did. It checks that the console took the type's extension and returns
// the answer with Node's name.
func (r *filesReplay) renameAsNode(body string, audit []any, recorded any, mimeType string) string {
	r.t.Helper()
	got := reStoredUpload.FindStringSubmatch(body)
	want := reRecordedUpload.FindStringSubmatch(toString(recorded))
	if got == nil || want == nil {
		return body
	}
	if got[2] != imageExtensions[mimeType] {
		r.t.Errorf("stored as %s%s, want the extension %s of the type %s", got[1], got[2], imageExtensions[mimeType], mimeType)
	}
	from, to := got[1]+got[2], got[1]+want[1]
	if from == to {
		return body
	}
	r.renamed++
	if err := os.Rename(filepath.Join(r.uploadsDir(), from), filepath.Join(r.uploadsDir(), to)); err != nil {
		r.t.Fatal(err)
	}
	for _, entry := range audit {
		pair := entry.([]any)
		pair[1] = strings.ReplaceAll(toString(pair[1]), from, to)
	}
	return strings.ReplaceAll(body, from, to)
}

// TestStoreLogoTakesTheExtensionOfItsType: the stored name of a logo or
// background has the extension of its content type, never the one of the
// client's file name, and a type that is no accepted picture is refused
// without a file (BUGS I9).
func TestStoreLogoTakesTheExtensionOfItsType(t *testing.T) {
	s := newService(t, storetest.Migrated(t))
	for mimeType, ext := range map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif", "image/webp": ".webp", "image/svg+xml": ".svg"} {
		for prefix, store := range map[string]func(string, io.Reader) (string, error){"logo-": s.StoreLogo, "bg-": s.StoreBackground} {
			name, err := store(mimeType, strings.NewReader("<script>x</script>"))
			if err != nil || !regexp.MustCompile(`^`+prefix+`[0-9a-f]{16}\`+ext+`$`).MatchString(name) {
				t.Errorf("%s as %s: %q, %v; want %s<hex>%s", prefix, mimeType, name, err, prefix, ext)
			}
		}
	}
	before, _ := os.ReadDir(s.opts.UploadsDir)
	for _, mimeType := range []string{"text/html", "application/javascript", "image/svg", "image/PNG", ""} {
		if name, err := s.StoreLogo(mimeType, strings.NewReader("x")); err != ErrInvalidImageType {
			t.Errorf("a logo as %q: %q, %v", mimeType, name, err)
		}
	}
	if after, _ := os.ReadDir(s.opts.UploadsDir); len(after) != len(before) {
		t.Errorf("a refused type left a file: %d entries, were %d", len(after), len(before))
	}
}

// TestBackgroundAddressWithALineBreakStaysInItsDeclaration: an address on
// this server with a control character is refused like one with "..", so
// a line break can no longer end the url("...") of a background and add
// rules of its own to the stylesheet. A stored row of that kind gives no
// picture (BUGS BR1).
func TestBackgroundAddressWithALineBreakStaysInItsDeclaration(t *testing.T) {
	const attack = "/a\n}\nbody{display:none}"
	for _, address := range []string{attack, "/a\rb", "/a\fb", "/a\x00b", "/a\x7fb", "/a\tb", " /a\nb "} {
		if url, ok, err := ValidateBrandingURL(address); ok || url != "" || err != nil {
			t.Errorf("ValidateBrandingURL(%q) = %q, %v, %v; want refused", address, url, ok, err)
		}
	}
	if url, ok, _ := ValidateBrandingURL(" /uploads/bg one.jpg\n"); !ok || url != "/uploads/bg one.jpg" {
		t.Errorf("an address with white space around it: %q, %v", url, ok)
	}
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		s := newService(t, db)
		ctx := context.Background()
		if err := s.SaveBranding(ctx, object("bgType", "image", "bgImageUrl", "/uploads/bg.jpg", "loginBgType", "image", "rdclientBgType", "image")); err != nil {
			t.Fatal(err)
		}
		if err := s.SaveBranding(ctx, object("bgImageUrl", attack, "loginBgImageUrl", attack, "rdclientBgImageUrl", "/x\fy")); err != nil {
			t.Fatal(err)
		}
		branding := s.GetBranding()
		if branding.Text("bgImageUrl") != "/uploads/bg.jpg" || branding.Text("loginBgImageUrl") != "" || branding.Text("rdclientBgImageUrl") != "" {
			t.Errorf("stored addresses: %q, %q, %q", branding.Text("bgImageUrl"), branding.Text("loginBgImageUrl"), branding.Text("rdclientBgImageUrl"))
		}
		css := mustCSS(t, s)
		if strings.Contains(css, "display:none") || !strings.Contains(css, `url("/uploads/bg.jpg")`) {
			t.Errorf("the stylesheet after the save:\n%s", css)
		}
		checkCSSStructure(t, css, "")
		// a row an older version stored
		setRows(t, s, [2]string{"bgType", "image"}, [2]string{"bgImageUrl", attack})
		if css := mustCSS(t, s); strings.Contains(css, "display:none") || strings.Contains(css, "url(") {
			t.Errorf("the stylesheet of a stored row:\n%s", css)
		}
	})
}

// TestStoredSVGIsCleanedOnRead: the logo and favicon an older version
// stored are cleaned when the settings are read, so what Node's cleaner
// let through reaches neither the favicon nor the settings the web app
// and the remote pages get; the stored rows stay as they are until the
// next save (BUGS BR13).
func TestStoredSVGIsCleanedOnRead(t *testing.T) {
	const (
		stored  = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 4 4"><a href="java&#115;cript:alert(1)"><rect width="4" height="4"/></a></svg>`
		cleaned = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 4 4"><a><rect width="4" height="4"/></a></svg>`
		clean   = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 4 4"><circle cx="2" cy="2" r="2"/></svg>`
	)
	if nodeSVG(stored) != stored {
		t.Fatalf("Node's cleaner changes the stored logo: %q", nodeSVG(stored))
	}
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		s := newService(t, db)
		ctx := context.Background()
		setRows(t, s, [2]string{"logoType", "svg"}, [2]string{"logoSvg", stored}, [2]string{"faviconSvg", stored})
		b := s.GetBranding()
		if b.Text("logoSvg") != cleaned || b.Text("faviconSvg") != cleaned || s.GenerateFavicon() != cleaned {
			t.Errorf("read: logo %q, favicon setting %q, favicon %q", b.Text("logoSvg"), b.Text("faviconSvg"), s.GenerateFavicon())
		}
		if got := Stringify(s.BootstrapBranding("x")); !strings.Contains(got, `"logoSvg":"`+strings.ReplaceAll(cleaned, `"`, `\"`)+`"`) {
			t.Errorf("bootstrap: %s", got)
		}
		if value, _ := storedValue(t, s, "logoSvg"); value != stored {
			t.Errorf("the stored row was changed: %q", value)
		}
		// a clean one is read as it is, one with nothing left gives the
		// default favicon and logo
		setRows(t, s, [2]string{"logoType", "svg"}, [2]string{"logoSvg", clean}, [2]string{"faviconSvg", clean})
		if b := s.GetBranding(); b.Text("logoSvg") != clean || s.GenerateFavicon() != clean || b.Text("logoType") != "svg" {
			t.Errorf("a clean logo: %q, favicon %q, type %q", b.Text("logoSvg"), s.GenerateFavicon(), b.Text("logoType"))
		}
		defaultIcon, err := os.ReadFile(filepath.Join("testdata", "console", "public", "favicon.svg"))
		if err != nil {
			t.Fatal(err)
		}
		setRows(t, s, [2]string{"logoType", "svg"}, [2]string{"logoSvg", "<svg><script>x</svg>"}, [2]string{"faviconSvg", `<svg onload="x()"`})
		if b := s.GetBranding(); b.Text("logoSvg") != "" || b.Text("logoType") != "default" || s.GenerateFavicon() != string(defaultIcon) {
			t.Errorf("markup that is no SVG document: logo %q, type %q, favicon %q", b.Text("logoSvg"), b.Text("logoType"), s.GenerateFavicon())
		}
		// a save stores what it reads
		if err := s.SaveBranding(ctx, object("logoSvg", stored)); err != nil {
			t.Fatal(err)
		}
		if value, _ := storedValue(t, s, "logoSvg"); value != cleaned {
			t.Errorf("saved: %q", value)
		}
	})
}

// TestCleanersRemoveWordsWrappedAroundThemselves: the CSS cleaners run
// until nothing is left to remove, so a word wrapped around a copy of
// itself does not come back when the inner copy goes (BUGS BR7).
func TestCleanersRemoveWordsWrappedAroundThemselves(t *testing.T) {
	for in, gone := range map[string]string{
		"@im@import;port url(//evil.example/x.css);": "@import",
		"a { width: expexpression(ression(1) }":      "expression(",
		"a { bebehavior:havior: url(x.htc) }":        "behavior:",
		"a { -moz--moz-binding:binding: url(x) }":    "-moz-binding:",
		"a { background: <st<style>yle>red }":        "<style",
	} {
		out, err := SanitizeCustomCSS(in)
		if err != nil || strings.Contains(strings.ToLower(out), gone) {
			t.Errorf("SanitizeCustomCSS(%q) = %q, %v", in, out, err)
		}
	}
	for in, gone := range map[string]string{
		"expexpression(ression(1)":         "expression(",
		"linear-gradient(ururl(l(x), red)": "url(",
	} {
		out, err := SanitizeCSSColorValue(in)
		if err != nil || strings.Contains(out, gone) {
			t.Errorf("SanitizeCSSColorValue(%q) = %q, %v", in, out, err)
		}
	}
	if out, _ := SanitizeCustomCSS("a { background: url(/img/x.png) }"); out != "a { background: url(/img/x.png) }" {
		t.Errorf("a plain picture address in custom CSS: %q", out)
	}
}

// TestRefusedFontUploadLeavesNoTemporaryFile: a font upload leaves no
// temporary file, neither when its name is refused before anything is
// written nor when the install fails after the file was written (BUGS
// BR3).
func TestRefusedFontUploadLeavesNoTemporaryFile(t *testing.T) {
	s := oracleService(t)
	if _, err := s.UploadFont("你好", "x.ttf", []byte("font")); Message(err) != "Invalid font family name" {
		t.Errorf("a name without a folder name: %v", err)
	}
	// a file where the font's folder would go
	if err := os.MkdirAll(s.opts.FontsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.opts.FontsDir, "blocked"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UploadFont("Blocked", "x.ttf", []byte("\x00\x01\x00\x00font")); err == nil {
		t.Error("an install into a file did not fail")
	}
	if left, _ := os.ReadDir(s.opts.TempDir); len(left) != 0 {
		t.Errorf("temporary files: %v", left)
	}
}

// TestFontsAreStoredByTheirBytes: a font is stored and served as what its
// first bytes say it is, whatever the client named it, and a file that is
// no font is refused before anything is written, also when it reaches
// RegisterUploadedFont (BUGS BR4).
func TestFontsAreStoredByTheirBytes(t *testing.T) {
	rest := strings.Repeat("\x00", 60)
	for _, tc := range []struct{ content, name, stored, format string }{
		{"\x00\x01\x00\x00" + rest, "face.woff2", "face.ttf", "truetype"},
		{"true" + rest, "face.bin", "face.ttf", "truetype"},
		{"OTTO" + rest, "face.ttf", "face.otf", "opentype"},
		{"wOFF" + rest, "face.woff2", "face.woff", "woff"},
		{"wOF2" + rest, "face.ttf", "face-400.woff2", "woff2"},
	} {
		s := oracleService(t)
		font, err := s.UploadFont("Face", tc.name, []byte(tc.content))
		if err != nil || font.Text("cssPath") != "/fonts/face/font.css" {
			t.Fatalf("%q as %s: %v %s", tc.content[:4], tc.name, err, Stringify(font))
		}
		if got := Stringify(tree(t, s.opts.FontsDir)); !strings.Contains(got, `"face/`+tc.stored+` = `) || strings.Count(got, `"face/`) != 4 {
			t.Errorf("%q as %s: the fonts folder %s", tc.content[:4], tc.name, got)
		}
		css, _ := os.ReadFile(filepath.Join(s.opts.FontsDir, "face", "font.css"))
		if want := "src: url('/fonts/face/" + tc.stored + "') format('" + tc.format + "');"; !strings.Contains(string(css), want) {
			t.Errorf("%q as %s: font.css %s", tc.content[:4], tc.name, css)
		}
	}

	for _, content := range []string{"\x89PNG\r\n\x1a\n" + rest, "<!doctype html><script>alert(1)</script>", "this is not a font", "wOFX" + rest, "OTT", ""} {
		s := oracleService(t)
		if font, err := s.UploadFont("Face", "face.ttf", []byte(content)); Message(err) != NotAFont || font != nil {
			t.Errorf("%q: %s %v", content, Stringify(font), err)
		}
		if left := tree(t, s.opts.FontsDir); len(left) != 0 {
			t.Errorf("%q left %s in the fonts folder", content, Stringify(left))
		}
		if left, _ := os.ReadDir(s.opts.TempDir); len(left) != 0 {
			t.Errorf("%q left temporary files: %v", content, left)
		}

		file := filepath.Join(t.TempDir(), "in.ttf")
		if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := s.RegisterUploadedFont("Face", file, "in.ttf"); Message(err) != NotAFont {
			t.Errorf("register %q: %v", content, err)
		}
		if left := tree(t, s.opts.FontsDir); len(left) != 0 || !exists(file) {
			t.Errorf("register %q left %s in the fonts folder, its file is there: %v", content, Stringify(left), exists(file))
		}
	}
}
