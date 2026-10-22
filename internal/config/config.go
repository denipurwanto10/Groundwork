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
	Fleet    FleetConfig
	Service  ServiceConfig
	Push     PushConfig
}

// The defaults of the service schedule. They are the values of the two
// variables of the .env, so a server without them behaves exactly like one
// that has them.
const (
	// defaultServiceIntervalKM is the interval a unit is serviced on when it
	// has no interval of its own.
	defaultServiceIntervalKM = 5000.0
	// defaultServiceWarnKM is how close to the schedule a unit has to come
	// before the server warns about it.
	defaultServiceWarnKM = 500.0
)

// ServiceConfig holds the settings of the service reminders.
//
// The schedule of a unit is counted from its odometer: the next service is
// due when the odometer reaches the reading of the last service plus the
// interval of the unit. A unit without an interval of its own uses the
// default of the server.
type ServiceConfig struct {
	// IntervalDefaultKM is the interval a unit falls back to when
	// vehicles.service_interval_km is null.
	IntervalDefaultKM float64
	// WarnKM is how close to the schedule a unit has to come before it
	// counts as due.
	WarnKM float64
}

// PushConfig holds the VAPID keys of the Web Push feature.
//
// The keys are optional on purpose: a server without them runs exactly as
// before, it simply never sends a push. The private key never leaves the
// server — not the log, not the API, not the repository.
type PushConfig struct {
	// PublicKey is handed to the browser so it can subscribe.
	PublicKey string
	// PrivateKey signs the requests to the push service.
	PrivateKey string
	// Subject says who the sender is: a mailto: address or an https URL.
	Subject string
}

// DefaultPushSubject is the sender the push services are told about when
// nothing else is configured. It names the local machine, which is honest:
// this feature runs on localhost.
const DefaultPushSubject = "mailto:admin@localhost"

// FleetConfig holds the settings of the tracked fleet.
type FleetConfig struct {
	// OfflineAfter is how long a unit may stay silent before it counts as
	// offline. Zero turns the check off, so a unit keeps whatever status its
	// last report gave it.
	OfflineAfter time.Duration
	// IdleAfter is how long a unit may stand still — while it keeps
	// reporting — before the server calls it idle. Zero turns the server-side
	// idle detection off, so only the reporter decides the status again.
	IdleAfter time.Duration
	// IdleRadiusM is how far a unit may drift from its anchor and still
	// count as standing still, in meters.
	IdleRadiusM float64
	// IdleMaxSpeedKmh is the speed above which a report counts as movement,
	// however little the position moved.
	IdleMaxSpeedKmh float64
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
		Fleet: FleetConfig{
			OfflineAfter:    getDurationEnv("SERVER_OFFLINE_AFTER", 60*time.Second),
			IdleAfter:       getDurationEnv("SERVER_IDLE_AFTER", 10*time.Minute),
			IdleRadiusM:     getFloatEnv("SERVER_IDLE_RADIUS_M", 25),
			IdleMaxSpeedKmh: getFloatEnv("SERVER_IDLE_MAX_SPEED_KMH", 2),
		},
		Service: ServiceConfig{
			IntervalDefaultKM: getPositiveFloatEnv("SERVICE_INTERVAL_DEFAULT_KM", defaultServiceIntervalKM),
			WarnKM:            getPositiveFloatEnv("SERVICE_WARN_KM", defaultServiceWarnKM),
		},
		Push: PushConfig{
			PublicKey:  getEnv("VAPID_PUBLIC_KEY", ""),
			PrivateKey: getEnv("VAPID_PRIVATE_KEY", ""),
			Subject:    getEnv("VAPID_SUBJECT", DefaultPushSubject),
		},
	}

	return cfg, nil
}

// Validate rejects an incomplete config instead of letting the server boot with
// a random signing key, which would silently invalidate every token on restart.
//
// The VAPID keys are not part of this: they switch the Web Push feature on,
// and a server without them simply never sends a push.
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

// getFloatEnv reads a decimal number, used for the idle detection.
func getFloatEnv(key string, fallback float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return fallback
}

// getPositiveFloatEnv reads a decimal number that has to be positive.
//
// A value that cannot be read, or that is not positive, is replaced by the
// fallback: the service schedule needs a real interval and a real warning
// distance to work with, and a zero would turn every unit into an overdue
// one.
func getPositiveFloatEnv(key string, fallback float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
			return f
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
