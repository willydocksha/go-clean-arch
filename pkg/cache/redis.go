// package cache wraps go-redis with helpers that most services end up needing:
// json get/set, cache-aside, counters, rate limiting, distributed locks and pattern delete
package cache

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// returned when a key does not exist
var ErrMiss = errors.New("cache: key not found")

// returned when a lock is already held by someone else
var ErrLockNotAcquired = errors.New("cache: lock not acquired")

// redis connection options
type RedisConfig struct {
	Addr      string
	Password  string
	DB        int
	KeyPrefix string // prepended to every key, e.g. "myapp:"
}

// redis client wrapper
type Redis struct {
	rdb    *redis.Client
	prefix string
}

// connect to redis and verify with ping
func NewRedis(ctx context.Context, cfg RedisConfig) (*Redis, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
	})

	// fail fast when redis is unreachable
	pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := rdb.Ping(pingCtx).Err(); err != nil {
		_ = rdb.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}
	return &Redis{rdb: rdb, prefix: cfg.KeyPrefix}, nil
}

// raw go-redis client for anything not covered here
func (r *Redis) Client() *redis.Client { return r.rdb }

// close the connection pool
func (r *Redis) Close() error { return r.rdb.Close() }

// health check
func (r *Redis) Ping(ctx context.Context) error { return r.rdb.Ping(ctx).Err() }

// apply key prefix
func (r *Redis) k(key string) string { return r.prefix + key }

// ---------- json values ----------

// get a json value into dst, returns ErrMiss when absent
func (r *Redis) Get(ctx context.Context, key string, dst any) error {
	raw, err := r.rdb.Get(ctx, r.k(key)).Bytes()
	if errors.Is(err, redis.Nil) {
		return ErrMiss
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, dst)
}

// store a value as json; ttl 0 means no expiry
func (r *Redis) Set(ctx context.Context, key string, value any, ttl time.Duration) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("cache marshal: %w", err)
	}
	return r.rdb.Set(ctx, r.k(key), raw, ttl).Err()
}

// ---------- plain strings ----------

// get a raw string value
func (r *Redis) GetString(ctx context.Context, key string) (string, error) {
	v, err := r.rdb.Get(ctx, r.k(key)).Result()
	if errors.Is(err, redis.Nil) {
		return "", ErrMiss
	}
	return v, err
}

// set a raw string value
func (r *Redis) SetString(ctx context.Context, key, value string, ttl time.Duration) error {
	return r.rdb.Set(ctx, r.k(key), value, ttl).Err()
}

// set only if the key does not exist yet (idempotency keys, dedup)
func (r *Redis) SetNX(ctx context.Context, key string, value any, ttl time.Duration) (bool, error) {
	return r.rdb.SetNX(ctx, r.k(key), value, ttl).Result()
}

// ---------- key management ----------

// delete one or more keys
func (r *Redis) Delete(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	full := make([]string, len(keys))
	for i, key := range keys {
		full[i] = r.k(key)
	}
	return r.rdb.Del(ctx, full...).Err()
}

// delete all keys matching a glob pattern using scan (safe for production, unlike keys)
func (r *Redis) DeleteByPattern(ctx context.Context, pattern string) (int, error) {
	var (
		cursor  uint64
		deleted int
	)
	for {
		keys, next, err := r.rdb.Scan(ctx, cursor, r.k(pattern), 500).Result()
		if err != nil {
			return deleted, err
		}
		if len(keys) > 0 {
			n, err := r.rdb.Unlink(ctx, keys...).Result()
			if err != nil {
				return deleted, err
			}
			deleted += int(n)
		}
		if next == 0 {
			return deleted, nil
		}
		cursor = next
	}
}

// check if a key exists
func (r *Redis) Exists(ctx context.Context, key string) (bool, error) {
	n, err := r.rdb.Exists(ctx, r.k(key)).Result()
	return n > 0, err
}

// set expiry on an existing key
func (r *Redis) Expire(ctx context.Context, key string, ttl time.Duration) error {
	return r.rdb.Expire(ctx, r.k(key), ttl).Err()
}

// remaining ttl of a key (-1 no expiry, -2 missing)
func (r *Redis) TTL(ctx context.Context, key string) (time.Duration, error) {
	return r.rdb.TTL(ctx, r.k(key)).Result()
}

// ---------- counters & rate limiting ----------

// increment a counter; ttl is applied only when the key is created
func (r *Redis) Incr(ctx context.Context, key string, by int64, ttl time.Duration) (int64, error) {
	pipe := r.rdb.TxPipeline()
	incr := pipe.IncrBy(ctx, r.k(key), by)
	if ttl > 0 {
		pipe.ExpireNX(ctx, r.k(key), ttl)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return 0, err
	}
	return incr.Val(), nil
}

// result of a rate limit check
type RateLimitResult struct {
	Allowed   bool
	Remaining int64
	ResetIn   time.Duration
}

// fixed-window rate limiter: at most limit hits per window for the key
func (r *Redis) RateLimit(ctx context.Context, key string, limit int64, window time.Duration) (RateLimitResult, error) {
	full := r.k("ratelimit:" + key)

	// count hit and read ttl atomically
	pipe := r.rdb.TxPipeline()
	incr := pipe.Incr(ctx, full)
	pipe.ExpireNX(ctx, full, window)
	ttl := pipe.PTTL(ctx, full)
	if _, err := pipe.Exec(ctx); err != nil {
		return RateLimitResult{}, err
	}

	// compute result
	count := incr.Val()
	return RateLimitResult{
		Allowed:   count <= limit,
		Remaining: max(limit-count, 0),
		ResetIn:   ttl.Val(),
	}, nil
}

// ---------- distributed lock ----------

// release only if we still own the lock
var unlockScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
	return redis.call("DEL", KEYS[1])
end
return 0`)

// acquire a lock for ttl; call the returned func to release it
func (r *Redis) Lock(ctx context.Context, key string, ttl time.Duration) (func(context.Context) error, error) {
	full := r.k("lock:" + key)

	// random token identifies the owner
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return nil, err
	}
	token := hex.EncodeToString(buf)

	// try to take the lock
	ok, err := r.rdb.SetNX(ctx, full, token, ttl).Result()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrLockNotAcquired
	}

	// unlock func
	return func(ctx context.Context) error {
		return unlockScript.Run(ctx, r.rdb, []string{full}, token).Err()
	}, nil
}

// ---------- cache-aside ----------

// return cached value or compute it with fn and cache the result
func Remember[T any](ctx context.Context, r *Redis, key string, ttl time.Duration, fn func(context.Context) (T, error)) (T, error) {
	// try cache first
	var cached T
	if err := r.Get(ctx, key, &cached); err == nil {
		return cached, nil
	}

	// compute and store (cache write errors are ignored on purpose)
	val, err := fn(ctx)
	if err != nil {
		return val, err
	}
	_ = r.Set(ctx, key, val, ttl)
	return val, nil
}
