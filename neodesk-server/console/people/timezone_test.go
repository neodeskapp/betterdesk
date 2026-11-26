package people

import (
	"testing"
	"time"
)

func TestCheckTimeZoneTakesOnlyNamesTheServerKnows(t *testing.T) {
	for _, ok := range []string{"Europe/Berlin", " America/New_York ", "UTC", "Asia/Tokyo", ""} {
		if _, err := CheckTimeZone(ok); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"Local", "Mars/Olympus", "../../etc/passwd", "Europe/../Berlin", "Europe Berlin", `Europe\Berlin`} {
		if _, err := CheckTimeZone(bad); err == nil {
			t.Errorf("%q taken", bad)
		}
	}
}

func TestTimeInGivesTheZoneOrUTC(t *testing.T) {
	at := time.Date(2026, 11, 26, 13, 5, 0, 0, time.UTC)
	if got := TimeIn(at, "Europe/Berlin"); got != "2026-11-26 14:05 Europe/Berlin" {
		t.Errorf("Berlin: %q", got)
	}
	if got := TimeIn(at, ""); got != "2026-11-26 13:05 UTC" {
		t.Errorf("none: %q", got)
	}
	if got := TimeIn(at, "Mars/Olympus"); got != "2026-11-26 13:05 UTC" {
		t.Errorf("unknown: %q", got)
	}
}
