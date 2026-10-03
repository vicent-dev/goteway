package cache

import (
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
)

type testCacheable struct {
	key   string
	value string
}

func (t *testCacheable) Key() string {
	return t.key
}

func (t *testCacheable) Value() string {
	return t.value
}

func (t *testCacheable) SetValue(s string) {
	t.value = s
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
	cache.Set(item)

	retrieved := &testCacheable{key: "test-key"}
	cache.Get(retrieved)
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
	cache.Get(item)
	// Should remain unchanged
	assert.Empty(t, item.value)
}

func TestRedisCache_ErrorSwallowed(t *testing.T) {
	// Use a redis client pointing to invalid addr - operations will fail
	rdb := redis.NewClient(&redis.Options{
		Addr: "localhost:9999",
	})
	cache := NewRedis[*testCacheable](rdb)

	item := &testCacheable{key: "key", value: "value"}
	cache.Set(item) // Should not panic

	retrieved := &testCacheable{key: "key"}
	cache.Get(retrieved) // Should not panic, value remains empty
	assert.Empty(t, retrieved.value)
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
	cache.Set(item)

	// Check TTL is set (default 15s)
	ttl := mr.TTL("ttl-key")
	assert.True(t, ttl > 10*time.Second && ttl <= 15*time.Second)
}
