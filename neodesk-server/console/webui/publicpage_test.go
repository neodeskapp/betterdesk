package webui

import "testing"

// The pages a mail links to open without a session: the invite and reset
// page, and the page that confirms a new email address.
func TestMailLinkPagesArePublic(t *testing.T) {
	for _, page := range []string{"/set-password", "/confirm-email"} {
		if !isPublicPage(page) {
			t.Errorf("%s needs a session", page)
		}
	}
	if isPublicPage("/confirm-emails") {
		t.Error("/confirm-emails is public")
	}
}
