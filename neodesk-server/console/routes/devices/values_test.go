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
// rule: only RustDesk's keys, letter for letter; flags and Y/N in either
// case become "Y" and "N"; every other key and value is dropped, custom
// text included.
func TestNormalizeStrategyPermissions(t *testing.T) {
	in, err := goapi.Parse([]byte(`{"enable-audio":true,"enable-tunnel":false,"enable-camera":"Y","enable-clipboard":"y",` +
		`"disable-clipboard":"N","enable-file-transfer":"n","enable-block-input":" Y ","enable-remote-restart":5,` +
		`"a":true,"b":"N","Enable-Audio":"Y","2":"Y","enable-audio ":"Y"}`))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(normalizeStrategyPermissions(in))
	want := `{"enable-audio":"Y","enable-tunnel":"N","enable-camera":"Y","enable-clipboard":"Y","disable-clipboard":"N","enable-file-transfer":"N"}`
	if string(got) != want {
		t.Errorf("got %s, want %s", got, want)
	}
	for _, raw := range []string{`{"enable-audio":" custom "}`, `{"enable-tunnel":null}`, `{"enable-camera":[1]}`, `["Y"]`, `"Y"`} {
		v, _ := goapi.Parse([]byte(raw))
		if got, _ := json.Marshal(normalizeStrategyPermissions(v)); string(got) != `{}` {
			t.Errorf("%s: got %s, want {}", raw, got)
		}
	}
}
