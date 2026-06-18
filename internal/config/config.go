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

	"github.com/coreoptimizer/promptcloak/internal/detect"
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

	// Detect is the fully-resolved, backend-agnostic detection configuration.
	Detect detect.Options
	Redis  RedisConfig
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
		Detect: detect.Options{
			Backend: getenv("DETECTOR", detect.BackendPresidio),
			// Shared detection knobs use generic DETECT_* names; the legacy
			// PRESIDIO_* names are accepted as a fallback for compatibility.
			Language:       getenvFallback("DETECT_LANGUAGE", "PRESIDIO_LANGUAGE", "en"),
			Entities:       getenvListFallback("DETECT_ENTITIES", "PRESIDIO_ENTITIES"),
			ScoreThreshold: getenvFloatFallback("DETECT_SCORE_THRESHOLD", "PRESIDIO_SCORE_THRESHOLD", 0.5),
			Timeout:        getenvDurationFallback("DETECT_TIMEOUT", "PRESIDIO_TIMEOUT", 5*time.Second),
			PresidioURL:    getenv("PRESIDIO_URL", "http://presidio-analyzer.promptcloak-system.svc:3000"),
			GCPDLP: detect.GCPDLPOptions{
				Project:       getenv("GCP_DLP_PROJECT", ""),
				Location:      getenv("GCP_DLP_LOCATION", "global"),
				MinLikelihood: getenv("GCP_DLP_MIN_LIKELIHOOD", "POSSIBLE"),
				Token:         getenv("GCP_DLP_TOKEN", ""),
				Endpoint:      getenv("GCP_DLP_ENDPOINT", "https://dlp.googleapis.com"),
			},
		},
		Redis: RedisConfig{
			Addr:     getenv("REDIS_ADDR", ""),
			Password: getenv("REDIS_PASSWORD", ""),
			DB:       getenvInt("REDIS_DB", 0),
		},
	}

	// Validate only the selected backend's required settings.
	switch c.Detect.Backend {
	case detect.BackendPresidio:
		if c.Detect.PresidioURL == "" {
			return nil, fmt.Errorf("PRESIDIO_URL must be set for DETECTOR=%s", detect.BackendPresidio)
		}
	case detect.BackendGCPDLP:
		if c.Detect.GCPDLP.Project == "" {
			return nil, fmt.Errorf("GCP_DLP_PROJECT must be set for DETECTOR=%s", detect.BackendGCPDLP)
		}
	case detect.BackendRegex:
		// no required configuration
	default:
		return nil, fmt.Errorf("unknown DETECTOR %q (want %q, %q or %q)",
			c.Detect.Backend, detect.BackendPresidio, detect.BackendRegex, detect.BackendGCPDLP)
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

// firstEnv returns the value of the first set, non-empty key among keys. It
// lets generic DETECT_* settings fall back to the legacy PRESIDIO_* names.
func firstEnv(keys ...string) (string, bool) {
	for _, k := range keys {
		if v, ok := os.LookupEnv(k); ok && v != "" {
			return v, true
		}
	}
	return "", false
}

func getenvFallback(primary, fallback, def string) string {
	if v, ok := firstEnv(primary, fallback); ok {
		return v
	}
	return def
}

func getenvFloatFallback(primary, fallback string, def float64) float64 {
	v, ok := firstEnv(primary, fallback)
	if !ok {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return def
	}
	return f
}

func getenvDurationFallback(primary, fallback string, def time.Duration) time.Duration {
	v, ok := firstEnv(primary, fallback)
	if !ok {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}

// getenvListFallback parses a comma-separated list (trimming whitespace and
// dropping empties) from the first set key. Returns nil when none is set.
func getenvListFallback(primary, fallback string) []string {
	v, ok := firstEnv(primary, fallback)
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
