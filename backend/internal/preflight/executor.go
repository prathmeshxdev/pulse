// Package preflight implements Redis singleflight + result cache for ClickHouse
// queries, adapted from a production preflight/singleflight executor.
package preflight

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	cachePrefix    = "sony:ch:result:"
	inflightPrefix = "sony:ch:inflight:"
)

type Config struct {
	Enabled     bool
	CacheTTL    time.Duration
	LockTTL     time.Duration
	WaitTimeout time.Duration
	PollEvery   time.Duration
}

func DefaultConfig() Config {
	return Config{
		Enabled:     true,
		CacheTTL:    5 * time.Minute,
		LockTTL:     30 * time.Second,
		WaitTimeout: 10 * time.Second,
		PollEvery:   50 * time.Millisecond,
	}
}

// Executor coordinates cross-process deduplication of identical ClickHouse work.
type Executor struct {
	redis *redis.Client
	cfg   Config
}

func New(redisClient *redis.Client, cfg Config) *Executor {
	if cfg.PollEvery == 0 {
		cfg.PollEvery = 50 * time.Millisecond
	}
	return &Executor{redis: redisClient, cfg: cfg}
}

// KeyFromString hashes an arbitrary stable key (preferred over raw SQL).
func KeyFromString(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:16])
}

// Do runs queryFn unless a cached result or in-flight peer can satisfy the request.
func Do[T any](ctx context.Context, e *Executor, key string, queryFn func(context.Context) (T, error)) (T, error) {
	if e == nil || !e.cfg.Enabled || e.redis == nil {
		return queryFn(ctx)
	}

	cacheKey := cachePrefix + key
	inflightKey := inflightPrefix + key

	if cached, err := getCache[T](ctx, e.redis, cacheKey); err == nil {
		return cached, nil
	}

	acquired, err := e.redis.SetNX(ctx, inflightKey, strconv.FormatInt(time.Now().UnixMilli(), 10), e.cfg.LockTTL).Result()
	if err != nil {
		// Redis unhappy — degrade to direct execution.
		return queryFn(ctx)
	}
	if acquired {
		return executeAndCache(ctx, e, cacheKey, inflightKey, queryFn)
	}
	return waitForResult(ctx, e, cacheKey, inflightKey, queryFn)
}

func executeAndCache[T any](ctx context.Context, e *Executor, cacheKey, inflightKey string, queryFn func(context.Context) (T, error)) (T, error) {
	var zero T
	result, err := queryFn(ctx)
	if err != nil {
		_ = e.redis.Del(ctx, inflightKey).Err()
		return zero, err
	}
	if data, mErr := json.Marshal(result); mErr == nil {
		_ = e.redis.Set(ctx, cacheKey, data, e.cfg.CacheTTL).Err()
	}
	// Shrink lock TTL rather than delete — avoids race with waiters (singleflight pattern).
	_ = e.redis.Expire(ctx, inflightKey, 2*time.Second).Err()
	return result, nil
}

func waitForResult[T any](ctx context.Context, e *Executor, cacheKey, inflightKey string, queryFn func(context.Context) (T, error)) (T, error) {
	deadline := time.Now().Add(e.cfg.WaitTimeout)
	for time.Now().Before(deadline) {
		if cached, err := getCache[T](ctx, e.redis, cacheKey); err == nil {
			return cached, nil
		}
		// Inflight key disappeared without a cache write — fall through.
		if n, err := e.redis.Exists(ctx, inflightKey).Result(); err == nil && n == 0 {
			break
		}
		select {
		case <-ctx.Done():
			var zero T
			return zero, ctx.Err()
		case <-time.After(e.cfg.PollEvery):
		}
	}
	return queryFn(ctx)
}

func getCache[T any](ctx context.Context, rdb *redis.Client, key string) (T, error) {
	var zero T
	data, err := rdb.Get(ctx, key).Bytes()
	if err != nil {
		return zero, err
	}
	var out T
	if err := json.Unmarshal(data, &out); err != nil {
		return zero, err
	}
	return out, nil
}

// ErrDisabled is returned only by helpers that require preflight; Do never returns it.
var ErrDisabled = errors.New("preflight disabled")
