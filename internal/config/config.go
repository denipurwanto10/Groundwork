package config

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	Server   ServerConfig
	Database DatabaseConfig
	Auth     AuthConfig
}

type ServerConfig struct {
	// Host is the interface the server binds to. It defaults to the loopback
	// address: the API has no TLS of its own, so it must not be reachable
	// from the network unless somebody deliberately opens it up.
	Host         string
	Port         string
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	IdleTimeout  time.Duration
	// Compression turns the gzip layer on or off. It is on by default: the
	// zone and region answers are large and compress about ten to one.
	Compression bool
	// CompressThreshold is the smallest body worth compressing.
	CompressThreshold int
}

// Addr is the address the HTTP server listens on.
func (s ServerConfig) Addr() string {
	return s.Host + ":" + s.Port
}

// Loopback reports whether the server only accepts connections from the
// machine itself. A server that listens on every interface is reachable from
// the network, and this one speaks plain HTTP: it belongs behind a reverse
// proxy with TLS, not directly on a public address.
func (s ServerConfig) Loopback() bool {
	host := strings.TrimSpace(s.Host)
	switch strings.ToLower(host) {
	case "", "localhost":
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// AuthConfig holds the JWT signing secret and token lifetime. The secret is
// never hardcoded: it comes from JWT_SECRET in the environment.
type AuthConfig struct {
	Secret string
	TTL    time.Duration
	// RefreshTTL is the lifetime of the token that renews the access token.
	RefreshTTL time.Duration
}

type DatabaseConfig struct {
	Host            string
	Port            string
	User            string
	Password        string
	Name            string
	SSLMode         string
	MaxConns        int32
	MinConns        int32
	MaxConnLifetime time.Duration
	MaxConnIdleTime time.Duration
	ConnectTimeout  time.Duration
}

func Load() (*Config, error) {
	_ = godotenv.Load()

	cfg := &Config{
		Server: ServerConfig{
			Host:              getEnv("SERVER_HOST", "127.0.0.1"),
			Port:              getEnv("SERVER_PORT", "8080"),
			ReadTimeout:       getDurationEnv("SERVER_READ_TIMEOUT", 10*time.Second),
			WriteTimeout:      getDurationEnv("SERVER_WRITE_TIMEOUT", 10*time.Second),
			IdleTimeout:       getDurationEnv("SERVER_IDLE_TIMEOUT", 60*time.Second),
			Compression:       getBoolEnv("SERVER_COMPRESSION", true),
			CompressThreshold: getIntEnv("SERVER_COMPRESS_THRESHOLD", 1024),
		},
		Database: DatabaseConfig{
			Host:            getEnv("DB_HOST", "localhost"),
			Port:            getEnv("DB_PORT", "5432"),
			User:            getEnv("DB_USER", "postgres"),
			Password:        getEnv("DB_PASSWORD", ""),
			Name:            getEnv("DB_NAME", "entity_management"),
			SSLMode:         getEnv("DB_SSL_MODE", "disable"),
			MaxConns:        getInt32Env("DB_MAX_CONNS", 10),
			MinConns:        getInt32Env("DB_MIN_CONNS", 0),
			MaxConnLifetime: getDurationEnv("DB_MAX_CONN_LIFETIME", 30*time.Minute),
			MaxConnIdleTime: getDurationEnv("DB_MAX_CONN_IDLE_TIME", 5*time.Minute),
			ConnectTimeout:  getDurationEnv("DB_CONNECT_TIMEOUT", 10*time.Second),
		},
		Auth: AuthConfig{
			Secret:     getEnv("JWT_SECRET", ""),
			TTL:        getDurationEnv("JWT_TTL", 24*time.Hour),
			RefreshTTL: getDurationEnv("JWT_REFRESH_TTL", 30*24*time.Hour),
		},
	}

	return cfg, nil
}

// Validate rejects an incomplete config instead of letting the server boot with
// a random signing key, which would silently invalidate every token on restart.
func (c *Config) Validate() error {
	if c.Auth.Secret == "" {
		return fmt.Errorf("JWT_SECRET is required (generate one with: openssl rand -base64 32)")
	}
	return nil
}

func (c *DatabaseConfig) DSN() string {
	return "postgres://" + c.User + ":" + c.Password + "@" + c.Host + ":" + c.Port + "/" + c.Name + "?sslmode=" + c.SSLMode
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getDurationEnv(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}

func getInt32Env(key string, fallback int32) int32 {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.ParseInt(v, 10, 32); err == nil {
			return int32(i)
		}
	}
	return fallback
}

// getIntEnv reads a plain integer, used for the compression threshold.
func getIntEnv(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return fallback
}

// getBoolEnv reads a boolean flag. "1", "true", "yes" and "on" mean true.
func getBoolEnv(key string, fallback bool) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	switch v {
	case "":
		return fallback
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return fallback
}
