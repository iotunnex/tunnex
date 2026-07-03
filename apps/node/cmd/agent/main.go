// Command agent is the tunnex-node data-plane agent.
//
// Foundation story (S0.1): this is a stub. It logs a registration-handshake
// placeholder, exposes a liveness endpoint so `docker compose` healthchecks are
// meaningful, and stays alive with a periodic heartbeat. The real control-plane
// protocol (mTLS enrollment via one-time join token + desired-state reconcile
// loop) lands in S3.1.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	apiURL := getenv("TUNNEX_API_URL", "http://api:8080")
	healthAddr := getenv("TUNNEX_AGENT_HEALTH_ADDR", ":9091")
	joinToken := os.Getenv("TUNNEX_JOIN_TOKEN") // exchanged for an mTLS cert in S3.1

	logger.Info("agent_starting",
		slog.String("api_url", apiURL),
		slog.String("health_addr", healthAddr),
		slog.Bool("has_join_token", joinToken != ""),
	)

	// Liveness endpoint (S0.2 healthcheck target). Readiness — which will report
	// control-channel + reconcile status — arrives with the real protocol in S3.1.
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "service": "tunnex-node"})
	})
	health := &http.Server{Addr: healthAddr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := health.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("agent_health_failed", slog.String("error", err.Error()))
		}
	}()

	// Placeholder for S3.1: enroll via join token, obtain mTLS cert, then open
	// the control channel and start the reconcile loop against wgctrl.
	logger.Info("agent_registration_pending",
		slog.String("note", "control-plane protocol arrives in S3.1"),
	)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = health.Shutdown(shutdownCtx)
			cancel()
			logger.Info("agent_stopped")
			return
		case <-ticker.C:
			logger.Info("agent_heartbeat")
		}
	}
}

func getenv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}
