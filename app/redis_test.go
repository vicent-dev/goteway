package app

import (
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRedisSetup(t *testing.T) {
	tests := []struct {
		name     string
		cfg      RedisConfig
		wantAddr string
		wantUser string
		wantPass string
		wantDB   int
	}{
		{
			name: "credentials and database from the configuration",
			cfg: RedisConfig{
				Host: "cache", Port: "6380", Username: "gateway", Password: "s3cret", DB: 3,
			},
			wantAddr: "cache:6380",
			wantUser: "gateway",
			wantPass: "s3cret",
			wantDB:   3,
		},
		{
			name:     "unauthenticated redis",
			cfg:      RedisConfig{Host: "localhost", Port: "6379"},
			wantAddr: "localhost:6379",
			wantUser: "",
			wantPass: "",
			wantDB:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &server{c: &Config{Redis: tt.cfg}}

			s.redis()

			require.NotNil(t, s.rdb)
			assert.IsType(t, &redis.Client{}, s.rdb)

			opts := s.rdb.Options()
			assert.Equal(t, tt.wantAddr, opts.Addr)
			assert.Equal(t, tt.wantUser, opts.Username)
			assert.Equal(t, tt.wantPass, opts.Password)
			assert.Equal(t, tt.wantDB, opts.DB)
		})
	}
}

// withDefaults must not invent credentials: an empty password is what an
// unauthenticated redis needs, and database 0 is a real index, not an unset one.
func TestRedisConfigDefaultsLeaveCredentialsAlone(t *testing.T) {
	c := (&Config{Redis: RedisConfig{Host: "localhost"}}).withDefaults()

	assert.Equal(t, "6379", c.Redis.Port)
	assert.Empty(t, c.Redis.Username)
	assert.Empty(t, c.Redis.Password)
	assert.Equal(t, 0, c.Redis.DB)
}
