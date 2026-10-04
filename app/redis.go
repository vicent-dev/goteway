package app

import (
	"github.com/redis/go-redis/v9"
)

func (s *server) redis() {
	s.rdb = redis.NewClient(&redis.Options{
		Addr:     s.c.Redis.Addr(),
		Username: s.c.Redis.Username,
		Password: s.c.Redis.Password,
		DB:       s.c.Redis.DB,
	})
}
