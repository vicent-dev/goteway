package app

import (
	"fmt"

	"github.com/redis/go-redis/v9"
)

func (s *server) redis() {
	s.rdb = redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%s", s.c.Redis.Host, s.c.Redis.Port),
		Password: "", //  @todo env
		DB:       0,  // @todo env
	})
}
