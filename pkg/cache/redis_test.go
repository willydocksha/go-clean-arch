package cache

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

// connect to a real redis, skipped unless REDIS_TEST_ADDR is set
func testRedis(t *testing.T) *Redis {
	t.Helper()
	addr := os.Getenv("REDIS_TEST_ADDR")
	if addr == "" {
		t.Skip("set REDIS_TEST_ADDR=localhost:6379 to run redis tests")
	}
	r, err := NewRedis(context.Background(), RedisConfig{Addr: addr, KeyPrefix: "test:" + t.Name() + ":"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = r.DeleteByPattern(context.Background(), "*")
		_ = r.Close()
	})
	return r
}

// json get/set, miss and pattern delete
func TestRedisJSON(t *testing.T) {
	r := testRedis(t)
	ctx := context.Background()

	// miss
	var v map[string]int
	if err := r.Get(ctx, "nope", &v); !errors.Is(err, ErrMiss) {
		t.Fatalf("want miss, got %v", err)
	}

	// set + get
	_ = r.Set(ctx, "user:1", map[string]int{"age": 30}, time.Minute)
	_ = r.Set(ctx, "user:2", map[string]int{"age": 31}, time.Minute)
	if err := r.Get(ctx, "user:1", &v); err != nil || v["age"] != 30 {
		t.Fatalf("get: %v %v", err, v)
	}

	// pattern delete
	if n, err := r.DeleteByPattern(ctx, "user:*"); err != nil || n != 2 {
		t.Fatalf("pattern delete: %d %v", n, err)
	}
}

// cache-aside computes once
func TestRedisRemember(t *testing.T) {
	r := testRedis(t)
	ctx := context.Background()
	calls := 0
	fn := func(context.Context) (string, error) { calls++; return "value", nil }
	for range 3 {
		if v, err := Remember(ctx, r, "k", time.Minute, fn); err != nil || v != "value" {
			t.Fatalf("remember: %q %v", v, err)
		}
	}
	if calls != 1 {
		t.Fatalf("want 1 call, got %d", calls)
	}
}

// fixed window rate limit
func TestRedisRateLimit(t *testing.T) {
	r := testRedis(t)
	ctx := context.Background()
	var last RateLimitResult
	for range 4 {
		last, _ = r.RateLimit(ctx, "ip:1", 3, time.Minute)
	}
	if last.Allowed || last.Remaining != 0 || last.ResetIn <= 0 {
		t.Fatalf("4th hit should be blocked: %+v", last)
	}
}

// lock is exclusive and released by owner
func TestRedisLock(t *testing.T) {
	r := testRedis(t)
	ctx := context.Background()
	unlock, err := r.Lock(ctx, "job", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Lock(ctx, "job", time.Minute); !errors.Is(err, ErrLockNotAcquired) {
		t.Fatalf("want lock held, got %v", err)
	}
	_ = unlock(ctx)
	if _, err := r.Lock(ctx, "job", time.Minute); err != nil {
		t.Fatalf("relock after unlock: %v", err)
	}
}

// counter with ttl
func TestRedisIncr(t *testing.T) {
	r := testRedis(t)
	ctx := context.Background()
	_, _ = r.Incr(ctx, "views", 1, time.Minute)
	n, err := r.Incr(ctx, "views", 5, time.Minute)
	ttl, _ := r.TTL(ctx, "views")
	if err != nil || n != 6 || ttl <= 0 {
		t.Fatalf("incr: %d %v ttl=%v", n, err, ttl)
	}
}
