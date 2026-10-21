package config

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	Server   ServerConfig
	Database DatabaseConfig
	Auth     AuthConfig
}

type ServerConfig struct {
	Port         string
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	IdleTimeout  time.Duration
}

// AuthConfig holds the JWT signing secret and token lifetime. The secret is
// never hardcoded: it comes from JWT_SECRET in the environment.
type AuthConfig struct {
	Secret string
	TTL    time.Duration
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
			Port:         getEnv("SERVER_PORT", "8080"),
			ReadTimeout:  getDurationEnv("SERVER_READ_TIMEOUT", 10*time.Second),
			WriteTimeout: getDurationEnv("SERVER_WRITE_TIMEOUT", 10*time.Second),
			IdleTimeout:  getDurationEnv("SERVER_IDLE_TIMEOUT", 60*time.Second),
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
			Secret: getEnv("JWT_SECRET", ""),
			TTL:    getDurationEnv("JWT_TTL", 24*time.Hour),
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
