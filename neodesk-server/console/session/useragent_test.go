package session

import "testing"

func TestDescribeUserAgent(t *testing.T) {
	for ua, want := range map[string][2]string{
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36 Edg/131.0.0.0":  {"Edge", "Windows"},
		"Mozilla/5.0 (X11; Linux x86_64; rv:133.0) Gecko/20100101 Firefox/133.0":                                                         {"Firefox", "Linux"},
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Safari/605.1.15":          {"Safari", "macOS"},
		"Mozilla/5.0 (iPhone; CPU iPhone OS 18_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/131.0 Mobile/15E148":      {"Chrome", "iOS"},
		"Mozilla/5.0 (iPad; CPU OS 18_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Mobile/15E148 Safari/604.1": {"Safari", "iPadOS"},
		"Mozilla/5.0 (Linux; Android 14; SM-S918B) AppleWebKit/537.36 (KHTML, like Gecko) SamsungBrowser/26.0 Chrome/122.0 Mobile":       {"Samsung Internet", "Android"},
		"Mozilla/5.0 (X11; CrOS x86_64 15917.71.0) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36":                {"Chrome", "ChromeOS"},
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36 OPR/115.0":      {"Opera", "Windows"},
		"curl/8.5.0": {"", ""},
		"":           {"", ""},
	} {
		browser, system := DescribeUserAgent(ua)
		if browser != want[0] || system != want[1] {
			t.Errorf("%q: %q on %q, want %q on %q", ua, browser, system, want[0], want[1])
		}
	}
	d := Device{Browser: "Firefox", System: "Windows", IPAddress: "203.0.113.5"}
	if got := d.Describe(); got != "Firefox on Windows, 203.0.113.5" {
		t.Errorf("Describe: %q", got)
	}
	if got := (Device{}).Describe(); got != "unknown browser" {
		t.Errorf("Describe of nothing: %q", got)
	}
}
