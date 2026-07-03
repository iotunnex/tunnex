// Command server is the Tunnex control-plane API.
//
// Foundation story (S0.1): it serves /healthz with structured, correlated
// logging and a graceful shutdown. Database, Redis, auth, and the node-agent
// control protocol are layered on in later stories.
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
	applog "github.com/tunnexio/tunnex/apps/api/internal/log"
	apphttp "github.com/tunnexio/tunnex/apps/api/internal/http"
)

func main() {
	cfg := config.Load()

	logger := applog.New(cfg.LogLevel)
	slog.SetDefault(logger)

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           apphttp.NewRouter(logger),
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Run the server until we receive a termination signal.
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
