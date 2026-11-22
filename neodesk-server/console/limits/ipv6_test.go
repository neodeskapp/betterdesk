package limits

import (
	"fmt"
	"net/http"
	"testing"
)

// TestIPv6CountsByItsSlash64: addresses of one IPv6 /64 share one budget,
// on the console's limiters and on the client API port's; IPv4 addresses
// and other /64s count apart (BUGS.md RV102).
func TestIPv6CountsByItsSlash64(t *testing.T) {
	c := &clock{}
	set := newSet(c, nil)
	login := set.Login.Middleware(http.HandlerFunc(ok))
	for i := 0; i < 5; i++ {
		if rec := hit(login, request("POST", "/api/auth/login", fmt.Sprintf("[2001:db8:1:2::%x]", i+1))); rec.Code != http.StatusOK {
			t.Fatalf("sign-in %d from the /64: %d", i+1, rec.Code)
		}
	}
	for addr, want := range map[string]int{
		"[2001:db8:1:2:ffff:1:2:3]": http.StatusTooManyRequests,
		"[2001:db8:1:3::1]":         http.StatusOK,
		"198.51.100.7":              http.StatusOK,
	} {
		if rec := hit(login, request("POST", "/api/auth/login", addr)); rec.Code != want {
			t.Errorf("sign-in from %s: %d, want %d", addr, rec.Code, want)
		}
	}

	wan := wanHandler(WANOptions{Now: c.now}, nil)
	for i := 0; i < 6; i++ {
		r := wanRequest("POST", "/api/login", 2)
		r.RemoteAddr = fmt.Sprintf("[2001:db8:5:6::%x]:40000", i+1)
		rec := hit(wan, r)
		if want := http.StatusOK; i == 5 {
			if rec.Code != http.StatusTooManyRequests {
				t.Errorf("sixth client API sign-in from the /64: %d", rec.Code)
			}
		} else if rec.Code == http.StatusTooManyRequests {
			t.Errorf("client API sign-in %d from the /64: %d, want %d", i+1, rec.Code, want)
		}
	}

	for addr, want := range map[string]string{
		"2001:db8:1:2:3:4:5:6": "2001:db8:1:2::/64",
		"fe80::1%eth0":         "fe80::/64",
		"203.0.113.5":          "203.0.113.5",
		"::ffff:203.0.113.5":   "::ffff:203.0.113.5",
		"unknown":              "unknown",
	} {
		if got := bucketKey(addr); got != want {
			t.Errorf("bucketKey(%q) = %q, want %q", addr, got, want)
		}
	}
}
