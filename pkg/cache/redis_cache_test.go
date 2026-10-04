package cache

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testCacheable struct {
	key      string
	value    string
	valueErr error
	setErr   error
}

func (t *testCacheable) Key() string {
	return t.key
}

func (t *testCacheable) Value() (string, error) {
	return t.value, t.valueErr
}

func (t *testCacheable) SetValue(s string) error {
	if t.setErr != nil {
		return t.setErr
	}
	t.value = s
	return nil
}

func TestRedisCache_SetGet(t *testing.T) {
	mr, err := miniredis.Run()
	assert.NoError(t, err)
	defer mr.Close()

	rdb := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	cache := NewRedis[*testCacheable](rdb)

	item := &testCacheable{key: "test-key", value: "test-value"}
	require.NoError(t, cache.Set(context.Background(), item))

	retrieved := &testCacheable{key: "test-key"}
	require.NoError(t, cache.Get(context.Background(), retrieved))
	assert.Equal(t, "test-value", retrieved.value)
}

func TestRedisCache_GetMissingKey(t *testing.T) {
	mr, err := miniredis.Run()
	assert.NoError(t, err)
	defer mr.Close()

	rdb := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	cache := NewRedis[*testCacheable](rdb)

	item := &testCacheable{key: "missing"}

	// A miss is reported as ErrNotFound and nothing is loaded, which is what
	// lets a caller tell it apart from a cache that is down.
	assert.ErrorIs(t, cache.Get(context.Background(), item), ErrNotFound)
	assert.Empty(t, item.value)
}

func TestRedisCache_UnreachableIsNotAMiss(t *testing.T) {
	// A redis that cannot be reached must not look like a cold cache: the whole
	// point of reporting the error is that the outage is visible.
	rdb := redis.NewClient(&redis.Options{
		Addr: "localhost:9999",
	})
	cache := NewRedis[*testCacheable](rdb)

	err := cache.Set(context.Background(), &testCacheable{key: "key", value: "value"})
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrNotFound)

	err = cache.Get(context.Background(), &testCacheable{key: "key"})
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrNotFound)
}

func TestRedisCache_ValueErrorFailsTheWrite(t *testing.T) {
	mr, err := miniredis.Run()
	assert.NoError(t, err)
	defer mr.Close()

	rdb := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	cache := NewRedis[*testCacheable](rdb)

	// A value that cannot be encoded must not be stored as if it could: the
	// entry it would leave behind is a corrupt response.
	item := &testCacheable{key: "key", value: "value", valueErr: errors.New("cannot encode")}
	assert.Error(t, cache.Set(context.Background(), item))
	assert.False(t, mr.Exists("key"))
}

func TestRedisCache_SetValueErrorSurfaces(t *testing.T) {
	mr, err := miniredis.Run()
	assert.NoError(t, err)
	defer mr.Close()

	rdb := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	cache := NewRedis[*testCacheable](rdb)

	require.NoError(t, cache.Set(context.Background(), &testCacheable{key: "key", value: "value"}))

	retrieved := &testCacheable{key: "key", setErr: errors.New("cannot decode")}
	assert.Error(t, cache.Get(context.Background(), retrieved))
}

func TestRedisCache_TTL(t *testing.T) {
	mr, err := miniredis.Run()
	assert.NoError(t, err)
	defer mr.Close()

	rdb := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	cache := NewRedis[*testCacheable](rdb)

	item := &testCacheable{key: "ttl-key", value: "ttl-value"}
	require.NoError(t, cache.Set(context.Background(), item))

	// Check TTL is set (default 15s)
	ttl := mr.TTL("ttl-key")
	assert.True(t, ttl > 10*time.Second && ttl <= 15*time.Second)
}
