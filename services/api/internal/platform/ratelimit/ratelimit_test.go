package ratelimit

import (
	"context"
	"testing"
	"time"
)

func TestMemoryLimiter(t *testing.T) {
	m := NewMemory()
	base := time.UnixMilli(1_000_000)
	m.now = func() time.Time { return base }
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if ok, _, _ := m.Allow(ctx, "k", 3, time.Minute); !ok {
			t.Fatalf("hit %d rejected", i)
		}
	}
	ok, retry, _ := m.Allow(ctx, "k", 3, time.Minute)
	if ok || retry <= 0 {
		t.Fatalf("4th hit should be rejected with retry-after, got ok=%v retry=%v", ok, retry)
	}
	if ok, _, _ := m.Allow(ctx, "other", 3, time.Minute); !ok {
		t.Fatal("keys must be independent")
	}
	m.now = func() time.Time { return base.Add(time.Minute) }
	if ok, _, _ := m.Allow(ctx, "k", 3, time.Minute); !ok {
		t.Fatal("new window should allow")
	}
}
