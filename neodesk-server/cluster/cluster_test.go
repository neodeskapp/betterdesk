package cluster

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestFromEnvReadsValkeyOrRedisAndDefaults(t *testing.T) {
	c, err := FromEnv(envOf(map[string]string{"REDIS_URL": "redis://cache:6379/2", "POD_NAME": "neodesk-1"}))
	if err != nil {
		t.Fatal(err)
	}
	if !c.Enabled() || c.ValkeyURL != "redis://cache:6379/2" || c.KeyPrefix != DefaultKeyPrefix || c.Name != "neodesk-1" || c.Port != DefaultClusterPort || c.ShutdownGrace != DefaultShutdownGrace {
		t.Fatalf("config %+v", c)
	}
	c, err = FromEnv(envOf(map[string]string{"VALKEY_URL": "valkeys://u:p@v:6380", "REDIS_URL": "redis://other", "NEODESK_KEY_PREFIX": "acme:", "NEODESK_SHUTDOWN_GRACE": "45"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.ValkeyURL != "valkeys://u:p@v:6380" || c.KeyPrefix != "acme:" || c.ShutdownGrace != 45*time.Second {
		t.Fatalf("VALKEY_URL must win over REDIS_URL, prefix and grace must be read: %+v", c)
	}
	alone, err := FromEnv(envOf(nil))
	if err != nil || alone.Enabled() {
		t.Fatalf("no URL means alone: %+v %v", alone, err)
	}
}

func TestABadValkeyURLNamesItWithoutThePassword(t *testing.T) {
	_, err := FromEnv(envOf(map[string]string{"VALKEY_URL": "http://admin:hunter2@cache:6379"}))
	if err == nil {
		t.Fatal("http:// URL accepted")
	}
	if strings.Contains(err.Error(), "hunter2") {
		t.Fatalf("error carries the password: %v", err)
	}
	if !strings.Contains(err.Error(), "VALKEY_URL") {
		t.Fatalf("error doesn't name the setting: %v", err)
	}
}

// memInstances is an InstanceStore in memory.
type memInstances struct {
	mu   sync.Mutex
	rows map[string]Instance
}

func (m *memInstances) TouchInstance(inst Instance) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.rows == nil {
		m.rows = map[string]Instance{}
	}
	m.rows[inst.Name] = inst
	return nil
}

func (m *memInstances) ListInstances(since time.Time) ([]Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Instance
	for _, r := range m.rows {
		if !r.LastSeen.Before(since) {
			out = append(out, r)
		}
	}
	return out, nil
}

func (m *memInstances) RemoveInstance(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.rows, name)
	return nil
}

func TestCheckStartRefusesASecondInstanceWithoutWhatItNeeds(t *testing.T) {
	now := time.Now()
	clustered := Config{Name: "b", ValkeyURL: "redis://v:6379"}
	alone := Config{Name: "b"}

	store := &memInstances{}
	_ = store.TouchInstance(Instance{Name: "a", Clustered: true, LastSeen: now.Add(-5 * time.Second)})

	err := CheckStart(store, alone, false, now)
	if err == nil || !strings.Contains(err.Error(), "VALKEY_URL") {
		t.Fatalf("second instance without Valkey: %v, want a refusal naming VALKEY_URL", err)
	}
	err = CheckStart(store, clustered, true, now)
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL=postgres://") || strings.Contains(err.Error(), "VALKEY_URL=") {
		t.Fatalf("second instance on SQLite: %v, want a refusal naming PostgreSQL only", err)
	}
	if err := CheckStart(store, clustered, false, now); err != nil {
		t.Fatalf("clustered instance on PostgreSQL next to a clustered one: %v", err)
	}

	// The first instance runs without Valkey: the second may not join it
	_ = store.TouchInstance(Instance{Name: "a", Clustered: false, LastSeen: now})
	if err := CheckStart(store, clustered, false, now); err == nil || !strings.Contains(err.Error(), "a runs without VALKEY_URL") {
		t.Fatalf("joining an unclustered instance: %v", err)
	}
}

func TestCheckStartIgnoresItselfAndInstancesLongGone(t *testing.T) {
	now := time.Now()
	store := &memInstances{}
	_ = store.TouchInstance(Instance{Name: "self", LastSeen: now})
	_ = store.TouchInstance(Instance{Name: "dead", LastSeen: now.Add(-InstanceLive - time.Second)})
	if err := CheckStart(store, Config{Name: "self"}, true, now); err != nil {
		t.Fatalf("a restart next to its own row and a dead instance: %v", err)
	}
}

func TestHeartbeatRemovesTheRowWhenItStops(t *testing.T) {
	store := &memInstances{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { Heartbeat(ctx, store, Config{Name: "hb", ValkeyURL: "redis://x"}, time.Now()); close(done) }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		rows, _ := store.ListInstances(time.Now().Add(-time.Minute))
		if len(rows) == 1 && rows[0].Name == "hb" && rows[0].Clustered {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("heartbeat never wrote its row")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	if rows, _ := store.ListInstances(time.Time{}); len(rows) != 0 {
		t.Fatalf("rows after stop: %v", rows)
	}
}

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("NEODESK_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("NEODESK_TEST_POSTGRES_DSN not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// TestOnlyOneInstanceHoldsALeaseAndAnotherTakesItOver: two instances on
// one PostgreSQL never both hold a lease; when the holder goes, the other
// takes the lease within the retry interval.
func TestOnlyOneInstanceHoldsALeaseAndAnotherTakesItOver(t *testing.T) {
	poolA, poolB := testPool(t), testPool(t)
	name := "test-lease-" + time.Now().Format("150405.000000000")
	a := NewPGLeases(poolA, "a")
	b := NewPGLeases(poolB, "b")
	defer b.Close()

	if !a.Held(name) {
		t.Fatal("the first instance doesn't get a free lease")
	}
	if b.Held(name) {
		t.Fatal("both instances hold the lease")
	}
	a.Close()
	deadline := time.Now().Add(leaseRetry + 3*time.Second)
	for !b.Held(name) {
		if time.Now().After(deadline) {
			t.Fatal("the second instance never took the lease over")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !b.Held("other-" + name) {
		t.Fatal("a second, free lease isn't taken")
	}
}

// TestMigrationLockRunsInstancesOneAfterTheOther: two instances migrating
// at once never overlap.
func TestMigrationLockRunsInstancesOneAfterTheOther(t *testing.T) {
	pool := testPool(t)
	var mu sync.Mutex
	inside, maxInside := 0, 0
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := WithMigrationLock(context.Background(), pool, func() error {
				mu.Lock()
				inside++
				maxInside = max(maxInside, inside)
				mu.Unlock()
				time.Sleep(50 * time.Millisecond)
				mu.Lock()
				inside--
				mu.Unlock()
				return nil
			})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if maxInside != 1 {
		t.Fatalf("%d migrations ran at once", maxInside)
	}
}

func TestDialFailsFastWithoutThePassword(t *testing.T) {
	start := time.Now()
	_, err := Dial(context.Background(), Config{ValkeyURL: "redis://:s3cret@127.0.0.1:1/0", Name: "t"})
	if err == nil {
		t.Fatal("dial to a closed port succeeded")
	}
	if strings.Contains(err.Error(), "s3cret") {
		t.Fatalf("error carries the password: %v", err)
	}
	if time.Since(start) > 8*time.Second {
		t.Fatalf("dial took %s", time.Since(start))
	}
}

func TestDialMarksTheConnectionHealthy(t *testing.T) {
	url := os.Getenv("NEODESK_TEST_VALKEY_URL")
	if url == "" {
		if os.Getenv("NEODESK_TEST_REQUIRE_VALKEY") == "1" {
			t.Fatal("NEODESK_TEST_VALKEY_URL not set")
		}
		t.Skip("NEODESK_TEST_VALKEY_URL not set")
	}
	v, err := Dial(context.Background(), Config{ValkeyURL: url, KeyPrefix: "t:", Name: "t"})
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	if !v.Healthy() || v.Key("relay", "t", "x") != "t:relay:t:x" {
		t.Fatalf("healthy=%v key=%q", v.Healthy(), v.Key("relay", "t", "x"))
	}
}
