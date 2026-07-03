// Command agent is the tunnex-node data-plane agent.
//
// Foundation story (S0.1): this is a stub. It logs a registration-handshake
// placeholder and stays alive with a periodic heartbeat so it is a first-class
// service in `docker compose` from day one. The real control-plane protocol
// (mTLS enrollment via one-time join token + desired-state reconcile loop)
// lands in S3.1.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	apiURL := getenv("TUNNEX_API_URL", "http://api:8080")
	joinToken := os.Getenv("TUNNEX_JOIN_TOKEN") // exchanged for an mTLS cert in S3.1

	logger.Info("agent_starting",
		slog.String("api_url", apiURL),
		slog.Bool("has_join_token", joinToken != ""),
	)

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
