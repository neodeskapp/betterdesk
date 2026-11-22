package generator

import "testing"

// TestRequestRelayPort takes a request's relayPort only when it is a
// whole number from 1 to 65535, as a number or as its digits (BUGS UM4:
// any value that was truthy went into the relay address as text).
func TestRequestRelayPort(t *testing.T) {
	for _, c := range []struct {
		in   any
		want string
	}{
		{float64(7), "7"},
		{"21118", "21118"},
		{"007", "7"},
		{float64(65535), "65535"},
		{float64(65536), ""},
		{"65536", ""},
		{"123456", ""},
		{float64(0), ""},
		{float64(-5), ""},
		{1.5, ""},
		{" 3 ", ""},
		{"", ""},
		{true, ""},
		{[]any{float64(1)}, ""},
		{nil, ""},
	} {
		if got := requestRelayPort(c.in); got != c.want {
			t.Errorf("requestRelayPort(%#v) = %q, want %q", c.in, got, c.want)
		}
	}
}
