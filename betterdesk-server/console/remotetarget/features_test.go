package remotetarget

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/ws/wstest"
)

// The tunnel carries the limits the account's client features set: read
// only without remote control, no clipboard, no sound; none with every
// feature on; all of them when the lookup fails.
func TestTheTunnelCarriesTheAccountsLimits(t *testing.T) {
	for _, c := range []struct {
		name string
		off  []string
		err  error
		want string
	}{
		{"only other features off", []string{"file_transfer", "terminal"}, nil, "/api/remote-targets/rt_echo-0001/tunnel"},
		{"remote control off", []string{"remote_control"}, nil, "/api/remote-targets/rt_echo-0001/tunnel?read_only=1"},
		{"clipboard and audio off", []string{"audio", "clipboard"}, nil, "/api/remote-targets/rt_echo-0001/tunnel?no_clipboard=1&no_audio=1"},
		{"lookup failed", nil, errors.New("store down"), "/api/remote-targets/rt_echo-0001/tunnel?read_only=1&no_clipboard=1&no_audio=1"},
	} {
		t.Run(c.name, func(t *testing.T) {
			check := wstest.Goroutines(t)
			var mu sync.Mutex
			var targets []string
			var asked []int64
			w := &world{}
			addr, stop := gateway(t, w, func(o *Options) {
				o.FeaturesOff = func(_ context.Context, userID int64) ([]string, error) {
					mu.Lock()
					defer mu.Unlock()
					asked = append(asked, userID)
					return c.off, c.err
				}
				o.API = http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
					mu.Lock()
					targets = append(targets, r.URL.RequestURI())
					mu.Unlock()
					http.NotFound(rw, r)
				})
			})
			conn := open(t, addr, "rt_echo-0001")
			conn.Wait(2*time.Second, func(_ []wstest.Frame, closed bool) bool { return closed })
			conn.Drop()
			stop()
			mu.Lock()
			defer mu.Unlock()
			if len(targets) != 1 || targets[0] != c.want {
				t.Errorf("tunnel %v, want %s", targets, c.want)
			}
			if len(asked) != 1 || asked[0] != 7 {
				t.Errorf("features looked up for %v, want the account 7", asked)
			}
			check()
		})
	}
}
