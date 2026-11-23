package wsrelay

import (
	"net/http"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
)

// TestExpiresAt is new Date(grant.expires_at).getTime(): a missing end is
// NaN, null is 0.
func TestExpiresAt(t *testing.T) {
	for _, c := range []struct {
		data string
		ms   int64
		ok   bool
	}{
		{`{"valid":true}`, 0, false},
		{`{"valid":true,"expires_at":null}`, 0, true},
		{`{"valid":true,"expires_at":"2026-11-18T10:00:00Z"}`, time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC).UnixMilli(), true},
		{`{"valid":true,"expires_at":"2026-11-18T10:00:00.250+01:00"}`, time.Date(2026, 11, 18, 9, 0, 0, 250e6, time.UTC).UnixMilli(), true},
		{`{"valid":true,"expires_at":1700000000123.9}`, 1700000000123, true},
		{`{"valid":true,"expires_at":"soon"}`, 0, false},
		{`{"valid":true,"expires_at":true}`, 1, true},
	} {
		data, err := goapi.Parse([]byte(c.data))
		if err != nil {
			t.Fatal(err)
		}
		ms, ok := (&guestGrant{data: data}).expiresAt()
		if ms != c.ms || ok != c.ok {
			t.Errorf("%s: want %d %v, got %d %v", c.data, c.ms, c.ok, ms, ok)
		}
	}
}

// countingAPI answers every consume with success and counts the calls.
type countingAPI struct{ consumes int }

func (a *countingAPI) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	a.consumes++
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"valid":true}`))
}

// TestConsumeIsRememberedUntilTheLinkEnds: a counted use is kept until the
// link's end, a second at least, and 15 seconds for a link without an
// end; then the next bridge of the guest session counts again.
func TestConsumeIsRememberedUntilTheLinkEnds(t *testing.T) {
	start := time.UnixMilli(1700000000000)
	for _, c := range []struct {
		name string
		data string
		keep time.Duration
	}{
		{"end in 20 s", `{"valid":true,"id":"g","expires_at":"2023-11-14T22:13:40Z"}`, 20 * time.Second},
		{"no end", `{"valid":true,"id":"g"}`, 15 * time.Second},
		{"end passed", `{"valid":true,"id":"g","expires_at":"2023-11-14T22:00:00Z"}`, time.Second},
	} {
		t.Run(c.name, func(t *testing.T) {
			clock := &fakeClock{now: start}
			api := &countingAPI{}
			svc := New(Options{GoAPI: goapi.New(api), Clock: clock, Interfaces: func() []InterfaceAddr { return nil }})
			data, err := goapi.Parse([]byte(c.data))
			if err != nil {
				t.Fatal(err)
			}
			g := &guestGrant{data: data, token: "tok", peerID: "TARGET01", sessionID: "s1"}
			use := func() {
				if err := svc.consume(t.Context(), g); err != nil {
					t.Fatal(err)
				}
			}
			use()
			clock.Advance(c.keep - time.Millisecond)
			use()
			if api.consumes != 1 {
				t.Fatalf("counted %d times before the end", api.consumes)
			}
			clock.Advance(time.Millisecond)
			use()
			if api.consumes != 2 {
				t.Fatalf("counted %d times after the end", api.consumes)
			}
		})
	}
}
