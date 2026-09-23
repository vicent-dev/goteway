package cache

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

type Redis[T Cacheable] struct {
	rdb *redis.Client
	ttl time.Duration
}

func NewRedis[T Cacheable](rdb *redis.Client) Cache[T] {
	return &Redis[T]{rdb, time.Second * 15}
}

func (r *Redis[T]) Set(t T) {
	r.rdb.Set(context.Background(), t.Key(), t.Value(), r.ttl)
}

func (r *Redis[T]) Get(t T) {
	val, err := r.rdb.Get(context.Background(), t.Key()).Result()

	// key not found
	if err != nil {
		return
	}

	t.SetValue(val)
}
