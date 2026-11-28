package relay

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/neodeskapp/neodesk/neodesk-server/config"
)

// ValkeyTickets is a ticket store in Valkey (or Redis) that several server
// pods share: a ticket that signal issues on one pod is claimed by the
// relay on another. Every check-and-change runs as one Lua script, so the
// guarantees of AuthorizationRegistry hold across pods.
//
// Keys, all under the store's prefix:
//
//	relay:t:<uuid>      hash {i, t, a, s, c}: the ticket and its claims.
//	                    Expires at the ticket's expiry (PEXPIREAT), at most
//	                    config.RelayPairTimeout after it was issued.
//	relay:used:<uuid>   tombstone of a spent or revoked ticket, so the UUID
//	                    can't be authorized again (replay). Expires when the
//	                    ticket would have expired.
//	relay:live:<uuid>   hash {i, t, a, s, since}: the running session.
//	                    Deleted by End; expires after liveMaxAge for a
//	                    session whose end the relay never reported.
//	relay:peer:<peer>   set of the UUIDs of a peer's tickets, for
//	                    RevokeForPeer. Expires with the latest ticket in it.
//
// The channel relay:auth carries a UUID whenever its ticket is issued or
// revoked, so WaitForAuthorization wakes on any pod.
//
// Scripts touch several keys at once, so on Redis Cluster the prefix must
// carry a hash tag (e.g. "{neodesk}:") that puts every key in one slot.
type ValkeyTickets struct {
	client redis.UniversalClient
	prefix string

	mu      sync.Mutex
	waiters map[string]*valkeyWaiter // local waiters per UUID

	// recheck is how often WaitForAuthorization asks again while it waits
	// (valkeyWaitRecheck; tests turn it off to see the message alone).
	recheck time.Duration

	pubsub *redis.PubSub
	cancel context.CancelFunc
	done   chan struct{}
	close  sync.Once
}

var (
	_ Tickets      = (*ValkeyTickets)(nil)
	_ TicketClaims = (*ValkeyTickets)(nil)
)

const (
	// valkeyOpTimeout bounds every call to Valkey; the calls without a ctx
	// (Claim, Admitted, Live, ...) use it alone.
	valkeyOpTimeout = 2 * time.Second
	// valkeyWaitRecheck is how often WaitForAuthorization asks again while
	// it waits, in case it missed a message (subscription reconnect).
	valkeyWaitRecheck = 250 * time.Millisecond
)

// NewValkeyTickets returns a store on client with its keys under prefix
// and starts its subscription to relay:auth. Close stops the subscription;
// the client stays open.
func NewValkeyTickets(client redis.UniversalClient, prefix string) *ValkeyTickets {
	ctx, cancel := context.WithCancel(context.Background())
	s := &ValkeyTickets{
		client:  client,
		prefix:  prefix,
		waiters: make(map[string]*valkeyWaiter),
		recheck: valkeyWaitRecheck,
		cancel:  cancel,
		done:    make(chan struct{}),
	}
	s.pubsub = client.Subscribe(ctx, s.authChannel())
	go s.listen()
	return s
}

// Close stops the subscription goroutine. It doesn't close the client.
func (s *ValkeyTickets) Close() error {
	var err error
	s.close.Do(func() {
		s.cancel()
		err = s.pubsub.Close()
		<-s.done
	})
	return err
}

func (s *ValkeyTickets) listen() {
	defer close(s.done)
	for msg := range s.pubsub.Channel() {
		s.mu.Lock()
		if w := s.waiters[msg.Payload]; w != nil {
			delete(s.waiters, msg.Payload)
			close(w.ch)
		}
		s.mu.Unlock()
	}
}

func (s *ValkeyTickets) ticketKey(uuid string) string { return s.prefix + "relay:t:" + uuid }
func (s *ValkeyTickets) usedKey(uuid string) string   { return s.prefix + "relay:used:" + uuid }
func (s *ValkeyTickets) liveKey(uuid string) string   { return s.prefix + "relay:live:" + uuid }
func (s *ValkeyTickets) peerKey(peer string) string   { return s.prefix + "relay:peer:" + peer }
func (s *ValkeyTickets) authChannel() string          { return s.prefix + "relay:auth" }

// luaNow is the server's time in unix ms; scripts compare and set expiries
// on the server's clock.
const luaNow = `local tm = redis.call('TIME')
local now = tonumber(tm[1]) * 1000 + math.floor(tonumber(tm[2]) / 1000)
`

// authorizeScript issues a ticket. KEYS: ticket, used, peer set of the
// initiator, peer set of the target. ARGV: initiator, target, account,
// session, expiry (unix ms), uuid, channel. Returns 1 when issued or an
// identical retry, 0 when refused.
var authorizeScript = redis.NewScript(luaNow + `
if redis.call('EXISTS', KEYS[2]) == 1 then return 0 end
local cur = redis.call('HMGET', KEYS[1], 'i', 't', 'a', 's')
if cur[1] then
  if cur[1] == ARGV[1] and cur[2] == ARGV[2] and cur[3] == ARGV[3] and cur[4] == ARGV[4] then return 1 end
  return 0
end
local exp = tonumber(ARGV[5])
if exp <= now then return 1 end
redis.call('HSET', KEYS[1], 'i', ARGV[1], 't', ARGV[2], 'a', ARGV[3], 's', ARGV[4], 'c', 0)
redis.call('PEXPIREAT', KEYS[1], exp)
for k = 3, 4 do
  redis.call('SADD', KEYS[k], ARGV[6])
  if redis.call('PTTL', KEYS[k]) < exp - now then redis.call('PEXPIREAT', KEYS[k], exp) end
end
redis.call('PUBLISH', ARGV[7], ARGV[6])
return 1
`)

// claimScript takes one claim. KEYS: ticket, used, live. ARGV: live max
// age (ms). The second claim deletes the ticket, tombstones the UUID for
// the rest of the ticket's life and makes the session live.
var claimScript = redis.NewScript(`
local c = redis.call('HGET', KEYS[1], 'c')
if not c then return 0 end
c = tonumber(c)
if c >= 2 then return 0 end
if c == 0 then
  redis.call('HSET', KEYS[1], 'c', 1)
  return 1
end
` + luaNow + `
local f = redis.call('HMGET', KEYS[1], 'i', 't', 'a', 's')
local ttl = redis.call('PTTL', KEYS[1])
redis.call('DEL', KEYS[1])
if ttl > 0 then redis.call('SET', KEYS[2], '1', 'PX', ttl) end
redis.call('HSET', KEYS[3], 'i', f[1], 't', f[2], 'a', f[3], 's', f[4], 'since', now)
redis.call('PEXPIRE', KEYS[3], ARGV[1])
return 1
`)

// releaseScript gives back a first claim. KEYS: ticket.
var releaseScript = redis.NewScript(`
local c = tonumber(redis.call('HGET', KEYS[1], 'c') or '0')
if c > 0 then redis.call('HINCRBY', KEYS[1], 'c', -1) end
return 0
`)

// revokeScript drops one ticket of a peer. KEYS: ticket, used, peer set.
// ARGV: peer, uuid, channel. The tombstone lives as long as the ticket
// would have. A UUID whose ticket is gone or names other peers only
// leaves the set.
var revokeScript = redis.NewScript(`
redis.call('SREM', KEYS[3], ARGV[2])
local f = redis.call('HMGET', KEYS[1], 'i', 't')
if not f[1] or (f[1] ~= ARGV[1] and f[2] ~= ARGV[1]) then return 0 end
local ttl = redis.call('PTTL', KEYS[1])
redis.call('DEL', KEYS[1])
if ttl > 0 then redis.call('SET', KEYS[2], '1', 'PX', ttl) end
redis.call('PUBLISH', ARGV[3], ARGV[2])
return 1
`)

func unavailable(op string, err error) error {
	return fmt.Errorf("relay tickets: %s: %w: %w", op, ErrTicketsUnavailable, err)
}

func logStoreError(op, uuid string, err error) {
	log.Printf("[relay] Ticket store %s of UUID %s failed: %v", op, relayUUIDLogID(uuid), err)
}

// Authorize issues t (Tickets).
func (s *ValkeyTickets) Authorize(ctx context.Context, t Ticket) error {
	if t.UUID == "" || t.Initiator == "" || t.Target == "" {
		return ErrTicketRejected
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, valkeyOpTimeout)
	defer cancel()

	expires := time.Now().Add(config.RelayPairTimeout)
	if !t.Expires.IsZero() && t.Expires.Before(expires) {
		expires = t.Expires
	}
	keys := []string{s.ticketKey(t.UUID), s.usedKey(t.UUID), s.peerKey(t.Initiator), s.peerKey(t.Target)}
	issued, err := authorizeScript.Run(ctx, s.client, keys,
		t.Initiator, t.Target, strconv.FormatInt(t.Account, 10), t.ConsoleSession,
		expires.UnixMilli(), t.UUID, s.authChannel()).Int()
	if err != nil {
		return unavailable("authorize", err)
	}
	if issued != 1 {
		return ErrTicketRejected
	}
	return nil
}

// Admitted reports whether uuid has a ticket for the pair, unclaimed or
// live (Tickets).
func (s *ValkeyTickets) Admitted(uuid, initiator, target string) bool {
	if uuid == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), valkeyOpTimeout)
	defer cancel()
	var ticket, live *redis.SliceCmd
	_, err := s.client.TxPipelined(ctx, func(p redis.Pipeliner) error {
		ticket = p.HMGet(ctx, s.ticketKey(uuid), "i", "t")
		live = p.HMGet(ctx, s.liveKey(uuid), "i", "t")
		return nil
	})
	if err != nil {
		logStoreError("admission check", uuid, err)
		return false
	}
	if pair := ticket.Val(); pair[0] != nil {
		return pair[0] == initiator && pair[1] == target
	}
	pair := live.Val()
	return pair[0] != nil && pair[0] == initiator && pair[1] == target
}

// RevokeForPeer drops the unclaimed tickets of peer and tombstones their
// UUIDs (Tickets).
func (s *ValkeyTickets) RevokeForPeer(peer string) {
	if peer == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), valkeyOpTimeout)
	defer cancel()
	peerKey := s.peerKey(peer)
	uuids, err := s.client.SMembers(ctx, peerKey).Result()
	if err != nil {
		log.Printf("[relay] Ticket store revoke for a peer failed: %v", err)
		return
	}
	// Each ticket is checked and dropped atomically on its own; a ticket
	// issued after the SMEMBERS was issued after the ban.
	for _, uuid := range uuids {
		keys := []string{s.ticketKey(uuid), s.usedKey(uuid), peerKey}
		if err := revokeScript.Run(ctx, s.client, keys, peer, uuid, s.authChannel()).Err(); err != nil {
			logStoreError("revoke", uuid, err)
		}
	}
}

// Live returns the session of uuid while it runs: from the second claim
// of its ticket until End.
func (s *ValkeyTickets) Live(uuid string) (LivePair, bool) {
	if uuid == "" {
		return LivePair{}, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), valkeyOpTimeout)
	defer cancel()
	f, err := s.client.HGetAll(ctx, s.liveKey(uuid)).Result()
	if err != nil {
		logStoreError("live lookup", uuid, err)
		return LivePair{}, false
	}
	if len(f) == 0 {
		return LivePair{}, false
	}
	account, _ := strconv.ParseInt(f["a"], 10, 64)
	since, _ := strconv.ParseInt(f["since"], 10, 64)
	return LivePair{
		Initiator:      f["i"],
		Target:         f["t"],
		Account:        account,
		ConsoleSession: f["s"],
		Since:          time.UnixMilli(since),
	}, true
}

// Claim reserves one of the two connections of uuid; the second claim
// spends the ticket and makes the session live (TicketClaims).
func (s *ValkeyTickets) Claim(uuid string) bool {
	if uuid == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), valkeyOpTimeout)
	defer cancel()
	keys := []string{s.ticketKey(uuid), s.usedKey(uuid), s.liveKey(uuid)}
	claimed, err := claimScript.Run(ctx, s.client, keys, liveMaxAge.Milliseconds()).Int()
	if err != nil {
		logStoreError("claim", uuid, err)
		return false
	}
	return claimed == 1
}

// Release returns a first claim whose partner never came (TicketClaims).
func (s *ValkeyTickets) Release(uuid string) {
	if uuid == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), valkeyOpTimeout)
	defer cancel()
	if err := releaseScript.Run(ctx, s.client, []string{s.ticketKey(uuid)}).Err(); err != nil {
		logStoreError("release", uuid, err)
	}
}

// End forgets the live session of uuid (TicketClaims).
func (s *ValkeyTickets) End(uuid string) {
	if uuid == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), valkeyOpTimeout)
	defer cancel()
	if err := s.client.Del(ctx, s.liveKey(uuid)).Err(); err != nil {
		logStoreError("end", uuid, err)
	}
}

// WaitForAuthorization waits until uuid has a ticket (true), its UUID is
// spent or revoked, or ctx ends (false). It wakes on relay:auth and asks
// again every valkeyWaitRecheck in case it missed a message (TicketClaims).
func (s *ValkeyTickets) WaitForAuthorization(ctx context.Context, uuid string) bool {
	if uuid == "" {
		return false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ticker := time.NewTicker(s.recheck)
	defer ticker.Stop()
	logged := false
	for {
		// Register before the check, so a message between the check and
		// the select still wakes this waiter.
		waiter := s.addWaiter(uuid)
		authorized, spent, err := s.authorizationState(ctx, uuid)
		switch {
		case err == nil && spent:
			s.removeWaiter(uuid, waiter)
			return false
		case err == nil && authorized:
			s.removeWaiter(uuid, waiter)
			return true
		case err != nil && ctx.Err() == nil && !logged:
			logStoreError("authorization wait", uuid, err)
			logged = true
		}
		select {
		case <-waiter.ch:
			s.removeWaiter(uuid, waiter)
		case <-ticker.C:
			s.removeWaiter(uuid, waiter)
		case <-ctx.Done():
			s.removeWaiter(uuid, waiter)
			return false
		}
	}
}

func (s *ValkeyTickets) authorizationState(ctx context.Context, uuid string) (authorized, spent bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, valkeyOpTimeout)
	defer cancel()
	var used, ticket *redis.IntCmd
	_, err = s.client.TxPipelined(ctx, func(p redis.Pipeliner) error {
		used = p.Exists(ctx, s.usedKey(uuid))
		ticket = p.Exists(ctx, s.ticketKey(uuid))
		return nil
	})
	if err != nil {
		return false, false, err
	}
	return ticket.Val() == 1, used.Val() == 1, nil
}

// valkeyWaiter is the channel that the local waiters of one UUID share;
// a message on relay:auth closes it. n counts the waiters holding it.
type valkeyWaiter struct {
	ch chan struct{}
	n  int
}

func (s *ValkeyTickets) addWaiter(uuid string) *valkeyWaiter {
	s.mu.Lock()
	defer s.mu.Unlock()
	w := s.waiters[uuid]
	if w == nil {
		w = &valkeyWaiter{ch: make(chan struct{})}
		s.waiters[uuid] = w
	}
	w.n++
	return w
}

// removeWaiter drops one waiter; the last one takes the channel out of
// the map unless a message already did.
func (s *ValkeyTickets) removeWaiter(uuid string, w *valkeyWaiter) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w.n--
	if w.n == 0 && s.waiters[uuid] == w {
		delete(s.waiters, uuid)
	}
}
