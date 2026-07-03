// Package config loads runtime configuration from the environment.
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
	// SecretsDir is the dedicated volume holding the roots of trust (S0.3).
	SecretsDir string
	// SMTP holds mail delivery configuration (S0.3).
	SMTP SMTP
}

// SMTP holds outbound mail settings.
type SMTP struct {
	Host     string
	Port     string
	From     string
	Username string
	Password string
}

// IsProduction reports whether the process runs in a production environment.
func (c Config) IsProduction() bool { return c.Env == "production" }

// Load reads configuration from the environment, applying sane defaults so the
// server runs with zero configuration during development.
func Load() Config {
	return Config{
		Addr:       getenv("TUNNEX_API_ADDR", ":8080"),
		Env:        getenv("TUNNEX_ENV", "development"),
		LogLevel:   strings.ToLower(getenv("TUNNEX_LOG_LEVEL", "info")),
		SecretsDir: getenv("TUNNEX_SECRETS_DIR", "/var/lib/tunnex/secrets"),
		SMTP: SMTP{
			Host:     getenv("SMTP_HOST", ""),
			Port:     getenv("SMTP_PORT", "1025"),
			From:     getenv("SMTP_FROM", "no-reply@tunnex.local"),
			Username: getenv("SMTP_USERNAME", ""),
			Password: getenv("SMTP_PASSWORD", ""),
		},
	}
}

func getenv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}
