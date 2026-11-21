package cdap

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/unitronix/betterdesk-server/auth"
	"github.com/unitronix/betterdesk-server/config"
	"github.com/unitronix/betterdesk-server/db"
	"github.com/unitronix/betterdesk-server/events"
	"github.com/unitronix/betterdesk-server/peer"
)

// TestForwardedAddressNeedsATrustedProxy: the per-address limit counts the
// socket's address; X-Real-IP and X-Forwarded-For count only when the
// connection comes from a trusted proxy (BUGS RV161).
func TestForwardedAddressNeedsATrustedProxy(t *testing.T) {
	for _, trusted := range []bool{false, true} {
		cfg := config.DefaultConfig()
		cfg.CDAPRateLimit = 3
		if trusted {
			nets, err := config.ParseTrustedProxies("127.0.0.1")
			if err != nil {
				t.Fatal(err)
			}
			cfg.TrustProxy, cfg.TrustedProxies = true, nets
		}
		g := New(cfg, nil, peer.NewMap(), events.NewBus())
		g.ctx = context.Background()
		srv := httptest.NewServer(http.HandlerFunc(g.handleWebSocket))
		url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/cdap"
		refused := 0
		for i := 0; i < 6; i++ {
			header := http.Header{}
			if i%2 == 0 {
				header.Set("X-Real-IP", "198.51.100."+strconv.Itoa(i))
			} else {
				header.Set("X-Forwarded-For", "203.0.113."+strconv.Itoa(i)+", 10.0.0.1")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			c, resp, err := websocket.Dial(ctx, url, &websocket.DialOptions{Subprotocols: []string{"cdap-v1"}, HTTPHeader: header})
			cancel()
			if err == nil {
				c.CloseNow()
			} else if resp != nil && resp.StatusCode == http.StatusTooManyRequests {
				refused++
			}
		}
		srv.Close()
		if want := map[bool]int{false: 3, true: 0}[trusted]; refused != want {
			t.Errorf("trusted proxy %v: %d of 6 refused, want %d", trusted, refused, want)
		}
	}
}

// TestFailedPasswordSignInsAreCounted: failed password sign-ins are counted
// per account and per address; when either has had too many, even the
// right password is refused (BUGS RV161).
func TestFailedPasswordSignInsAreCounted(t *testing.T) {
	newGateway := func(t *testing.T) *Gateway {
		w := newCDAPWorld(t)
		for _, name := range []string{"anna", "ben"} {
			hash, err := auth.HashPassword("right-" + name)
			if err != nil {
				t.Fatal(err)
			}
			if err := w.db.CreateUser(&db.User{Username: name, PasswordHash: hash, Role: "operator"}); err != nil {
				t.Fatal(err)
			}
		}
		return w.g
	}
	signIn := func(g *Gateway, name, password, ip string) error {
		_, _, err := g.authUserPassword(AuthPayload{Username: name, Password: password}, ip)
		return err
	}

	t.Run("per account", func(t *testing.T) {
		g := newGateway(t)
		for i := 0; i < 5; i++ {
			if signIn(g, "anna", "wrong", "198.51.100."+strconv.Itoa(i)) == nil {
				t.Fatal("a wrong password was taken")
			}
		}
		if err := signIn(g, "anna", "right-anna", "198.51.100.99"); err == nil {
			t.Error("the account took a sign-in after five failures from other addresses")
		}
		if err := signIn(g, "ben", "right-ben", "198.51.100.99"); err != nil {
			t.Errorf("another account: %v", err)
		}
	})
	t.Run("per address", func(t *testing.T) {
		g := newGateway(t)
		for i := 0; i < 5; i++ {
			if signIn(g, "user"+strconv.Itoa(i), "wrong", "198.51.100.1") == nil {
				t.Fatal("an unknown account was taken")
			}
		}
		if err := signIn(g, "anna", "right-anna", "198.51.100.1"); err == nil {
			t.Error("the address took a sign-in after five failures")
		}
		if err := signIn(g, "anna", "right-anna", "198.51.100.2"); err != nil {
			t.Errorf("another address: %v", err)
		}
	})
}
