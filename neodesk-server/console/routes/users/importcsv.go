package users

import (
	"encoding/csv"
	"errors"
	"io"
	"strings"
	"unicode"
)

// Reading the CSV file of an import. The first line names the columns;
// every other line that is not empty is an account. What the reader
// takes: fields in double quotes with commas, semicolons, quotes ("") and
// line breaks in them, a UTF-8 byte order mark, a comma, a semicolon (as
// Excel writes in German and other languages) or a tab between fields, and
// CRLF or LF line ends.

// The most an import takes.
const (
	maxImportBytes = 1 << 20
	maxImportRows  = 1000
	// maxImportName is the most characters of a first or last name.
	maxImportName = 100
)

// importFields are the columns of the template, in its order.
var importFields = []string{"username", "email", "first_name", "last_name", "role", "organization", "language"}

// importHeaders maps a column's name, in lower case without spaces, dots,
// dashes and underscores, to its field. The English names of the template
// and the German ones.
var importHeaders = map[string]string{
	"username": "username", "user": "username", "login": "username", "benutzername": "username", "benutzer": "username",
	"email": "email", "mail": "email", "emailaddress": "email", "emailadresse": "email",
	"firstname": "first_name", "givenname": "first_name", "vorname": "first_name",
	"lastname": "last_name", "surname": "last_name", "familyname": "last_name", "nachname": "last_name",
	"role": "role", "rolle": "role",
	"organization": "organization", "organisation": "organization", "org": "organization",
	"language": "language", "lang": "language", "sprache": "language",
}

// headerKey is a column's name as importHeaders has it.
func headerKey(name string) string {
	var b strings.Builder
	for _, c := range strings.ToLower(strings.TrimSpace(name)) {
		if unicode.IsSpace(c) || c == '.' || c == '-' || c == '_' {
			continue
		}
		b.WriteRune(c)
	}
	return b.String()
}

// importRow is one account of the file: its line (the first line of the
// file is 1, the column names), its fields as written, and what is wrong
// with it.
type importRow struct {
	Line         int      `json:"line"`
	Username     string   `json:"username"`
	Email        string   `json:"email"`
	FirstName    string   `json:"first_name"`
	LastName     string   `json:"last_name"`
	Role         string   `json:"role"`
	Organization string   `json:"organization"`
	Language     string   `json:"language"`
	Problems     []string `json:"problems"`
}

// set gives the row the value of a field.
func (row *importRow) set(field, value string) {
	value = strings.TrimSpace(value)
	switch field {
	case "username":
		row.Username = value
	case "email":
		row.Email = value
	case "first_name":
		row.FirstName = value
	case "last_name":
		row.LastName = value
	case "role":
		row.Role = value
	case "organization":
		row.Organization = value
	case "language":
		row.Language = value
	}
}

// csvProblem is why a file can't be read: a text key and its values.
type csvProblem struct {
	key  string
	vars []any
}

func (p *csvProblem) Error() string { return p.key }

// separatorOf is the separator of the file: the one of comma, semicolon
// and tab that the first line has most of outside quotes, a comma when it
// has none.
func separatorOf(text string) rune {
	counts := map[rune]int{}
	quoted := false
	for _, c := range text {
		if c == '"' {
			quoted = !quoted
			continue
		}
		if quoted {
			continue
		}
		if c == '\n' || c == '\r' {
			break
		}
		if c == ',' || c == ';' || c == '\t' {
			counts[c]++
		}
	}
	best := ','
	for _, c := range []rune{';', '\t'} {
		if counts[c] > counts[best] {
			best = c
		}
	}
	return best
}

// parseImport reads the file. ignored are the names of the columns the
// import doesn't know.
func parseImport(text string) (rows []importRow, ignored []string, err error) {
	if len(text) > maxImportBytes {
		return nil, nil, &csvProblem{key: "users.import_too_large", vars: []any{"size", "1 MB"}}
	}
	text = strings.TrimPrefix(text, "\uFEFF")
	reader := csv.NewReader(strings.NewReader(text))
	reader.Comma = separatorOf(text)
	reader.FieldsPerRecord = -1
	header, err := reader.Read()
	if errors.Is(err, io.EOF) {
		return nil, nil, &csvProblem{key: "users.import_empty"}
	}
	if err != nil {
		return nil, nil, unreadable(err)
	}
	fields := make([]string, len(header))
	hasUsername := false
	for i, name := range header {
		field := importHeaders[headerKey(name)]
		if field != "" && !contains(fields[:i], field) {
			fields[i] = field
			hasUsername = hasUsername || field == "username"
		} else if strings.TrimSpace(name) != "" {
			ignored = append(ignored, strings.TrimSpace(name))
		}
	}
	if !hasUsername {
		return nil, nil, &csvProblem{key: "users.import_no_username"}
	}
	rows = []importRow{}
	for {
		record, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, nil, unreadable(err)
		}
		line, _ := reader.FieldPos(0)
		row := importRow{Line: line, Problems: []string{}}
		blank := true
		for i, value := range record {
			if strings.TrimSpace(value) != "" {
				blank = false
			}
			if i < len(fields) && fields[i] != "" {
				row.set(fields[i], value)
			}
		}
		if blank {
			continue
		}
		if len(rows) == maxImportRows {
			return nil, nil, &csvProblem{key: "users.import_too_many_rows", vars: []any{"max", maxImportRows}}
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return nil, nil, &csvProblem{key: "users.import_empty"}
	}
	return rows, ignored, nil
}

// unreadable is the problem of a file the CSV reader stopped at.
func unreadable(err error) error {
	var parse *csv.ParseError
	if errors.As(err, &parse) {
		return &csvProblem{key: "users.import_bad_csv", vars: []any{"line", parse.StartLine}}
	}
	return &csvProblem{key: "users.import_bad_csv", vars: []any{"line", 1}}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
