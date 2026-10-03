package app

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v2"
)

func TestExpandEnvFromString(t *testing.T) {
	os.Setenv("PORT", "9090")
	os.Setenv("REDIS_HOST", "testredis")
	defer os.Unsetenv("PORT")
	defer os.Unsetenv("REDIS_HOST")

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
	expanded := []byte(os.ExpandEnv(string(testConfig)))
	c := &config{}
	err := yaml.Unmarshal(expanded, c)
	assert.NoError(t, err)
	assert.Equal(t, "9090", c.Server.Port)
	assert.Equal(t, "testredis", c.Redis.Host)
}

func TestConvertServicesToRequest(t *testing.T) {
	c := &config{
		Services: struct {
			Internal []service `yaml:"internal"`
			External []service `yaml:"external"`
		}{
			Internal: []service{{Path: "internal", Host: "int:8080"}},
			External: []service{{Path: "external", Host: "ext:8080"}},
		},
	}
	sc := c.convertServicesToRequest()
	assert.Len(t, sc.Internal, 1)
	assert.Len(t, sc.External, 1)
	assert.Equal(t, "internal", sc.Internal[0].Path)
	assert.Equal(t, "ext:8080", sc.External[0].Host)
}
