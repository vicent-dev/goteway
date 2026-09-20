package cache

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

type Redis struct {
	rdb *redis.Client
	ttl time.Duration
}

func NewRedis(rdb *redis.Client) Cache {
	return &Redis{rdb, time.Minute}
}

func (r *Redis) Set(ca Cacheable) {
	r.rdb.Set(context.Background(), ca.hashKey(), ca.base64Value(), r.ttl)
}

func (r *Redis) Get(ca Cacheable) *Cacheable {
	s := r.rdb.Get(context.Background(), ca.hashKey())

	ca.setValueBase64(s.Val())

	return nil
}
