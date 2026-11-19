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

// fixedPure checks a recorded call of TestOraclePure whose answer a fix
// changes on purpose. It returns the bug id when Go answered as the fix
// says, and "" for a call no fix changes or one Go answers otherwise
// (which is then reported against Node's answer).
func fixedPure(name string, a []any, got string) string {
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
	if same && !(f.diverged == "BR1" && anyValue(rows, isControlAddress)) {
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
	for bug, atLeast := range map[string]int{"BR1": 5} {
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
