// Package config loads runtime configuration from the environment.
//
// First-boot secret generation (S0.3) is intentionally NOT here yet — for the
// foundation story we only read what we need to serve /healthz. Later stories
// extend this to load DB/Redis credentials and the master encryption key.
package config

import (
	"os"
	"strings"
)

// Config holds the process configuration resolved at startup.
type Config struct {
	// Addr is the host:port the HTTP server binds to.
	Addr string
	// Env is the deployment environment name (development, production).
	Env string
	// LogLevel controls the minimum slog level (debug, info, warn, error).
	LogLevel string
}

// Load reads configuration from the environment, applying sane defaults so the
// server runs with zero configuration during development.
func Load() Config {
	return Config{
		Addr:     getenv("TUNNEX_API_ADDR", ":8080"),
		Env:      getenv("TUNNEX_ENV", "development"),
		LogLevel: strings.ToLower(getenv("TUNNEX_LOG_LEVEL", "info")),
	}
}

func getenv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}
