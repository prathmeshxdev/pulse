package preflight

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDo_CacheHit(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	e := New(rdb, DefaultConfig())

	var calls atomic.Int32
	fn := func(ctx context.Context) (string, error) {
		calls.Add(1)
		return "hello", nil
	}

	v1, err := Do(context.Background(), e, "k1", fn)
	require.NoError(t, err)
	assert.Equal(t, "hello", v1)

	v2, err := Do(context.Background(), e, "k1", fn)
	require.NoError(t, err)
	assert.Equal(t, "hello", v2)
	assert.Equal(t, int32(1), calls.Load())
}

func TestDo_DisabledFallsThrough(t *testing.T) {
	e := New(nil, Config{Enabled: false})
	var calls atomic.Int32
	v, err := Do(context.Background(), e, "k", func(ctx context.Context) (int, error) {
		calls.Add(1)
		return 7, nil
	})
	require.NoError(t, err)
	assert.Equal(t, 7, v)
	assert.Equal(t, int32(1), calls.Load())
}

func TestDo_Singleflight(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	cfg := DefaultConfig()
	cfg.PollEvery = 10 * time.Millisecond
	e := New(rdb, cfg)

	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})

	fn := func(ctx context.Context) (string, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-release
		}
		return "ok", nil
	}

	errCh := make(chan error, 2)
	go func() {
		_, err := Do(context.Background(), e, "sf", fn)
		errCh <- err
	}()
	<-started
	go func() {
		_, err := Do(context.Background(), e, "sf", fn)
		errCh <- err
	}()

	// Give waiter time to observe the lock.
	time.Sleep(30 * time.Millisecond)
	close(release)

	require.NoError(t, <-errCh)
	require.NoError(t, <-errCh)
	assert.Equal(t, int32(1), calls.Load())
}

func TestKeyFromString_Stable(t *testing.T) {
	assert.Equal(t, KeyFromString("a"), KeyFromString("a"))
	assert.NotEqual(t, KeyFromString("a"), KeyFromString("b"))
}
