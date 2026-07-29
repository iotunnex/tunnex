// Package leader provides single-writer election for the control plane's in-process schedulers (S11 D4).
//
// WHY THIS EXISTS. The CP runs three schedulers — the hub failover tick, the CRL rebuild, and the flow-log
// retention sweep. N replicas meant N tickers, so the deployment was pinned to replicas=1 and "is the control
// plane HA?" had to be answered "no, and the fix is registered". Leader election unlocks N replicas without
// N writers.
//
// ONLY THE SCHEDULERS ARE GATED. Request serving runs on EVERY replica — a follower is a fully functional
// API server that simply does not tick. Gating request serving would take healthy replicas out of a load
// balancer for no reason.
//
// MECHANISM: a POSTGRES SESSION-SCOPED ADVISORY LOCK (pg_advisory_lock), argued against the alternatives:
//
//   - A LEASE TABLE (row with a TTL, renewed by the leader) requires comparing wall clocks across replicas.
//     Under clock skew — or a leader that stalls past its TTL and resumes — TWO replicas can believe they
//     hold the lease simultaneously. For these schedulers that means a double failover promotion or two
//     concurrent CRL rebuilds. The wrong failure direction.
//   - KUBERNETES LEASES (coordination.k8s.io) are unavailable by construction: the control plane must run on
//     a plain VM pair as well as in Kubernetes, and a mechanism that only works in one is not a mechanism.
//   - A SESSION-SCOPED ADVISORY LOCK is granted by Postgres to exactly ONE session, and is released BY THE
//     DATABASE when that session's connection ends — including when the leader is SIGKILLed, loses its
//     network, or panics. No TTL, no clock, no stale-lock reaper. Two leaders are impossible while both
//     replicas talk to the same Postgres, which is already a hard requirement of the deployment.
//
// FAILURE DIRECTION (the safety property, stated deliberately): this fails toward NO LEADER, never toward
// two. A gap with nothing ticking delays a failover promotion or a CRL refresh by seconds; two leaders
// ticking would double-promote or double-rebuild. The lock is the safety boundary, and it is enforced by
// Postgres rather than by our code being correct.
//
// HONEST LIMIT (the fourth such sentence in the S11 paper): after a leader dies, a follower takes over
// within RetryInterval plus however long Postgres takes to notice the dead connection. With the default
// RetryInterval that is bounded by ~10s in the clean case (process exit closes the socket immediately); a
// hard network partition of the leader's DB connection is bounded instead by the server's TCP keepalive
// settings, which can be minutes. During that window NOTHING ticks — which is safe, not degraded: the
// schedulers are periodic reconcilers, not request-path work, and running tunnels are unaffected.
package leader

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// RetryInterval is how often a follower re-attempts acquisition. It bounds the failover gap.
const RetryInterval = 10 * time.Second

// SchedulerLockKey is the advisory-lock key for the CP scheduler leadership. Advisory locks live in a global
// int64 keyspace shared with every other advisory lock in the database, so the value is chosen to be
// distinctive and is declared exactly once.
const SchedulerLockKey int64 = 0x54554E4E58530001 // "TUNNXS" + slot 1

// Elector reports whether THIS process currently holds scheduler leadership.
type Elector struct {
	leading atomic.Bool
}

// IsLeader reports current leadership. It is safe for concurrent use and is the ONLY thing a scheduler needs
// to consult: `if !elector.IsLeader() { continue }` at the top of each tick.
//
// Deliberately a snapshot, not a lock: a tick that begins microseconds before leadership is lost may still
// run to completion. That is acceptable because every scheduler's work is idempotent (the failover tick is an
// atomic generation bump, the CRL rebuild is content-idempotent, the retention sweep is a delete-by-age) —
// the lock prevents STEADY-STATE double-ticking, and idempotence covers the microsecond seam. A design that
// needed exactly-once semantics here would need a different mechanism, and that is stated rather than assumed.
func (e *Elector) IsLeader() bool { return e.leading.Load() }

// Run campaigns for leadership until ctx is cancelled. It blocks, so callers run it in a goroutine.
//
// It holds a DEDICATED connection out of the pool for the whole duration of leadership: a session-scoped
// advisory lock belongs to a CONNECTION, and pgxpool recycles connections between queries, so the lock would
// be released the moment the connection returned to the pool. Holding it also means the lock's fate is tied
// to that connection's liveness — exactly the property the mechanism relies on.
func (e *Elector) Run(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) {
	for {
		if ctx.Err() != nil {
			return
		}
		if err := e.campaign(ctx, pool, log); err != nil && ctx.Err() == nil && log != nil {
			log.Warn("leader_campaign_error", "error", err.Error(), "retry_in", RetryInterval.String())
		}
		// Leadership lost (or never acquired) — always drop the flag before sleeping, so a replica that
		// lost its DB connection stops ticking immediately rather than at the next successful campaign.
		e.leading.Store(false)
		select {
		case <-ctx.Done():
			return
		case <-time.After(RetryInterval):
		}
	}
}

// campaign acquires a connection, tries the lock, and — if it wins — holds both until leadership ends.
func (e *Elector) campaign(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release() // releasing the connection also releases the session lock

	var got bool
	// TRY, never wait: pg_try_advisory_lock returns immediately. A blocking pg_advisory_lock would park this
	// goroutine inside Postgres indefinitely, making "am I the leader?" unanswerable and shutdown ugly.
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", SchedulerLockKey).Scan(&got); err != nil {
		return err
	}
	if !got {
		return nil // someone else leads; the caller sleeps and retries
	}

	e.leading.Store(true)
	if log != nil {
		log.Info("leader_acquired", "key", SchedulerLockKey)
	}
	defer func() {
		e.leading.Store(false)
		if log != nil {
			log.Info("leader_released", "key", SchedulerLockKey)
		}
	}()

	// Hold leadership until the context ends or the connection dies. The ping detects a dead connection so
	// this replica stops claiming leadership promptly; Postgres has already released the lock by then.
	ticker := time.NewTicker(RetryInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			// Graceful shutdown: unlock explicitly so a follower can take over immediately rather than
			// waiting for the connection teardown to propagate.
			unlockCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_, _ = conn.Exec(unlockCtx, "SELECT pg_advisory_unlock($1)", SchedulerLockKey)
			cancel()
			return nil
		case <-ticker.C:
			if err := conn.Ping(ctx); err != nil {
				if errors.Is(err, context.Canceled) {
					return nil
				}
				return err // connection dead → leadership lost → Postgres has freed the lock
			}
		}
	}
}
