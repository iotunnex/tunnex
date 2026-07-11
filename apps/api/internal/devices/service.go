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
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
	"github.com/tunnexio/tunnex/apps/api/internal/apierr"
	"github.com/tunnexio/tunnex/apps/api/internal/ipalloc"
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
	// afterResizeCheck is a test-only barrier fired inside ResizePool's window
	// (after the orphan check, before commit) so TestResizeAllocationRace can drive
	// a concurrent CreateDevice into that window. Always nil in production; a
	// per-Service field (not a package global) so parallel tests can't clobber it.
	afterResizeCheck func()
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
	// FullTunnel routes all client traffic (0.0.0.0/0); default is split-tunnel
	// (org network only) — the zero-trust posture.
	FullTunnel bool
}

// CreateResult is the created device plus, only for the server-generated flow,
// the one-time private key and the ready-to-use .conf (never stored, never
// returned again).
type CreateResult struct {
	Device            sqlc.Device
	PrivateKeyOneTime string // non-empty only when the server generated the key
	Config            string // full .conf, only for the server-generated flow
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
	var node sqlc.Node
	var assignedIP, poolCIDR string
	err := s.withTx(ctx, func(q *sqlc.Queries) error {
		// Take the user AND org advisory locks (in sorted order -> no deadlock) so
		// the per-user cap check and the org-wide IP allocation are both atomic
		// against concurrent creates.
		for _, key := range sortedKeys(in.OwnerID.String(), in.OrgID.String()) {
			if e := q.LockDeviceKey(ctx, key); e != nil {
				return e
			}
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
		n, e := q.GetOrgNode(ctx, sqlc.GetOrgNodeParams{ID: in.NodeID, OrgID: in.OrgID})
		if e != nil {
			if errors.Is(e, pgx.ErrNoRows) {
				return apierr.NotFound("node_not_found", "no such active node in this organization")
			}
			return e
		}
		node = n
		// A device is useless without a reachable gateway endpoint (the classic
		// self-hosted first-run failure is a config with an internal container IP)
		// or the node's WG public key (the peer would dial an empty server key).
		if node.Endpoint == "" || node.WgPublicKey == "" {
			return apierr.Conflict("node_not_ready", "the node has not reported its endpoint/key yet; ensure the agent is enrolled and TUNNEX_NODE_ENDPOINT is set")
		}
		// S3.7: a full-tunnel device routes ALL traffic to the gateway, so the gateway
		// MUST be able to source-NAT it to the internet. Refuse rather than mint a
		// config that silently blackholes everything. The agent probes + reports this
		// capability every reconcile; split-tunnel is always allowed.
		if in.FullTunnel {
			var caps struct {
				EgressNAT bool `json:"egress_nat"`
			}
			if len(node.Capabilities) > 0 {
				_ = json.Unmarshal(node.Capabilities, &caps)
			}
			if !caps.EgressNAT {
				return apierr.Conflict("gateway_no_egress", "this gateway can't route full-tunnel internet traffic yet; use split tunnel")
			}
		}
		// Per-user cap (0 = unlimited, per the org setting).
		org, e := q.GetOrganizationByID(ctx, in.OrgID)
		if e != nil {
			return e
		}
		poolCIDR = org.PoolCidr
		if org.MaxDevicesPerUser > 0 {
			count, ce := q.CountActiveDevicesForUser(ctx, sqlc.CountActiveDevicesForUserParams{OrgID: in.OrgID, UserID: in.OwnerID})
			if ce != nil {
				return ce
			}
			if count >= int64(org.MaxDevicesPerUser) {
				return apierr.Conflict("device_limit", "device limit reached for this user")
			}
		}
		// Allocate the lowest free tunnel address from the org's flat pool
		// (deterministic; safe under the org advisory lock + the org_ip unique index).
		// Uses the SAME query as resize's orphan check (ListActiveDeviceAllocations)
		// so there is one definition of "live allocation" — no two filtered reads to
		// drift apart.
		allocs, e := q.ListActiveDeviceAllocations(ctx, in.OrgID)
		if e != nil {
			return e
		}
		usedIPs := make([]string, 0, len(allocs))
		for _, r := range allocs {
			if r.AssignedIp != nil {
				usedIPs = append(usedIPs, *r.AssignedIp)
			}
		}
		ip, e := ipalloc.Allocate(org.PoolCidr, usedIPs)
		if e != nil {
			if errors.Is(e, ipalloc.ErrPoolExhausted) {
				return apierr.Conflict("pool_exhausted", "no free tunnel address in the org pool")
			}
			return e // bad/too-small CIDR is a server misconfiguration
		}
		assignedIP = ip

		created, e := q.CreateDevice(ctx, sqlc.CreateDeviceParams{
			OrgID: in.OrgID, UserID: in.OwnerID, NodeID: in.NodeID,
			Name: in.Name, Platform: in.Platform, PublicKey: pub,
			AssignedIp: &assignedIP,
		})
		if e != nil {
			if c := pgerr.UniqueConstraint(e); c != "" {
				if strings.Contains(c, "_ip_") { // devices_org_ip_key
					return apierr.Conflict("ip_conflict", "tunnel address already in use in this org")
				}
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

	res := CreateResult{Device: dev, PrivateKeyOneTime: oneTimePriv}
	// Only the server-generated flow can produce a complete config (it holds the
	// one-time private key); the client-generated flow assembles its own.
	if oneTimePriv != "" {
		res.Config = buildConfig(configParams{
			address:      assignedIP,
			privateKey:   oneTimePriv,
			serverPubKey: node.WgPublicKey,
			endpoint:     node.Endpoint,
			allowedIPs:   allowedIPsFor(in.FullTunnel, poolCIDR),
			dns:          dnsFor(in.FullTunnel),
		})
	}
	return res, nil
}

// sortedKeys returns a and b in ascending order, so multiple advisory locks are
// always acquired in the same order across callers (deadlock-free).
func sortedKeys(a, b string) [2]string {
	if a <= b {
		return [2]string{a, b}
	}
	return [2]string{b, a}
}

// OrphanDevice is a device a resize would strand: which device, its address, and
// why (out_of_range | reserved_collision — the latter is numerically inside the
// new range and looks fine, so the reason must reach the UI).
type OrphanDevice struct {
	DeviceID   uuid.UUID
	Name       string
	AssignedIP string
	Reason     string
}

// ShrinkOrphansError is returned when a resize would strand live allocations.
// Orphans is the FULL list, ordered by assigned_ip ascending; the HTTP layer caps
// the rendered slice and reports the true total, so the 409 body is bounded but
// the count is honest.
type ShrinkOrphansError struct{ Orphans []OrphanDevice }

func (e *ShrinkOrphansError) Error() string {
	return fmt.Sprintf("resize would strand %d live allocation(s)", len(e.Orphans))
}

// ResizePool changes the org's tunnel pool CIDR, atomically with an
// org.cidr_resized audit row, under the SAME per-org lock the allocator takes
// (LockDeviceKey) — so a concurrent device-create cannot slip a new allocation
// past the orphan check during the resize window. (Lock ordering: resize takes
// only the org key; allocation takes {owner,org} sorted; resize never waits on
// the owner key, so no inversion/deadlock.)
//
// Legal shapes are grow-superset or shrink-subset only; an identical CIDR is an
// idempotent no-op. The orphan check runs UNCONDITIONALLY (not shrink-only): on
// a valid grow it is provably empty for Allocate-produced IPs — every reserved
// address of the new range is outside the usable interval [O_net+2, O_bcast-1]
// that any allocation occupies — so it fires only if that INVARIANT is broken.
// PREMISE: this assumes every assigned_ip was produced by ipalloc.Allocate (∈ the
// usable interval); UNIQUE(org_id,assigned_ip) enforces uniqueness, NOT range
// membership. Any future path that writes assigned_ip directly (a Pritunl-style
// import that preserves IPs; EPIC 9 OpenVPN if it doesn't allocate through
// ipalloc) MUST use Allocate or re-validate this — otherwise a grow could strand
// such an address on a new reserved slot, and the check firing here is the
// backstop that turns silent corruption into a refused resize.
func (s *Service) ResizePool(ctx context.Context, actor, orgID uuid.UUID, newCIDR string) (sqlc.Organization, error) {
	newP, err := netip.ParsePrefix(newCIDR)
	if err != nil || !newP.Addr().Is4() {
		return sqlc.Organization{}, apierr.BadRequest("invalid_cidr", "pool_cidr must be a valid IPv4 CIDR")
	}
	newP = newP.Masked()
	if 32-newP.Bits() < 2 { // need network + gateway + >=1 host + broadcast
		return sqlc.Organization{}, apierr.BadRequest("cidr_too_small", "the pool is too small to hold the gateway and at least one device (need /30 or larger)")
	}
	// Persist the CANONICAL (masked) form, not the raw input — so a host-bits-dirty
	// but valid input (e.g. "10.0.1.5/23") is stored/audited/echoed as "10.0.0.0/23",
	// matching what the pool actually is.
	masked := newP.String()
	var result sqlc.Organization
	err = s.withTx(ctx, func(q *sqlc.Queries) error {
		if e := q.LockDeviceKey(ctx, orgID.String()); e != nil {
			return e
		}
		org, e := q.GetOrganizationByID(ctx, orgID)
		if e != nil {
			return e
		}
		oldP, e := netip.ParsePrefix(org.PoolCidr)
		if e != nil {
			return e // a stored CIDR should always be valid
		}
		oldP = oldP.Masked()
		if oldP == newP {
			result = org // idempotent: identical CIDR is a successful no-op (no update, no audit)
			return nil
		}
		grow := newP.Bits() <= oldP.Bits() && newP.Contains(oldP.Addr())
		shrink := oldP.Bits() <= newP.Bits() && oldP.Contains(newP.Addr())
		if !grow && !shrink {
			return apierr.BadRequest("illegal_resize", "the new range must contain, or be contained by, the current range")
		}
		// SINGLE read: the same device rows feed both the orphan check and the 409
		// objects, so the check and the build can't drift (no phantom orphan, no
		// count mismatch) under this org lock.
		rows, e := q.ListActiveDeviceAllocations(ctx, orgID)
		if e != nil {
			return e
		}
		ips := make([]string, 0, len(rows))
		byIP := make(map[string]sqlc.ListActiveDeviceAllocationsRow, len(rows))
		for _, r := range rows {
			if r.AssignedIp == nil { // query filters NOT NULL; defensive
				continue
			}
			ips = append(ips, *r.AssignedIp)
			byIP[*r.AssignedIp] = r
		}
		orphans, e := ipalloc.Orphans(masked, ips)
		if e != nil {
			return apierr.BadRequest("invalid_cidr", "pool_cidr must be a valid IPv4 CIDR")
		}
		// Test seam (per-Service, nil in prod): a barrier fired AFTER the orphan
		// check, BEFORE the commit — lets TestResizeAllocationRace drive a real
		// concurrent CreateDevice into this window to prove the LockDeviceKey above
		// actually excludes it.
		if s.afterResizeCheck != nil {
			s.afterResizeCheck()
		}
		// See this function's doc-comment PREMISE: on a valid grow this is provably
		// empty for Allocate-produced IPs; if it fires on a grow, that invariant was
		// violated (a direct assigned_ip writer). Do NOT drop this as "shrink-only"
		// without re-reading that proof.
		if len(orphans) > 0 {
			objs := make([]OrphanDevice, len(orphans))
			for i, o := range orphans {
				r := byIP[o.Addr] // present by construction: built from the same rows
				objs[i] = OrphanDevice{DeviceID: r.ID, Name: r.Name, AssignedIP: o.Addr, Reason: o.Reason}
			}
			return &ShrinkOrphansError{Orphans: objs}
		}
		updated, e := q.UpdateOrgPoolCidr(ctx, sqlc.UpdateOrgPoolCidrParams{ID: orgID, PoolCidr: masked})
		if e != nil {
			return e
		}
		result = updated // return the row we committed, in-tx — no post-commit re-fetch to race a concurrent delete
		return audit(ctx, q, orgID, &actor, "org.cidr_resized", "organization", orgID.String(),
			map[string]any{"from": org.PoolCidr, "to": masked})
	})
	if err != nil {
		return sqlc.Organization{}, err
	}
	return result, nil
}

// DeviceWithStatus is a device plus its live telemetry (nil when never reported).
type DeviceWithStatus struct {
	Device          sqlc.Device
	LastHandshakeAt *time.Time
	RxBytes         *int64
	TxBytes         *int64
}

// ListForUser returns a user's devices in an org (self-service view) with status.
func (s *Service) ListForUser(ctx context.Context, orgID, userID uuid.UUID) ([]DeviceWithStatus, error) {
	rows, err := s.q.ListDevicesByUser(ctx, sqlc.ListDevicesByUserParams{OrgID: orgID, UserID: userID})
	if err != nil {
		return nil, err
	}
	out := make([]DeviceWithStatus, 0, len(rows))
	for _, r := range rows {
		out = append(out, DeviceWithStatus{Device: r.Device, LastHandshakeAt: tsPtr(r.LastHandshakeAt), RxBytes: r.RxBytes, TxBytes: r.TxBytes})
	}
	return out, nil
}

// ListForOrg returns all devices in an org (admin view) with status.
func (s *Service) ListForOrg(ctx context.Context, orgID uuid.UUID) ([]DeviceWithStatus, error) {
	rows, err := s.q.ListDevicesByOrg(ctx, orgID)
	if err != nil {
		return nil, err
	}
	out := make([]DeviceWithStatus, 0, len(rows))
	for _, r := range rows {
		out = append(out, DeviceWithStatus{Device: r.Device, LastHandshakeAt: tsPtr(r.LastHandshakeAt), RxBytes: r.RxBytes, TxBytes: r.TxBytes})
	}
	return out, nil
}

// tsPtr converts a nullable timestamptz to *time.Time.
func tsPtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	u := t.Time
	return &u
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
		// Release the device's live status so a revoked device can't report stale
		// online/handshake via the API.
		if e := q.DeleteDeviceStatus(ctx, deviceID); e != nil {
			return e
		}
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
