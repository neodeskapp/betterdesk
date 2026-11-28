package relay

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// ticketStore is both sides of a ticket store, what the conformance suite
// runs against.
type ticketStore interface {
	Tickets
	TicketClaims
}

func TestInMemoryTicketStore(t *testing.T) {
	runTicketStoreSuite(t, func(t *testing.T) ticketStore { return NewAuthorizationRegistry() })
}

func TestValkeyTicketStore(t *testing.T) {
	opts := testValkeyOptions(t)
	runTicketStoreSuite(t, func(t *testing.T) ticketStore {
		return newTestValkeyTickets(t, opts, randomTestPrefix(t))
	})
}

// TestValkeyTicketStoreAcrossInstances: two stores on two clients share
// one prefix, as two server pods share one Valkey.
func TestValkeyTicketStoreAcrossInstances(t *testing.T) {
	opts := testValkeyOptions(t)
	prefix := randomTestPrefix(t)
	a := newTestValkeyTickets(t, opts, prefix)
	b := newTestValkeyTickets(t, opts, prefix)
	ctx := t.Context()

	if err := a.Authorize(ctx, Ticket{UUID: "x-claim", Initiator: "INIT", Target: "TGT", Account: 5, ConsoleSession: "h"}); err != nil {
		t.Fatalf("authorize on A: %v", err)
	}
	if !b.Admitted("x-claim", "INIT", "TGT") {
		t.Fatal("B doesn't see A's ticket")
	}
	if !b.Claim("x-claim") || !b.Claim("x-claim") {
		t.Fatal("B can't claim A's ticket twice")
	}
	if a.Claim("x-claim") {
		t.Fatal("A claims a third time what B spent")
	}
	pair, ok := a.Live("x-claim")
	if !ok || pair.Initiator != "INIT" || pair.Target != "TGT" || pair.Account != 5 || pair.ConsoleSession != "h" {
		t.Fatalf("Live on A = %+v, %v", pair, ok)
	}

	// Only the message on relay:auth can wake B: it no longer asks again
	b.recheck = time.Hour
	waitForSubscribers(t, a.client, a.authChannel(), 2)
	waitCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	woke := make(chan bool, 1)
	go func() { woke <- b.WaitForAuthorization(waitCtx, "x-wait") }()
	time.Sleep(100 * time.Millisecond)
	if err := a.Authorize(ctx, Ticket{UUID: "x-wait", Initiator: "INIT", Target: "TGT"}); err != nil {
		t.Fatalf("authorize on A: %v", err)
	}
	select {
	case got := <-woke:
		if !got {
			t.Fatal("B's wait ended without the ticket")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("B's wait never woke on A's ticket")
	}
}

// TestValkeyTicketStoreUnavailable: with nothing listening, Authorize says
// the store is unavailable (not that it refused) and Claim refuses.
func TestValkeyTicketStoreUnavailable(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 200 * time.Millisecond, MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })
	store := NewValkeyTickets(client, "neodesk-test:")
	t.Cleanup(func() { _ = store.Close() })

	err := store.Authorize(t.Context(), Ticket{UUID: "down", Initiator: "INIT", Target: "TGT"})
	if !errors.Is(err, ErrTicketsUnavailable) || errors.Is(err, ErrTicketRejected) {
		t.Fatalf("Authorize = %v, want ErrTicketsUnavailable", err)
	}
	if store.Claim("down") {
		t.Fatal("Claim succeeds without a store")
	}
	if store.Admitted("down", "INIT", "TGT") {
		t.Fatal("Admitted without a store")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	if store.WaitForAuthorization(ctx, "down") {
		t.Fatal("WaitForAuthorization succeeds without a store")
	}
}

func runTicketStoreSuite(t *testing.T, newStore func(t *testing.T) ticketStore) {
	t.Run("RetryBySamePairOnly", func(t *testing.T) {
		s := newStore(t)
		ctx := t.Context()
		ticket := Ticket{UUID: "u-retry", Initiator: "INIT", Target: "TGT", Account: 3, ConsoleSession: "h"}
		if err := s.Authorize(ctx, ticket); err != nil {
			t.Fatalf("issue: %v", err)
		}
		if err := s.Authorize(ctx, ticket); err != nil {
			t.Fatalf("retry by the same pair: %v", err)
		}
		for name, change := range map[string]func(*Ticket){
			"initiator": func(t *Ticket) { t.Initiator = "OTHER" },
			"target":    func(t *Ticket) { t.Target = "OTHER" },
			"account":   func(t *Ticket) { t.Account = 4 },
			"session":   func(t *Ticket) { t.ConsoleSession = "h2" },
		} {
			other := ticket
			change(&other)
			if err := s.Authorize(ctx, other); !errors.Is(err, ErrTicketRejected) {
				t.Errorf("retry with another %s: %v, want ErrTicketRejected", name, err)
			}
		}
		for _, bad := range []Ticket{
			{UUID: "", Initiator: "INIT", Target: "TGT"},
			{UUID: "u-empty", Initiator: "", Target: "TGT"},
			{UUID: "u-empty", Initiator: "INIT", Target: ""},
		} {
			if err := s.Authorize(ctx, bad); !errors.Is(err, ErrTicketRejected) {
				t.Errorf("Authorize(%+v) = %v, want ErrTicketRejected", bad, err)
			}
		}
	})

	t.Run("TwoClaimsThenReplayRejected", func(t *testing.T) {
		s := newStore(t)
		ctx := t.Context()
		ticket := Ticket{UUID: "u-claims", Initiator: "INIT", Target: "TGT"}
		if err := s.Authorize(ctx, ticket); err != nil {
			t.Fatal(err)
		}
		if !s.Claim("u-claims") || !s.Claim("u-claims") {
			t.Fatal("the two sides can't claim")
		}
		if s.Claim("u-claims") {
			t.Fatal("a third claim succeeded")
		}
		if err := s.Authorize(ctx, ticket); !errors.Is(err, ErrTicketRejected) {
			t.Fatalf("replay of a spent ticket: %v, want ErrTicketRejected", err)
		}
		if s.Claim("u-never-issued") {
			t.Fatal("claim of a UUID without a ticket")
		}
	})

	t.Run("ReleaseGivesBackAFirstClaim", func(t *testing.T) {
		s := newStore(t)
		if err := s.Authorize(t.Context(), Ticket{UUID: "u-release", Initiator: "INIT", Target: "TGT"}); err != nil {
			t.Fatal(err)
		}
		if !s.Claim("u-release") {
			t.Fatal("first claim")
		}
		s.Release("u-release")
		if !s.Claim("u-release") || !s.Claim("u-release") {
			t.Fatal("after Release a new pair of claims fails")
		}
		if s.Claim("u-release") {
			t.Fatal("Release gave back more than one claim")
		}
		s.Release("u-release") // spent: nothing to give back
		if s.Claim("u-release") {
			t.Fatal("Release revived a spent ticket")
		}
	})

	t.Run("Expiry", func(t *testing.T) {
		s := newStore(t)
		if err := s.Authorize(t.Context(), Ticket{UUID: "u-expiry", Initiator: "INIT", Target: "TGT", Expires: time.Now().Add(300 * time.Millisecond)}); err != nil {
			t.Fatal(err)
		}
		if !s.Admitted("u-expiry", "INIT", "TGT") {
			t.Fatal("not admitted before its expiry")
		}
		time.Sleep(600 * time.Millisecond)
		if s.Admitted("u-expiry", "INIT", "TGT") {
			t.Fatal("admitted after its expiry")
		}
		if s.Claim("u-expiry") {
			t.Fatal("claimed after its expiry")
		}
	})

	t.Run("RevokeForPeer", func(t *testing.T) {
		s := newStore(t)
		ctx := t.Context()
		for _, ticket := range []Ticket{
			{UUID: "u-ban-init", Initiator: "BANNED", Target: "TGT"},
			{UUID: "u-ban-target", Initiator: "INIT", Target: "BANNED"},
			{UUID: "u-ban-half", Initiator: "BANNED", Target: "TGT2"},
			{UUID: "u-ban-live", Initiator: "BANNED", Target: "TGT3"},
			{UUID: "u-other", Initiator: "INIT", Target: "TGT"},
		} {
			if err := s.Authorize(ctx, ticket); err != nil {
				t.Fatalf("issue %s: %v", ticket.UUID, err)
			}
		}
		if !s.Claim("u-ban-half") {
			t.Fatal("first claim of u-ban-half")
		}
		if !s.Claim("u-ban-live") || !s.Claim("u-ban-live") {
			t.Fatal("claims of u-ban-live")
		}
		s.RevokeForPeer("BANNED")
		for _, uuid := range []string{"u-ban-init", "u-ban-target", "u-ban-half"} {
			if s.Claim(uuid) {
				t.Errorf("%s claimed after its peer was revoked", uuid)
			}
		}
		if err := s.Authorize(ctx, Ticket{UUID: "u-ban-init", Initiator: "BANNED", Target: "TGT"}); !errors.Is(err, ErrTicketRejected) {
			t.Errorf("re-authorize of a revoked ticket: %v, want ErrTicketRejected", err)
		}
		if err := s.Authorize(ctx, Ticket{UUID: "u-ban-target", Initiator: "X", Target: "Y"}); !errors.Is(err, ErrTicketRejected) {
			t.Errorf("a revoked UUID taken by another pair: %v, want ErrTicketRejected", err)
		}
		if _, ok := s.Live("u-ban-live"); !ok {
			t.Error("revoke ended a live session")
		}
		if !s.Admitted("u-other", "INIT", "TGT") || !s.Claim("u-other") {
			t.Error("revoke dropped a ticket of other peers")
		}
	})

	t.Run("LiveUntilEnd", func(t *testing.T) {
		s := newStore(t)
		if err := s.Authorize(t.Context(), Ticket{UUID: "u-live", Initiator: "INIT", Target: "TGT", Account: 42, ConsoleSession: "h-1"}); err != nil {
			t.Fatal(err)
		}
		if !s.Claim("u-live") {
			t.Fatal("first claim")
		}
		if _, ok := s.Live("u-live"); ok {
			t.Fatal("live with one side")
		}
		before := time.Now()
		if !s.Claim("u-live") {
			t.Fatal("second claim")
		}
		pair, ok := s.Live("u-live")
		if !ok {
			t.Fatal("not live after two claims")
		}
		if pair.Initiator != "INIT" || pair.Target != "TGT" || pair.Account != 42 || pair.ConsoleSession != "h-1" {
			t.Fatalf("Live = %+v", pair)
		}
		if d := pair.Since.Sub(before); d < -time.Minute || d > time.Minute {
			t.Fatalf("Since %v is far from the second claim at %v", pair.Since, before)
		}
		s.End("u-live")
		if _, ok := s.Live("u-live"); ok {
			t.Fatal("live after End")
		}
		if _, ok := s.Live("u-unknown"); ok {
			t.Fatal("an unknown UUID is live")
		}
	})

	t.Run("Admitted", func(t *testing.T) {
		s := newStore(t)
		if err := s.Authorize(t.Context(), Ticket{UUID: "u-adm", Initiator: "INIT", Target: "TGT"}); err != nil {
			t.Fatal(err)
		}
		if !s.Admitted("u-adm", "INIT", "TGT") {
			t.Fatal("unclaimed ticket not admitted")
		}
		if s.Admitted("u-adm", "TGT", "INIT") || s.Admitted("u-adm", "INIT", "OTHER") {
			t.Fatal("another pair admitted on an unclaimed ticket")
		}
		if !s.Claim("u-adm") || !s.Claim("u-adm") {
			t.Fatal("claims")
		}
		if !s.Admitted("u-adm", "INIT", "TGT") {
			t.Fatal("live session not admitted")
		}
		if s.Admitted("u-adm", "OTHER", "TGT") {
			t.Fatal("another pair admitted on a live session")
		}
		s.End("u-adm")
		if s.Admitted("u-adm", "INIT", "TGT") {
			t.Fatal("admitted after End")
		}
		if s.Admitted("", "INIT", "TGT") {
			t.Fatal("empty UUID admitted")
		}
	})

	t.Run("WaitForAuthorization", func(t *testing.T) {
		s := newStore(t)
		ctx := t.Context()

		waitCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		woke := make(chan bool, 1)
		go func() { woke <- s.WaitForAuthorization(waitCtx, "u-late") }()
		time.Sleep(100 * time.Millisecond)
		if err := s.Authorize(ctx, Ticket{UUID: "u-late", Initiator: "INIT", Target: "TGT"}); err != nil {
			t.Fatal(err)
		}
		if !<-woke {
			t.Fatal("wait didn't see the late ticket")
		}

		short, cancelShort := context.WithTimeout(ctx, 200*time.Millisecond)
		defer cancelShort()
		start := time.Now()
		if s.WaitForAuthorization(short, "u-nobody") {
			t.Fatal("wait for a UUID nobody authorizes returned true")
		}
		if time.Since(start) < 150*time.Millisecond {
			t.Fatal("wait gave up before its ctx ended")
		}

		if !s.Claim("u-late") || !s.Claim("u-late") {
			t.Fatal("claims")
		}
		spentCtx, cancelSpent := context.WithTimeout(ctx, 2*time.Second)
		defer cancelSpent()
		start = time.Now()
		if s.WaitForAuthorization(spentCtx, "u-late") {
			t.Fatal("wait for a spent UUID returned true")
		}
		if time.Since(start) > time.Second {
			t.Fatal("wait for a spent UUID ran until its ctx ended")
		}

		// A revoke wakes a waiter with false
		if err := s.Authorize(ctx, Ticket{UUID: "u-revoked", Initiator: "BANNED", Target: "TGT"}); err != nil {
			t.Fatal(err)
		}
		s.RevokeForPeer("BANNED")
		start = time.Now()
		if s.WaitForAuthorization(spentCtx, "u-revoked") {
			t.Fatal("wait for a revoked UUID returned true")
		}
		if time.Since(start) > time.Second {
			t.Fatal("wait for a revoked UUID ran until its ctx ended")
		}
	})

	t.Run("ConcurrentClaims", func(t *testing.T) {
		s := newStore(t)
		if err := s.Authorize(t.Context(), Ticket{UUID: "u-race", Initiator: "INIT", Target: "TGT"}); err != nil {
			t.Fatal(err)
		}
		var wins atomic.Int32
		var wg sync.WaitGroup
		start := make(chan struct{})
		for range 20 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				if s.Claim("u-race") {
					wins.Add(1)
				}
			}()
		}
		close(start)
		wg.Wait()
		if got := wins.Load(); got != 2 {
			t.Fatalf("%d of 20 concurrent claims succeeded, want 2", got)
		}
	})
}

// testValkeyOptions reads NEODESK_TEST_VALKEY_URL. Without it the test
// skips, unless NEODESK_TEST_REQUIRE_VALKEY=1 makes that a failure.
func testValkeyOptions(t *testing.T) *redis.Options {
	t.Helper()
	url := os.Getenv("NEODESK_TEST_VALKEY_URL")
	if url == "" {
		if os.Getenv("NEODESK_TEST_REQUIRE_VALKEY") == "1" {
			t.Fatal("NEODESK_TEST_REQUIRE_VALKEY=1 but NEODESK_TEST_VALKEY_URL is not set")
		}
		t.Skip("NEODESK_TEST_VALKEY_URL is not set")
	}
	opts, err := redis.ParseURL(url)
	if err != nil {
		t.Fatalf("NEODESK_TEST_VALKEY_URL: %v", err)
	}
	return opts
}

// newTestValkeyTickets opens a client and a store under prefix; cleanup
// deletes the prefix's keys and closes both.
func newTestValkeyTickets(t *testing.T, opts *redis.Options, prefix string) *ValkeyTickets {
	t.Helper()
	client := redis.NewClient(opts)
	if err := client.Ping(t.Context()).Err(); err != nil {
		_ = client.Close()
		t.Fatalf("ping Valkey: %v", err)
	}
	store := NewValkeyTickets(client, prefix)
	t.Cleanup(func() {
		_ = store.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		iter := client.Scan(ctx, 0, prefix+"*", 100).Iterator()
		for iter.Next(ctx) {
			client.Del(ctx, iter.Val())
		}
		_ = client.Close()
	})
	return store
}

func randomTestPrefix(t *testing.T) string {
	t.Helper()
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return "neodesk-test:" + hex.EncodeToString(b) + ":"
}

// waitForSubscribers waits until channel has n subscribers, so a message
// published next reaches all of them.
func waitForSubscribers(t *testing.T, client redis.UniversalClient, channel string, n int64) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		counts, err := client.PubSubNumSub(t.Context(), channel).Result()
		if err == nil && counts[channel] >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s has %d subscribers (err %v), want %d", channel, counts[channel], err, n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
