package app

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/redis/go-redis/v9"
)

func TestRedisSetup(t *testing.T) {
	s := &server{
		c: &config{
			Redis: struct {
				Host string `yaml:"host"`
				Port string `yaml:"port"`
			}{
				Host: "localhost",
				Port: "6379",
			},
		},
	}
	s.redis()
	assert.NotNil(t, s.rdb)
	assert.IsType(t, &redis.Client{}, s.rdb)
}
