package peerdir

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/neodeskapp/neodesk/neodesk-server/peer"
)

// testClient connects to NEODESK_TEST_VALKEY_URL and returns it with a
// random key prefix whose keys are deleted at the end of the test.
func testClient(t *testing.T) (redis.UniversalClient, string) {
	t.Helper()
	raw := os.Getenv("NEODESK_TEST_VALKEY_URL")
	if raw == "" {
		if os.Getenv("NEODESK_TEST_REQUIRE_VALKEY") == "1" {
			t.Fatal("NEODESK_TEST_REQUIRE_VALKEY=1 but NEODESK_TEST_VALKEY_URL is not set")
		}
		t.Skip("NEODESK_TEST_VALKEY_URL not set")
	}
	opts, err := redis.ParseURL(raw)
	if err != nil {
		t.Fatalf("NEODESK_TEST_VALKEY_URL: %v", err)
	}
	client := redis.NewClient(opts)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatalf("Valkey does not answer: %v", err)
	}
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	prefix := "peerdirtest:" + hex.EncodeToString(b) + ":"
	t.Cleanup(func() {
		ctx := context.Background()
		keys, _ := client.Keys(ctx, prefix+"*").Result()
		if len(keys) > 0 {
			client.Del(ctx, keys...)
		}
		client.Close()
	})
	return client, prefix
}

func udp(s string) *net.UDPAddr {
	a, err := net.ResolveUDPAddr("udp", s)
	if err != nil {
		panic(err)
	}
	return a
}

func entry(id, addr string) *peer.Entry {
	now := time.Now()
	return &peer.Entry{
		ID: id, UUID: []byte("uuid-" + id), PK: []byte{1, 2, 3, 4},
		IP: addr, UDPAddr: udp(addr), NATType: 2, Serial: 5, ConnType: peer.ConnUDP,
		LastReg: now, FirstSeen: now.Add(-time.Minute), Banned: true, LocalAddr: "192.168.1.5:21116",
		Version: "1.4.2", RelayServer: "relay.example:21117", StatusTier: peer.StatusOnline,
	}
}

func TestPublishLookupWithdraw(t *testing.T) {
	client, prefix := testClient(t)
	a := New(client, prefix, "pod-a")
	b := New(client, prefix, "pod-b")

	e := entry("PEER1", "203.0.113.10:40001")
	a.Publish([]*peer.Entry{e, entry("PEER2", "203.0.113.10:40002")})

	got, ok := b.Lookup("PEER1")
	if !ok {
		t.Fatal("B doesn't see A's peer")
	}
	if got.Owner != "pod-a" || got.IP != e.IP || got.UDPAddr.String() != e.IP || got.NATType != 2 || got.Serial != 5 ||
		got.ConnType != peer.ConnUDP || string(got.PK) != string(e.PK) || string(got.UUID) != string(e.UUID) ||
		!got.Banned || got.LocalAddr != e.LocalAddr || got.Version != e.Version || got.RelayServer != e.RelayServer ||
		got.StatusTier != peer.StatusOnline || got.LastReg.UnixMilli() != e.LastReg.UnixMilli() ||
		got.FirstSeen.UnixMilli() != e.FirstSeen.UnixMilli() || got.TCPConn != nil || got.WSConn != nil {
		t.Fatalf("B read %+v", got)
	}
	if _, ok := a.Lookup("PEER1"); ok {
		t.Fatal("A reads its own entry back from the directory")
	}
	if got, ok := b.LookupAddr("203.0.113.10:40001"); !ok || got.ID != "PEER1" {
		t.Fatalf("LookupAddr = %+v %v", got, ok)
	}
	if got, ok := b.LookupAddr("[::ffff:203.0.113.10]:40002"); !ok || got.ID != "PEER2" {
		t.Fatalf("LookupAddr of the IPv4-mapped form = %+v %v", got, ok)
	}
	if all := b.ByIP("203.0.113.10"); len(all) != 2 {
		t.Fatalf("ByIP = %d entries, want 2", len(all))
	}
	if many := b.LookupMany([]string{"PEER1", "NOPE", "PEER2"}); len(many) != 2 || many["PEER2"] == nil {
		t.Fatalf("LookupMany = %v", many)
	}

	a.Withdraw([]string{"PEER1"})
	if _, ok := b.Lookup("PEER1"); ok {
		t.Fatal("B still sees a withdrawn peer")
	}
	if _, ok := b.LookupAddr("203.0.113.10:40001"); ok {
		t.Fatal("B still finds a withdrawn peer by address")
	}
	if all := b.ByIP("203.0.113.10"); len(all) != 1 || all[0].ID != "PEER2" {
		t.Fatalf("ByIP after withdraw = %+v", all)
	}
	if n, _ := client.Exists(context.Background(), prefix+"peeraddr:203.0.113.10:40001").Result(); n != 0 {
		t.Fatal("withdraw left the address key")
	}
}

func TestWithdrawKeepsTheEntryOfThePodThatTookThePeerOver(t *testing.T) {
	client, prefix := testClient(t)
	a := New(client, prefix, "pod-a")
	b := New(client, prefix, "pod-b")
	c := New(client, prefix, "pod-c")

	a.Publish([]*peer.Entry{entry("MOVER", "198.51.100.20:5000")})
	time.Sleep(5 * time.Millisecond)
	// The peer reconnects through pod B from the same address.
	b.Publish([]*peer.Entry{entry("MOVER", "198.51.100.20:5000")})
	a.Withdraw([]string{"MOVER"})

	got, ok := c.Lookup("MOVER")
	if !ok || got.Owner != "pod-b" {
		t.Fatalf("A's withdraw removed B's entry: %+v %v", got, ok)
	}
	if got, ok := c.LookupAddr("198.51.100.20:5000"); !ok || got.Owner != "pod-b" {
		t.Fatalf("A's withdraw removed B's address key: %+v %v", got, ok)
	}
	if all := c.ByIP("198.51.100.20"); len(all) != 1 {
		t.Fatalf("A's withdraw removed B's IP member: %+v", all)
	}
	// A's stale entry is republished (say its status tier changed): B's
	// newer registration stays.
	stale := entry("MOVER", "198.51.100.21:5000")
	stale.LastReg = time.Now().Add(-5 * time.Second)
	a.Publish([]*peer.Entry{stale})
	if got, ok := c.Lookup("MOVER"); !ok || got.Owner != "pod-b" || got.IP != "198.51.100.20:5000" {
		t.Fatalf("A's stale publish overwrote B's entry: %+v %v", got, ok)
	}
	if got, ok := c.LookupAddr("198.51.100.21:5000"); ok {
		t.Fatalf("the stale address finds the peer: %+v", got)
	}
}

func TestEveryKeyHasATTLAndExpiredEntriesVanish(t *testing.T) {
	client, prefix := testClient(t)
	a := New(client, prefix, "pod-a")
	b := New(client, prefix, "pod-b")
	a.RegTimeout = 400 * time.Millisecond
	b.RegTimeout = 400 * time.Millisecond

	a.Publish([]*peer.Entry{entry("SHORT", "192.0.2.30:7000")})
	ctx := context.Background()
	keys, err := client.Keys(ctx, prefix+"*").Result()
	if err != nil || len(keys) != 3 {
		t.Fatalf("keys %v %v, want peer, peeraddr and peerip", keys, err)
	}
	for _, k := range keys {
		ttl, err := client.PTTL(ctx, k).Result()
		if err != nil || ttl <= 0 || ttl > 800*time.Millisecond {
			t.Fatalf("key %s has TTL %v (%v), want at most 2*RegTimeout", k, ttl, err)
		}
	}
	if _, ok := b.Lookup("SHORT"); !ok {
		t.Fatal("fresh entry not found")
	}

	// Past RegTimeout the peer no longer counts as live at its IP.
	time.Sleep(500 * time.Millisecond)
	if all := b.ByIP("192.0.2.30"); len(all) != 0 {
		t.Fatalf("ByIP returns a peer past RegTimeout: %+v", all)
	}
	// Past 2*RegTimeout every key is gone.
	time.Sleep(450 * time.Millisecond)
	if _, ok := b.Lookup("SHORT"); ok {
		t.Fatal("expired entry still found")
	}
	if _, ok := b.LookupAddr("192.0.2.30:7000"); ok {
		t.Fatal("expired entry still found by address")
	}
	if keys, _ := client.Keys(ctx, prefix+"*").Result(); len(keys) != 0 {
		t.Fatalf("keys left after the TTL: %v", keys)
	}
}

func TestLookupIgnoresOldEntries(t *testing.T) {
	client, prefix := testClient(t)
	a := New(client, prefix, "pod-a")
	b := New(client, prefix, "pod-b")
	e := entry("OLD", "192.0.2.40:7000")
	e.LastReg = time.Now().Add(-time.Hour) // a key written with a long TTL by mistake
	a.Publish([]*peer.Entry{e})
	if _, ok := b.Lookup("OLD"); ok {
		t.Fatal("Lookup returned an entry with a heartbeat an hour old")
	}
}

func TestDownValkeyLooksLikeNotFound(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 100 * time.Millisecond})
	defer client.Close()
	d := New(client, "x:", "pod-a")
	d.Publish([]*peer.Entry{entry("P", "192.0.2.1:1")})
	d.Withdraw([]string{"P"})
	if _, ok := d.Lookup("P"); ok {
		t.Fatal("Lookup on a dead Valkey found something")
	}
	if all := d.ByIP("192.0.2.1"); all != nil {
		t.Fatal("ByIP on a dead Valkey found something")
	}
}

func TestTwoMapsSeeEachOthersPeers(t *testing.T) {
	client, prefix := testClient(t)
	mapA, mapB := peer.NewMap(), peer.NewMap()
	mapA.SetDirectory(New(client, prefix, "pod-a"), "pod-a")
	mapB.SetDirectory(New(client, prefix, "pod-b"), "pod-b")
	defer mapA.Close()
	defer mapB.Close()

	mapA.Put(&peer.Entry{ID: "E2E", IP: "203.0.113.77:21116", UDPAddr: udp("203.0.113.77:21116"), PK: []byte{9}, LastReg: time.Now()})
	start := time.Now()
	var got *peer.Entry
	for time.Since(start) < 200*time.Millisecond {
		if got = mapB.Get("E2E"); got != nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got == nil {
		t.Fatal("map B doesn't see map A's peer within 200 ms")
	}
	if got.Owner != "pod-a" || got.UDPAddr == nil || got.UDPAddr.String() != "203.0.113.77:21116" {
		t.Fatalf("map B read %+v", got)
	}
	if !mapB.IsOnline("E2E", 30*time.Second) {
		t.Fatal("map B doesn't count A's peer as online")
	}
	if e := mapB.FindByAddr(udp("203.0.113.77:21116")); e == nil || e.ID != "E2E" {
		t.Fatalf("map B FindByAddr = %+v", e)
	}
	if n := mapB.CountByIP(net.ParseIP("203.0.113.77")); n != 1 {
		t.Fatalf("map B CountByIP = %d", n)
	}
	if got := mapA.Get("E2E"); got == nil || got.Owner != "" {
		t.Fatalf("map A Get = %+v, want its own entry", got)
	}

	mapA.Remove("E2E")
	deadline := time.Now().Add(time.Second)
	for mapB.Get("E2E") != nil {
		if time.Now().After(deadline) {
			t.Fatal("map B still sees the peer A removed")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestPublishSaysWhoHadThePeerAndRefusesAStaleOne: the pod that takes a
// peer over learns the old owner; the old pod's later publish of its
// older entry is refused and reported.
func TestPublishSaysWhoHadThePeerAndRefusesAStaleOne(t *testing.T) {
	client, prefix := testClient(t)
	a := New(client, prefix, "pod-a")
	b := New(client, prefix, "pod-b")

	old := entry("MOVED2", "198.51.100.21:5000")
	if res := a.Publish([]*peer.Entry{old}); len(res.Took) != 0 || len(res.Refused) != 0 {
		t.Fatalf("first publish: %+v", res)
	}
	time.Sleep(5 * time.Millisecond)
	if res := b.Publish([]*peer.Entry{entry("MOVED2", "198.51.100.21:5000")}); res.Took["MOVED2"] != "pod-a" {
		t.Fatalf("takeover publish: %+v, want Took MOVED2 from pod-a", res)
	}
	if res := a.Publish([]*peer.Entry{old}); len(res.Refused) != 1 || res.Refused[0] != "MOVED2" {
		t.Fatalf("stale publish: %+v, want MOVED2 refused", res)
	}
}
