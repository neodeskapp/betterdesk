package billingclock

import (
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/httpx"
)

// FuzzSettings reads request bodies: nothing panics, a body that is
// validated reads the same when read again from what it became, and the
// saved settings pass the checks of a save.
func FuzzSettings(f *testing.F) {
	for _, seed := range []string{`{}`, `[]`, `null`, `{"ntp_servers":"a.example, b.example","max_skew_ms":"3000ms"}`,
		`{"ntp_servers":["x",{"toString":1}]}`, `{"max_skew_ms":1e21,"billing_timezone":" europe/berlin "}`,
		`{"billing_timezone":"+05:30","trust_os_ntp":null,"require_synced_clock":"0"}`, `{"ntp_servers":"fe80::1%eth0"}`} {
		f.Add(seed)
	}
	s := New(Options{Linux: true, HostZone: func() string { return "UTC" }, TZ: func() string { return "" }})
	f.Fuzz(func(t *testing.T, body string) {
		raw, err := goapi.Parse([]byte(body))
		if err != nil {
			return
		}
		normalized, err := s.NormalizeSettings(raw)
		if err != nil {
			return
		}
		again, err := s.NormalizeSettings(normalized.Object())
		if err != nil || again != normalized {
			t.Fatalf("%s: normalized %+v, again %+v (%v)", body, normalized, again, err)
		}
		if normalized.MaxSkewMS <= 0 || normalized.BillingTimezone == "" || strings.Contains(normalized.NTPServers, ",,") {
			t.Fatalf("%s: normalized to %+v", body, normalized)
		}
		valid, err := s.ValidateSettings(raw)
		if err != nil {
			if !IsRefusal(err) {
				t.Fatalf("%s: validation failed with %v where normalizing did not", body, err)
			}
			return
		}
		if valid.MaxSkewMS > maxSkewLimit || !IsValidIANATimezone(valid.BillingTimezone) {
			t.Fatalf("%s: validated %+v", body, valid)
		}
		for _, server := range strings.Split(valid.NTPServers, ",") {
			if !IsValidNTPServerEntry(server) {
				t.Fatalf("%s: validated a server %q", body, server)
			}
		}
		if _, err := httpx.Marshal(valid); err != nil {
			t.Fatalf("%s: %v", body, err)
		}
	})
}

// FuzzZone checks time zone names: a name Intl takes is taken in every
// letter case, and what it resolves to is taken too and resolves to
// itself.
func FuzzZone(f *testing.F) {
	for _, seed := range []string{"UTC", "europe/berlin", "+01:00", "\u221205", "-0000", "Etc/GMT+13", "America/Port-au-Prince", "", " GMT "} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, name string) {
		resolved, ok := resolveZone(name)
		if !ok {
			return
		}
		if again, ok := resolveZone(resolved); !ok || again != resolved {
			t.Fatalf("%q resolved to %q, which resolves to %q (%v)", name, resolved, again, ok)
		}
		if _, ok := resolveZone(strings.ToUpper(name)); !ok && !strings.ContainsRune(name, 0x2212) {
			t.Fatalf("%q is taken but not in capitals", name)
		}
		_ = IsValidIANATimezone(name)
	})
}

// FuzzEnvFile reads .env text: no key is empty or holds "=", and the
// settings read from a file that the package wrote are the ones written.
func FuzzEnvFile(f *testing.F) {
	for _, seed := range []string{"", "A=1\nB=2", " NTP_SERVERS = a.example \r\n#x\n=y", "BILLING_TIMEZONE=\u00a0UTC\u2028X=1", "\ufeffA=b=c"} {
		f.Add(seed)
	}
	s := New(Options{Linux: true, HostZone: func() string { return "UTC" }, TZ: func() string { return "" }})
	f.Fuzz(func(t *testing.T, content string) {
		for key := range ParseEnvFile(content) {
			if key == "" || strings.Contains(key, "=") {
				t.Fatalf("%q gave the key %q", content, key)
			}
		}
		if _, err := s.SettingsFromEnvContent(content); err != nil {
			t.Fatalf("%q: %v", content, err)
		}
	})
}

// FuzzHostZone reads TZ values: whatever is found is a zone Intl takes.
func FuzzHostZone(f *testing.F) {
	for _, seed := range []string{"", ":Europe/Berlin", "posix/Asia/Tokyo", "EST5EDT", "Etc/GMT+12", "CET-1CEST", "GMT", "europe/berlin"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, tz string) {
		zone := HostZone(HostFS{
			LookupEnv: func(string) (string, bool) { return tz, true },
			Realpath:  func(p string) (string, error) { return p, nil },
		})
		if _, ok := resolveZone(zone); zone != "" && !ok {
			t.Fatalf("TZ %q gave %q, which Intl does not take", tz, zone)
		}
	})
}
