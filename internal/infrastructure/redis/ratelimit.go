// Package redis provides rate limiting backed by Redis, with an in-memory fallback for
// local development and tests (a single process). Redis is used only where it pays off:
// limits shared across API instances.
package redis

import (
	"context"
	"sync"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// Limiter allows at most `limit` hits per `window` for a key (fixed window).
type Limiter interface {
	Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, error)
}

// NewClient connects to Redis from a URL like redis://localhost:6379/0.
func NewClient(ctx context.Context, url string) (*goredis.Client, error) {
	opt, err := goredis.ParseURL(url)
	if err != nil {
		return nil, err
	}
	c := goredis.NewClient(opt)
	if err := c.Ping(ctx).Err(); err != nil {
		return nil, err
	}
	return c, nil
}

// RedisLimiter: INCR + EXPIRE in a pipeline.
type RedisLimiter struct{ C *goredis.Client }

func (l RedisLimiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, error) {
	k := "rl:" + key
	pipe := l.C.TxPipeline()
	incr := pipe.Incr(ctx, k)
	pipe.ExpireNX(ctx, k, window)
	if _, err := pipe.Exec(ctx); err != nil {
		return true, err // fail open: never lock everyone out because Redis is down
	}
	return incr.Val() <= int64(limit), nil
}

// MemoryLimiter is a process-local fixed-window limiter.
type MemoryLimiter struct {
	mu      sync.Mutex
	buckets map[string]bucket
	now     func() time.Time
}

type bucket struct {
	count int
	reset time.Time
}

func NewMemoryLimiter() *MemoryLimiter {
	return &MemoryLimiter{buckets: map[string]bucket{}, now: time.Now}
}

func (l *MemoryLimiter) Allow(_ context.Context, key string, limit int, window time.Duration) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	b := l.buckets[key]
	if now.After(b.reset) {
		b = bucket{reset: now.Add(window)}
	}
	b.count++
	l.buckets[key] = b
	if len(l.buckets) > 100_000 { // keep memory bounded
		for k, v := range l.buckets {
			if now.After(v.reset) {
				delete(l.buckets, k)
			}
		}
	}
	return b.count <= limit, nil
}
