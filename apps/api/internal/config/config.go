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
	// DatabaseURL is the postgres DSN (S0.4).
	DatabaseURL string
	// AutoMigrate runs pending migrations on boot so `docker compose up`
	// self-provisions the schema (S0.4).
	AutoMigrate bool
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
		LogLevel:    strings.ToLower(getenv("TUNNEX_LOG_LEVEL", "info")),
		SecretsDir:  getenv("TUNNEX_SECRETS_DIR", "/var/lib/tunnex/secrets"),
		DatabaseURL: getenv("DATABASE_URL", ""),
		AutoMigrate: getbool("TUNNEX_AUTO_MIGRATE", true),
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

func getbool(key string, fallback bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return fallback
	}
	switch strings.ToLower(v) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}
