// Package ratelimit implements fixed-window rate limiting backed by Redis,
// with an in-memory implementation for single-process development and tests.
package ratelimit

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// Limiter decides whether an action identified by key may proceed.
type Limiter interface {
	// Allow records one hit and reports whether it is within limit per window.
	// retryAfter is set when the hit is rejected.
	Allow(ctx context.Context, key string, limit int, window time.Duration) (ok bool, retryAfter time.Duration, err error)
}

// Redis is a fixed-window limiter using INCR + PEXPIRE.
type Redis struct{ c *redis.Client }

func NewRedis(c *redis.Client) *Redis { return &Redis{c: c} }

func (r *Redis) Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, time.Duration, error) {
	bucket := time.Now().UnixMilli() / window.Milliseconds()
	k := fmt.Sprintf("rl:%s:%d", key, bucket)
	pipe := r.c.TxPipeline()
	incr := pipe.Incr(ctx, k)
	pipe.PExpire(ctx, k, window)
	if _, err := pipe.Exec(ctx); err != nil {
		return false, 0, fmt.Errorf("rate limit: %w", err)
	}
	if incr.Val() > int64(limit) {
		next := (bucket + 1) * window.Milliseconds()
		return false, time.Duration(next-time.Now().UnixMilli()) * time.Millisecond, nil
	}
	return true, 0, nil
}

// Memory is a process-local fixed-window limiter (dev/test only; does not
// coordinate across replicas).
type Memory struct {
	mu      sync.Mutex
	buckets map[string]int
	now     func() time.Time
}

func NewMemory() *Memory { return &Memory{buckets: map[string]int{}, now: time.Now} }

func (m *Memory) Allow(_ context.Context, key string, limit int, window time.Duration) (bool, time.Duration, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	bucket := now.UnixMilli() / window.Milliseconds()
	k := fmt.Sprintf("%s:%d", key, bucket)
	if len(m.buckets) > 100_000 { // bound memory: drop old windows
		m.buckets = map[string]int{}
	}
	m.buckets[k]++
	if m.buckets[k] > limit {
		next := (bucket + 1) * window.Milliseconds()
		return false, time.Duration(next-now.UnixMilli()) * time.Millisecond, nil
	}
	return true, 0, nil
}
