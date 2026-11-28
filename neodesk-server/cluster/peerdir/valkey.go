// Package peerdir is the cluster-wide peer directory in Valkey or Redis:
// every pod publishes the peers registered with it, so the other pods can
// read a peer's entry and see which pod owns it.
package peerdir

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/netip"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/neodeskapp/neodesk/neodesk-server/config"
	"github.com/neodeskapp/neodesk/neodesk-server/peer"
)

// Keys, all behind the prefix. None lives without a TTL, so a pod that
// dies without withdrawing its peers leaves nothing behind for long.
//
//	peer:<id>          JSON record of the peer's entry, Owner = the pod.
//	                   TTL 2*RegTimeout: a live peer heartbeats at least
//	                   every RegTimeout and its pod republishes it at least
//	                   every RegTimeout/3, so the key outlives every gap of
//	                   a live peer and goes soon after a dead pod stops.
//	peeraddr:<ip:port> the peer id at that address (UDP address and the
//	                   address of its IP field). Same TTL and reason.
//	peerip:<ip>        sorted set of the ids at that IP, scored by LastReg
//	                   in unix ms. Readers skip members older than
//	                   RegTimeout; members older than the TTL are trimmed
//	                   and the key's TTL is set to 2*RegTimeout on every
//	                   publish, so the set of an IP nobody uses expires.
const (
	keyPeer = "peer:"
	keyAddr = "peeraddr:"
	keyIP   = "peerip:"
)

// callTimeout bounds every call into Valkey: a slow directory makes a
// remote peer look unknown instead of holding up the signal path.
const callTimeout = 500 * time.Millisecond

// logEvery is the shortest time between two logged errors.
const logEvery = 10 * time.Second

// publishScript writes peer:<id> unless another pod holds a newer
// registration of the peer: a pod whose entry went stale after the peer
// moved (it still republishes on a status change) can't overwrite the new
// owner. ARGV: record, owner, LastReg ms, TTL ms. Every script touches one
// key only, so the layout works on Redis Cluster too.
var publishScript = redis.NewScript(`
local v = redis.call('GET', KEYS[1])
if v then
  local ok, d = pcall(cjson.decode, v)
  if ok and type(d) == 'table' and d['owner'] ~= ARGV[2] and (tonumber(d['last_reg']) or 0) > tonumber(ARGV[3]) then
    return 0
  end
end
redis.call('SET', KEYS[1], ARGV[1], 'PX', ARGV[4])
return 1
`)

// withdrawScript deletes peer:<id> only while this pod owns it, and
// returns the deleted record so the caller can clean the indexes. A peer
// that moved to another pod keeps that pod's entry.
var withdrawScript = redis.NewScript(`
local v = redis.call('GET', KEYS[1])
if not v then return false end
local ok, d = pcall(cjson.decode, v)
if not ok or type(d) ~= 'table' or d['owner'] ~= ARGV[1] then return false end
redis.call('DEL', KEYS[1])
return v
`)

// delIfScript deletes KEYS[1] only while it still holds ARGV[1].
var delIfScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then return redis.call('DEL', KEYS[1]) end
return 0
`)

// Directory is a peer.Directory in Valkey or Redis.
type Directory struct {
	client redis.UniversalClient
	prefix string
	self   string

	// RegTimeout is how long a peer counts as live after its last
	// heartbeat; keys live twice as long. Zero means config.RegTimeout.
	// Tests set it short to see entries expire.
	RegTimeout time.Duration

	lastLog atomic.Int64 // unix nanoseconds of the last logged error
}

var _ peer.Directory = (*Directory)(nil)
var _ peer.BatchLookup = (*Directory)(nil)

// New returns the directory of the pod named self in client, with every
// key behind prefix.
func New(client redis.UniversalClient, prefix, self string) *Directory {
	return &Directory{client: client, prefix: prefix, self: self}
}

// record is what peer:<id> holds: the entry fields other pods need.
type record struct {
	ID          string `json:"id"`
	UUID        []byte `json:"uuid,omitempty"`
	PK          []byte `json:"pk,omitempty"`
	IP          string `json:"ip,omitempty"`
	UDPAddr     string `json:"udp_addr,omitempty"`
	NATType     int32  `json:"nat_type,omitempty"`
	Serial      int32  `json:"serial,omitempty"`
	ConnType    int    `json:"conn_type"`
	LastReg     int64  `json:"last_reg"` // unix ms
	Disabled    bool   `json:"disabled,omitempty"`
	Banned      bool   `json:"banned,omitempty"`
	KeyRefused  bool   `json:"key_refused,omitempty"`
	LocalAddr   string `json:"local_addr,omitempty"`
	Version     string `json:"version,omitempty"`
	RelayServer string `json:"relay_server,omitempty"`
	FirstSeen   int64  `json:"first_seen,omitempty"` // unix ms
	StatusTier  string `json:"status_tier,omitempty"`
	Owner       string `json:"owner"`
}

func (d *Directory) regTimeout() time.Duration {
	if d.RegTimeout > 0 {
		return d.RegTimeout
	}
	return config.RegTimeout
}

func (d *Directory) ttl() time.Duration { return 2 * d.regTimeout() }

func (d *Directory) key(kind, s string) string { return d.prefix + kind + s }

// fail logs err at most once per logEvery. Errors of go-redis name the
// server's host and port at most, never the URL or its password.
func (d *Directory) fail(op string, err error) {
	if err == nil || errors.Is(err, redis.Nil) {
		return
	}
	now := time.Now().UnixNano()
	last := d.lastLog.Load()
	if last != 0 && time.Duration(now-last) < logEvery {
		return
	}
	if d.lastLog.CompareAndSwap(last, now) {
		log.Printf("[peerdir] %s failed, remote peers look offline until it works again: %v", op, err)
	}
}

func unixMs(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func fromMs(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}

// normAddr is the canonical "ip:port" of addr, "" when it is none.
func normAddr(addr string) string {
	ap, err := netip.ParseAddrPort(addr)
	if err != nil {
		return ""
	}
	return netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port()).String()
}

// normIP is the canonical form of an IP or of the host of "ip:port", ""
// when it is neither.
func normIP(s string) string {
	if a, err := netip.ParseAddr(s); err == nil {
		return a.Unmap().String()
	}
	if ap, err := netip.ParseAddrPort(s); err == nil {
		return ap.Addr().Unmap().String()
	}
	return ""
}

func toRecord(e *peer.Entry, owner string) record {
	r := record{
		ID:          e.ID,
		UUID:        e.UUID,
		PK:          e.PK,
		IP:          e.IP,
		NATType:     e.NATType,
		Serial:      e.Serial,
		ConnType:    int(e.ConnType),
		LastReg:     unixMs(e.LastReg),
		Disabled:    e.Disabled,
		Banned:      e.Banned,
		KeyRefused:  e.KeyRefused,
		LocalAddr:   e.LocalAddr,
		Version:     e.Version,
		RelayServer: e.RelayServer,
		FirstSeen:   unixMs(e.FirstSeen),
		StatusTier:  string(e.StatusTier),
		Owner:       owner,
	}
	if e.UDPAddr != nil {
		r.UDPAddr = e.UDPAddr.String()
	}
	return r
}

func (r *record) entry() *peer.Entry {
	e := &peer.Entry{
		ID:          r.ID,
		UUID:        r.UUID,
		PK:          r.PK,
		IP:          r.IP,
		NATType:     r.NATType,
		Serial:      r.Serial,
		ConnType:    peer.ConnType(r.ConnType),
		LastReg:     fromMs(r.LastReg),
		Disabled:    r.Disabled,
		Banned:      r.Banned,
		KeyRefused:  r.KeyRefused,
		LocalAddr:   r.LocalAddr,
		Version:     r.Version,
		RelayServer: r.RelayServer,
		FirstSeen:   fromMs(r.FirstSeen),
		StatusTier:  peer.Status(r.StatusTier),
		Owner:       r.Owner,
	}
	if ap, err := netip.ParseAddrPort(r.UDPAddr); err == nil {
		e.UDPAddr = net.UDPAddrFromAddrPort(netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port()))
	}
	return e
}

// addrs are the addresses r is found at: its UDP address and its IP field.
func (r *record) addrs() []string {
	var out []string
	for _, a := range []string{normAddr(r.UDPAddr), normAddr(r.IP)} {
		if a != "" && (len(out) == 0 || out[0] != a) {
			out = append(out, a)
		}
	}
	return out
}

// ips are the IPs of r's addresses.
func (r *record) ips() []string {
	var out []string
	for _, ip := range []string{normIP(r.UDPAddr), normIP(r.IP)} {
		if ip != "" && (len(out) == 0 || out[0] != ip) {
			out = append(out, ip)
		}
	}
	return out
}

// usable reports whether a record read back is another pod's and recent
// enough to be read at all.
func (d *Directory) usable(r *record) bool {
	return r.ID != "" && r.Owner != "" && r.Owner != d.self &&
		time.Since(fromMs(r.LastReg)) <= 2*d.regTimeout()
}

// Publish writes entries as owned by this pod, in one pipeline.
func (d *Directory) Publish(entries []*peer.Entry) {
	if len(entries) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	ttl := d.ttl()
	ttlMs := strconv.FormatInt(ttl.Milliseconds(), 10)
	stale := strconv.FormatInt(time.Now().Add(-ttl).UnixMilli(), 10)
	pipe := d.client.Pipeline()
	for _, e := range entries {
		if e == nil || e.ID == "" {
			continue
		}
		r := toRecord(e, d.self)
		b, err := json.Marshal(r)
		if err != nil {
			continue
		}
		publishScript.Eval(ctx, pipe, []string{d.key(keyPeer, r.ID)}, string(b), d.self, r.LastReg, ttlMs)
		// The indexes are written even when the record was not: readers
		// check every index hit against the record, and GT keeps a stale
		// publish from lowering a newer score.
		for _, a := range r.addrs() {
			pipe.Set(ctx, d.key(keyAddr, a), r.ID, ttl)
		}
		for _, ip := range r.ips() {
			k := d.key(keyIP, ip)
			pipe.ZAddGT(ctx, k, redis.Z{Score: float64(r.LastReg), Member: r.ID})
			pipe.ZRemRangeByScore(ctx, k, "-inf", "("+stale)
			pipe.PExpire(ctx, k, ttl)
		}
	}
	if _, err := pipe.Exec(ctx); err != nil {
		d.fail("publish", err)
	}
}

// Withdraw removes the entries of ids this pod still owns, with their
// address and IP index entries.
func (d *Directory) Withdraw(ids []string) {
	if len(ids) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	pipe := d.client.Pipeline()
	cmds := make([]*redis.Cmd, 0, len(ids))
	for _, id := range ids {
		cmds = append(cmds, withdrawScript.Eval(ctx, pipe, []string{d.key(keyPeer, id)}, d.self))
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		d.fail("withdraw", err)
		return
	}
	clean := d.client.Pipeline()
	n := 0
	for _, c := range cmds {
		v, err := c.Text()
		if err != nil {
			continue // not ours (any more), or already gone
		}
		var r record
		if json.Unmarshal([]byte(v), &r) != nil || r.ID == "" {
			continue
		}
		for _, a := range r.addrs() {
			delIfScript.Eval(ctx, clean, []string{d.key(keyAddr, a)}, r.ID)
			n++
		}
		for _, ip := range r.ips() {
			clean.ZRem(ctx, d.key(keyIP, ip), r.ID)
			n++
		}
	}
	if n == 0 {
		return
	}
	if _, err := clean.Exec(ctx); err != nil {
		d.fail("withdraw", err)
	}
}

// get reads the records of ids, in one pipeline. Missing and unusable
// records are left out.
func (d *Directory) get(ctx context.Context, ids []string) map[string]*record {
	if len(ids) == 0 {
		return nil
	}
	pipe := d.client.Pipeline()
	cmds := make([]*redis.StringCmd, len(ids))
	for i, id := range ids {
		cmds[i] = pipe.Get(ctx, d.key(keyPeer, id))
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		d.fail("lookup", err)
		return nil
	}
	out := make(map[string]*record, len(ids))
	for i, c := range cmds {
		b, err := c.Bytes()
		if err != nil {
			continue
		}
		var r record
		if json.Unmarshal(b, &r) != nil || r.ID != ids[i] || !d.usable(&r) {
			continue
		}
		out[r.ID] = &r
	}
	return out
}

// Lookup returns the entry of a peer registered on another pod.
func (d *Directory) Lookup(id string) (*peer.Entry, bool) {
	if id == "" {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	b, err := d.client.Get(ctx, d.key(keyPeer, id)).Bytes()
	if err != nil {
		d.fail("lookup", err)
		return nil, false
	}
	var r record
	if json.Unmarshal(b, &r) != nil || r.ID != id || !d.usable(&r) {
		return nil, false
	}
	return r.entry(), true
}

// LookupMany returns the entries of those ids registered on other pods.
func (d *Directory) LookupMany(ids []string) map[string]*peer.Entry {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	recs := d.get(ctx, ids)
	out := make(map[string]*peer.Entry, len(recs))
	for id, r := range recs {
		out[id] = r.entry()
	}
	return out
}

// LookupAddr returns the entry of the peer registered on another pod at
// addr ("ip:port").
func (d *Directory) LookupAddr(addr string) (*peer.Entry, bool) {
	want := normAddr(addr)
	if want == "" {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	id, err := d.client.Get(ctx, d.key(keyAddr, want)).Result()
	if err != nil {
		d.fail("lookup", err)
		return nil, false
	}
	r := d.get(ctx, []string{id})[id]
	if r == nil {
		return nil, false
	}
	for _, a := range r.addrs() {
		if a == want {
			return r.entry(), true
		}
	}
	return nil, false // the peer moved on from addr since
}

// ByIP returns the live peers on other pods whose address has ip.
func (d *Directory) ByIP(ip string) []*peer.Entry {
	want := normIP(ip)
	if want == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	live := time.Now().Add(-d.regTimeout())
	ids, err := d.client.ZRangeByScore(ctx, d.key(keyIP, want), &redis.ZRangeBy{
		Min: strconv.FormatInt(live.UnixMilli(), 10),
		Max: "+inf",
	}).Result()
	if err != nil {
		d.fail("lookup", err)
		return nil
	}
	recs := d.get(ctx, ids)
	var out []*peer.Entry
	for _, id := range ids {
		r := recs[id]
		if r == nil || fromMs(r.LastReg).Before(live) {
			continue
		}
		for _, have := range r.ips() {
			if have == want {
				out = append(out, r.entry())
				break
			}
		}
	}
	return out
}
