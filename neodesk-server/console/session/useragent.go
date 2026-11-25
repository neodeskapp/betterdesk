package session

import "strings"

// DescribeUserAgent reads the browser and the system from a user agent,
// in the words a person knows them by ("Firefox", "Windows"). Either is ""
// when the user agent does not say. Only the common browsers and systems
// are told apart; the list is for people telling their own devices apart,
// not for statistics.
func DescribeUserAgent(ua string) (browser, system string) {
	has := func(s string) bool { return strings.Contains(ua, s) }
	switch {
	case has("Edg/") || has("EdgA/") || has("EdgiOS/"):
		browser = "Edge"
	case has("OPR/") || has("Opera"):
		browser = "Opera"
	case has("SamsungBrowser/"):
		browser = "Samsung Internet"
	case has("Vivaldi/"):
		browser = "Vivaldi"
	case has("Firefox/") || has("FxiOS/"):
		browser = "Firefox"
	case has("Chromium/"):
		browser = "Chromium"
	case has("Chrome/") || has("CriOS/"):
		browser = "Chrome"
	case has("Safari/") && has("Version/"):
		browser = "Safari"
	}
	switch {
	case has("Windows"):
		system = "Windows"
	case has("iPhone") || has("iPod"):
		system = "iOS"
	case has("iPad"):
		system = "iPadOS"
	case has("Android"):
		system = "Android"
	case has("CrOS"):
		system = "ChromeOS"
	case has("Macintosh") || has("Mac OS X"):
		system = "macOS"
	case has("Linux") || has("X11"):
		system = "Linux"
	}
	return browser, system
}
