// Command server is the Tunnex control-plane API.
//
// Boot sequence:
//   S0.1 — structured logging, /healthz, graceful shutdown.
//   S0.3 — first-boot secrets bootstrap (fail-loud), crypto self-test, mailer.
//
// Database, Redis, auth, and the node-agent control protocol layer on later.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tunnexio/tunnex/apps/api/internal/config"
	"github.com/tunnexio/tunnex/apps/api/internal/crypto"
	apphttp "github.com/tunnexio/tunnex/apps/api/internal/http"
	applog "github.com/tunnexio/tunnex/apps/api/internal/log"
	"github.com/tunnexio/tunnex/apps/api/internal/mail"
	"github.com/tunnexio/tunnex/apps/api/internal/secrets"
)

func main() {
	cfg := config.Load()

	logger := applog.New(cfg.LogLevel)
	slog.SetDefault(logger)

	// --- S0.3: bootstrap roots of trust (fail loudly, never regenerate) ---
	sec, err := secrets.LoadOrInit(cfg.SecretsDir)
	if err != nil {
		logger.Error("secrets_bootstrap_failed",
			slog.String("secrets_dir", cfg.SecretsDir),
			slog.String("error", err.Error()),
		)
		os.Exit(1)
	}

	sealer, err := crypto.NewSealer(sec.MasterKey)
	if err != nil {
		logger.Error("sealer_init_failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
	if err := crypto.SelfTest(sealer); err != nil {
		logger.Error("crypto_selftest_failed", slog.String("error", err.Error()))
		os.Exit(1)
	}

	mailer := mail.New(mail.Config{
		Host:       cfg.SMTP.Host,
		Port:       cfg.SMTP.Port,
		From:       cfg.SMTP.From,
		Username:   cfg.SMTP.Username,
		Password:   cfg.SMTP.Password,
		DevLogging: !cfg.IsProduction(),
	}, logger)

	// Log fingerprints (never the secrets). Stable fingerprints across restarts
	// prove keys were reused, not regenerated.
	logger.Info("secrets_ready",
		slog.Bool("first_boot", sec.GeneratedAny),
		slog.String("master_key_fp", secrets.Fingerprint(sec.MasterKey)),
		slog.String("session_secret_fp", secrets.Fingerprint(sec.SessionSecret)),
		slog.String("wg_server_pubkey_fp", secrets.Fingerprint(sec.WGPublicKey)),
		slog.String("mailer", mailer.Kind()),
	)

	// sealer and mailer are consumed by auth/SSO flows starting in EPIC 2.
	_ = sealer
	_ = mailer

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           apphttp.NewRouter(logger),
		ReadHeaderTimeout: 10 * time.Second,
	}

	serverErr := make(chan error, 1)
	go func() {
		logger.Info("api_starting",
			slog.String("addr", cfg.Addr),
			slog.String("env", cfg.Env),
		)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-serverErr:
		logger.Error("api_failed", slog.String("error", err.Error()))
		os.Exit(1)
	case sig := <-stop:
		logger.Info("api_shutting_down", slog.String("signal", sig.String()))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("api_shutdown_error", slog.String("error", err.Error()))
		os.Exit(1)
	}
	logger.Info("api_stopped")
}
