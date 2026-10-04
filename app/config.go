package app

import (
	"context"
	"fmt"
	"net"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"

	"goteway/pkg/auth"
	"goteway/pkg/log"
	"goteway/pkg/request"
	"goteway/static"
)

// Upstream is a service the gateway forwards to.
type Upstream struct {
	Path string `yaml:"path"`
	Host string `yaml:"host"`
}

// ServerConfig is the http listener configuration.
type ServerConfig struct {
	Host     string `yaml:"host"`
	Port     string `yaml:"port"`
	Env      string `yaml:"env"`
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
}

// RedisConfig is the cache backend configuration.
//
// Username and Password authenticate the client, and are empty against a redis
// without authentication. DB is the database index, whose zero value is the
// intended default rather than an unset one, so none of the three are defaulted
// in withDefaults.
type RedisConfig struct {
	Host     string `yaml:"host"`
	Port     string `yaml:"port"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	DB       int    `yaml:"db"`
}

// Addr renders the redis server address.
func (c RedisConfig) Addr() string {
	return net.JoinHostPort(c.Host, c.Port)
}

// DBConfig is the relational store configuration.
type DBConfig struct {
	Host     string `yaml:"host"`
	Port     string `yaml:"port"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	Name     string `yaml:"name"`
	SSLMode  string `yaml:"sslmode"`
	MaxConns int    `yaml:"max_conns"`
	MaxIdle  int    `yaml:"max_idle"`
}

// DSN renders the postgres connection string.
func (c DBConfig) DSN() string {
	return fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		c.Host,
		c.Port,
		c.User,
		c.Password,
		c.Name,
		c.SSLMode,
	)
}

// AuthConfig is the yaml shape of the auth domain configuration. It is mapped
// to auth.Config, which is what the domain actually consumes.
type AuthConfig struct {
	AccessSecret         string        `yaml:"access_secret"`
	RefreshSecret        string        `yaml:"refresh_secret"`
	AccessTTL            time.Duration `yaml:"access_ttl"`
	RefreshTTL           time.Duration `yaml:"refresh_ttl"`
	RegistrationTokenTTL time.Duration `yaml:"registration_token_ttl"`
	Issuer               string        `yaml:"issuer"`
	Audience             string        `yaml:"audience"`
	BcryptCost           int           `yaml:"bcrypt_cost"`
	ClockSkew            time.Duration `yaml:"clock_skew"`
}

// Config is the whole gateway configuration.
type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Redis    RedisConfig    `yaml:"redis"`
	DB       DBConfig       `yaml:"db"`
	Auth     AuthConfig     `yaml:"auth"`
	Services ServicesConfig `yaml:"services"`
}

// ServicesConfig are the upstreams the gateway proxies to.
type ServicesConfig struct {
	Internal []Upstream `yaml:"internal"`
	External []Upstream `yaml:"external"`
}

// AuthConfig maps the yaml section to the auth domain configuration. Auth
// defaults live in auth.Config, not here.
func (c Config) AuthConfig() auth.Config {
	return auth.Config{
		AccessSecret:         c.Auth.AccessSecret,
		RefreshSecret:        c.Auth.RefreshSecret,
		AccessTTL:            c.Auth.AccessTTL,
		RefreshTTL:           c.Auth.RefreshTTL,
		RegistrationTokenTTL: c.Auth.RegistrationTokenTTL,
		Issuer:               c.Auth.Issuer,
		Audience:             c.Auth.Audience,
		BcryptCost:           c.Auth.BcryptCost,
		ClockSkew:            c.Auth.ClockSkew,
	}
}

func (c Config) convertServicesToRequest() request.ServicesConfig {
	rsc := request.ServicesConfig{
		Internal: make([]request.ServiceConfig, 0, len(c.Services.Internal)),
		External: make([]request.ServiceConfig, 0, len(c.Services.External)),
	}
	for _, s := range c.Services.Internal {
		rsc.Internal = append(rsc.Internal, request.ServiceConfig{Path: s.Path, Host: s.Host})
	}
	for _, s := range c.Services.External {
		rsc.External = append(rsc.External, request.ServiceConfig{Path: s.Path, Host: s.Host})
	}
	return rsc
}

func (c Config) withDefaults() Config {
	if c.DB.MaxConns == 0 {
		c.DB.MaxConns = 100
	}
	if c.DB.MaxIdle == 0 {
		c.DB.MaxIdle = 10
	}
	if c.Server.Port == "" {
		c.Server.Port = "8080"
	}
	if c.Server.Env == "" {
		c.Server.Env = "local"
	}
	if c.Redis.Port == "" {
		c.Redis.Port = "6379"
	}
	if c.DB.SSLMode == "" {
		c.DB.SSLMode = "disable"
	}
	return c
}

// LoadConfig reads the embedded configuration file, expanding environment
// variables and applying defaults. It is exported so that admin commands share
// exactly the same configuration as the server.
func LoadConfig() (*Config, error) {
	// Load .env file if present (non-fatal)
	if err := godotenv.Load(); err != nil {
		log.LogWarn(context.Background(), "failed to load .env file: "+err.Error())
	}

	expanded := expandEnv(string(static.GetConfigFile()))

	var c Config
	if err := yaml.Unmarshal([]byte(expanded), &c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	c = c.withDefaults()
	return &c, nil
}

// TLSEnabled reports whether the server should expose TLS. TLS is enabled for
// any environment other than "local" or "test".
func (c Config) TLSEnabled() bool {
	env := strings.ToLower(strings.TrimSpace(c.Server.Env))
	if env == "local" || env == "test" {
		return false
	}
	return true
}

// envRef matches ${VAR} and ${VAR:-default}. Anything else, like a nested
// expansion, is left untouched so a typo shows up in the loaded configuration
// instead of silently becoming empty.
var envRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::-(.*?))?\}`)

// expandEnv resolves environment variables in the configuration, falling back to
// the default written in the file when the variable is unset or empty.
//
// os.ExpandEnv cannot be used for this: it reads ${VAR:-default} as a variable
// named "VAR:-default", which always resolves to an empty string.
func expandEnv(in string) string {
	return envRef.ReplaceAllStringFunc(in, func(ref string) string {
		match := envRef.FindStringSubmatch(ref)
		if value := os.Getenv(match[1]); value != "" {
			return value
		}
		return match[2]
	})
}
