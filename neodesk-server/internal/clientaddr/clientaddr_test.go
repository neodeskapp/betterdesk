package clientaddr

import (
	"net/http"
	"reflect"
	"testing"
)

func TestForwardedJoinsEveryLine(t *testing.T) {
	h := http.Header{}
	h.Add("X-Forwarded-For", " 6.6.6.6 ,, 203.0.113.7")
	h.Add("X-Forwarded-For", "10.0.0.9")
	want := []string{"6.6.6.6", "203.0.113.7", "10.0.0.9"}
	if got := Forwarded(h); !reflect.DeepEqual(got, want) {
		t.Fatalf("Forwarded = %q, want %q", got, want)
	}
}

func TestRightmostSkipsOnlyTrustedHops(t *testing.T) {
	inTen := func(addr string, _ int) bool { return len(addr) > 3 && addr[:3] == "10." }
	cases := []struct {
		socket    string
		forwarded []string
		trusted   func(string, int) bool
		want      string
	}{
		{"10.0.0.2", []string{"6.6.6.6", "203.0.113.7"}, inTen, "203.0.113.7"},
		{"10.0.0.2", []string{"6.6.6.6", "203.0.113.7", "10.0.0.9"}, inTen, "203.0.113.7"},
		{"10.0.0.2", []string{"10.0.0.8", "10.0.0.9"}, inTen, "10.0.0.8"},
		{"198.51.100.9", []string{"203.0.113.7"}, inTen, "198.51.100.9"},
		{"10.0.0.2", nil, inTen, "10.0.0.2"},
		// A hop count: the socket and the next hop speak for the one before
		{"203.0.113.1", []string{"6.6.6.6", "203.0.113.7", "203.0.113.8"}, func(_ string, hop int) bool { return hop < 2 }, "203.0.113.7"},
	}
	for _, c := range cases {
		if got := Rightmost(c.socket, c.forwarded, c.trusted); got != c.want {
			t.Errorf("Rightmost(%q, %q) = %q, want %q", c.socket, c.forwarded, got, c.want)
		}
	}
}
