package app

import (
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
)

func TestRedisSetup(t *testing.T) {
	s := &server{
		c: &Config{
			Redis: RedisConfig{
				Host: "localhost",
				Port: "6379",
			},
		},
	}

	s.redis()

	assert.NotNil(t, s.rdb)
	assert.IsType(t, &redis.Client{}, s.rdb)
	assert.Equal(t, "localhost:6379", s.rdb.Options().Addr)
}
