package ratelimit

import (
	"testing"
	"time"
)

func TestWindowCounterCountsPerKeyAndWindow(t *testing.T) {
	c := NewWindowCounter(2, time.Minute)
	t0 := time.Date(2026, 11, 25, 8, 0, 0, 0, time.UTC)
	for i := 0; i < 2; i++ {
		if ok, _ := c.Allow("a", t0.Add(time.Duration(i)*time.Second)); !ok {
			t.Fatalf("event %d refused", i+1)
		}
	}
	ok, wait := c.Allow("a", t0.Add(20*time.Second))
	if ok || wait != 40*time.Second {
		t.Errorf("third event in the window: %v, wait %v, want refused with 40s", ok, wait)
	}
	if ok, _ := c.Allow("b", t0.Add(20*time.Second)); !ok {
		t.Error("another key was refused")
	}
	if ok, _ := c.Allow("a", t0.Add(time.Minute)); !ok {
		t.Error("the next window was refused")
	}
}

func TestWindowCounterFullOfFreshKeysAllowsNewOnes(t *testing.T) {
	c := NewWindowCounter(1, time.Minute)
	c.maxEntries = 2
	now := time.Now()
	c.Allow("a", now)
	c.Allow("b", now)
	if ok, _ := c.Allow("c", now); !ok {
		t.Error("a new key was refused while the counter was full")
	}
	if ok, _ := c.Allow("a", now); ok {
		t.Error("a known key over its limit was allowed")
	}
	if ok, _ := c.Allow("d", now.Add(time.Minute)); !ok || len(c.entries) != 1 {
		t.Errorf("ended windows were not dropped: %d keys", len(c.entries))
	}
}
