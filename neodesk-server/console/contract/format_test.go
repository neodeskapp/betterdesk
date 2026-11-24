package contract

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestFormatClass(t *testing.T) {
	cases := map[string]string{
		"2026-11-10 01:02:03":                  "9999-99-99 99:99:99",
		"2026-11-10T01:02:03.456Z":             "9999-99-99A99:99:99.999A",
		"Tue, 10 Nov 2026 01:02:03 GMT":        "Aaa, 99 Aaa 9999 99:99:99 AAA",
		"a3F9c2":                               "a9A9a9",
		"172.18.0.5":                           "999.99.9.9",
		"":                                     "",
		"   ":                                  "   ",
		"Größe 5 μ":                            "Aaößa 9 μ",
		"e2e_admin@example.org":                "a9a_aaaaa@aaaaaaa.aaa",
		"<volatile>":                           "<aaaaaaaa>",
		"550e8400-e29b-41d4-a716-446655440000": "999a9999-a99a-99a9-a999-999999999999",
	}
	for in, want := range cases {
		if got := formatClass(in); got != want {
			t.Errorf("formatClass(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGeneralizeClasses(t *testing.T) {
	cases := []struct {
		name            string
		classes         []string
		format, pattern string
	}{
		{"one value", []string{"9999-99-99"}, "9999-99-99", ""},
		{"all the same", []string{"9999-99-99 99:99:99", "9999-99-99 99:99:99", "9999-99-99 99:99:99"}, "9999-99-99 99:99:99", ""},
		{"digit runs differ", []string{"999.99.9.9", "999.99.9.99", "99.9.9.999"}, "", `^9+\.9+\.9\.9+$`},
		{"letter runs differ", []string{"aaaa-99 (AA)", "aa-99 (AAAA)"}, "", `^a+-99 \(A+\)$`},
		{"one run differs", []string{"99 aaa", "9 aaa"}, "", "^9+ aaa$"},
		{"another character", []string{"9999-99-99 99:99:99", "9999-99-99A99:99:99"}, "", ""},
		{"more pieces", []string{"9999-99-99", "9999-99-99.999"}, "", ""},
		{"a run of another class", []string{"a9a9", "9a9a"}, "", ""},
		{"runs of other characters do not stretch", []string{"9--9", "9-9"}, "", ""},
		{"an empty string among others", []string{"", "999"}, "", ""},
		{"only empty strings", []string{"", ""}, "", ""},
		{"nothing", nil, "", ""},
	}
	for _, c := range cases {
		format, pattern := generalizeClasses(c.classes)
		if format != c.format || pattern != c.pattern {
			t.Errorf("%s: generalizeClasses(%q) = %q, %q; want %q, %q", c.name, c.classes, format, pattern, c.format, c.pattern)
		}
	}
}

func TestDeriveFormat(t *testing.T) {
	cases := []struct {
		name            string
		values          []string
		format, pattern string
	}{
		{"timestamps", []string{"2026-11-10 01:02:03", "2026-11-10 04:05:06"}, "9999-99-99 99:99:99", ""},
		{"timestamps with T and Z", []string{"2026-11-10T01:02:03.456Z", "2026-11-10T04:05:06.789Z"}, "9999-99-99A99:99:99.999A", ""},
		{"fractions of any length", []string{"2026-11-10T01:02:03.4Z", "2026-11-10T04:05:06.78901Z"}, "", `^9999-99-99A99:99:99\.9+A$`},
		{"durations", []string{"3m3.933979475s", "4m28.40166852s", "12m1.5s"}, "", `^9+a9+\.9+a$`},
		// Go leaves the zeros at the end of nanoseconds out.
		{"nanoseconds", []string{"2026-11-10T04:05:28.305952741Z", "2026-11-10T04:05:29.118220013Z"}, "", `^9999-99-99A99:99:99(\.9{1,9})?A$`},
		{"nanoseconds of a duration", []string{"1m3.933979475s", "12m28.401668521s"}, "", `^9+a9+(\.9{1,9})?a$`},
		{"more than nine digits", []string{"1.9999999991", "2.0000000002"}, "9.9999999999", ""},
		{"milliseconds", []string{"2026-11-10T04:05:28.305Z", "2026-11-10T04:05:29.118Z"}, "9999-99-99A99:99:99.999A", ""},
		{"addresses", []string{"172.18.0.5", "172.18.0.12"}, "", `^999\.99\.9\.9+$`},
		{"names", []string{"demo-harbor-logistics", "demo-northwind-clinic"}, "", "^aaaa-a+-a+$"},
		// Two random tokens that happen to have the same class: the letters
		// differ, so the class says nothing about the next token.
		{"tokens of the same class", []string{"mwa1slae", "mwa1tbcd"}, "", "^[9aA+/_=-]{8}$"},
		{"tokens whose runs line up", []string{"a1bc22d", "x9yz1q"}, "", "^[9aA+/_=-]+$"},
		{"hashes", []string{"9f86d081884c7d65", "2c26b46b68ffc68f"}, "", "^[9aA+/_=-]{16}$"},
		{"UUIDs", []string{"550e8400-e29b-41d4-a716-446655440000", "6ba7b810-9dad-11d1-80b4-00c04fd430c8"}, "", "^[9aA+/_=-]{36}$"},
		// Two keys in base64 without + and /: the next one may have them.
		{"base64", []string{"Qm9iQWxpY2VFdmUwMTIz=", "enl4d3Z1dHNycXBvbm1s="}, "", "^[9aA+/_=-]{21}$"},
		{"prefixed keys", []string{"nd_live_a1b2c3", "nd_live_ffe019aa"}, "", "^[9aA+/_=-]+$"},
		{"words of another kind", []string{"ab-12", "12-ab"}, "", "^[9aA+/_=-]{5}$"},
		{"another shape", []string{"2026-11-10", "10.11.2026 12:00"}, "", ""},
		{"sentences of different length", []string{"User logged in", "Session replaced by a new login"}, "", ""},
		{"empty and not", []string{"", "abc"}, "", ""},
	}
	for _, c := range cases {
		format, pattern := deriveFormat(c.values)
		if format != c.format || pattern != c.pattern {
			t.Errorf("%s: deriveFormat(%q) = %q, %q; want %q, %q", c.name, c.values, format, pattern, c.format, c.pattern)
		}
	}
}

func TestWordPatternTakesTheWholeAlphabetOfAToken(t *testing.T) {
	_, pattern := deriveFormat([]string{"Qm9iQWxpY2VFdmUwMTIz=", "enl4d3Z1dHNycXBvbm1s="})
	want := newGoldenAnswer(jsonAnswer(200, `{"public_key": "<volatile>"}`), []VolatileMark{{Path: "public_key", Pattern: pattern}}, nil)
	for value, fits := range map[string]bool{
		"Aa9Aa9aa/9aaAA+aAa0b=":  true,
		"Aa9Aa9aa_9aaAA-aAa0b=":  true,
		"Aa9Aa9aa/9aaAA+aAa0b==": false,
		"Aa9Aa9aa/9aaAA+aAa0=":   false,
		"Aa9Aa9aa 9aaAA+aAa0b=":  false,
	} {
		got := jsonAnswer(200, `{"public_key": "`+value+`"}`)
		if diffs := CompareAnswers(want, got); (len(diffs) == 0) != fits {
			t.Errorf("%q against %s: %q, want it to fit: %v", value, pattern, diffStrings(diffs), fits)
		}
	}
}

func TestNanosecondsMayBeShorter(t *testing.T) {
	_, pattern := deriveFormat([]string{"2026-11-10T04:05:28.305952741Z", "2026-11-10T04:05:29.118220013Z"})
	want := newGoldenAnswer(jsonAnswer(200, `{"at": "<volatile>"}`), []VolatileMark{{Path: "at", Pattern: pattern}}, nil)
	for value, fits := range map[string]bool{
		"2027-01-02T03:04:05.123456789Z":  true,
		"2027-01-02T03:04:05.12345678Z":   true,
		"2027-01-02T03:04:05.1Z":          true,
		"2027-01-02T03:04:05Z":            true,
		"2027-01-02T03:04:05.1234567891Z": false,
		"2027-01-02T03:04:05.Z":           false,
		"2027-01-02 03:04:05":             false,
	} {
		got := jsonAnswer(200, `{"at": "`+value+`"}`)
		if diffs := CompareAnswers(want, got); (len(diffs) == 0) != fits {
			t.Errorf("%q against %s: %q, want it to fit: %v", value, pattern, diffStrings(diffs), fits)
		}
	}
}

func TestDeriveMarkTakesEverySample(t *testing.T) {
	decode := func(text string) any {
		value, _ := decodeJSON([]byte(text))
		return value
	}
	// Two samples agree on the number of digits by chance; the third shows
	// that the run can be longer.
	two := deriveMark("uptime", decode(`{"uptime": "58.1s"}`), decode(`{"uptime": "59.3s"}`))
	if two.Format != "99.9a" {
		t.Errorf("two samples: %+v, want the format both share", two)
	}
	three := deriveMark("uptime", decode(`{"uptime": "58.1s"}`), decode(`{"uptime": "59.3s"}`), decode(`{"uptime": "101.25s"}`), decode(`{"other": 1}`))
	if three.Format != "" || three.Pattern != `^9+\.9+a$` {
		t.Errorf("three samples: %+v, want a pattern with runs of any length", three)
	}
}

func TestDeriveMark(t *testing.T) {
	first, _ := decodeJSON([]byte(`{
		"at": "2026-11-10 01:02:03", "ip": "172.18.0.5", "id": "a3f9", "n": 5, "ratio": 1.5, "whole": 2.0,
		"items": [{"seen": "2026-11-10 01:02:03", "count": 1}, {"seen": "2026-11-10 04:05:06", "count": 2}],
		"mixed": [{"v": "x"}, {"v": 1}], "log": [1, 2], "name": ""
	}`))
	second, _ := decodeJSON([]byte(`{
		"at": "2026-11-10 07:08:09", "ip": "172.18.0.12", "id": "9c2e", "n": 7, "ratio": 2, "whole": 3,
		"items": [{"seen": "2026-11-10 07:08:09", "count": 3}, {"seen": "2026-11-10 10:11:12", "count": 4.5}],
		"mixed": [{"v": "y"}, {"v": 2}], "log": [1], "name": "x"
	}`))
	cases := []VolatileMark{
		{Path: "at", Format: "9999-99-99 99:99:99"},
		{Path: "ip", Pattern: `^999\.99\.9\.9+$`},
		{Path: "id", Pattern: "^[9aA+/_=-]{4}$"},
		{Path: "n", Integer: true},
		{Path: "ratio"},
		{Path: "whole", Integer: true},
		{Path: "items[*].seen", Format: "9999-99-99 99:99:99"},
		{Path: "items[0].count", Integer: true},
		{Path: "items[*].count"},
		{Path: "mixed[*].v"},
		{Path: "log[#]"},
		{Path: "name"},
		{Path: "missing.path"},
		{Path: "items"},
	}
	for _, want := range cases {
		if got := deriveMark(want.Path, first, second); got != want {
			t.Errorf("deriveMark(%s) = %+v, want %+v", want.Path, got, want)
		}
	}
}

func TestVolatileMarkJSON(t *testing.T) {
	marks := []VolatileMark{
		{Path: "data.id"},
		{Path: "data.at", Format: "9999-99-99 99:99:99"},
		{Path: "data.ip", Pattern: `^9+\.9+$`},
		{Path: "data.n", Integer: true},
	}
	data, err := json.Marshal(marks)
	if err != nil {
		t.Fatal(err)
	}
	want := `["data.id",{"path":"data.at","format":"9999-99-99 99:99:99"},{"path":"data.ip","pattern":"^9+\\.9+$"},{"path":"data.n","integer":true}]`
	if string(data) != want {
		t.Errorf("marks as JSON:\n got %s\nwant %s", data, want)
	}
	var back []VolatileMark
	if err := json.Unmarshal(data, &back); err != nil || !reflect.DeepEqual(back, marks) {
		t.Errorf("marks read back = %+v, %v", back, err)
	}
	for _, bad := range []string{`[{"path":"a","fromat":"9"}]`, `[{"format":"9"}]`, `[5]`} {
		if err := json.Unmarshal([]byte(bad), &back); err == nil {
			t.Errorf("%s was accepted as marks", bad)
		}
	}
}

func TestCompareVolatileChecksTheFormat(t *testing.T) {
	want := newGoldenAnswer(
		jsonAnswer(200, `{"at": "<volatile>", "ip": "<volatile>", "free": "<volatile>", "n": 0, "ratio": 0, "csrfToken": "<volatile>", "items": [{"seen": "<volatile>"}, {"seen": "<volatile>"}]}`),
		[]VolatileMark{
			{Path: "at", Format: "9999-99-99 99:99:99"},
			{Path: "ip", Pattern: `^9+\.9+\.9+\.9+$`},
			{Path: "free"},
			{Path: "n", Integer: true},
			{Path: "ratio"},
			{Path: "csrfToken", Format: "aaaa-9999"},
			{Path: "items[*].seen", Format: "9999-99-99"},
		}, nil)

	fits := jsonAnswer(200, `{"at": "2031-01-02 03:04:05", "ip": "10.0.0.200", "free": "anything at all", "n": 12345678901234567890, "ratio": 0.25, "csrfToken": "abcd-1234", "items": [{"seen": "2031-01-02"}, {"seen": "2031-01-03"}]}`)
	if diffs := CompareAnswers(want, fits); len(diffs) > 0 {
		t.Errorf("values in the recorded formats differ: %q", diffStrings(diffs))
	}
	if diffs := CompareAnswers(want, jsonAnswer(200, `{"at": "2031-01-02 03:04:05", "ip": "10.0.0.200", "free": "", "n": 1e3, "ratio": 7, "csrfToken": "abcd-1234", "items": [{"seen": "2031-01-02"}, {"seen": "2031-01-03"}]}`)); len(diffs) > 0 {
		t.Errorf("1e3 is a whole number and a free string may be empty: %q", diffStrings(diffs))
	}

	other := jsonAnswer(200, `{"at": "2031-01-02T03:04:05.678Z", "ip": "fe80::1", "free": "x", "n": 1.5, "ratio": 1, "csrfToken": "ABCD-1234-secret", "items": [{"seen": "2031-01-02"}, {"seen": "02.01.2031"}]}`)
	diffs := diffStrings(CompareAnswers(want, other))
	wantDiffs := []string{
		`body at: format of a volatile value: expected format 9999-99-99 99:99:99, got "2031-01-02T03:04:05.678Z" (9999-99-99A99:99:99.999A)`,
		`body csrfToken: format of a volatile value: expected format aaaa-9999, got a string of the class AAAA-9999-aaaaaa`,
		`body ip: format of a volatile value: expected format ^9+\.9+\.9+\.9+$, got "fe80::1" (aa99::9)`,
		`body items[1].seen: format of a volatile value: expected format 9999-99-99, got "02.01.2031" (99.99.9999)`,
		`body n: format of a volatile value: expected a whole number, got 1.5`,
	}
	if !reflect.DeepEqual(diffs, wantDiffs) {
		t.Errorf("diffs:\n got %q\nwant %q", diffs, wantDiffs)
	}
}

// A product version with a suffix has the format of the same version
// without it, so a volatile value made from it, such as the cache version
// of restart-status, fits its golden on a release candidate too, and a
// recording there gives the format of a release (L302).
func TestAVersionSuffixKeepsTheFormat(t *testing.T) {
	saved := productVersion
	productVersion = func() string { return "3.6.0-rc.2" }
	defer func() { productVersion = saved }()

	want := newGoldenAnswer(jsonAnswer(200, `{"data": {"cacheVersion": "<volatile>"}}`),
		[]VolatileMark{{Path: "data.cacheVersion", Format: "9.9.9.9999999999999"}}, nil)
	if diffs := CompareAnswers(want, jsonAnswer(200, `{"data": {"cacheVersion": "3.6.0-rc.2.1795409105554"}}`)); len(diffs) > 0 {
		t.Errorf("the cache version of a release candidate: %q", diffStrings(diffs))
	}
	// Another version keeps its suffix, and the rest of the value is still checked
	for _, other := range []string{"3.6.1-rc.2.1795409105554", "13.6.0-rc.2.1795409105554", "3.6.0-rc.21.1795409105554", "3.6.0-rc.2.17954091055x4"} {
		if diffs := CompareAnswers(want, jsonAnswer(200, `{"data": {"cacheVersion": "`+other+`"}}`)); len(diffs) != 1 {
			t.Errorf("%s fits the format of the product version: %q", other, diffStrings(diffs))
		}
	}
	if format, pattern := deriveFormat([]string{"3.6.0-rc.2.1795409105554", "3.6.0-rc.2.1795409105912"}); format != "9.9.9.9999999999999" || pattern != "" {
		t.Errorf("format recorded on a release candidate: %q, %q", format, pattern)
	}

	productVersion = func() string { return "3.6.1" }
	if diffs := CompareAnswers(want, jsonAnswer(200, `{"data": {"cacheVersion": "3.6.1.1795409105554"}}`)); len(diffs) > 0 {
		t.Errorf("the cache version of a release: %q", diffStrings(diffs))
	}
}

// A volatile value made from the product version fits a golden recorded at
// a version with fewer digits in a part: the cache version of 3.6.10 fits
// the format recorded at 3.6.9, and a recording at 3.6.10 gives that same
// format. Other numbers in the value keep their digits (L437).
func TestAVersionPartWithMoreDigitsKeepsTheFormat(t *testing.T) {
	saved := productVersion
	defer func() { productVersion = saved }()
	recordedAt369 := newGoldenAnswer(jsonAnswer(200, `{"data": {"cacheVersion": "<volatile>"}}`),
		[]VolatileMark{{Path: "data.cacheVersion", Format: "9.9.9.9999999999999"}}, nil)

	for _, version := range []string{"3.6.10", "3.10.0", "10.0.0", "3.6.10-rc.1"} {
		productVersion = func() string { return version }
		value := version + ".1795409105554"
		if diffs := CompareAnswers(recordedAt369, jsonAnswer(200, `{"data": {"cacheVersion": "`+value+`"}}`)); len(diffs) > 0 {
			t.Errorf("%s against the golden of 3.6.9: %q", value, diffStrings(diffs))
		}
		if format, pattern := deriveFormat([]string{value, version + ".1795409105912"}); format != "9.9.9.9999999999999" || pattern != "" {
			t.Errorf("format recorded at %s: %q, %q", version, format, pattern)
		}
	}

	productVersion = func() string { return "3.6.10" }
	for _, other := range []string{"3.6.100.1795409105554", "13.6.10.1795409105554", "3.6.10.17954091055x4", "3.6.10.179540910555"} {
		if diffs := CompareAnswers(recordedAt369, jsonAnswer(200, `{"data": {"cacheVersion": "`+other+`"}}`)); len(diffs) != 1 {
			t.Errorf("%s fits the format of the product version: %q", other, diffStrings(diffs))
		}
	}
}

func TestMessagesDoNotShowSecrets(t *testing.T) {
	for _, text := range []string{"data.csrfToken", "data.api_key", "user.Password", "data.clientSecret", "data.cookies[0].value", "sessions[2].sid", `data["API-KEY"]`} {
		p, err := parsePath(text)
		if err != nil {
			t.Fatal(err)
		}
		if !looksSecret(p) {
			t.Errorf("looksSecret(%s) = false", text)
		}
	}
	for _, text := range []string{"data.id", "data.items[3].name", "$", "data.created_at"} {
		p, _ := parsePath(text)
		if looksSecret(p) {
			t.Errorf("looksSecret(%s) = true", text)
		}
	}

	want := newGoldenAnswer(jsonAnswer(200, `{"sid": 0, "name": 0}`), plainMarks([]string{"sid", "name"}), nil)
	diffs := diffStrings(CompareAnswers(want, jsonAnswer(200, `{"sid": "s3cr3t-value", "name": "plain"}`)))
	wantDiffs := []string{
		`body name: type: expected number 0, got string "plain"`,
		`body sid: type: expected number 0, got string`,
	}
	if !reflect.DeepEqual(diffs, wantDiffs) {
		t.Errorf("diffs:\n got %q\nwant %q", diffs, wantDiffs)
	}
}
