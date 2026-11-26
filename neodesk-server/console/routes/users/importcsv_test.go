package users

import (
	"errors"
	"strconv"
	"strings"
	"testing"
)

// rowsOf reads a file and writes its rows as "line:username/email/first/last/role/org/lang".
func rowsOf(t *testing.T, text string) string {
	t.Helper()
	rows, _, err := parseImport(text)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var out []string
	for _, r := range rows {
		out = append(out, strings.Join([]string{strconv.Itoa(r.Line) + ":" + r.Username, r.Email, r.FirstName, r.LastName, r.Role, r.Organization, r.Language}, "/"))
	}
	return strings.Join(out, " | ")
}

// The reader takes what spreadsheets write: commas or semicolons, CRLF, a
// byte order mark, quoted fields with separators, quotes and line breaks
// in them; it skips empty lines, knows the German column names and leaves
// out columns it doesn't know.
func TestParseImport(t *testing.T) {
	cases := []struct{ name, text, want string }{
		{"commas", "username,email,first name,last name,role,organization,language\nanna,anna@x.test,Anna,Ade,operator,Sales,de\n",
			"2:anna/anna@x.test/Anna/Ade/operator/Sales/de"},
		{"semicolons, CRLF and a byte order mark", "\uFEFFBenutzername;E-Mail;Vorname;Nachname;Rolle\r\nbernd;b@x.test;Bernd;\"Bauer; Sohn\";viewer\r\n",
			"2:bernd/b@x.test/Bernd/Bauer; Sohn/viewer//"},
		{"quotes and a line break in a field", "username,last_name\n\"carl\",\"O\"\"Neil\nthe second\"\ndora,Dorn\n",
			"2:carl///O\"Neil\nthe second/// | 4:dora///Dorn///"},
		{"empty lines and spaces", "Username , Email\n\n  erik , e@x.test \n,\n",
			"3:erik/e@x.test/////"},
		{"a tab between fields", "username\temail\nfrida\tf@x.test\n", "2:frida/f@x.test/////"},
	}
	for _, c := range cases {
		if got := rowsOf(t, c.text); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
	_, ignored, err := parseImport("username,phone,team\nanna,1,x\n")
	if err != nil || strings.Join(ignored, ",") != "phone,team" {
		t.Errorf("ignored columns: %v %v", ignored, err)
	}
}

// What the reader refuses, with the reason: no username column, nothing
// but the column names, a broken quote, too many rows, too many bytes.
func TestParseImportRefuses(t *testing.T) {
	key := func(text string) string {
		_, _, err := parseImport(text)
		var p *csvProblem
		if !errors.As(err, &p) {
			return "no problem"
		}
		return p.key
	}
	if k := key("email,role\na@x.test,viewer\n"); k != "users.import_no_username" {
		t.Errorf("no username column: %s", k)
	}
	if k := key("username\n\n"); k != "users.import_empty" {
		t.Errorf("no accounts: %s", k)
	}
	if k := key(""); k != "users.import_empty" {
		t.Errorf("empty file: %s", k)
	}
	if k := key("username\n\"anna\n"); k != "users.import_bad_csv" {
		t.Errorf("broken quote: %s", k)
	}
	many := "username\n" + strings.Repeat("x\n", maxImportRows)
	if k := key(many); k != "no problem" {
		t.Errorf("%d rows: %s", maxImportRows, k)
	}
	if k := key(many + "y\n"); k != "users.import_too_many_rows" {
		t.Errorf("%d rows: %s", maxImportRows+1, k)
	}
	if k := key("username\n" + strings.Repeat("a", maxImportBytes)); k != "users.import_too_large" {
		t.Errorf("too large: %s", k)
	}
}
