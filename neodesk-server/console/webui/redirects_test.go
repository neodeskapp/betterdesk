package webui

import (
	"path/filepath"
	"testing"
)

// The build's redirects.json (web-ui/src/lib/redirects.ts) sends old
// console addresses on to their new place before the page is served, with
// the other parameters of the address kept.
func TestOldConsoleAddressesRedirect(t *testing.T) {
	bases(t, func(t *testing.T, h *Host, base, dir string) {
		writeFile(t, filepath.Join(dir, "redirects.json"), `[
			{"from":"/settings","hash":["account-password"],"to":"/account#password"},
			{"from":"/settings","tab":"backup","to":"/server/backups"},
			{"from":"/settings","tab":"auth","to":"/settings?tab=ldap#ldap-host"},
			{"from":"/commercialization","to":"/billing"},
			{"from":"/client-branding","to":"/settings?tab=brand#client-app"},
			{"from":"/api-tokens","to":"/account#api-tokens"}
		]`)
		h = New(Options{Dir: dir, Production: true, Session: sessionFromHeader})
		for from, to := range map[string]string{
			"/settings?tab=backup":              "/server/backups",
			"/settings?tab=BACKUP&guide=backup": "/server/backups?guide=backup",
			"/Settings?tab=backup":              "/server/backups",
			"/settings?guide=x&tab=auth":        "/settings?tab=ldap&guide=x#ldap-host",
			"/commercialization?tab=sessions":   "/billing?tab=sessions",
			"/commercialization":                "/billing",
			"/client-branding":                  "/settings?tab=brand#client-app",
			"/api-tokens":                       "/account#api-tokens",
		} {
			wantRedirect(t, serve(h, "GET", base+from, admin), from, 302, base+to)
			// Signed out too: the sign-in page then has the new address
			wantRedirect(t, serve(h, "GET", base+from, nil), from+" signed out", 302, base+to)
		}
		// Another tab, or no tab, stays where it is. A rule that needs a #id
		// is the app's alone: the server never sees the #id
		wantPage(t, serve(h, "GET", base+"/settings?tab=email", admin), "another tab", 200)
		wantPage(t, serve(h, "GET", base+"/settings", admin), "no tab", 200)
		// Writes are not redirected
		wantDeclined(t, serve(h, "POST", base+"/settings?tab=backup", admin), "POST")
	})
}

// A build without redirects.json, or with one that is not a list, sends
// nobody anywhere.
func TestWithoutRedirectListNothingMoves(t *testing.T) {
	h, dir := testHost(t, "")
	wantPage(t, serve(h, "GET", "/settings?tab=backup", admin), "no list", 200)
	writeFile(t, filepath.Join(dir, "redirects.json"), `{"from":"/settings"}`)
	h = New(Options{Dir: dir, Production: true, Session: sessionFromHeader})
	wantPage(t, serve(h, "GET", "/settings?tab=backup", admin), "a list that is not one", 200)
}
