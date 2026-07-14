// Package config loads runtime configuration from the environment.
package config

import (
	"os"
	"strings"
	"time"
)

// Config holds the process configuration resolved at startup.
type Config struct {
	// Addr is the host:port the HTTP server binds to.
	Addr string
	// AgentAddr is the host:port the mTLS agent control channel binds to (S3.1).
	AgentAddr string
	// Env is the deployment environment name (development, production).
	Env string
	// LogLevel controls the minimum slog level (debug, info, warn, error).
	LogLevel string
	// SecretsDir is the dedicated volume holding the roots of trust (S0.3).
	SecretsDir string
	// FlowLogDir is where the S7.5.1 access-event JSONL source-of-truth stream is written
	// (rotated segments + manifests). On the customer's disk; the PG hot-window is separate.
	FlowLogDir string
	// DatabaseURL is the postgres DSN (S0.4).
	DatabaseURL string
	// AutoMigrate runs pending migrations on boot so `docker compose up`
	// self-provisions the schema (S0.4).
	AutoMigrate bool
	// AppBaseURL is the public base URL used to build email links (S2.1).
	AppBaseURL string
	// RedisURL is the session store DSN (S2.2).
	RedisURL string
	// CookieSecure sets the Secure flag on the session cookie. MUST be true in
	// production; a false value is logged loudly at boot.
	CookieSecure bool
	// SessionIdleTTL is the sliding inactivity timeout (S2.2).
	SessionIdleTTL time.Duration
	// SessionAbsoluteTTL is the hard maximum session lifetime (S2.2).
	SessionAbsoluteTTL time.Duration
	// CORSAllowedOrigins are the EXACT origins allowed to make cross-origin,
	// BEARER-authenticated requests (S6.2 desktop client, whose renderer origin
	// is app://tunnex). Credentials (cookies) are NEVER allowed cross-origin, so
	// this cannot weaken the same-origin cookie/CSRF posture. Comma-separated.
	CORSAllowedOrigins []string
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

// AppBaseURLLooksLocal reports whether AppBaseURL points at the local host. On a
// remote deploy this is a misconfiguration: every email link (verify/reset/invite)
// would point at localhost and be unreachable from the user's machine. Boot warns
// loudly on it (POC-surfaced: a remote deploy shipped localhost verify links).
func (c Config) AppBaseURLLooksLocal() bool {
	if c.AppBaseURL == "" {
		return true // unset is not a reachable remote URL either
	}
	for _, local := range []string{"localhost", "127.0.0.1", "0.0.0.0"} {
		if strings.Contains(c.AppBaseURL, local) {
			return true
		}
	}
	return false
}

// Load reads configuration from the environment, applying sane defaults so the
// server runs with zero configuration during development.
func Load() Config {
	return Config{
		Addr:               getenv("TUNNEX_API_ADDR", ":8080"),
		AgentAddr:          getenv("TUNNEX_AGENT_ADDR", ":8443"),
		Env:                getenv("TUNNEX_ENV", "development"),
		LogLevel:           strings.ToLower(getenv("TUNNEX_LOG_LEVEL", "info")),
		SecretsDir:         getenv("TUNNEX_SECRETS_DIR", "/var/lib/tunnex/secrets"),
		FlowLogDir:         getenv("TUNNEX_FLOWLOG_DIR", "/var/lib/tunnex/flowlog"),
		DatabaseURL:        getenv("DATABASE_URL", ""),
		AutoMigrate:        getbool("TUNNEX_AUTO_MIGRATE", true),
		AppBaseURL:         getenv("APP_BASE_URL", "http://localhost"),
		RedisURL:           getenv("REDIS_URL", "redis://redis:6379/0"),
		CookieSecure:       getbool("TUNNEX_COOKIE_SECURE", false),
		SessionIdleTTL:     getdur("TUNNEX_SESSION_IDLE_TTL", 24*time.Hour),
		SessionAbsoluteTTL: getdur("TUNNEX_SESSION_ABSOLUTE_TTL", 720*time.Hour),
		CORSAllowedOrigins: splitList(getenv("TUNNEX_CORS_ALLOWED_ORIGINS", "app://tunnex")),
		SMTP: SMTP{
			Host:     getenv("SMTP_HOST", ""),
			Port:     getenv("SMTP_PORT", "1025"),
			From:     getenv("SMTP_FROM", "no-reply@tunnex.local"),
			Username: getenv("SMTP_USERNAME", ""),
			Password: getenv("SMTP_PASSWORD", ""),
		},
	}
}

// splitList parses a comma-separated env value into a trimmed, non-empty slice.
func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func getenv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func getdur(key string, fallback time.Duration) time.Duration {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
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
