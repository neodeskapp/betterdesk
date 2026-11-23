package jsreq

import (
	"encoding/json"
	"os"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
)

// dateVectors is testdata/dates.json, recorded from JavaScript in the Node
// console (testdata/README.md).
type dateVectors struct {
	Zone      string `json:"zone"`
	Supported []struct {
		Text string `json:"text"`
		MS   *int64 `json:"ms"`
	} `json:"supported"`
	Values []struct {
		JSON string `json:"json"`
		MS   *int64 `json:"ms"`
	} `json:"values"`
	Texts []struct {
		JSON   string `json:"json"`
		Out    string `json:"out"`
		Length int    `json:"length"`
	} `json:"texts"`
}

func loadDates(t *testing.T) (dateVectors, *time.Location) {
	t.Helper()
	raw, err := os.ReadFile("testdata/dates.json")
	if err != nil {
		t.Fatal(err)
	}
	var v dateVectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	if len(v.Supported) < 90 || len(v.Values) < 20 || len(v.Texts) < 25 {
		t.Fatalf("testdata/dates.json holds %d texts, %d values and %d JSON texts", len(v.Supported), len(v.Values), len(v.Texts))
	}
	zone, err := time.LoadLocation(v.Zone)
	if err != nil {
		t.Fatal(err)
	}
	return v, zone
}

func TestParseDateMatchesJavaScript(t *testing.T) {
	vectors, zone := loadDates(t)
	read := 0
	for _, vec := range vectors.Supported {
		got, ok := ParseDate(vec.Text, zone)
		switch {
		case vec.MS == nil && ok:
			t.Errorf("ParseDate(%q) = %d, JavaScript gave NaN", vec.Text, got)
		case vec.MS != nil && (!ok || got != *vec.MS):
			t.Errorf("ParseDate(%q) = %d, %v; JavaScript gave %d", vec.Text, got, ok, *vec.MS)
		}
		if vec.MS != nil {
			read++
		}
	}
	if read < 40 {
		t.Errorf("JavaScript read only %d of the texts", read)
	}
}

func TestDateOfMatchesJavaScript(t *testing.T) {
	vectors, zone := loadDates(t)
	for _, vec := range vectors.Values {
		v, err := goapi.Parse([]byte(vec.JSON))
		if err != nil {
			t.Fatal(err)
		}
		got, ok := DateOf(v, zone)
		switch {
		case vec.MS == nil && ok:
			t.Errorf("DateOf(%s) = %d, JavaScript gave NaN", vec.JSON, got)
		case vec.MS != nil && (!ok || got != *vec.MS):
			t.Errorf("DateOf(%s) = %d, %v; JavaScript gave %d", vec.JSON, got, ok, *vec.MS)
		}
	}
}

func TestStringifyMatchesJavaScript(t *testing.T) {
	vectors, _ := loadDates(t)
	for _, vec := range vectors.Texts {
		v, err := goapi.Parse([]byte(vec.JSON))
		if err != nil {
			t.Fatal(err)
		}
		got := Stringify(v)
		if got != vec.Out {
			t.Errorf("Stringify(%s) = %s, JavaScript gave %s", vec.JSON, got, vec.Out)
		}
		if n := Length(got); n != vec.Length {
			t.Errorf("Length of %s = %d, JavaScript gave %d", got, n, vec.Length)
		}
	}
}

// FuzzParseDate checks ParseDate against the standard library for the
// form the API server writes, and that no text makes it panic.
func FuzzParseDate(f *testing.F) {
	for _, seed := range []string{
		"2026-11-10T07:32:11Z", "2026-11-10T07:32:11.123456789+01:00", "2026-11-10T07:32Z", "0001-01-01T00:00:00Z",
		"2026-02-30T00:00:00Z", "2026-11-10T24:00:00Z", "2026-11-10T07:32:11-0130", "", "2026-11-10 07:32:11", "9999-12-31T23:59:59.999999999-23:59",
		"2026-11-10", "2026-11-10T07:32:11",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, text string) {
		got, ok := ParseDate(text, time.UTC)
		std, err := time.Parse(time.RFC3339Nano, text)
		if err != nil {
			return
		}
		// The standard library takes forms JavaScript does not (a comma
		// before the fraction, an hour of one digit, an offset of 60
		// minutes or 24 hours). What it writes itself for a time zone
		// there is, as the API server does, has to be read
		if !ok {
			_, offset := std.Zone()
			if std.Format(time.RFC3339Nano) == text && offset > -24*3600 && offset < 24*3600 {
				t.Errorf("ParseDate(%q) found no time, the standard library found %s", text, std)
			}
			return
		}
		if got != std.UnixMilli() {
			t.Errorf("ParseDate(%q) = %d, the standard library says %d", text, got, std.UnixMilli())
		}
	})
}
