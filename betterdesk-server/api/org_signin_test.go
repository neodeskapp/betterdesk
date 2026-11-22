package api

import (
	"encoding/json"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/auth"
	"github.com/unitronix/betterdesk-server/db"
)

// orgSignIn signs in at /api/org/login and returns the status and the
// token.
func orgSignIn(t *testing.T, srv *Server, org, username, password string) (int, string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"org_id": org, "username": username, "password": password})
	status, answer := listenerCall(t, srv, http.MethodPost, "/api/org/login", string(body), nil)
	var out struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal([]byte(answer), &out)
	return status, out.Token
}

// An organization account's password follows the server's rule: 8 to 128
// characters, checked when the account is made and when its password
// changes, which now changes it. Passwords are stored the server's way, so
// a long one works too, and an account stored with an older hash still
// signs in (BUGS L120).
func TestOrganizationAccountPasswordRule(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	srv.SetJWTManager(auth.NewJWTManager("org-password-test-secret", time.Hour))
	if err := database.CreateOrganization(&db.Organization{ID: "org-p", Name: "Passwords", Slug: "passwords", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateOrgUser(&db.OrgUser{ID: "ou-bcrypt", OrgID: "org-p", Username: "older", Role: db.OrgRoleUser, PasswordHash: mustHash(t, "older-pass-1"), CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}

	for i, c := range []struct {
		password string
		want     int
	}{
		{"", http.StatusBadRequest},
		{"seven77", http.StatusBadRequest},
		{strings.Repeat("x", 129), http.StatusBadRequest},
		{"eight888", http.StatusCreated},
	} {
		body, _ := json.Marshal(map[string]string{"username": "person-" + strconv.Itoa(i), "password": c.password})
		if status, answer := asConsole(t, srv, http.MethodPost, "/api/org/org-p/users", string(body)); status != c.want {
			t.Errorf("a password of %d characters: %d %s, want %d", len(c.password), status, answer, c.want)
		}
	}
	long := strings.Repeat("long-password-", 10)[:128]
	status, answer := asConsole(t, srv, http.MethodPost, "/api/org/org-p/users", `{"username":"longpass","password":"`+long+`"}`)
	if status != http.StatusCreated {
		t.Fatalf("a password of 128 characters: %d %s", status, answer)
	}
	var created struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal([]byte(answer), &created)
	if status, _ := orgSignIn(t, srv, "org-p", "longpass", long); status != http.StatusOK {
		t.Errorf("sign-in with the long password: %d", status)
	}
	if status, _ := orgSignIn(t, srv, "org-p", "older", "older-pass-1"); status != http.StatusOK {
		t.Errorf("sign-in of an account with an older hash: %d", status)
	}

	if status, answer := asConsole(t, srv, http.MethodPut, "/api/org/org-p/users/"+created.ID, `{"password":"short"}`); status != http.StatusBadRequest {
		t.Errorf("a short new password: %d %s, want 400", status, answer)
	}
	if status, _ := orgSignIn(t, srv, "org-p", "longpass", long); status != http.StatusOK {
		t.Errorf("a refused new password changed the password: sign-in %d", status)
	}
	if status, answer := asConsole(t, srv, http.MethodPut, "/api/org/org-p/users/"+created.ID, `{"password":"a-new-password"}`); status != http.StatusOK {
		t.Fatalf("a new password: %d %s", status, answer)
	}
	if status, _ := orgSignIn(t, srv, "org-p", "longpass", "a-new-password"); status != http.StatusOK {
		t.Errorf("sign-in with the new password: %d", status)
	}
	if status, _ := orgSignIn(t, srv, "org-p", "longpass", long); status != http.StatusUnauthorized {
		t.Errorf("sign-in with the old password after the change: %d, want 401", status)
	}
}

// getRoutes lists the GET patterns of the API server's route table, read
// from server.go.
func getRoutes(t *testing.T) []string {
	t.Helper()
	src, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, m := range regexp.MustCompile(`mux\.HandleFunc\("GET (/[^"]*)"`).FindAllStringSubmatch(string(src), -1) {
		out = append(out, m[1])
	}
	if len(out) < 100 {
		t.Fatalf("read %d GET routes from server.go", len(out))
	}
	return out
}

// orgTokenReads are the GET routes that answer 2xx to an organization
// sign-in (POST /api/org/login) of an account with the default role, with
// its own organization in the path. What each gives such an account:
var orgTokenReads = []string{
	// its organization: id, name, slug, logo address, created
	"/api/org",
	"/api/org/{id}",
	// the shared address book and whether it is shared. Members, devices,
	// settings, stored passwords and policies are refused (A169).
	"/api/org/{id}/address-book",
	// the RustDesk client's account routes: its name, its address book
	// with the shared one merged in (host name and platform of each
	// contact), its personal book and tags; groups and peer lists answer
	// empty, because the account holds no server permission
	"/api/currentUser",
	"/api/ab",
	"/api/ab/personal",
	"/api/ab/tags",
	"/api/group",
	"/api/group/get",
	"/api/device-group",
	"/api/device-group/accessible",
	"/api/peers/list",
	"/api/user/group",
	// what the server tells every client: health, sign-in options,
	// branding, its key, software links, the permission catalog
	"/api/health",
	"/api/login-options",
	"/api/auth/oidc/status",
	"/api/auth/sso/status",
	"/api/branding",
	"/api/server-key",
	"/api/server-key/fingerprint",
	"/api/software",
	"/api/software/client-download-link",
	"/api/peer-key/{id}",
	"/api/panel/catalog",
}

// TestWhatAnOrganizationSignInReads walks every GET route of the API
// server with the token of an organization account and pins the ones
// that answer: a route that starts to answer such a token fails here
// (BUGS L120, for the docs; DECISIONS A169).
func TestWhatAnOrganizationSignInReads(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	srv.SetJWTManager(auth.NewJWTManager("org-reach-test-secret", time.Hour))
	if err := database.CreateOrganization(&db.Organization{ID: "org-a", Name: "Acme", Slug: "acme", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateOrgUser(&db.OrgUser{ID: "ou-1", OrgID: "org-a", Username: "member", Role: db.OrgRoleUser, PasswordHash: mustHash(t, "member-pass-1"), CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := database.AssignDeviceToOrg(&db.OrgDevice{OrgID: "org-a", DeviceID: "PEERAAA1"}); err != nil {
		t.Fatal(err)
	}
	status, token := orgSignIn(t, srv, "org-a", "member", "member-pass-1")
	if status != http.StatusOK || token == "" {
		t.Fatalf("org sign-in: %d", status)
	}
	param := regexp.MustCompile(`\{([A-Za-z_]+)(\.\.\.)?\}`)
	var answered []string
	for _, pattern := range getRoutes(t) {
		path := param.ReplaceAllStringFunc(pattern, func(m string) string {
			switch name := param.FindStringSubmatch(m)[1]; name {
			case "id", "orgId":
				return "org-a"
			default:
				return "PEERAAA1"
			}
		})
		if strings.HasSuffix(path, "/") {
			path += "x"
		}
		if status, _ := listenerCall(t, srv, http.MethodGet, path, "", bearer(token)); status >= 200 && status < 300 {
			answered = append(answered, pattern)
		}
	}
	sort.Strings(answered)
	want := append([]string(nil), orgTokenReads...)
	sort.Strings(want)
	if strings.Join(answered, "\n") != strings.Join(want, "\n") {
		t.Errorf("GET routes that answer an organization sign-in:\n%s\nwant\n%s", strings.Join(answered, "\n"), strings.Join(want, "\n"))
	}
}
