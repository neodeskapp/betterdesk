package branding

import (
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// The bug fixes of BUGS.md that change what this package answers where
// the Node recordings in testdata say otherwise. The replays take the
// fixed answers from the helpers here.

var (
	reStoredUpload   = regexp.MustCompile(`"url":"/uploads/((?:logo|bg)-[0-9a-f]{16})([^"]*)"`)
	reRecordedUpload = regexp.MustCompile(`"url":"/uploads/(?:logo|bg)-<HEX>([^"]*)"`)
)

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
