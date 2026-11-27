package mail

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// unbranded are the ways to put a message on the wire without
// brandmail's layout: the raw sender of this package, building a MIME
// message, an SMTP connection, the transport itself, and the standard
// library's SMTP client.
var unbranded = regexp.MustCompile(`\bSendUnbranded\(|\bmessage\.Build\(|\bsmtpwire\.Send\(|\.Transport\.Send\(|"net/smtp"|\bRawEmail\{`)

// allowedUnbranded are the files that may: the mail service's own sending
// path and its transport.
var allowedUnbranded = map[string]bool{
	"mail/send.go":      true,
	"mail/transport.go": true,
}

// TestNothingSendsUnbranded fails when code outside the mail service's
// own sending path builds or sends a message: every mail the console
// sends goes through SendEmail, which lays it out with brandmail.
func TestNothingSendsUnbranded(t *testing.T) {
	root := filepath.Join("..")
	var found []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "node_modules", "testdata", "static", "webui":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if allowedUnbranded[rel] || strings.HasPrefix(rel, "mail/message/") || strings.HasPrefix(rel, "mail/smtpwire/") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(data), "\n") {
			if trimmed := strings.TrimSpace(line); strings.HasPrefix(trimmed, "//") {
				continue
			}
			if unbranded.MatchString(line) {
				found = append(found, rel+":"+strconv.Itoa(i+1)+": "+strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range found {
		t.Errorf("a mail sent around the shared layout: %s", f)
	}
}
