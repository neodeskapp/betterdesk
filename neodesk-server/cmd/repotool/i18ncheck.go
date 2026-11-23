package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// i18nCheck is "i18n-check": it audits the translation files against the
// keys of en.json and pl.json together (the baseline). A language that
// misses a baseline key, a file that does not parse, an empty value, or
// en.json and pl.json disagreeing on their keys fails the check. A value
// that is still the English text is reported as unfinished translation
// work and does not fail it. The command never writes: filling missing
// keys with English would hide untranslated text behind full coverage.
func i18nCheck(root string, args []string, stdout, stderr io.Writer) int {
	jsonMode := slices.Contains(args, "--json")
	verbose := slices.Contains(args, "--verbose")
	requested := ""
	if i := slices.Index(args, "--system"); i >= 0 && i+1 < len(args) {
		requested = args[i+1]
	}
	if slices.Contains(args, "--fix") {
		fmt.Fprintln(stderr, "Automatic i18n --fix is disabled. Missing keys must be translated in the target language, not filled with English fallback text.")
		return 2
	}
	if slices.Contains(args, "--help") || slices.Contains(args, "-h") {
		fmt.Fprint(stdout, i18nHelp)
		return 0
	}

	systems := i18nSystems
	if requested != "" {
		systems = nil
		for _, s := range i18nSystems {
			if s.ID == requested {
				systems = append(systems, s)
			}
		}
		if len(systems) == 0 {
			ids := make([]string, len(i18nSystems))
			for i, s := range i18nSystems {
				ids[i] = s.ID
			}
			fmt.Fprintf(stderr, "Unknown i18n system: %s\n", requested)
			fmt.Fprintf(stderr, "Known systems: %s\n", strings.Join(ids, ", "))
			return 2
		}
	}

	results := make([]i18nResult, 0, len(systems))
	for _, s := range systems {
		r, err := auditSystem(root, s)
		if err != nil {
			fmt.Fprintf(stderr, "i18n audit failed: %v\n", err)
			return 1
		}
		results = append(results, r)
	}

	if jsonMode {
		enc := json.NewEncoder(stdout)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		if err := enc.Encode(i18nJSON(results)); err != nil {
			fmt.Fprintf(stderr, "i18n audit failed: %v\n", err)
			return 1
		}
	} else {
		printI18nHuman(stdout, results, verbose)
	}

	if hasBlockingFailures(results) {
		if !jsonMode {
			fmt.Fprintln(stdout, "i18n audit failed: every active locale must match the EN+PL key baseline, parse cleanly, and contain no empty values.")
		}
		return 1
	}
	soft := 0
	for _, r := range results {
		for _, row := range r.Rows {
			soft += len(row.EnglishFallback)
		}
	}
	if jsonMode {
		return 0
	}
	if soft > 0 {
		fmt.Fprintf(stdout, "i18n audit passed with %d untranslated string(s) still falling back to English.\n", soft)
		fmt.Fprintln(stdout, "These are tracked translation work, not build failures. Run with --verbose for details.")
	} else {
		fmt.Fprintln(stdout, "i18n audit passed: no parity, empty, or fallback findings.")
	}
	return 0
}

const i18nHelp = `NeoDesk i18n audit

Usage:
  go run ./cmd/repotool i18n-check [--system console|agent-client|mgmt] [--json] [--verbose]

Checks:
  - EN and PL key parity
  - every locale has the same EN+PL key baseline
  - no empty values
  - no likely English fallback values in non-English locales

The command is read-only. It never inserts English values into target locales.
`

// i18nSystem is a set of translation files.
type i18nSystem struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Dir   string `json:"dir"`
}

var i18nSystems = []i18nSystem{
	{ID: "console", Label: "Web Console", Dir: "neodesk-server/console/static/files/lang"},
	{ID: "agent-client", Label: "Agent Client", Dir: "neodesk-agent-client/src/locales"},
	{ID: "mgmt", Label: "MGMT Client", Dir: "neodesk-mgmt/src/locales"},
}

var i18nReferenceFiles = []string{"en.json", "pl.json"}

// i18nResult is the audit of one system. A system whose folder is
// missing is skipped.
type i18nResult struct {
	i18nSystem
	Skipped       bool
	Reason        string
	Files         int
	BaselineCount int
	EnCount       int
	PlCount       int
	EnMissingVsPL []string
	PlMissingVsEN []string
	Rows          []i18nRow
}

// i18nRow is the audit of one translation file.
type i18nRow struct {
	File            string
	ParseError      string
	Keys            int
	Missing         []string
	Extra           []string
	IgnoredExtra    []string
	Empty           []string
	EnglishFallback []string
}

// i18nJSON is what --json writes for the results: the fields in the
// order of the report, empty lists as [].
func i18nJSON(results []i18nResult) []any {
	type skipped struct {
		i18nSystem
		Skipped bool   `json:"skipped"`
		Reason  string `json:"reason"`
	}
	type row struct {
		File            string   `json:"file"`
		ParseError      string   `json:"parseError,omitempty"`
		Keys            int      `json:"keys"`
		Missing         []string `json:"missing"`
		Extra           []string `json:"extra"`
		IgnoredExtra    []string `json:"ignoredExtra"`
		Empty           []string `json:"empty"`
		EnglishFallback []string `json:"englishFallback"`
	}
	type audited struct {
		i18nSystem
		Files         int      `json:"files"`
		BaselineCount int      `json:"baselineCount"`
		EnCount       int      `json:"enCount"`
		PlCount       int      `json:"plCount"`
		EnMissingVsPL []string `json:"enMissingVsPL"`
		PlMissingVsEN []string `json:"plMissingVsEN"`
		Rows          []row    `json:"rows"`
	}
	out := make([]any, 0, len(results))
	for _, r := range results {
		if r.Skipped {
			out = append(out, skipped{r.i18nSystem, true, r.Reason})
			continue
		}
		rows := make([]row, 0, len(r.Rows))
		for _, x := range r.Rows {
			rows = append(rows, row{x.File, x.ParseError, x.Keys, nonNil(x.Missing), nonNil(x.Extra),
				nonNil(x.IgnoredExtra), nonNil(x.Empty), nonNil(x.EnglishFallback)})
		}
		out = append(out, audited{r.i18nSystem, r.Files, r.BaselineCount, r.EnCount, r.PlCount,
			nonNil(r.EnMissingVsPL), nonNil(r.PlMissingVsEN), rows})
	}
	return out
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func (r i18nRow) status() string {
	switch {
	case r.ParseError != "":
		return "PARSE_ERROR"
	case len(r.Missing) > 0 || len(r.Extra) > 0 || len(r.Empty) > 0 || len(r.EnglishFallback) > 0:
		return "FAIL"
	}
	return "OK"
}

// flatKeys is a translation file's leaves by dotted key, in order.
type flatKeys struct {
	keys   []string
	values map[string]string
}

func (f *flatKeys) has(key string) bool { _, ok := f.values[key]; return ok }

// flattenKeys turns nested objects into dotted keys; arrays and other
// values are leaves, as their text.
func flattenKeys(value any, prefix string, out *flatKeys) {
	var keys []string
	var get func(string) any
	switch t := value.(type) {
	case *jsObject:
		keys, get = t.keys, func(k string) any { return t.values[k] }
	case []any:
		keys = jsKeys(t)
		get = func(k string) any { i, _ := strconv.Atoi(k); return t[i] }
	default:
		return
	}
	for _, k := range keys {
		full := k
		if prefix != "" {
			full = prefix + "." + k
		}
		if obj, ok := get(k).(*jsObject); ok {
			flattenKeys(obj, full, out)
			continue
		}
		if _, ok := out.values[full]; !ok {
			out.keys = append(out.keys, full)
		}
		out.values[full] = jsString(get(k))
	}
}

func readFlatKeys(path string) (*flatKeys, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%s", readError(path, err))
	}
	text := strings.TrimPrefix(string(data), string(rune(0xFEFF)))
	value, err := parseJSON([]byte(text))
	if err != nil {
		return nil, err
	}
	out := &flatKeys{values: map[string]string{}}
	flattenKeys(value, "", out)
	return out, nil
}

func auditSystem(root string, s i18nSystem) (i18nResult, error) {
	dir := filepath.Join(root, filepath.FromSlash(s.Dir))
	if _, err := os.Stat(dir); err != nil {
		return i18nResult{i18nSystem: s, Skipped: true, Reason: "Directory not found: " + s.Dir}, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return i18nResult{}, err
	}
	var files []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			files = append(files, e.Name())
		}
	}
	slices.Sort(files)

	refs := map[string]*flatKeys{}
	for _, ref := range i18nReferenceFiles {
		path := filepath.Join(dir, ref)
		if _, err := os.Stat(path); err != nil {
			return i18nResult{}, fmt.Errorf("%s: missing reference file %s", s.ID, ref)
		}
		keys, err := readFlatKeys(path)
		if err != nil {
			return i18nResult{}, err
		}
		refs[ref] = keys
	}
	en, pl := refs["en.json"], refs["pl.json"]

	var baseline []string
	inBaseline := map[string]bool{}
	for _, k := range append(slices.Clone(en.keys), pl.keys...) {
		if !inBaseline[k] {
			inBaseline[k] = true
			baseline = append(baseline, k)
		}
	}
	var enMissing, plMissing []string
	for _, k := range pl.keys {
		if !en.has(k) {
			enMissing = append(enMissing, k)
		}
	}
	for _, k := range en.keys {
		if !pl.has(k) {
			plMissing = append(plMissing, k)
		}
	}

	rows := make([]i18nRow, 0, len(files))
	for _, file := range files {
		locale, err := readFlatKeys(filepath.Join(dir, file))
		if err != nil {
			rows = append(rows, i18nRow{File: file, ParseError: err.Error()})
			continue
		}
		row := i18nRow{File: file, Keys: len(locale.keys)}
		for _, k := range baseline {
			if !locale.has(k) {
				row.Missing = append(row.Missing, k)
			} else if jsTrim(locale.values[k]) == "" {
				row.Empty = append(row.Empty, k)
			}
		}
		for _, k := range locale.keys {
			if inBaseline[k] {
				continue
			}
			if strings.HasPrefix(k, "_meta.") {
				row.IgnoredExtra = append(row.IgnoredExtra, k)
			} else {
				row.Extra = append(row.Extra, k)
			}
		}
		if file != "en.json" {
			for _, k := range en.keys {
				if !locale.has(k) {
					continue
				}
				if v := locale.values[k]; v == en.values[k] && looksLikeEnglishFallback(k, v) {
					row.EnglishFallback = append(row.EnglishFallback, k)
				}
			}
		}
		rows = append(rows, row)
	}

	return i18nResult{
		i18nSystem:    s,
		Files:         len(files),
		BaselineCount: len(baseline),
		EnCount:       len(en.keys),
		PlCount:       len(pl.keys),
		EnMissingVsPL: enMissing,
		PlMissingVsEN: plMissing,
		Rows:          rows,
	}, nil
}

// Values a translation may share with English: names, abbreviations,
// product and protocol names.
var sharedExactValues = toSet(
	"", "OK", "ID", "API", "API Key", "API Keys", "Auto", "Automatic", "BETA", "Beta",
	"CDAP", "CPU", "DataGuard", "GPU", "HTTP", "HTTPS", "IoT", "JSON", "JWT", "LAN",
	"LDAP", "LDAPS (TLS)", "OIDC", "OAuth2", "RAM", "SCADA", "SDK", "SDK Studio", "SSO",
	"StartTLS", "TCP", "TLS", "TOTP", "UDP", "URL", "UTF-8", "WS", "WSS", "WebSocket",
	"Windows", "Linux", "macOS", "Node.js", "RustDesk", "NeoDesk", "NeoDesk MGMT",
	"Ctrl+Alt+Del", "Ctrl+K", "Wake on LAN", "OpenID Connect", "Active Directory",
	"Azure AD", "Okta", "Google", "Keycloak", "GitHub", "Docker", "PostgreSQL", "SQLite",
	"NSIS", "MSI", "AppImage",
)

var sharedShortTerms = toSet(
	"Admin", "Agent", "Audit", "Chat", "Desktop", "Error", "Generator", "Info", "Login",
	"Logo", "Media", "Model", "Offline", "Online", "Operator", "Pro", "Status", "System",
	"Terminal", "Toolkit",
)

var allowedIdenticalKeyPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(^|\.)(id|lang|direction|flag|code|version|name|native_name)$`),
	regexp.MustCompile(`(?i)(^|\.)(api_key|api_key_placeholder|api_key_hint|key_prefix|token|secret)$`),
	regexp.MustCompile(`(?i)(^|\.)(platform|protocol|port|host|hostname|os|cpu|gpu|ram|disk|memory)$`),
	regexp.MustCompile(`(?i)(^|\.)(ctrl_alt_del|totp_code|ldap_|oidc_|cdap_|sdk_)`),
	regexp.MustCompile(`(?i)(^|\.)(filter_type_rustdesk|filter_type_scada|filter_type_iot)$`),
}

var (
	reEnglishHint  = regexp.MustCompile(`(?i)\b(the|and|or|to|from|with|without|for|this|that|these|those|your|you|are|is|was|were|will|can|cannot|could|should|please|enter|select|search|loading|failed|successfully|required|available|enabled|disabled|device|devices|server|settings|management|dashboard|connection|connections|operator|operators|permission|permissions|password|username|language|translation|translations|configure|configuration|clipboard|screen|session|sessions|request|requests|history|metrics|automation|notification|notifications|delete|deleted|save|saved|create|created|update|updated|restart|shutdown|confirm|cancel|close|open|view|edit|back|next|finish|remote|toolbar|display|sync|ready|organization|organizations|member|members|policy|policies|group|groups|file|files|send|share|terminal|widget|widgets|overview|basics|tour|setup)\b`)
	reLetter       = regexp.MustCompile(`[A-Za-z]`)
	reShortCaps    = regexp.MustCompile(`^[A-Z0-9_./:+ -]{1,14}$`)
	reURL          = regexp.MustCompile(`(?i)^https?://`)
	rePlaceholder  = regexp.MustCompile(`^\{\{?[A-Za-z0-9_]+\}?\}$`)
	reFileName     = regexp.MustCompile(`(?i)^[A-Za-z0-9_.-]+\.(json|db|sqlite3|exe|sh|ps1|dll|msi|nsi|nsh)$`)
	reSpace        = regexp.MustCompile(jsSpace)
	reSentenceStop = regexp.MustCompile(`[.!?]$`)
)

func toSet(values ...string) map[string]bool {
	m := make(map[string]bool, len(values))
	for _, v := range values {
		m[v] = true
	}
	return m
}

func isAllowedIdentical(key, value string) bool {
	v := jsTrim(value)
	if !reLetter.MatchString(v) || sharedExactValues[v] || sharedShortTerms[v] {
		return true
	}
	if reShortCaps.MatchString(v) || reURL.MatchString(v) || rePlaceholder.MatchString(v) || reFileName.MatchString(v) {
		return true
	}
	for _, re := range allowedIdenticalKeyPatterns {
		if re.MatchString(key) {
			return true
		}
	}
	return false
}

func looksLikeEnglishFallback(key, value string) bool {
	v := jsTrim(value)
	if v == "" || !reLetter.MatchString(v) || isAllowedIdentical(key, v) {
		return false
	}
	if reEnglishHint.MatchString(v) {
		return true
	}
	if reSpace.MatchString(v) && jsLen(v) >= 12 {
		return true
	}
	return reSentenceStop.MatchString(v) && jsLen(v) >= 8
}

func hasBlockingFailures(results []i18nResult) bool {
	for _, r := range results {
		if r.Skipped {
			continue
		}
		if len(r.EnMissingVsPL) > 0 || len(r.PlMissingVsEN) > 0 {
			return true
		}
		for _, row := range r.Rows {
			if row.ParseError != "" || len(row.Missing) > 0 || len(row.Empty) > 0 {
				return true
			}
		}
	}
	return false
}

// listed is up to n keys joined, with " ..." when there are more.
func listed(keys []string, n int) string {
	if len(keys) > n {
		return strings.Join(keys[:n], ", ") + " ..."
	}
	return strings.Join(keys, ", ")
}

func printI18nHuman(w io.Writer, results []i18nResult, verbose bool) {
	fmt.Fprintln(w, "\nNeoDesk i18n audit")
	fmt.Fprintln(w, "Reference baseline: union of en.json and pl.json keys")
	fmt.Fprint(w, "English fallback check: exact EN values filtered through a technical-term allowlist\n\n")

	for _, r := range results {
		if r.Skipped {
			fmt.Fprintf(w, "=== %s (%s) ===\n", r.Label, r.ID)
			fmt.Fprintf(w, "Skipped: %s\n\n", r.Reason)
			continue
		}
		fmt.Fprintf(w, "=== %s (%s) ===\n", r.Label, r.Dir)
		fmt.Fprintf(w, "files=%d baseline=%d en=%d pl=%d en_missing_vs_pl=%d pl_missing_vs_en=%d\n",
			r.Files, r.BaselineCount, r.EnCount, r.PlCount, len(r.EnMissingVsPL), len(r.PlMissingVsEN))
		if len(r.EnMissingVsPL) > 0 {
			fmt.Fprintf(w, "  EN missing keys present in PL: %s\n", listed(r.EnMissingVsPL, 20))
		}
		if len(r.PlMissingVsEN) > 0 {
			fmt.Fprintf(w, "  PL missing keys present in EN: %s\n", listed(r.PlMissingVsEN, 20))
		}

		table := [][]tableCell{}
		for i, row := range r.Rows {
			table = append(table, []tableCell{
				{text: strconv.Itoa(i)},
				{text: row.File, quoted: true},
				{text: strconv.Itoa(row.Keys)},
				{text: strconv.Itoa(len(row.Missing))},
				{text: strconv.Itoa(len(row.Extra))},
				{text: strconv.Itoa(len(row.IgnoredExtra))},
				{text: strconv.Itoa(len(row.Empty))},
				{text: strconv.Itoa(len(row.EnglishFallback))},
				{text: row.status(), quoted: true},
			})
		}
		consoleTable(w, []string{"(index)", "file", "keys", "missing", "extra", "ignoredExtra", "empty", "englishFallback", "status"}, table)

		for _, row := range r.Rows {
			if row.status() == "OK" && !verbose {
				continue
			}
			fmt.Fprintf(w, "-- %s\n", row.File)
			if row.ParseError != "" {
				fmt.Fprintf(w, "  parse-error: %s\n", row.ParseError)
			}
			if len(row.Missing) > 0 {
				fmt.Fprintf(w, "  missing(%d): %s\n", len(row.Missing), listed(row.Missing, 30))
			}
			if len(row.Extra) > 0 {
				fmt.Fprintf(w, "  extra(%d): %s\n", len(row.Extra), listed(row.Extra, 30))
			}
			if len(row.IgnoredExtra) > 0 && verbose {
				fmt.Fprintf(w, "  ignored-extra(%d): %s\n", len(row.IgnoredExtra), strings.Join(row.IgnoredExtra, ", "))
			}
			if len(row.Empty) > 0 {
				fmt.Fprintf(w, "  empty(%d): %s\n", len(row.Empty), listed(row.Empty, 30))
			}
			if len(row.EnglishFallback) > 0 {
				fmt.Fprintf(w, "  english-fallback(%d): %s\n", len(row.EnglishFallback), listed(row.EnglishFallback, 40))
			}
		}
		fmt.Fprintln(w)
	}
}

// tableCell is a value of console.table: numbers as they are, strings
// quoted as util.inspect quotes them.
type tableCell struct {
	text   string
	quoted bool
}

func (c tableCell) String() string {
	if !c.quoted {
		return c.text
	}
	quote := "'"
	switch {
	case !strings.Contains(c.text, "'"):
	case !strings.Contains(c.text, `"`):
		quote = `"`
	case !strings.Contains(c.text, "`"):
		quote = "`"
	}
	s := strings.NewReplacer("\\", `\`, "\n", `\n`, "\t", `\t`, "\r", `\r`, quote, `\`+quote).Replace(c.text)
	return quote + s + quote
}

// consoleTable writes rows the way Node's console.table draws them. The
// audit always has rows: en.json and pl.json are files of the folder.
func consoleTable(w io.Writer, head []string, rows [][]tableCell) {
	widths := make([]int, len(head))
	for i, h := range head {
		widths[i] = utf8.RuneCountInString(h)
	}
	cells := make([][]string, len(rows))
	for r, row := range rows {
		cells[r] = make([]string, len(head))
		for i := range head {
			if i < len(row) {
				cells[r][i] = row[i].String()
			}
			widths[i] = max(widths[i], utf8.RuneCountInString(cells[r][i]))
		}
	}
	line := func(left, mid, right string) string {
		parts := make([]string, len(widths))
		for i, n := range widths {
			parts[i] = strings.Repeat("─", n+2)
		}
		return left + strings.Join(parts, mid) + right + "\n"
	}
	render := func(row []string) string {
		parts := make([]string, len(row))
		for i, c := range row {
			parts[i] = c + strings.Repeat(" ", widths[i]-utf8.RuneCountInString(c))
		}
		return "│ " + strings.Join(parts, " │ ") + " │\n"
	}
	var b strings.Builder
	b.WriteString(line("┌", "┬", "┐"))
	b.WriteString(render(head))
	b.WriteString(line("├", "┼", "┤"))
	for _, row := range cells {
		b.WriteString(render(row))
	}
	b.WriteString(strings.TrimSuffix(line("└", "┴", "┘"), "\n"))
	fmt.Fprintln(w, b.String())
}
