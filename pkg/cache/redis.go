package cache

import (
	"context"
	"errors"
	"fmt"
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

// Set writes the value under its own key, with the cache's ttl. A value that
// cannot be encoded fails the write, since storing a truncated payload would
// hand out a corrupt response later.
func (r *Redis[T]) Set(ctx context.Context, value T) error {
	payload, err := value.Value()
	if err != nil {
		return err
	}

	if err := r.rdb.Set(ctx, value.Key(), payload, r.ttl).Err(); err != nil {
		return fmt.Errorf("cache: set: %w", err)
	}
	return nil
}

// Get loads the value into the same instance, so the caller reads what it was
// given. A missing key is ErrNotFound and nothing is loaded; an unreachable
// redis is a different error, so an outage is never read as a cold cache.
func (r *Redis[T]) Get(ctx context.Context, value T) error {
	payload, err := r.rdb.Get(ctx, value.Key()).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return ErrNotFound
		}
		return fmt.Errorf("cache: get: %w", err)
	}

	return value.SetValue(payload)
}
