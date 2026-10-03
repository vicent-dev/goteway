package app

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestLoadConfigExpandsEnvironment(t *testing.T) {
	t.Setenv("PORT", "9090")
	t.Setenv("REDIS_HOST", "testredis")

	testConfig := []byte(`
server:
  host: 127.0.0.1
  port: ${PORT}
redis:
  host: ${REDIS_HOST}
  port: 6379
services:
  internal: []
  external: []
`)
	c := &Config{}
	require.NoError(t, yaml.Unmarshal([]byte(expandEnv(string(testConfig))), c))
	assert.Equal(t, "9090", c.Server.Port)
	assert.Equal(t, "testredis", c.Redis.Host)
}

func TestExpandEnv(t *testing.T) {
	t.Setenv("SET", "value")
	t.Setenv("EMPTY", "")

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain variable", in: "${SET}", want: "value"},
		{name: "unset variable", in: "${UNSET}", want: ""},
		{name: "default when unset", in: "${UNSET:-fallback}", want: "fallback"},
		{name: "default when empty", in: "${EMPTY:-fallback}", want: "fallback"},
		{name: "environment wins over the default", in: "${SET:-fallback}", want: "value"},
		{name: "default may be empty", in: "${UNSET:-}", want: ""},
		{name: "inside a value", in: "host: ${SET}:${UNSET:-5432}", want: "host: value:5432"},
		{name: "a fallback is not expanded again", in: "${UNSET:-$OTHER}", want: "$OTHER"},
		{name: "no variable", in: "plain", want: "plain"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, expandEnv(tt.in))
		})
	}
}

func TestConvertServicesToRequest(t *testing.T) {
	c := &Config{
		Services: ServicesConfig{
			Internal: []Upstream{{Path: "internal", Host: "int:8080"}},
			External: []Upstream{{Path: "external", Host: "ext:8080"}},
		},
	}

	sc := c.convertServicesToRequest()

	assert.Len(t, sc.Internal, 1)
	assert.Len(t, sc.External, 1)
	assert.Equal(t, "internal", sc.Internal[0].Path)
	assert.Equal(t, "ext:8080", sc.External[0].Host)
}

func TestConfigDefaults(t *testing.T) {
	c := (&Config{}).withDefaults()

	assert.Equal(t, "8080", c.Server.Port)
	assert.Equal(t, "6379", c.Redis.Port)
	assert.Equal(t, 100, c.DB.MaxConns)
	assert.Equal(t, 10, c.DB.MaxIdle)
	assert.Equal(t, "disable", c.DB.SSLMode)
}

func TestDBConfigDSN(t *testing.T) {
	dsn := DBConfig{
		Host: "db", Port: "5432", User: "goteway", Password: "secret", Name: "goteway", SSLMode: "require",
	}.DSN()

	assert.Equal(t, "host=db port=5432 user=goteway password=secret dbname=goteway sslmode=require", dsn)
}

func TestConfigAuthConfigMapping(t *testing.T) {
	c := Config{
		Auth: AuthConfig{
			AccessSecret:         "access",
			RefreshSecret:        "refresh",
			AccessTTL:            time.Minute,
			RefreshTTL:           time.Hour,
			RegistrationTokenTTL: 2 * time.Hour,
			Issuer:               "goteway",
			Audience:             "clients",
			BcryptCost:           10,
			ClockSkew:            3 * time.Second,
		},
	}

	authCfg := c.AuthConfig()

	assert.Equal(t, "access", authCfg.AccessSecret)
	assert.Equal(t, "refresh", authCfg.RefreshSecret)
	assert.Equal(t, time.Minute, authCfg.AccessTTL)
	assert.Equal(t, time.Hour, authCfg.RefreshTTL)
	assert.Equal(t, 2*time.Hour, authCfg.RegistrationTokenTTL)
	assert.Equal(t, "goteway", authCfg.Issuer)
	assert.Equal(t, "clients", authCfg.Audience)
	assert.Equal(t, 10, authCfg.BcryptCost)
	assert.Equal(t, 3*time.Second, authCfg.ClockSkew)

	// What the yaml section produces has to be usable as is.
	require.NoError(t, authCfg.Validate())
}

func TestLoadConfig(t *testing.T) {
	c, err := LoadConfig()

	require.NoError(t, err)
	require.NotNil(t, c)
	assert.NotEmpty(t, c.Server.Port)
	assert.NotEmpty(t, c.Redis.Port)

	// The shipped configuration has to yield a usable auth domain.
	require.NoError(t, c.AuthConfig().Validate())
}
