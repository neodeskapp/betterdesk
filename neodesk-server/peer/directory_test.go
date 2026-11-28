package peer

import (
	"net"
	"runtime"
	"sync"
	"testing"
	"time"
)

// fakeDir is a Directory in memory. remote holds entries of other pods;
// Publish and Withdraw are recorded.
type fakeDir struct {
	mu        sync.Mutex
	remote    map[string]*Entry
	published [][]*Entry
	withdrawn [][]string
}

func newFakeDir() *fakeDir { return &fakeDir{remote: map[string]*Entry{}} }

func (f *fakeDir) put(e *Entry) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.remote[e.ID] = e
}

func (f *fakeDir) Publish(entries []*Entry) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range entries {
		if e.TCPConn != nil || e.WSConn != nil {
			panic("published entry carries a connection")
		}
	}
	f.published = append(f.published, entries)
}

func (f *fakeDir) Withdraw(ids []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.withdrawn = append(f.withdrawn, ids)
}

func cp(e *Entry) *Entry { c := *e; return &c }

func (f *fakeDir) Lookup(id string) (*Entry, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.remote[id]
	if !ok {
		return nil, false
	}
	return cp(e), true
}

func (f *fakeDir) LookupAddr(addr string) (*Entry, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range f.remote {
		if (e.UDPAddr != nil && NormalizeAddr(e.UDPAddr.String()) == addr) || NormalizeAddr(e.IP) == addr {
			return cp(e), true
		}
	}
	return nil, false
}

func (f *fakeDir) ByIP(ip string) []*Entry {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*Entry
	for _, e := range f.remote {
		if peerEntryMatchesIP(e, net.ParseIP(ip)) {
			out = append(out, cp(e))
		}
	}
	return out
}

// publishedIDs is every id published so far, in order, with repeats.
func (f *fakeDir) publishedIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var ids []string
	for _, b := range f.published {
		for _, e := range b {
			ids = append(ids, e.ID)
		}
	}
	return ids
}

func (f *fakeDir) lastPublished(id string) *Entry {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.published) - 1; i >= 0; i-- {
		for _, e := range f.published[i] {
			if e.ID == id {
				return e
			}
		}
	}
	return nil
}

func (f *fakeDir) withdrawnIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var ids []string
	for _, b := range f.withdrawn {
		ids = append(ids, b...)
	}
	return ids
}

func count(ids []string, id string) int {
	n := 0
	for _, x := range ids {
		if x == id {
			n++
		}
	}
	return n
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func udp(s string) *net.UDPAddr {
	a, err := net.ResolveUDPAddr("udp", s)
	if err != nil {
		panic(err)
	}
	return a
}

func remoteEntry(id, addr string, lastReg time.Time) *Entry {
	return &Entry{ID: id, IP: addr, UDPAddr: udp(addr), ConnType: ConnUDP, LastReg: lastReg, Owner: "pod-b", PK: []byte{7, 7}, UUID: []byte{9}}
}

func dirMap(t *testing.T) (*Map, *fakeDir) {
	t.Helper()
	m := NewMap()
	f := newFakeDir()
	m.SetDirectory(f, "pod-a")
	t.Cleanup(m.Close)
	return m, f
}

func TestDirectoryLocalEntryWinsOverRemote(t *testing.T) {
	m, f := dirMap(t)
	now := time.Now()
	f.put(remoteEntry("P1", "10.0.0.2:1000", now))
	m.Put(&Entry{ID: "P1", IP: "10.0.0.1:1000", UDPAddr: udp("10.0.0.1:1000"), LastReg: now.Add(-time.Second)})

	got := m.Get("P1")
	if got == nil || got.Owner != "" || got.IP != "10.0.0.1:1000" {
		t.Fatalf("Get returned %+v, want the local entry", got)
	}
	if e := m.FindByAddr(udp("10.0.0.2:1000")); e != nil {
		t.Fatalf("FindByAddr found the remote copy of a local peer: %+v", e)
	}
	if n := m.CountByIP(net.ParseIP("10.0.0.2")); n != 0 {
		t.Fatalf("CountByIP counts the remote copy of a local peer: %d", n)
	}
	if ip, _, _ := m.Endpoint("P1"); !ip.Equal(net.ParseIP("10.0.0.1")) {
		t.Fatalf("Endpoint %v, want the local address", ip)
	}
}

func TestDirectoryReadsFallBackToRemoteEntries(t *testing.T) {
	m, f := dirMap(t)
	now := time.Now()
	f.put(remoteEntry("R1", "203.0.113.5:4000", now))
	old := remoteEntry("R2", "203.0.113.6:4000", now.Add(-time.Minute))
	f.put(old)
	refused := remoteEntry("R3", "203.0.113.7:4000", now)
	refused.KeyRefused = true
	f.put(refused)
	m.Put(&Entry{ID: "L1", IP: "198.51.100.1:1", UDPAddr: udp("198.51.100.1:1"), LastReg: now})

	if e := m.Get("R1"); e == nil || e.Owner != "pod-b" || e.IP != "203.0.113.5:4000" {
		t.Fatalf("Get R1 = %+v", e)
	}
	if e := m.FindByAddr(udp("203.0.113.5:4000")); e == nil || e.ID != "R1" {
		t.Fatalf("FindByAddr = %+v", e)
	}
	if e := m.FindByIP(net.ParseIP("203.0.113.5")); e == nil || e.ID != "R1" {
		t.Fatalf("FindByIP = %+v", e)
	}
	if n := m.CountByIP(net.ParseIP("203.0.113.5")); n != 1 {
		t.Fatalf("CountByIP = %d", n)
	}
	if ip, ct, ok := m.Endpoint("R1"); !ok || ct != ConnUDP || !ip.Equal(net.ParseIP("203.0.113.5")) {
		t.Fatalf("Endpoint = %v %v %v", ip, ct, ok)
	}
	if uuid, pk, ok := m.Keys("R1"); !ok || len(pk) != 2 || len(uuid) != 1 {
		t.Fatalf("Keys = %v %v %v", uuid, pk, ok)
	}
	if !m.IsOnline("R1", 30*time.Second) {
		t.Fatal("IsOnline R1 = false")
	}
	if m.IsOnline("R2", 30*time.Second) {
		t.Fatal("expired remote peer is online")
	}
	if m.IsOnline("R3", 30*time.Second) || !m.KeyRefused("R3") {
		t.Fatal("remote peer with a refused key is online")
	}
	// L1 local online, R1 remote online, X unknown, R2 expired, R3 refused.
	states := m.OnlineStates([]string{"L1", "R1", "X", "R2", "R3"}, 30*time.Second)
	if len(states) != 1 || states[0] != 0b11000000 {
		t.Fatalf("OnlineStates = %08b, want 11000000", states)
	}
}

func TestDirectoryCountByIPDoesNotCountAPeerTwice(t *testing.T) {
	m, f := dirMap(t)
	now := time.Now()
	m.Put(&Entry{ID: "L1", IP: "198.51.100.7:1", UDPAddr: udp("198.51.100.7:1"), LastReg: now})
	f.put(remoteEntry("L1", "198.51.100.7:2", now)) // stale copy on another pod
	f.put(remoteEntry("R2", "198.51.100.7:3", now))

	ip := net.ParseIP("198.51.100.7")
	if n := m.CountByIP(ip); n != 2 {
		t.Fatalf("CountByIP = %d, want 2 (L1 once, R2)", n)
	}
	all := m.FindAllByIP(ip)
	ids := map[string]string{}
	for _, e := range all {
		ids[e.ID] = e.Owner
	}
	if ids["L1"] != "" || ids["R2"] != "pod-b" {
		t.Fatalf("FindAllByIP = %v", ids)
	}
}

func TestDirectoryPublishesChangesAndThrottlesHeartbeats(t *testing.T) {
	m := NewMap()
	m.Put(&Entry{ID: "EARLY", IP: "10.1.0.1:1", LastReg: time.Now()})
	f := newFakeDir()
	m.SetDirectory(f, "pod-a")
	defer m.Close()
	m.mu.Lock()
	m.dir.beatEvery = 300 * time.Millisecond
	m.mu.Unlock()

	waitFor(t, "entry from before SetDirectory published", func() bool { return count(f.publishedIDs(), "EARLY") == 1 })

	m.Put(&Entry{ID: "P", IP: "10.1.0.2:1", UDPAddr: udp("10.1.0.2:1"), LastReg: time.Now(), StatusTier: StatusOnline})
	waitFor(t, "Put published", func() bool { return count(f.publishedIDs(), "P") == 1 })

	for i := 0; i < 5; i++ {
		m.TouchHeartbeat("P")
		m.UpdateHeartbeat("P", udp("10.1.0.2:1"), 0)
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	if n := count(f.publishedIDs(), "P"); n != 1 {
		t.Fatalf("heartbeats published right away: %d publishes", n)
	}
	beat := m.Get("P").LastReg
	// The pending heartbeat goes out once beatEvery has passed.
	waitFor(t, "throttled heartbeat published", func() bool { return count(f.publishedIDs(), "P") == 2 })
	if got := f.lastPublished("P").LastReg; !got.Equal(beat) {
		t.Fatalf("published LastReg %v, want the last heartbeat %v", got, beat)
	}

	// A heartbeat from a new address is a routing change: out at once.
	start := time.Now()
	m.UpdateHeartbeat("P", udp("10.1.0.3:1"), 0)
	waitFor(t, "address change published", func() bool { return count(f.publishedIDs(), "P") == 3 })
	if time.Since(start) > 200*time.Millisecond {
		t.Fatalf("address change took %v", time.Since(start))
	}
	if got := f.lastPublished("P"); got.IP != "10.1.0.3:1" || got.UDPAddr.String() != "10.1.0.3:1" {
		t.Fatalf("published %+v", got)
	}
	m.SetBanned("P", true)
	waitFor(t, "ban published", func() bool { return count(f.publishedIDs(), "P") == 4 && f.lastPublished("P").Banned })
}

func TestDirectoryRemoveWithdraws(t *testing.T) {
	m, f := dirMap(t)
	now := time.Now()
	m.Put(&Entry{ID: "A", IP: "10.2.0.1:1", LastReg: now})
	m.Put(&Entry{ID: "B", IP: "10.2.0.2:1", LastReg: now.Add(-time.Hour)})
	m.Put(&Entry{ID: "C", IP: "10.2.0.3:1", LastReg: now})
	waitFor(t, "Puts published", func() bool { return len(f.publishedIDs()) == 3 })

	m.Remove("A")
	m.CleanExpired(time.Minute)
	if _, ok := m.Rename("C", "C2"); !ok {
		t.Fatal("Rename failed")
	}
	waitFor(t, "withdraws", func() bool {
		w := f.withdrawnIDs()
		return count(w, "A") == 1 && count(w, "B") == 1 && count(w, "C") == 1
	})
	waitFor(t, "renamed peer published", func() bool { return count(f.publishedIDs(), "C2") == 1 })

}

func TestMapWithoutDirectoryPublishesNothing(t *testing.T) {
	before := runtime.NumGoroutine()
	m := NewMap()
	m.SetDirectory(nil, "pod-a")
	m.Put(&Entry{ID: "A", IP: "10.3.0.1:1", LastReg: time.Now()})
	m.TouchHeartbeat("A")
	m.SetBanned("A", true)
	m.Remove("A")
	if m.dir != nil {
		t.Fatal("SetDirectory(nil) set a directory")
	}
	if got := m.Get("missing"); got != nil {
		t.Fatalf("Get missing = %+v", got)
	}
	if runtime.NumGoroutine() > before {
		t.Fatalf("a map without a directory started a goroutine (%d > %d)", runtime.NumGoroutine(), before)
	}
	m.Close() // no-op
}

func TestDropIfMoved(t *testing.T) {
	m, f := dirMap(t)
	now := time.Now()
	c := &mapTestCloser{}
	m.Put(&Entry{ID: "M", IP: "10.4.0.1:1", WSConn: c, ConnType: ConnWS, LastReg: now})

	if m.DropIfMoved("M") {
		t.Fatal("dropped a peer the directory doesn't know")
	}
	f.put(remoteEntry("M", "10.4.0.2:1", now.Add(-time.Second)))
	if m.DropIfMoved("M") || m.Get("M").Owner != "" {
		t.Fatal("dropped a peer whose remote entry is older")
	}
	self := remoteEntry("M", "10.4.0.2:1", now.Add(time.Second))
	self.Owner = "pod-a"
	f.put(self)
	if m.DropIfMoved("M") {
		t.Fatal("dropped a peer for an entry of this pod")
	}
	f.put(remoteEntry("M", "10.4.0.2:1", now.Add(time.Second)))
	if !m.DropIfMoved("M") {
		t.Fatal("kept a peer that registered on pod-b since")
	}
	if !c.closed {
		t.Fatal("the dropped entry's WebSocket stays open")
	}
	if e := m.Get("M"); e == nil || e.Owner != "pod-b" {
		t.Fatalf("Get after the move = %+v, want pod-b's entry", e)
	}
	// The peer registers here again: the entry Put back is this pod's.
	e := m.Get("M")
	m.Put(e)
	if got := m.Get("M"); got == nil || got.Owner != "" {
		t.Fatalf("an entry Put from the directory keeps its Owner: %+v", got)
	}
}

func TestLeaveWithdrawsEveryLocalPeer(t *testing.T) {
	dir := newFakeDir()
	m := NewMap()
	m.SetDirectory(dir, "pod-a")
	m.Put(&Entry{ID: "LEAVE1", LastReg: time.Now()})
	m.Put(&Entry{ID: "LEAVE2", LastReg: time.Now()})
	m.Leave()
	for _, id := range []string{"LEAVE1", "LEAVE2"} {
		if !dir.wasWithdrawn(id) {
			t.Fatalf("%s not withdrawn on Leave", id)
		}
	}
}

func (f *fakeDir) wasWithdrawn(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, batch := range f.withdrawn {
		for _, w := range batch {
			if w == id {
				return true
			}
		}
	}
	return false
}
