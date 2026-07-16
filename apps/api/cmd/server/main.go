// Command server is the Tunnex control-plane API.
//
// Boot sequence:
//
//	S0.1 — structured logging, /healthz, graceful shutdown.
//	S0.3 — first-boot secrets bootstrap (fail-loud), crypto self-test, mailer.
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

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tunnexio/tunnex/apps/api/db"
	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
	"github.com/tunnexio/tunnex/apps/api/internal/accesslog"
	"github.com/tunnexio/tunnex/apps/api/internal/agentca"
	"github.com/tunnexio/tunnex/apps/api/internal/auth"
	"github.com/tunnexio/tunnex/apps/api/internal/cliauth"
	"github.com/tunnexio/tunnex/apps/api/internal/mfa"
	"github.com/tunnexio/tunnex/apps/api/internal/config"
	"github.com/tunnexio/tunnex/apps/api/internal/crypto"
	"github.com/tunnexio/tunnex/apps/api/internal/devices"
	apphttp "github.com/tunnexio/tunnex/apps/api/internal/http"
	"github.com/tunnexio/tunnex/apps/api/internal/invites"
	applog "github.com/tunnexio/tunnex/apps/api/internal/log"
	"github.com/tunnexio/tunnex/apps/api/internal/mail"
	"github.com/tunnexio/tunnex/apps/api/internal/nodepush"
	"github.com/tunnexio/tunnex/apps/api/internal/nodes"
	"github.com/tunnexio/tunnex/apps/api/internal/secrets"
	"github.com/tunnexio/tunnex/apps/api/internal/session"
	"github.com/tunnexio/tunnex/apps/api/internal/tenancy"
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
		slog.String("mailer", mailer.Kind()),
	)

	// sealer and mailer are consumed by auth/SSO flows starting in EPIC 2.
	_ = sealer
	_ = mailer

	// --- S0.4: self-provision the schema so `docker compose up` just works ---
	if cfg.AutoMigrate {
		if cfg.DatabaseURL == "" {
			logger.Error("auto_migrate_failed", slog.String("error", "DATABASE_URL is empty"))
			os.Exit(1)
		}
		if err := db.Up(cfg.DatabaseURL); err != nil {
			logger.Error("auto_migrate_failed", slog.String("error", err.Error()))
			os.Exit(1)
		}
		if v, dirty, ok, _ := db.Version(cfg.DatabaseURL); ok {
			logger.Info("schema_migrated", slog.Uint64("version", uint64(v)), slog.Bool("dirty", dirty))
		}
	}

	// Database connection pool (used by the tenancy services).
	pool, err := pgxpool.New(context.Background(), cfg.DatabaseURL)
	if err != nil {
		logger.Error("db_pool_failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
	defer pool.Close()

	// Session store (Redis) + session-backed authentication.
	sessions, err := session.New(cfg.RedisURL, cfg.SessionIdleTTL, cfg.SessionAbsoluteTTL)
	if err != nil {
		logger.Error("session_store_failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
	if !cfg.CookieSecure {
		logger.Warn("cookie_insecure",
			slog.String("warning", "session cookie Secure flag is OFF — development only; set TUNNEX_COOKIE_SECURE=true in production"))
	}
	// APP_BASE_URL builds every email link (verify, reset, invite). Left at the
	// localhost default on a remote deploy, those links point at localhost and are
	// UNREACHABLE from a user's machine — a silent, confusing failure (POC-surfaced).
	// Warn loudly so it's caught at boot, not by a user who can't verify their email.
	if cfg.AppBaseURLLooksLocal() {
		logger.Warn("app_base_url_local",
			slog.String("app_base_url", cfg.AppBaseURL),
			slog.String("warning", "APP_BASE_URL points at localhost — email verification/reset/invite links will be UNREACHABLE from other machines. Set APP_BASE_URL to this server's public URL for any non-local deployment."))
	}

	// Agent CA (root of trust for tunnex-node mTLS): load-or-create, sealed under
	// the master key, fail-loud on unusable, self-test at boot.
	agentCA, caFirstBoot, err := agentca.LoadOrCreate(context.Background(), sqlc.New(pool), sealer)
	if err != nil {
		logger.Error("agent_ca_failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
	if err := agentCA.SelfTest(); err != nil {
		logger.Error("agent_ca_selftest_failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
	logger.Info("agent_ca_ready", slog.Bool("first_boot", caFirstBoot), slog.String("ca_fp", agentCA.Fingerprint()))

	authSvc := auth.NewService(pool, mailer, cfg.AppBaseURL, sessions, logger)
	nodeSvc := nodes.NewService(pool, agentCA, sealer)
	// S7.2: wire the Zero Trust policy source for the desired state (nil in the open
	// build -> no policy field -> agents keep the legacy mesh).
	nodeSvc.SetPolicyProvider(apphttp.NewNodePolicyProvider(pool))
	nodes.LogPolicyHealthTuning(logger) // S7.4b: assumed R + derived T (operator discoverability)
	pushHub := nodepush.New()
	deviceSvc := devices.NewService(pool, pushHub, logger)
	cliAuthSvc := cliauth.NewService(pool, sealer)
	mfaSvc := mfa.NewService(pool, sealer, mailer, logger)

	// S7.5.1 access-log health is SHARED: the flow-event Ingester (mTLS channel) records
	// JSONL-degraded + retention on it; the enterprise query port surfaces it. One instance.
	flowHealth := accesslog.NewHealth()

	membersSvc := tenancy.NewMembershipService(pool, sessions).WithDevicePusher(deviceSvc)
	idpSyncPort := apphttp.NewIdpSyncPort(pool, sealer, membersSvc, deviceSvc, logger)

	router, err := apphttp.NewRouter(logger, apphttp.Deps{
		Orgs:                  tenancy.NewService(pool),
		CliAuth:               cliAuthSvc,
		Auth:                  authSvc,
		Members:               membersSvc,
		Invites:               invites.NewService(pool, mailer, cfg.AppBaseURL, logger),
		Nodes:                 nodeSvc,
		Devices:               deviceSvc,
		Sessions:              sessions,
		Mfa:                   mfaSvc,
		SSO:                   apphttp.NewSSOPort(pool, sealer, sessions.Client(), cfg.AppBaseURL, logger),
		Policy:                apphttp.NewPolicyPort(pool, pushHub),
		AccessLog:             apphttp.NewAccessLogPort(pool, flowHealth),
		IdpSync:               idpSyncPort,
		DeviceApprovalEnabled: apphttp.NewDeviceApprovalEdition(),
		DeviceHealthEnabled:   apphttp.NewDeviceHealthEdition(),
		MfaEnforceEnabled:     apphttp.NewMfaEnforceEdition(),
		CookieSecure:          cfg.CookieSecure,
		AppBaseURL:            cfg.AppBaseURL,
		CORSAllowedOrigins:    cfg.CORSAllowedOrigins,
		AuthFn:                apphttp.SessionAuth(sessions, sqlc.New(pool)),
		BearerFn:              apphttp.BearerAuth(sqlc.New(pool)),
	})
	if err != nil {
		logger.Error("router_init_failed", slog.String("error", err.Error()))
		os.Exit(1)
	}

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// mTLS agent control channel (separate listener; client certs verified vs CA).
	agentCh := apphttp.NewAgentChannel(nodeSvc, agentCA, pushHub, logger)
	// S7.5.1 flow-event ingest: the PG hot-window (queryable access-event store), wired onto the
	// SAME mTLS channel (node identity = client cert). (S7.5.1b) the JSONL on-disk source-of-truth
	// + verbatim export are DEFERRED — v1 is PG-only.
	retentionStop := make(chan struct{})
	fq := sqlc.New(pool)
	agentCh.SetFlowIngester(accesslog.NewIngester(pool, accesslog.SQLGrantResolver{Q: fq}, accesslog.SQLDeviceResolver{Q: fq}, flowHealth, nil))
	// D3 retention sweep: without this loop access_events grows unbounded and exhausts the DB
	// disk. Run it on an interval: delete by ingest age + trim each org to the row cap. Drop-count
	// + failure land on flowHealth.
	go func() {
		t := time.NewTicker(accesslog.RetentionSweepInterval)
		defer t.Stop()
		for {
			select {
			case <-retentionStop:
				return
			case <-t.C:
				sctx, scancel := context.WithTimeout(context.Background(), 2*time.Minute)
				orgs, err := fq.DistinctAccessEventOrgs(sctx)
				if err == nil {
					_, err = accesslog.Retain(sctx, fq, flowHealth, time.Now(), 0, 0, orgs)
				}
				if err != nil {
					logger.Error("flowlog_retention_sweep_failed", slog.String("error", err.Error()))
				}
				scancel()
			}
		}
	}()
	// S7.5.2 IdP-group sync poller (enterprise; no-op in the open build). Reconciles mapped
	// directory groups every ~10min. Cancelled on shutdown.
	pollCtx, pollCancel := context.WithCancel(context.Background())
	apphttp.StartIdpSyncPoller(pollCtx, idpSyncPort, logger)
	// S7.5.4 temporary-grant expiry sweep (enterprise only; no-op in the open build):
	// a lapsed temporary grant's /32 is pushed off every org gateway promptly. Shares
	// pollCtx (cancelled on shutdown).
	apphttp.StartPolicyGrantSweeper(pollCtx, pool, pushHub)
	// S7.5.3 device-health staleness sweep (enterprise only): a stale report is
	// ABSENCE, and absence never blocks — clears health_blocked past the TTL and
	// pushes the affected orgs. Shares pollCtx (cancelled on shutdown).
	if apphttp.NewDeviceHealthEdition() {
		go deviceSvc.StartHealthSweeper(pollCtx)
	} else {
		// DOWNGRADE-RELEASE ([1]): device-health is OFF in this build, so no report
		// will arrive and the sweeper never runs — a device left health_blocked by a
		// prior ENTERPRISE deployment would be excluded from every gateway FOREVER
		// (silent permanent network loss). Disabling a feature must RELEASE its
		// enforcement (the downgrade mirror of unlock-then-opt-in). Best-effort at
		// boot; the interval reconcile still converges if the push is missed.
		if n, err := deviceSvc.ReleaseAllHealthBlocks(context.Background()); err != nil {
			logger.Warn("health_downgrade_release_failed", slog.String("error", err.Error()))
		} else if n > 0 {
			logger.Info("health_downgrade_release", slog.Int("released", n))
		}
	}

	agentTLS, err := agentCh.TLSConfig("tunnex-control")
	if err != nil {
		logger.Error("agent_channel_tls_failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
	agentSrv := &http.Server{
		Addr:              cfg.AgentAddr,
		Handler:           agentCh.Handler(),
		TLSConfig:         agentTLS,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		logger.Info("agent_channel_starting", slog.String("addr", cfg.AgentAddr))
		if err := agentSrv.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("agent_channel_failed", slog.String("error", err.Error()))
		}
	}()

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
	_ = agentSrv.Shutdown(ctx)
	pollCancel()         // stop the idp-sync poller
	close(retentionStop) // stop the retention sweep loop
	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("api_shutdown_error", slog.String("error", err.Error()))
		os.Exit(1)
	}
	logger.Info("api_stopped")
}
