// Package config loads the ext_proc service configuration from the environment.
//
// All knobs are environment variables so the deployment can drive them from a
// ConfigMap/Secret. Sensible defaults are chosen for an in-cluster install in
// the `promptcloak-system` namespace.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the fully-resolved service configuration.
type Config struct {
	// ListenAddr is the gRPC ext_proc listen address.
	ListenAddr string
	// HealthAddr is the HTTP address serving /healthz and /readyz.
	HealthAddr string
	// LogLevel is one of debug, info, warn, error.
	LogLevel string
	// FailOpen controls behavior when a request body cannot be inspected.
	// When true (default) the original body is forwarded; when false the
	// request is rejected with 503. Regulated environments should set false.
	FailOpen bool

	// TokenTTL is how long token->value mappings live in the vault.
	TokenTTL time.Duration
	// TokenSalt is mixed into the token id digest so token ids are not a plain
	// hash of the value. Set this to a per-environment secret.
	TokenSalt string

	Presidio PresidioConfig
	Redis    RedisConfig
}

// PresidioConfig configures the Presidio analyzer client.
type PresidioConfig struct {
	URL            string
	Language       string
	ScoreThreshold float64
	// Entities restricts detection to these Presidio entity types. Empty means
	// "all entities the analyzer supports".
	Entities []string
	Timeout  time.Duration
}

// RedisConfig configures the Redis-backed token vault. An empty Addr selects
// the in-memory vault (single-replica / non-durable; intended for local dev).
type RedisConfig struct {
	Addr     string
	Password string
	DB       int
}

// Load reads configuration from the environment, applying defaults.
func Load() (*Config, error) {
	c := &Config{
		ListenAddr: getenv("LISTEN_ADDR", ":9002"),
		HealthAddr: getenv("HEALTH_ADDR", ":8080"),
		LogLevel:   getenv("LOG_LEVEL", "info"),
		FailOpen:   getenvBool("FAIL_OPEN", true),
		TokenTTL:   getenvDuration("TOKEN_TTL", 24*time.Hour),
		TokenSalt:  getenv("TOKEN_SALT", "promptcloak"),
		Presidio: PresidioConfig{
			URL:            getenv("PRESIDIO_URL", "http://presidio-analyzer.promptcloak-system.svc:3000"),
			Language:       getenv("PRESIDIO_LANGUAGE", "en"),
			ScoreThreshold: getenvFloat("PRESIDIO_SCORE_THRESHOLD", 0.5),
			Entities:       getenvList("PRESIDIO_ENTITIES"),
			Timeout:        getenvDuration("PRESIDIO_TIMEOUT", 5*time.Second),
		},
		Redis: RedisConfig{
			Addr:     getenv("REDIS_ADDR", ""),
			Password: getenv("REDIS_PASSWORD", ""),
			DB:       getenvInt("REDIS_DB", 0),
		},
	}
	if c.Presidio.URL == "" {
		return nil, fmt.Errorf("PRESIDIO_URL must be set")
	}
	return c, nil
}

func getenv(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func getenvBool(key string, def bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

func getenvInt(key string, def int) int {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func getenvFloat(key string, def float64) float64 {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return def
	}
	return f
}

func getenvDuration(key string, def time.Duration) time.Duration {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}

// getenvList parses a comma-separated list, trimming whitespace and dropping
// empties. Returns nil when unset.
func getenvList(key string) []string {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
