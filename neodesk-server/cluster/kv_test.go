package cluster

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// runKVSuite is the behaviour every KV has, in memory or in Valkey.
func runKVSuite(t *testing.T, newKV func(t *testing.T) KV) {
	ctx := context.Background()

	t.Run("set get del", func(t *testing.T) {
		kv := newKV(t)
		if _, ok, _ := kv.Get(ctx, "a"); ok {
			t.Fatal("empty store has a value")
		}
		if err := kv.Set(ctx, "a", "1", time.Minute); err != nil {
			t.Fatal(err)
		}
		if v, ok, err := kv.Get(ctx, "a"); !ok || v != "1" || err != nil {
			t.Fatalf("get = %q %v %v", v, ok, err)
		}
		_ = kv.Del(ctx, "a")
		if _, ok, _ := kv.Get(ctx, "a"); ok {
			t.Fatal("value outlived Del")
		}
	})

	t.Run("expiry", func(t *testing.T) {
		kv := newKV(t)
		_ = kv.Set(ctx, "short", "x", 150*time.Millisecond)
		if _, ok, _ := kv.Get(ctx, "short"); !ok {
			t.Fatal("value gone at once")
		}
		time.Sleep(400 * time.Millisecond)
		if _, ok, _ := kv.Get(ctx, "short"); ok {
			t.Fatal("value outlived its TTL")
		}
	})

	t.Run("setnx keeps the first and says who holds it", func(t *testing.T) {
		kv := newKV(t)
		cur, set, err := kv.SetNX(ctx, "meet", "pod-a", time.Minute)
		if err != nil || !set || cur != "pod-a" {
			t.Fatalf("first SetNX = %q %v %v", cur, set, err)
		}
		cur, set, _ = kv.SetNX(ctx, "meet", "pod-b", time.Minute)
		if set || cur != "pod-a" {
			t.Fatalf("second SetNX = %q %v, want pod-a held", cur, set)
		}
	})

	t.Run("setnx races have one winner", func(t *testing.T) {
		kv := newKV(t)
		var wins atomic.Int32
		var wg sync.WaitGroup
		for i := range 20 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, set, _ := kv.SetNX(ctx, "race", string(rune('a'+i)), time.Minute); set {
					wins.Add(1)
				}
			}()
		}
		wg.Wait()
		if wins.Load() != 1 {
			t.Fatalf("%d winners", wins.Load())
		}
	})

	t.Run("delif removes only the named value", func(t *testing.T) {
		kv := newKV(t)
		_ = kv.Set(ctx, "owner", "pod-b", time.Minute)
		_ = kv.DelIf(ctx, "owner", "pod-a")
		if v, ok, _ := kv.Get(ctx, "owner"); !ok || v != "pod-b" {
			t.Fatal("DelIf removed another pod's value")
		}
		_ = kv.DelIf(ctx, "owner", "pod-b")
		if _, ok, _ := kv.Get(ctx, "owner"); ok {
			t.Fatal("DelIf kept its own value")
		}
	})
}

func TestMemKV(t *testing.T) {
	runKVSuite(t, func(*testing.T) KV { return NewMemKV() })
}

func TestValkeyKV(t *testing.T) {
	runKVSuite(t, func(t *testing.T) KV { return NewValkeyKV(testValkey(t)) })
}
