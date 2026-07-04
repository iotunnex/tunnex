// Package devices is the control-plane side of user-owned WireGuard peers.
//
// Identity<->credential binding is enforced here and structurally in the schema:
// every device has a NOT NULL owning user who must be a member of the org, and a
// device is created only for an explicit owner (the session user, or — for an
// admin — a named target member). The control plane stores ONLY the peer public
// key: client-generated keys never leave the device; a server-generated key
// (browser flow) is returned once and never persisted.
package devices

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
	"github.com/tunnexio/tunnex/apps/api/internal/apierr"
	"github.com/tunnexio/tunnex/apps/api/internal/nodepush"
	"github.com/tunnexio/tunnex/apps/api/internal/pgerr"
	"github.com/tunnexio/tunnex/apps/api/internal/wgkey"
)

// Service provides device/peer operations.
type Service struct {
	pool   *pgxpool.Pool
	q      *sqlc.Queries
	hub    *nodepush.Hub
	logger *slog.Logger
}

// NewService builds the device service. hub may be nil (no push; interval
// reconcile still converges).
func NewService(pool *pgxpool.Pool, hub *nodepush.Hub, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{pool: pool, q: sqlc.New(pool), hub: hub, logger: logger}
}

func (s *Service) withTx(ctx context.Context, fn func(*sqlc.Queries) error) error {
	if s.pool == nil {
		return fn(s.q)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err := fn(sqlc.New(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// CreateInput describes a new device.
type CreateInput struct {
	OrgID    uuid.UUID
	ActorID  uuid.UUID // the authenticated caller (for the audit trail)
	OwnerID  uuid.UUID // the device's owning user (never inferred from the body)
	NodeID   uuid.UUID // the gateway the peer connects through
	Name     string
	Platform string
	// PublicKey, if set, is a client-generated peer key (preferred). If empty, the
	// server generates a keypair and returns the private key ONCE (browser flow).
	PublicKey string
}

// CreateResult is the created device plus, only for the server-generated flow,
// the one-time private key (never stored, never returned again).
type CreateResult struct {
	Device            sqlc.Device
	PrivateKeyOneTime string // non-empty only when the server generated the key
}

// Create issues a device/peer for OwnerID, enforcing owner membership and the
// per-user cap, then pushes the gateway so the peer applies within seconds. The
// membership check + cap check + insert + audit run in ONE transaction under a
// per-user advisory lock, so the cap cannot be raced past.
func (s *Service) Create(ctx context.Context, in CreateInput) (CreateResult, error) {
	if in.Name == "" {
		return CreateResult{}, apierr.BadRequest("name_required", "a device name is required")
	}
	// Key custody: use the client's key, or generate one server-side (returned once).
	pub, oneTimePriv := in.PublicKey, ""
	if pub == "" {
		priv, generated, gerr := wgkey.Generate()
		if gerr != nil {
			return CreateResult{}, gerr
		}
		pub, oneTimePriv = generated, priv
	} else if !wgkey.Valid(pub) {
		return CreateResult{}, apierr.BadRequest("invalid_wg_key", "public_key must be a 32-byte base64 WireGuard key")
	}

	var dev sqlc.Device
	err := s.withTx(ctx, func(q *sqlc.Queries) error {
		// Serialize this user's creates so the cap check-and-insert is atomic.
		if e := q.LockUserDeviceCreation(ctx, in.OwnerID.String()); e != nil {
			return e
		}
		// The owner must be a member of THIS org (identity binding — no cross-tenant
		// or non-member owners, even when an admin creates on someone's behalf).
		if _, e := q.GetMembership(ctx, sqlc.GetMembershipParams{OrgID: in.OrgID, UserID: in.OwnerID}); e != nil {
			if errors.Is(e, pgx.ErrNoRows) {
				return apierr.NotFound("owner_not_member", "the owner is not a member of this organization")
			}
			return e
		}
		// The node must belong to this org (and be active) — no cross-org attach.
		if _, e := q.GetOrgNode(ctx, sqlc.GetOrgNodeParams{ID: in.NodeID, OrgID: in.OrgID}); e != nil {
			if errors.Is(e, pgx.ErrNoRows) {
				return apierr.NotFound("node_not_found", "no such active node in this organization")
			}
			return e
		}
		// Per-user cap (0 = unlimited, per the org setting).
		org, e := q.GetOrganizationByID(ctx, in.OrgID)
		if e != nil {
			return e
		}
		if org.MaxDevicesPerUser > 0 {
			count, ce := q.CountActiveDevicesForUser(ctx, sqlc.CountActiveDevicesForUserParams{OrgID: in.OrgID, UserID: in.OwnerID})
			if ce != nil {
				return ce
			}
			if count >= int64(org.MaxDevicesPerUser) {
				return apierr.Conflict("device_limit", "device limit reached for this user")
			}
		}

		created, e := q.CreateDevice(ctx, sqlc.CreateDeviceParams{
			OrgID: in.OrgID, UserID: in.OwnerID, NodeID: in.NodeID,
			Name: in.Name, Platform: in.Platform, PublicKey: pub,
			AssignedIp: nil, // TODO(S3.5): allocate from the org pool
		})
		if e != nil {
			if pgerr.IsUnique(e) {
				return apierr.Conflict("duplicate_key", "this public key is already registered on the node")
			}
			return e
		}
		dev = created
		keySource := "client"
		if oneTimePriv != "" {
			keySource = "server"
		}
		return audit(ctx, q, in.OrgID, &in.ActorID, "device.created", "device", dev.ID.String(),
			map[string]any{"name": in.Name, "owner": in.OwnerID.String(), "node_id": in.NodeID.String(), "key_source": keySource})
	})
	if err != nil {
		return CreateResult{}, err
	}
	s.push(dev.NodeID)
	return CreateResult{Device: dev, PrivateKeyOneTime: oneTimePriv}, nil
}

// ListForUser returns a user's devices in an org (self-service view).
func (s *Service) ListForUser(ctx context.Context, orgID, userID uuid.UUID) ([]sqlc.Device, error) {
	return s.q.ListDevicesByUser(ctx, sqlc.ListDevicesByUserParams{OrgID: orgID, UserID: userID})
}

// ListForOrg returns all devices in an org (admin view).
func (s *Service) ListForOrg(ctx context.Context, orgID uuid.UUID) ([]sqlc.Device, error) {
	return s.q.ListDevicesByOrg(ctx, orgID)
}

// Get returns a device scoped to its org (not-found otherwise — no cross-tenant leak).
func (s *Service) Get(ctx context.Context, orgID, deviceID uuid.UUID) (sqlc.Device, error) {
	dev, err := s.q.GetDevice(ctx, sqlc.GetDeviceParams{ID: deviceID, OrgID: orgID})
	if err != nil {
		return sqlc.Device{}, apierr.NotFound("device_not_found", "device not found")
	}
	return dev, nil
}

// Revoke marks a device revoked and pushes its gateway so the peer is removed
// from the device within the <5s bound. A no-op (already revoked) is a conflict.
func (s *Service) Revoke(ctx context.Context, orgID, actorID, deviceID uuid.UUID) error {
	var nodeID uuid.UUID
	err := s.withTx(ctx, func(q *sqlc.Queries) error {
		n, e := q.RevokeDevice(ctx, sqlc.RevokeDeviceParams{ID: deviceID, OrgID: orgID})
		if errors.Is(e, pgx.ErrNoRows) {
			return apierr.Conflict("already_revoked", "device is not active")
		}
		if e != nil {
			return e
		}
		nodeID = n
		return audit(ctx, q, orgID, &actorID, "device.revoked", "device", deviceID.String(), map[string]any{})
	})
	if err != nil {
		return err
	}
	s.push(nodeID)
	return nil
}

// PushUserNodes nudges every node carrying one of a user's active devices to
// reconcile now. Used by the offboarding cascade: after a user is deactivated
// (or reactivated) their peers drop from / return to desired state.
func (s *Service) PushUserNodes(ctx context.Context, userID uuid.UUID) {
	if s.hub == nil {
		return
	}
	ids, err := s.q.ListNodeIDsForUserActiveDevices(ctx, userID)
	if err != nil {
		// The interval reconcile still converges; surface the missed fast path.
		s.logger.Warn("device_push_lookup_failed", slog.String("user_id", userID.String()), slog.String("error", err.Error()))
		return
	}
	s.hub.NotifyMany(ids)
}

func (s *Service) push(nodeID uuid.UUID) {
	if s.hub != nil {
		s.hub.Notify(nodeID)
	}
}

// audit writes an audit_logs row (same shape as the other services), in the
// caller's transaction so a mutation and its record commit atomically.
func audit(ctx context.Context, q *sqlc.Queries, orgID uuid.UUID, actor *uuid.UUID, action, targetType, targetID string, meta map[string]any) error {
	b := []byte("{}")
	if meta != nil {
		b, _ = json.Marshal(meta)
	}
	actorID := pgtype.UUID{}
	if actor != nil {
		actorID = pgtype.UUID{Bytes: [16]byte(*actor), Valid: true}
	}
	_, err := q.InsertAuditLog(ctx, sqlc.InsertAuditLogParams{
		OrgID: pgtype.UUID{Bytes: [16]byte(orgID), Valid: true}, ActorUserID: actorID,
		Action: action, TargetType: &targetType, TargetID: &targetID, Metadata: b,
	})
	return err
}
