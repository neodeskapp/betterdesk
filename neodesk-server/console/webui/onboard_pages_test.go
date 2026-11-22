package webui

import "testing"

// The pages behind invite and quick support links open for anyone: the
// person who gets the link has no account.
func TestInviteAndSupportPagesArePublic(t *testing.T) {
	for sub, open := range map[string]bool{
		"/join/ABCD2345": true,
		"/s/ABCD2345":    true,
		"/join":          false,
		"/joined/x":      false,
		"/s":             false,
		"/settings/x":    false,
	} {
		if got := isPublicPage(sub); got != open {
			t.Errorf("isPublicPage(%q) = %v, want %v", sub, got, open)
		}
	}
}
