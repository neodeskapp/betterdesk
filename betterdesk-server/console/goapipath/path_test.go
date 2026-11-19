package goapipath

import "testing"

// TestIDsWithTwoDotsAreRefused: an id with ".." anywhere in it is refused
// as an id, so a route answers 400 before it builds a path the API
// client's path check would refuse (BUGS.md FL4).
func TestIDsWithTwoDotsAreRefused(t *testing.T) {
	for _, id := range []string{"a..b", "..a", "a..", "...", " a..b ", "a...b", "0@@...:"} {
		if got, err := AssertSafeAPIID(id, "orgId"); err == nil || err.Error() != "Invalid orgId" {
			t.Errorf("AssertSafeAPIID(%q) = %q, %v; want Invalid orgId", id, got, err)
		}
		if got, err := SafeSegment(id, "deviceId"); err == nil || err.Error() != "Invalid deviceId" {
			t.Errorf("SafeSegment(%q) = %q, %v; want Invalid deviceId", id, got, err)
		}
	}
	for id, want := range map[string]string{"a.b": "a.b", ".a": ".a", "a.": "a.", "a.b.c": "a.b.c", "1.2.3.4": "1.2.3.4"} {
		if got, err := AssertSafeAPIID(id, "id"); err != nil || got != want {
			t.Errorf("AssertSafeAPIID(%q) = %q, %v; want %q", id, got, err, want)
		}
	}
}
