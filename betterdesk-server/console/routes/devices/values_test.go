package devices

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/goapi"
)

// TestParseStoredTime reads stored times in a process whose local time is
// not UTC: text without a zone is UTC, text with one keeps it.
func TestParseStoredTime(t *testing.T) {
	saved := time.Local
	time.Local = time.FixedZone("plus5", 5*3600)
	t.Cleanup(func() { time.Local = saved })
	utc := float64(time.Date(2020, 1, 1, 10, 0, 0, 0, time.UTC).UnixMilli())
	for _, text := range []string{"2020-01-01 10:00:00", "2020-01-01T10:00:00", "2020-01-01 10:00", "2020-01-01T10:00:00.000Z", "2020-01-01T15:00:00+05:00"} {
		if got := parseStoredTime(text); got != utc {
			t.Errorf("%q: %v, want %v", text, got, utc)
		}
	}
}

// TestNormalizeStrategyPermissions reads the permissions of a submitted
// rule: flags and Y/N in either case become "Y" and "N", other text is
// trimmed, empty text and other values are dropped.
func TestNormalizeStrategyPermissions(t *testing.T) {
	in, err := goapi.Parse([]byte(`{"a":true,"b":false,"c":"Y","d":"y","e":"N","f":"n","g":" v ","h":5,"i":"","j":"  ","k":null,"l":[1]}`))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(normalizeStrategyPermissions(in))
	if want := `{"a":"Y","b":"N","c":"Y","d":"Y","e":"N","f":"N","g":"v"}`; string(got) != want {
		t.Errorf("got %s, want %s", got, want)
	}
}
