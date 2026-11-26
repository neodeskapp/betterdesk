package users_test

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/routetest"
)

// importBody is the JSON body of an import of the file.
func importBody(t *testing.T, csv, mode string, extra ...string) string {
	t.Helper()
	raw, err := json.Marshal(csv)
	if err != nil {
		t.Fatal(err)
	}
	body := `{"csv":` + string(raw) + `,"mode":"` + mode + `"`
	for _, e := range extra {
		body += "," + e
	}
	return body + "}"
}

// previewLines are the lines of a preview as "line:username:problems".
func previewLines(t *testing.T, got routetest.Answer) string {
	t.Helper()
	var out struct {
		Data struct {
			Rows []struct {
				Line     int      `json:"line"`
				Username string   `json:"username"`
				Problems []string `json:"problems"`
			} `json:"rows"`
			Ready int `json:"ready"`
		} `json:"data"`
	}
	if got.Status != http.StatusOK || json.Unmarshal([]byte(got.Body), &out) != nil {
		t.Fatalf("preview: %d %s", got.Status, got.Body)
	}
	var lines []string
	for _, r := range out.Data.Rows {
		lines = append(lines, strconv.Itoa(r.Line)+":"+r.Username+":"+strings.Join(r.Problems, " "))
	}
	return strings.Join(lines, "\n")
}

type imported struct {
	Line       int    `json:"line"`
	Username   string `json:"username"`
	Outcome    string `json:"outcome"`
	Error      string `json:"error"`
	InviteSent *bool  `json:"invite_sent"`
	Password   string `json:"password"`
	Warning    string `json:"warning"`
}

func importResults(t *testing.T, got routetest.Answer) []imported {
	t.Helper()
	var out struct {
		Data struct {
			Results []imported `json:"results"`
		} `json:"data"`
	}
	if got.Status != http.StatusOK || json.Unmarshal([]byte(got.Body), &out) != nil {
		t.Fatalf("import: %d %s", got.Status, got.Body)
	}
	return out.Data.Results
}

const importFile = "username;email;first name;last name;role;organization;language\r\n" +
	"anna;anna@example.test;Anna;Ade;operator;;de\r\n" +
	"carol;c2@example.test;;;;;\r\n" +
	"bob;Carol@Example.test;;;viewer;;\r\n" +
	"Anna;a2@example.test;;;;;\r\n" +
	"eve;eve@example.test;;;boss;;\r\n" +
	"fred;;;;;;\r\n" +
	"gina;gina@example.test;;;viewer;;xx\r\n" +
	"\"h a\";h@example.test;;;;;\r\n" +
	"ida;ida@example.test;;;super admin;;\r\n"

// The preview marks what is wrong with each line and writes nothing; the
// import makes the accounts of the good lines with invites in their own
// language, refuses the others with the reasons, and audits each account
// it made.
func TestImportWithInvites(t *testing.T) {
	w, box := mailWorld(t, true)
	admin := w.As("admin")
	makeAccount(t, w, admin, "carol", "carol@example.test")
	want := strings.Join([]string{
		"2:anna:",
		"3:carol:An account with this username exists.",
		"4:bob:An account with this email address exists.",
		"5:Anna:This username is on line 2 too.",
		"6:eve:There is no role \"boss\".",
		"7:fred:An invite needs an email address.",
		"8:gina:The console has no language \"xx\".",
		"9:h a:A username has 3 to 64 characters: letters, numbers and . _ @ - only.",
		"10:ida:",
	}, "\n")
	if got := previewLines(t, admin.Do("POST", "/api/users/import/preview", importBody(t, importFile, "invite"))); got != want {
		t.Errorf("preview:\n%s\nwant:\n%s", got, want)
	}
	if countAccounts(t, w, "anna") != 0 || box.count() != 0 {
		t.Fatal("the preview wrote")
	}

	results := importResults(t, admin.Do("POST", "/api/users/import", importBody(t, importFile, "invite"), "Accept-Language", "en"))
	var outcomes []string
	for _, r := range results {
		outcomes = append(outcomes, r.Username+":"+r.Outcome)
	}
	if got := strings.Join(outcomes, " "); got != "anna:created carol:refused bob:refused Anna:refused eve:refused fred:refused gina:refused h a:refused ida:created" {
		t.Errorf("outcomes: %s", got)
	}
	if r := results[0]; r.InviteSent == nil || !*r.InviteSent || r.Password != "" {
		t.Errorf("anna: %+v", r)
	}
	if got := text(t, w, `SELECT role || '|' || first_name || '|' || last_name || '|' || preferred_language FROM users WHERE username = 'anna'`); got != "operator|Anna|Ade|de" {
		t.Errorf("anna's row: %s", got)
	}
	if got := text(t, w, `SELECT role FROM users WHERE username = 'ida'`); got != "super_admin" {
		t.Errorf("ida's role: %s", got)
	}
	mails := mailsTo(box, "anna@example.test", 0)
	wantSubject := strings.NewReplacer("{sender}", "e2e_admin", "{server}", "NeoDesk").Replace(langText(t, "de", "password_link.invite_subject"))
	if len(mails) != 1 || mails[0].Subject != wantSubject {
		t.Errorf("anna's invite: %v, want the German %q", subjects(mails), wantSubject)
	}
	id := text(t, w, `SELECT id FROM users WHERE username = 'anna'`)
	rows := strings.Join(auditRowsAbout(t, w, id), "\n")
	if !strings.Contains(rows, "user_created|Created user: anna (operator)|1") || !strings.Contains(rows, "user_invite_sent|Sent an invite to user: anna|1") {
		t.Errorf("audit:\n%s", rows)
	}

	// Done again, every line is refused: the accounts exist now
	again := importResults(t, admin.Do("POST", "/api/users/import", importBody(t, importFile, "invite")))
	if again[0].Outcome != "refused" || again[0].Error != "An account with this username exists. An account with this email address exists." {
		t.Errorf("again: %+v", again[0])
	}
}

// Without email settings an import with invites is refused; with
// temporary passwords each account gets one, answered once, to be changed
// at the first sign-in; with none the accounts can't sign in yet. Only
// the lines asked for are imported.
func TestImportWithoutMail(t *testing.T) {
	w, _ := mailWorld(t, false)
	admin := w.As("admin")
	file := "username,email\nkim,kim@example.test\nlea,\nmax,max@example.test\n"
	expect(t, "invites without mail", admin.Do("POST", "/api/users/import", importBody(t, file, "invite")), 409, "")
	if got := previewLines(t, admin.Do("POST", "/api/users/import/preview", importBody(t, file, "temporary"))); got != "2:kim:\n3:lea:\n4:max:" {
		t.Errorf("preview: %s", got)
	}
	results := importResults(t, admin.Do("POST", "/api/users/import", importBody(t, file, "temporary", `"lines":[2,3]`)))
	if len(results) != 3 || results[0].Outcome != "created" || results[1].Outcome != "created" || results[2].Outcome != "skipped" {
		t.Fatalf("results: %+v", results)
	}
	if countAccounts(t, w, "max") != 0 {
		t.Error("a line not asked for was imported")
	}
	password := results[0].Password
	if !temporaryForm.MatchString(password) {
		t.Fatalf("temporary password %q", password)
	}
	if got := signIn(w, "kim", password); got.Body != `{"success":true,"passwordChangeRequired":true}` {
		t.Errorf("kim's first sign-in: %s", got.Body)
	}

	none := importResults(t, admin.Do("POST", "/api/users/import", importBody(t, "username\nnils\n", "none")))
	if none[0].Outcome != "created" || none[0].Password != "" {
		t.Errorf("none: %+v", none[0])
	}
}

// The role rules of creating one account: a global admin can't give a
// Super admin role, and someone without user.create can't import at all.
func TestImportFollowsTheRoleRules(t *testing.T) {
	w, _ := mailWorld(t, false)
	file := "username,role\nolga,operator\npaul,super_admin\n"
	got := previewLines(t, w.As("global_admin").Do("POST", "/api/users/import/preview", importBody(t, file, "none")))
	if got != "2:olga:\n3:paul:You can't give the role \"super_admin\"." {
		t.Errorf("preview: %s", got)
	}
	results := importResults(t, w.As("global_admin").Do("POST", "/api/users/import", importBody(t, file, "none")))
	if results[0].Outcome != "created" || results[1].Outcome != "refused" || countAccounts(t, w, "paul") != 0 {
		t.Errorf("results: %+v", results)
	}
	expect(t, "operator", w.As("operator").Do("POST", "/api/users/import/preview", importBody(t, file, "none")), 403, "")
}

// The limits of a file: 1,000 accounts and 1 MB; and a file without a
// username column is refused with what to do.
func TestImportLimits(t *testing.T) {
	w, _ := mailWorld(t, false)
	admin := w.As("admin")
	var b strings.Builder
	b.WriteString("username\n")
	for i := 0; i <= 1000; i++ {
		b.WriteString("user" + strconv.Itoa(i) + "\n")
	}
	expect(t, "1001 rows", admin.Do("POST", "/api/users/import/preview", importBody(t, b.String(), "none")), 400,
		`{"success":false,"error":"The file has more than 1000 accounts. Split it into smaller files."}`)
	big := "username,note\nanna," + strings.Repeat("x", 1<<20) + "\n"
	expect(t, "over 1 MB", admin.Do("POST", "/api/users/import/preview", importBody(t, big, "none")), 400,
		`{"success":false,"error":"The file is larger than 1 MB. Split it into smaller files."}`)
	expect(t, "no username column", admin.Do("POST", "/api/users/import/preview", importBody(t, "email\na@x.test\n", "none")), 400,
		`{"success":false,"error":"The first line must name the columns, with one called username. Start from the template."}`)
	expect(t, "no file", admin.Do("POST", "/api/users/import/preview", `{"mode":"none"}`), 400, "")
	expect(t, "a mode it doesn't know", admin.Do("POST", "/api/users/import/preview", importBody(t, "username\nx\n", "later")), 400, "")
}

// Organizations by name: an unknown one is a problem of its line, a known
// one is where the new account goes; without org.manage_users a line with
// an organization is refused.
func TestImportIntoOrganizations(t *testing.T) {
	api := routetest.NewAPI()
	w := routetest.New(t, routetest.Options{API: api})
	admin := w.As("admin")
	api.On("GET /api/org", 200, `{"organizations":[{"id":"o1","name":"Sales","slug":"sales"}]}`)
	api.Fallback = func(c routetest.Call) routetest.Reply {
		if c.Method == "POST" && strings.HasSuffix(c.Target, "/organizations") {
			return routetest.Reply{Status: 201, Body: `{}`}
		}
		return routetest.Reply{Status: 404, Body: `{"error":"not found"}`}
	}
	file := "username,organization\nquinn,sales\nrita,Marketing\n"
	got := previewLines(t, admin.Do("POST", "/api/users/import/preview", importBody(t, file, "none")))
	if !strings.HasPrefix(got, "2:quinn:\n3:rita:") || strings.HasSuffix(got, "3:rita:") {
		t.Errorf("preview: %s", got)
	}
	api.Calls()
	results := importResults(t, admin.Do("POST", "/api/users/import", importBody(t, file, "none")))
	if results[0].Outcome != "created" || results[0].Warning != "" || results[1].Outcome != "refused" {
		t.Errorf("results: %+v", results)
	}
	id := text(t, w, `SELECT id FROM users WHERE username = 'quinn'`)
	if calls := strings.Join(api.Calls(), "\n"); !strings.Contains(calls, "POST /api/users/"+id+`/organizations {"org_id":"o1"}`) {
		t.Errorf("calls:\n%s", calls)
	}
	without := routetest.New(t, routetest.Options{API: api, Roles: routetest.Overrides{"global_admin": {"org.manage_users": false}}})
	if got := previewLines(t, without.As("global_admin").Do("POST", "/api/users/import/preview", importBody(t, "username,organization\nsven,Sales\n", "none"))); got != "2:sven:users.import_org_permission" {
		t.Errorf("without org.manage_users: %s", got)
	}
}
