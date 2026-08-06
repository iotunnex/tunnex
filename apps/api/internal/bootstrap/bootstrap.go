// Package bootstrap mints the one account a fresh deployment needs to be usable at all.
//
// ⛔ THERE IS NO PUBLIC SIGNUP. A self-hosted control plane is owned by one company: everyone inside
// arrives by invitation, and an invitation has to be sent BY somebody. This package is that somebody's
// account, and it exists because without it a fresh install has no way in.
package bootstrap

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"log/slog"
	"strings"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
	"github.com/tunnexio/tunnex/apps/api/internal/password"
)

// AdminEmail is the CP admin's address.
//
// ⚠ A LOCAL, NON-ROUTABLE ADDRESS ON PURPOSE. It is a login identifier, not a mailbox — nothing is ever
// sent to it, and choosing a real-looking domain would invite an operator to expect mail there.
const AdminEmail = "admin@tunnex.local"

// Store is the slice of sqlc this package needs. ⚠ An interface so the reds can drive both branches
// without a database — and the branch that matters is the one that does NOTHING.
type Store interface {
	CountUsers(ctx context.Context) (int64, error)
	CreateBootstrapAdmin(ctx context.Context, arg sqlc.CreateBootstrapAdminParams) (sqlc.User, error)
}

// EnsureAdmin creates the CP admin on a deployment that has never had a user, and prints its one-time
// credential. On every other start it does nothing at all.
//
// ⛔ IDEMPOTENT, AND THE NO-OP BRANCH IS THE SECURITY-CRITICAL ONE. A container restarts constantly —
// crashes, deploys, host reboots. Minting a second admin on any of those would be a privilege escalation
// with no actor behind it, and REPRINTING the first one's password would republish a live credential into
// logs that are shipped, aggregated and searched long after they were written.
//
// > ## ⛔ **A RESTART MUST NOT BE A SECURITY EVENT.**
//
// ⚠ THE CONDITION IS "HAS THIS DEPLOYMENT EVER HAD A USER", counting soft-deleted rows — self-closing in
// exactly the way `SetupComplete` is. Keyed on live users instead, deleting every account would reopen
// admin minting to whoever restarts the container next.
func EnsureAdmin(ctx context.Context, q Store, logger *slog.Logger) error {
	n, err := q.CountUsers(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		return nil // already set up — mint nothing, print nothing
	}

	pw, err := generatePassword()
	if err != nil {
		return err
	}
	hash, err := password.Hash(pw)
	if err != nil {
		return err
	}
	if _, err := q.CreateBootstrapAdmin(ctx, sqlc.CreateBootstrapAdminParams{
		Email: AdminEmail, Name: "Control Plane Admin", PasswordHash: &hash,
	}); err != nil {
		return err
	}

	// ⭐ PRINTED WHERE THE OPERATOR IS ALREADY LOOKING. `docker compose up` streams this; there is no file
	// to find, no env var to set, and no second place it could be. It is deliberately loud and framed,
	// because a credential that scrolls past inside a wall of JSON is a credential that is lost.
	//
	// ⛔ AND IT IS NEVER WRITTEN ANYWHERE ELSE. Not .env, not a file, not the database in plaintext — the
	// row stores an argon2id hash like every other account. This is the only moment the plaintext exists.
	logger.Warn("bootstrap_admin_created",
		slog.String("banner", strings.Repeat("=", 68)),
		slog.String("email", AdminEmail),
		slog.String("password", pw),
		slog.String("action", "SIGN IN NOW AND CHANGE THIS PASSWORD — you will be forced to"),
		slog.String("warning", "SHOWN ONCE. It is not stored in plaintext and cannot be reprinted. "+
			"If it is lost before you sign in, the only recovery is to reset the database "+
			"(docker compose down -v) — there is no signup and no second admin."),
	)
	return nil
}

// generatePassword returns a high-entropy one-time credential.
//
// ⚠ 24 RANDOM BYTES, base64url — ~192 bits. It is never typed from memory, only copied from a log line, so
// there is no reason to trade entropy for memorability.
func generatePassword() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
