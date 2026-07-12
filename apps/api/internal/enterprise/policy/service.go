package policy

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
	"github.com/tunnexio/tunnex/apps/api/internal/apierr"
	"github.com/tunnexio/tunnex/apps/api/internal/authctx"
)

// Service is the enterprise Zero Trust policy CRUD + snapshot service. Every
// mutation writes an actor-attributed audit row in the same transaction, and
// validates inputs before touching the DB. It is only constructed in the
// enterprise build (policy_wire_enterprise.go); the open build's port is nil.
type Service struct {
	pool *pgxpool.Pool
	q    *sqlc.Queries
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool, q: sqlc.New(pool)}
}

// ── groups ──────────────────────────────────────────────────────────────────────

func (s *Service) ListGroups(ctx context.Context, orgID uuid.UUID) ([]sqlc.UserGroup, error) {
	return s.q.ListUserGroupsByOrg(ctx, orgID)
}

func (s *Service) CreateGroup(ctx context.Context, orgID uuid.UUID, name, description string) (sqlc.UserGroup, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return sqlc.UserGroup{}, apierr.BadRequest("invalid_request", "group name is required")
	}
	var g sqlc.UserGroup
	err := s.withTx(ctx, func(q *sqlc.Queries) error {
		var e error
		g, e = q.CreateUserGroup(ctx, sqlc.CreateUserGroupParams{OrgID: orgID, Name: name, Description: description})
		if e != nil {
			return conflictIfDup(e, "a group with that name already exists")
		}
		return writeAudit(ctx, q, orgID, "group.created", "group", g.ID.String(), map[string]any{"name": name})
	})
	return g, err
}

func (s *Service) UpdateGroup(ctx context.Context, orgID, groupID uuid.UUID, name, description string) (sqlc.UserGroup, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return sqlc.UserGroup{}, apierr.BadRequest("invalid_request", "group name is required")
	}
	var g sqlc.UserGroup
	err := s.withTx(ctx, func(q *sqlc.Queries) error {
		var e error
		g, e = q.UpdateUserGroup(ctx, sqlc.UpdateUserGroupParams{ID: groupID, OrgID: orgID, Name: name, Description: description})
		if errors.Is(e, pgx.ErrNoRows) {
			return apierr.NotFound("group_not_found", "group not found")
		}
		if e != nil {
			return conflictIfDup(e, "a group with that name already exists")
		}
		return writeAudit(ctx, q, orgID, "group.updated", "group", groupID.String(), map[string]any{"name": name})
	})
	return g, err
}

func (s *Service) DeleteGroup(ctx context.Context, orgID, groupID uuid.UUID) error {
	return s.withTx(ctx, func(q *sqlc.Queries) error {
		n, e := q.DeleteUserGroup(ctx, sqlc.DeleteUserGroupParams{ID: groupID, OrgID: orgID})
		if e != nil {
			return e
		}
		if n == 0 {
			return apierr.NotFound("group_not_found", "group not found")
		}
		return writeAudit(ctx, q, orgID, "group.deleted", "group", groupID.String(), nil)
	})
}

// ── group members ───────────────────────────────────────────────────────────────

func (s *Service) ListGroupMembers(ctx context.Context, orgID, groupID uuid.UUID) ([]sqlc.ListGroupMembersRow, error) {
	if _, err := s.q.GetUserGroup(ctx, sqlc.GetUserGroupParams{ID: groupID, OrgID: orgID}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apierr.NotFound("group_not_found", "group not found")
		}
		return nil, err
	}
	return s.q.ListGroupMembers(ctx, sqlc.ListGroupMembersParams{OrgID: orgID, GroupID: groupID})
}

func (s *Service) AddGroupMember(ctx context.Context, orgID, groupID, userID uuid.UUID) error {
	return s.withTx(ctx, func(q *sqlc.Queries) error {
		if _, e := q.GetUserGroup(ctx, sqlc.GetUserGroupParams{ID: groupID, OrgID: orgID}); e != nil {
			if errors.Is(e, pgx.ErrNoRows) {
				return apierr.NotFound("group_not_found", "group not found")
			}
			return e
		}
		// The user must be a member of THIS org — no adding a foreign/unknown user
		// to a group (which would then inherit grants). GetMembership is org-scoped.
		if _, e := q.GetMembership(ctx, sqlc.GetMembershipParams{OrgID: orgID, UserID: userID}); e != nil {
			if errors.Is(e, pgx.ErrNoRows) {
				return apierr.BadRequest("not_a_member", "user is not a member of this organization")
			}
			return e
		}
		if e := q.AddGroupMember(ctx, sqlc.AddGroupMemberParams{OrgID: orgID, GroupID: groupID, UserID: userID}); e != nil {
			return e
		}
		return writeAudit(ctx, q, orgID, "group.member_added", "group", groupID.String(), map[string]any{"user_id": userID.String()})
	})
}

func (s *Service) RemoveGroupMember(ctx context.Context, orgID, groupID, userID uuid.UUID) error {
	return s.withTx(ctx, func(q *sqlc.Queries) error {
		n, e := q.RemoveGroupMember(ctx, sqlc.RemoveGroupMemberParams{OrgID: orgID, GroupID: groupID, UserID: userID})
		if e != nil {
			return e
		}
		if n == 0 {
			return apierr.NotFound("member_not_found", "user is not a member of this group")
		}
		return writeAudit(ctx, q, orgID, "group.member_removed", "group", groupID.String(), map[string]any{"user_id": userID.String()})
	})
}

// ── resources ───────────────────────────────────────────────────────────────────

// ResourceInput is a validated resource create/update payload.
type ResourceInput struct {
	Name     string
	CIDR     string
	Protocol string
	PortLow  *int
	PortHigh *int
}

func (in ResourceInput) validate() error {
	if strings.TrimSpace(in.Name) == "" {
		return apierr.BadRequest("invalid_request", "resource name is required")
	}
	if _, err := netip.ParsePrefix(in.CIDR); err != nil {
		return apierr.BadRequest("invalid_cidr", "cidr must be a valid IP prefix (e.g. 10.0.5.0/24)")
	}
	switch in.Protocol {
	case "any":
		if in.PortLow != nil || in.PortHigh != nil {
			return apierr.BadRequest("invalid_request", "protocol 'any' cannot carry ports")
		}
	case "tcp", "udp":
	default:
		return apierr.BadRequest("invalid_request", "protocol must be any, tcp, or udp")
	}
	for _, p := range []*int{in.PortLow, in.PortHigh} {
		if p != nil && (*p < 1 || *p > 65535) {
			return apierr.BadRequest("invalid_request", "ports must be in 1..65535")
		}
	}
	if in.PortLow != nil && in.PortHigh != nil && *in.PortLow > *in.PortHigh {
		return apierr.BadRequest("invalid_request", "port_low must be <= port_high")
	}
	return nil
}

func (s *Service) ListResources(ctx context.Context, orgID uuid.UUID) ([]sqlc.Resource, error) {
	return s.q.ListResourcesByOrg(ctx, orgID)
}

func (s *Service) CreateResource(ctx context.Context, orgID uuid.UUID, in ResourceInput) (sqlc.Resource, error) {
	if err := in.validate(); err != nil {
		return sqlc.Resource{}, err
	}
	var r sqlc.Resource
	err := s.withTx(ctx, func(q *sqlc.Queries) error {
		var e error
		r, e = q.CreateResource(ctx, sqlc.CreateResourceParams{
			OrgID: orgID, Name: strings.TrimSpace(in.Name), Cidr: in.CIDR,
			Protocol: in.Protocol, PortLow: i32ptr(in.PortLow), PortHigh: i32ptr(in.PortHigh),
		})
		if e != nil {
			return conflictIfDup(e, "a resource with that name already exists")
		}
		return writeAudit(ctx, q, orgID, "resource.created", "resource", r.ID.String(),
			map[string]any{"name": r.Name, "cidr": r.Cidr, "protocol": r.Protocol})
	})
	return r, err
}

func (s *Service) UpdateResource(ctx context.Context, orgID, resourceID uuid.UUID, in ResourceInput) (sqlc.Resource, error) {
	if err := in.validate(); err != nil {
		return sqlc.Resource{}, err
	}
	var r sqlc.Resource
	err := s.withTx(ctx, func(q *sqlc.Queries) error {
		var e error
		r, e = q.UpdateResource(ctx, sqlc.UpdateResourceParams{
			ID: resourceID, OrgID: orgID, Name: strings.TrimSpace(in.Name), Cidr: in.CIDR,
			Protocol: in.Protocol, PortLow: i32ptr(in.PortLow), PortHigh: i32ptr(in.PortHigh),
		})
		if errors.Is(e, pgx.ErrNoRows) {
			return apierr.NotFound("resource_not_found", "resource not found")
		}
		if e != nil {
			return conflictIfDup(e, "a resource with that name already exists")
		}
		return writeAudit(ctx, q, orgID, "resource.updated", "resource", resourceID.String(),
			map[string]any{"name": r.Name, "cidr": r.Cidr, "protocol": r.Protocol})
	})
	return r, err
}

func (s *Service) DeleteResource(ctx context.Context, orgID, resourceID uuid.UUID) error {
	return s.withTx(ctx, func(q *sqlc.Queries) error {
		n, e := q.DeleteResource(ctx, sqlc.DeleteResourceParams{ID: resourceID, OrgID: orgID})
		if e != nil {
			return e
		}
		if n == 0 {
			return apierr.NotFound("resource_not_found", "resource not found")
		}
		return writeAudit(ctx, q, orgID, "resource.deleted", "resource", resourceID.String(), nil)
	})
}

// ── rules ───────────────────────────────────────────────────────────────────────

// RuleInput is a validated allow-rule payload.
type RuleInput struct {
	SrcGroupID    uuid.UUID
	DstKind       string
	DstResourceID *uuid.UUID
	DstGroupID    *uuid.UUID
}

func (s *Service) ListPolicyRules(ctx context.Context, orgID uuid.UUID) ([]sqlc.PolicyRule, error) {
	return s.q.ListPolicyRulesByOrg(ctx, orgID)
}

func (s *Service) CreatePolicyRule(ctx context.Context, orgID uuid.UUID, in RuleInput) (sqlc.PolicyRule, error) {
	// Shape validation: exactly one dst_* set, matching dst_kind (the DB CHECKs
	// this too; we return a clean 400 rather than a raw constraint error).
	switch in.DstKind {
	case "resource":
		if in.DstResourceID == nil || in.DstGroupID != nil {
			return sqlc.PolicyRule{}, apierr.BadRequest("invalid_request", "dst_kind=resource requires dst_resource_id (and no dst_group_id)")
		}
	case "group":
		if in.DstGroupID == nil || in.DstResourceID != nil {
			return sqlc.PolicyRule{}, apierr.BadRequest("invalid_request", "dst_kind=group requires dst_group_id (and no dst_resource_id)")
		}
	default:
		return sqlc.PolicyRule{}, apierr.BadRequest("invalid_request", "dst_kind must be resource or group")
	}
	var r sqlc.PolicyRule
	err := s.withTx(ctx, func(q *sqlc.Queries) error {
		// Referenced src group + dst must belong to THIS org (no cross-tenant refs).
		if _, e := q.GetUserGroup(ctx, sqlc.GetUserGroupParams{ID: in.SrcGroupID, OrgID: orgID}); e != nil {
			if errors.Is(e, pgx.ErrNoRows) {
				return apierr.BadRequest("group_not_found", "src group not found")
			}
			return e
		}
		if in.DstResourceID != nil {
			if _, e := q.GetResource(ctx, sqlc.GetResourceParams{ID: *in.DstResourceID, OrgID: orgID}); e != nil {
				if errors.Is(e, pgx.ErrNoRows) {
					return apierr.BadRequest("resource_not_found", "dst resource not found")
				}
				return e
			}
		}
		if in.DstGroupID != nil {
			if _, e := q.GetUserGroup(ctx, sqlc.GetUserGroupParams{ID: *in.DstGroupID, OrgID: orgID}); e != nil {
				if errors.Is(e, pgx.ErrNoRows) {
					return apierr.BadRequest("group_not_found", "dst group not found")
				}
				return e
			}
		}
		var e error
		r, e = q.CreatePolicyRule(ctx, sqlc.CreatePolicyRuleParams{
			OrgID: orgID, SrcGroupID: in.SrcGroupID, DstKind: in.DstKind,
			DstResourceID: toPgUUID(in.DstResourceID), DstGroupID: toPgUUID(in.DstGroupID),
		})
		if e != nil {
			return conflictIfDup(e, "an identical rule already exists")
		}
		return writeAudit(ctx, q, orgID, "policy.rule_created", "policy_rule", r.ID.String(),
			map[string]any{"src_group_id": in.SrcGroupID.String(), "dst_kind": in.DstKind})
	})
	return r, err
}

func (s *Service) DeletePolicyRule(ctx context.Context, orgID, ruleID uuid.UUID) error {
	return s.withTx(ctx, func(q *sqlc.Queries) error {
		n, e := q.DeletePolicyRule(ctx, sqlc.DeletePolicyRuleParams{ID: ruleID, OrgID: orgID})
		if e != nil {
			return e
		}
		if n == 0 {
			return apierr.NotFound("rule_not_found", "rule not found")
		}
		return writeAudit(ctx, q, orgID, "policy.rule_deleted", "policy_rule", ruleID.String(), nil)
	})
}

// ── enforcement mode ──────────────────────────────────────────────────────────

func (s *Service) GetMode(ctx context.Context, orgID uuid.UUID) (string, error) {
	org, err := s.q.GetOrganizationByID(ctx, orgID)
	if err != nil {
		return "", err
	}
	return org.ZeroTrustMode, nil
}

// SetMode flips the org enforcement mode. Both directions are audited; disabling
// (enforcing -> off) re-opens the mesh and is the security-sensitive direction.
// Enabling with zero grants is ALLOWED (a locked-down posture is legitimate; the
// UI warns — the server obeys, per the S4.7 server-is-truth precedent).
func (s *Service) SetMode(ctx context.Context, orgID uuid.UUID, mode string) (string, error) {
	if mode != ModeOff && mode != ModeEnforcing {
		return "", apierr.BadRequest("invalid_request", "mode must be off or enforcing")
	}
	err := s.withTx(ctx, func(q *sqlc.Queries) error {
		org, e := q.SetOrgZeroTrustMode(ctx, sqlc.SetOrgZeroTrustModeParams{ID: orgID, ZeroTrustMode: mode})
		if errors.Is(e, pgx.ErrNoRows) {
			return apierr.NotFound("org_not_found", "organization not found")
		}
		if e != nil {
			return e
		}
		action := "org.zero_trust_disabled"
		if org.ZeroTrustMode == ModeEnforcing {
			action = "org.zero_trust_enabled"
		}
		return writeAudit(ctx, q, orgID, action, "organization", orgID.String(), map[string]any{"mode": mode})
	})
	return mode, err
}

// ── snapshot + invalidation (consumed by S7.2) ──────────────────────────────────

// BuildSnapshot loads the org's full policy state into the pure-compiler input.
// S7.2 calls Compile(BuildSnapshot(...)) when serving a node's desired state.
func (s *Service) BuildSnapshot(ctx context.Context, orgID uuid.UUID) (Snapshot, error) {
	org, err := s.q.GetOrganizationByID(ctx, orgID)
	if err != nil {
		return Snapshot{}, err
	}
	rules, err := s.q.ListPolicyRulesByOrg(ctx, orgID)
	if err != nil {
		return Snapshot{}, err
	}
	resources, err := s.q.ListResourcesByOrg(ctx, orgID)
	if err != nil {
		return Snapshot{}, err
	}
	members, err := s.q.ListGroupMembershipsByOrg(ctx, orgID)
	if err != nil {
		return Snapshot{}, err
	}
	devices, err := s.q.ListActiveDevicesForOrg(ctx, orgID)
	if err != nil {
		return Snapshot{}, err
	}
	snap := Snapshot{Mode: org.ZeroTrustMode}
	for _, r := range rules {
		snap.Rules = append(snap.Rules, Rule{
			SrcGroupID: r.SrcGroupID, DstKind: r.DstKind,
			DstResourceID: fromPgUUID(r.DstResourceID), DstGroupID: fromPgUUID(r.DstGroupID),
		})
	}
	for _, r := range resources {
		snap.Resources = append(snap.Resources, Resource{
			ID: r.ID, CIDR: r.Cidr, Protocol: r.Protocol,
			PortLow: derefI32(r.PortLow), PortHigh: derefI32(r.PortHigh),
		})
	}
	for _, m := range members {
		snap.Memberships = append(snap.Memberships, Membership{GroupID: m.GroupID, UserID: m.UserID})
	}
	for _, d := range devices {
		ip := ""
		if d.AssignedIp != nil {
			ip = *d.AssignedIp
		}
		snap.Devices = append(snap.Devices, Device{UserID: d.UserID, NodeID: d.NodeID, AssignedIP: ip})
	}
	return snap, nil
}

// AffectedNodeIDs returns the nodes whose compiled policy could change for this
// org — the nodes that currently host active devices. A policy mutation is
// org-wide, so S7.2 recompiles + pushes to exactly these nodes (the invalidation
// target). Model-layer logic, tested here; the push itself is S7.2.
func (s *Service) AffectedNodeIDs(ctx context.Context, orgID uuid.UUID) ([]uuid.UUID, error) {
	devices, err := s.q.ListActiveDevicesForOrg(ctx, orgID)
	if err != nil {
		return nil, err
	}
	seen := map[uuid.UUID]bool{}
	var out []uuid.UUID
	for _, d := range devices {
		if !seen[d.NodeID] {
			seen[d.NodeID] = true
			out = append(out, d.NodeID)
		}
	}
	return out, nil
}

// ── helpers ─────────────────────────────────────────────────────────────────────

func (s *Service) withTx(ctx context.Context, fn func(*sqlc.Queries) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after Commit
	if err := fn(sqlc.New(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// writeAudit records an actor-attributed, secret-free audit row in the same tx.
func writeAudit(ctx context.Context, q *sqlc.Queries, orgID uuid.UUID, action, targetType, targetID string, meta map[string]any) error {
	var raw []byte
	if meta != nil {
		b, err := json.Marshal(meta)
		if err != nil {
			return err
		}
		raw = b
	}
	tt := targetType
	tid := targetID
	_, err := q.InsertAuditLog(ctx, sqlc.InsertAuditLogParams{
		OrgID:       pgtype.UUID{Bytes: orgID, Valid: true},
		ActorUserID: actorPg(ctx),
		Action:      action,
		TargetType:  &tt,
		TargetID:    &tid,
		Metadata:    raw,
	})
	return err
}

func actorPg(ctx context.Context) pgtype.UUID {
	if p, ok := authctx.PrincipalFrom(ctx); ok {
		return pgtype.UUID{Bytes: p.UserID, Valid: true}
	}
	return pgtype.UUID{Valid: false}
}

// conflictIfDup maps a unique-violation (23505) to a clean 409; other errors pass through.
func conflictIfDup(err error, msg string) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return apierr.New(http.StatusConflict, "conflict", msg)
	}
	return err
}

func toPgUUID(id *uuid.UUID) pgtype.UUID {
	if id == nil {
		return pgtype.UUID{Valid: false}
	}
	return pgtype.UUID{Bytes: *id, Valid: true}
}

func fromPgUUID(v pgtype.UUID) uuid.UUID {
	if !v.Valid {
		return uuid.Nil
	}
	return uuid.UUID(v.Bytes)
}

func i32ptr(p *int) *int32 {
	if p == nil {
		return nil
	}
	v := int32(*p)
	return &v
}

func derefI32(p *int32) int {
	if p == nil {
		return 0
	}
	return int(*p)
}
