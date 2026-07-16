//go:build enterprise

package policy

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
	"github.com/tunnexio/tunnex/apps/api/internal/apierr"
	"github.com/tunnexio/tunnex/apps/api/internal/authctx"
	"github.com/tunnexio/tunnex/apps/api/internal/policyspec"
)

// Service is the enterprise Zero Trust policy CRUD + snapshot service. Every
// mutation writes an actor-attributed audit row in the same transaction, and
// validates inputs before touching the DB. It is only constructed in the
// enterprise build (policy_wire_enterprise.go); the open build's port is nil.
type Service struct {
	pool   *pgxpool.Pool
	q      *sqlc.Queries
	notify Notifier // nil => no push (provider-only service / tests)
}

// SetNotifier wires the push hub (S7.2). Call on the CRUD service; the desired-
// state provider service does not mutate and needs none.
func (s *Service) SetNotifier(n Notifier) { s.notify = n }

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
	err := s.mutate(ctx, orgID, func(q *sqlc.Queries) error {
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
	err := s.mutate(ctx, orgID, func(q *sqlc.Queries) error {
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
	return s.mutate(ctx, orgID, func(q *sqlc.Queries) error {
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
	return s.mutate(ctx, orgID, func(q *sqlc.Queries) error {
		g, e := q.GetUserGroup(ctx, sqlc.GetUserGroupParams{ID: groupID, OrgID: orgID})
		if e != nil {
			if errors.Is(e, pgx.ErrNoRows) {
				return apierr.NotFound("group_not_found", "group not found")
			}
			return e
		}
		// D1 (S7.5.2): an idp_sync group's membership is owned by the reconciler — hand-editing
		// it would be silently overwritten on the next poll, and worse, would blur the disjoint
		// manual/idp origins the schema guards. Refuse loudly instead.
		if g.Origin == "idp_sync" {
			return apierr.Conflict("idp_managed_group", "this group is managed by directory sync; members cannot be edited manually")
		}
		// The user must be a member of THIS org — no adding a foreign/unknown user
		// to a group (which would then inherit grants). GetMembership is org-scoped.
		if _, e := q.GetMembership(ctx, sqlc.GetMembershipParams{OrgID: orgID, UserID: userID}); e != nil {
			if errors.Is(e, pgx.ErrNoRows) {
				return apierr.BadRequest("not_a_member", "user is not a member of this organization")
			}
			return e
		}
		n, e := q.AddGroupMember(ctx, sqlc.AddGroupMemberParams{OrgID: orgID, GroupID: groupID, UserID: userID})
		if e != nil {
			return e
		}
		if n == 0 {
			return nil // already a member — no state change, so no audit event (idempotent)
		}
		return writeAudit(ctx, q, orgID, "group.member_added", "group", groupID.String(), map[string]any{"user_id": userID.String()})
	})
}

func (s *Service) RemoveGroupMember(ctx context.Context, orgID, groupID, userID uuid.UUID) error {
	return s.mutate(ctx, orgID, func(q *sqlc.Queries) error {
		g, e := q.GetUserGroup(ctx, sqlc.GetUserGroupParams{ID: groupID, OrgID: orgID})
		if e != nil {
			if errors.Is(e, pgx.ErrNoRows) {
				return apierr.NotFound("group_not_found", "group not found")
			}
			return e
		}
		if g.Origin == "idp_sync" { // D1: reconciler-owned; see AddGroupMember
			return apierr.Conflict("idp_managed_group", "this group is managed by directory sync; members cannot be edited manually")
		}
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

// validateResource validates a resource payload (the DTO lives in policyspec so
// the open build's http port can reference it without importing this package).
func validateResource(in policyspec.ResourceInput) error {
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
		// Ports are both-or-neither (finding #3). A half-set range (only low OR only
		// high) is rejected here so it can never reach the gateway, where renderAllow
		// fails it closed (skips the rule) — which would SILENTLY break a grant the API
		// reported as created. The renderer's fail-closed and this validation are the
		// two halves of the same invariant; neither alone is sufficient.
		if (in.PortLow == nil) != (in.PortHigh == nil) {
			return apierr.BadRequest("invalid_request", "port_low and port_high must be set together (both or neither)")
		}
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

func (s *Service) CreateResource(ctx context.Context, orgID uuid.UUID, in policyspec.ResourceInput) (sqlc.Resource, error) {
	if err := validateResource(in); err != nil {
		return sqlc.Resource{}, err
	}
	in.CIDR = canonicalCIDR(in.CIDR) // store the masked prefix, never host-bits-set
	var r sqlc.Resource
	err := s.mutate(ctx, orgID, func(q *sqlc.Queries) error {
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

func (s *Service) UpdateResource(ctx context.Context, orgID, resourceID uuid.UUID, in policyspec.ResourceInput) (sqlc.Resource, error) {
	if err := validateResource(in); err != nil {
		return sqlc.Resource{}, err
	}
	in.CIDR = canonicalCIDR(in.CIDR)
	var r sqlc.Resource
	err := s.mutate(ctx, orgID, func(q *sqlc.Queries) error {
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
	return s.mutate(ctx, orgID, func(q *sqlc.Queries) error {
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

func (s *Service) ListPolicyRules(ctx context.Context, orgID uuid.UUID) ([]sqlc.PolicyRule, error) {
	return s.q.ListPolicyRulesByOrg(ctx, orgID)
}

func (s *Service) CreatePolicyRule(ctx context.Context, orgID uuid.UUID, in policyspec.RuleInput) (sqlc.PolicyRule, error) {
	// SOURCE-subject shape (S7.5.4): "" defaults to "group" (back-compat). Exactly one
	// of src_group_id / src_user_id, matching src_kind (the DB CHECK backstops it).
	srcKind := in.SrcKind
	if srcKind == "" {
		srcKind = "group"
	}
	switch srcKind {
	case "group":
		if in.SrcGroupID == uuid.Nil || in.SrcUserID != nil {
			return sqlc.PolicyRule{}, apierr.BadRequest("invalid_request", "src_kind=group requires src_group_id (and no src_user_id)")
		}
	case "user":
		if in.SrcUserID == nil || in.SrcGroupID != uuid.Nil {
			return sqlc.PolicyRule{}, apierr.BadRequest("invalid_request", "src_kind=user requires src_user_id (and no src_group_id)")
		}
	default:
		return sqlc.PolicyRule{}, apierr.BadRequest("invalid_request", "src_kind must be group or user")
	}
	// Destination shape: exactly one dst_* set, matching dst_kind.
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
	// A temporary grant must expire in the FUTURE (a past expiry is a no-op grant —
	// reject it rather than silently create a rule that never compiles).
	if in.ExpiresAt != nil && !in.ExpiresAt.After(time.Now()) {
		return sqlc.PolicyRule{}, apierr.BadRequest("invalid_request", "expires_at must be in the future")
	}
	var r sqlc.PolicyRule
	err := s.mutate(ctx, orgID, func(q *sqlc.Queries) error {
		// Referenced src subject + dst must belong to THIS org (no cross-tenant refs).
		if srcKind == "group" {
			if _, e := q.GetUserGroup(ctx, sqlc.GetUserGroupParams{ID: in.SrcGroupID, OrgID: orgID}); e != nil {
				if errors.Is(e, pgx.ErrNoRows) {
					return apierr.BadRequest("group_not_found", "src group not found")
				}
				return e
			}
		} else { // user — must be a CURRENT org member (the FK enforces it too; clean 400 here)
			if _, e := q.GetMembership(ctx, sqlc.GetMembershipParams{OrgID: orgID, UserID: *in.SrcUserID}); e != nil {
				if errors.Is(e, pgx.ErrNoRows) {
					return apierr.BadRequest("user_not_member", "src user is not a member of this org")
				}
				return e
			}
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
			OrgID: orgID, SrcKind: srcKind, SrcGroupID: toPgUUIDVal(in.SrcGroupID), SrcUserID: toPgUUID(in.SrcUserID),
			DstKind: in.DstKind, DstResourceID: toPgUUID(in.DstResourceID), DstGroupID: toPgUUID(in.DstGroupID),
			ExpiresAt: toPgTimestamptz(in.ExpiresAt),
		})
		if e != nil {
			return conflictIfDup(e, "an identical rule already exists")
		}
		meta := map[string]any{"src_kind": srcKind, "dst_kind": in.DstKind}
		if srcKind == "user" {
			meta["src_user_id"] = in.SrcUserID.String()
		} else {
			meta["src_group_id"] = in.SrcGroupID.String()
		}
		if in.ExpiresAt != nil {
			meta["expires_at"] = in.ExpiresAt.UTC().Format(time.RFC3339)
		}
		return writeAudit(ctx, q, orgID, "policy.rule_created", "policy_rule", r.ID.String(), meta)
	})
	return r, err
}

func (s *Service) DeletePolicyRule(ctx context.Context, orgID, ruleID uuid.UUID) error {
	return s.mutate(ctx, orgID, func(q *sqlc.Queries) error {
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

// ── temporary-grant lifecycle (S7.5.4 slice 2) ──────────────────────────────────

// grantSweepInterval paces the expiry sweeper. Expiry is a PROMISE (a grant that
// says "expires 5:00" leaving at 5:04 is a poor look), so it is tighter than the
// health staleness cadence (5 min).
const grantSweepInterval = time.Minute

// ExtendGrant moves a temporary grant's window IN PLACE (window-extensible, never
// delete+recreate — the recreate would churn the /32 and cause a spurious push).
// The DB lapse-guard (expires_at > now()) makes extend and the sweeper compose on
// the row lock: a lapsed grant is terminal (409 grant_lapsed), only a live one moves.
func (s *Service) ExtendGrant(ctx context.Context, orgID, ruleID uuid.UUID, newExpiresAt time.Time) (sqlc.PolicyRule, error) {
	if !newExpiresAt.After(time.Now()) {
		return sqlc.PolicyRule{}, apierr.BadRequest("invalid_request", "expires_at must be in the future")
	}
	var r sqlc.PolicyRule
	// withTx, NOT mutate: extend moves only expires_at, which is NOT in the compiled
	// enforcement artifact (the CanonicalHash projection excludes it — a grant's window
	// never changes its src/dst allow tuple). A push here would recompile the whole org
	// and re-apply a BYTE-IDENTICAL ruleset on every gateway — the "spurious push" the
	// ExtendPolicyRule comment says the in-place update avoids. It's safe to skip because
	// nothing on the data plane consumes expires_at: lapse is enforced by the compiler's
	// active-rules filter on the next real recompile + the expiry sweeper's delete-push.
	// This endpoint is extend-ONLY (ExtendGrantRequest is expires_at-only, additionalProperties
	// false; ExtendPolicyRule SETs only expires_at) — no artifact-affecting field can flow
	// through it, so dropping the push can never hide an edit that SHOULD push. (S7.5.4 box-walk)
	err := s.withTx(ctx, func(q *sqlc.Queries) error {
		// Read the CURRENT window FIRST, under a row lock, so (a) old_expires_at is the true
		// PRE-update value for the D7 old->new audit, and (b) the sweeper's DELETE can't
		// interleave between this read and the UPDATE (extend and sweep serialize on this lock).
		existing, ge := q.GetPolicyRuleForUpdate(ctx, sqlc.GetPolicyRuleForUpdateParams{ID: ruleID, OrgID: orgID})
		if errors.Is(ge, pgx.ErrNoRows) {
			return apierr.NotFound("rule_not_found", "rule not found")
		}
		if ge != nil {
			return ge
		}
		if !existing.ExpiresAt.Valid {
			return apierr.Conflict("not_temporary", "this is a permanent grant — it has no expiry to extend")
		}
		if !existing.ExpiresAt.Time.After(time.Now()) {
			return apierr.Conflict("grant_lapsed", "this grant already expired — create a new one")
		}
		oldExpiry := existing.ExpiresAt.Time.UTC().Format(time.RFC3339) // captured BEFORE the update
		var e error
		r, e = q.ExtendPolicyRule(ctx, sqlc.ExtendPolicyRuleParams{
			ID: ruleID, OrgID: orgID, NewExpiresAt: pgtype.Timestamptz{Time: newExpiresAt, Valid: true},
		})
		if e != nil {
			return e // the row is locked + verified-live above, so 0-rows can't happen here
		}
		// D7: the audit shows the old->new window (who moved a grant's window, from when).
		return writeAudit(ctx, q, orgID, "policy.grant_extended", "policy_rule", ruleID.String(),
			map[string]any{"old_expires_at": oldExpiry, "new_expires_at": newExpiresAt.UTC().Format(time.RFC3339)})
	})
	return r, err
}

// SweepExpiredGrants DELETEs the currently-expired temporary grants (the story-end
// AMENDMENT — delete-on-sweep replaced linger; see docs/S7.5.4-decisions.md). Each lapse
// is audited grant_expired (SYSTEM actor grant-expiry, cause, SAME-TX with the delete),
// then each affected org is pushed org-wide (F1: a lapsed grant's /32 must leave EVERY
// gateway, not just the subject's node — incl. a non-hosting gateway that had the /32 as a
// group destination). STATELESS: every expired grant is deleted each call, so a failed or
// interrupted (downtime) tick leaves rows for the next tick — no window to skip, no lapse
// unaudited. Composes with ExtendGrant on the row lock (an extend that moved expires_at to
// the future is no longer <= now(), so it is neither deleted nor falsely audited expired).
func (s *Service) SweepExpiredGrants(ctx context.Context) (int, error) {
	var expired []sqlc.DeleteExpiredGrantsRow
	err := s.withTx(ctx, func(q *sqlc.Queries) error {
		rows, e := q.DeleteExpiredGrants(ctx)
		if e != nil {
			return e
		}
		expired = rows
		for _, r := range rows {
			if e := writeSystemAudit(ctx, q, r.OrgID, "policy.grant_expired", "policy_rule", r.ID.String(),
				map[string]any{"cause": "grant_expired"}); e != nil {
				return e
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	seen := map[uuid.UUID]bool{}
	for _, r := range expired {
		if !seen[r.OrgID] {
			seen[r.OrgID] = true
			s.pushOrg(ctx, r.OrgID)
		}
	}
	return len(expired), nil
}

// StartGrantExpirySweeper runs the stateless expiry sweep on an interval until ctx ends.
// No in-memory window: a sweep error just retries next tick (the rows are still expired),
// and a grant that lapses during downtime is deleted+audited on the next tick after
// restart — the audit trail has no hole. Enterprise-only (started in main).
func (s *Service) StartGrantExpirySweeper(ctx context.Context) {
	t := time.NewTicker(grantSweepInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if n, err := s.SweepExpiredGrants(ctx); err != nil {
				slog.Warn("grant_expiry_sweep_failed", slog.String("error", err.Error()))
			} else if n > 0 {
				slog.Info("grant_expiry_swept", slog.Int("count", n))
			}
		}
	}
}

// writeSystemAudit records a SYSTEM-actor audit row (0027) in the caller's tx — the
// sweeper's grant_expired lapses have no human initiator.
func writeSystemAudit(ctx context.Context, q *sqlc.Queries, orgID uuid.UUID, action, targetType, targetID string, meta map[string]any) error {
	raw := []byte("{}")
	if meta != nil {
		b, err := json.Marshal(meta)
		if err != nil {
			return err
		}
		raw = b
	}
	as := "policy-grants"
	tt, tid := targetType, targetID
	_, err := q.InsertSystemAuditLog(ctx, sqlc.InsertSystemAuditLogParams{
		OrgID: pgtype.UUID{Bytes: orgID, Valid: true}, ActorSystem: &as,
		Action: action, TargetType: &tt, TargetID: &tid, Metadata: raw,
	})
	return err
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
// SetMode flips the org enforcement mode. Enabling (off->enforcing) returns the
// AFFECTED full-tunnel devices (S7.2 decision 2a): the server OBEYS regardless (S4.7
// server-is-truth), but the response tells the caller / the S7.4 warn-and-confirm
// exactly whose internet egress the flip governs (blast radius). Disabling returns no
// list (re-opening the mesh restores egress). Both directions are audited + push the
// gateways (via mutate).
func (s *Service) SetMode(ctx context.Context, orgID uuid.UUID, mode string) (string, []policyspec.AffectedDevice, error) {
	if mode != ModeOff && mode != ModeEnforcing {
		return "", nil, apierr.BadRequest("invalid_request", "mode must be off or enforcing")
	}
	err := s.mutate(ctx, orgID, func(q *sqlc.Queries) error {
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
	if err != nil {
		return "", nil, err
	}
	// The mode is committed + the gateways pushed the instant mutate() returned nil —
	// the enforcement change is ALREADY live. The affected-device list is advisory
	// blast-radius info for the caller / S7.4 warn-and-confirm; it is BEST-EFFORT and
	// must NEVER fail the call (finding #A). A failure here returning 500 would tell the
	// admin "failed to enable" while the org is in fact live-enforcing and blocking — a
	// UX-to-breach path. On its error we log and return success with no list (S4.7
	// server-is-truth: the mutation is truth; everything after is advisory).
	var affected []policyspec.AffectedDevice
	if mode == ModeEnforcing {
		if rows, e := s.q.ListActiveFullTunnelDevices(ctx, orgID); e != nil {
			slog.Warn("affected_full_tunnel_enumeration_failed_after_mode_commit",
				slog.String("org_id", orgID.String()), slog.String("error", e.Error()))
		} else {
			for _, r := range rows {
				affected = append(affected, policyspec.AffectedDevice{ID: r.ID, Name: r.Name})
			}
		}
	}
	return mode, affected, nil
}

// ── snapshot + invalidation (consumed by S7.2) ──────────────────────────────────

// BuildSnapshot loads the org's full policy state into the pure-compiler input.
// S7.2 calls Compile(BuildSnapshot(...)) when serving a node's desired state.
func (s *Service) BuildSnapshot(ctx context.Context, orgID uuid.UUID) (Snapshot, error) {
	org, err := s.q.GetOrganizationByID(ctx, orgID)
	if err != nil {
		return Snapshot{}, err
	}
	// COMPILER INPUT: active rules only — expired temporary grants are excluded here
	// (the clockless pure compiler can't filter by now(); the snapshot build applies
	// it). The admin LIST uses ListPolicyRulesByOrg (shows expired rules distinctly).
	rules, err := s.q.ListActivePolicyRulesForOrg(ctx, orgID)
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
			ID:         r.ID,
			SrcKind:    r.SrcKind, SrcGroupID: fromPgUUID(r.SrcGroupID), SrcUserID: fromPgUUID(r.SrcUserID),
			DstKind:       r.DstKind,
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
		snap.Devices = append(snap.Devices, Device{ID: d.ID, UserID: d.UserID, NodeID: d.NodeID, AssignedIP: ip})
	}
	return snap, nil
}

// CompiledForNode builds the per-node compiled artifact the control plane pushes in
// the desired state (S7.2). A node with active devices gets its compiled entry; a
// device-LESS node gets an explicit deny-all under enforcing (so the blanket mesh is
// removed proactively, not left until the first device) or nil under off (legacy
// mesh). This is the nodes.PolicyProvider the desired-state path calls.
func (s *Service) CompiledForNode(ctx context.Context, orgID, nodeID uuid.UUID) (*policyspec.Compiled, error) {
	snap, err := s.BuildSnapshot(ctx, orgID)
	if err != nil {
		return nil, err
	}
	compiled := Compile(snap)
	if c, ok := compiled[nodeID]; ok {
		return &c, nil
	}
	if snap.Mode == ModeEnforcing {
		return &policyspec.Compiled{
			Version: policyspec.ProtocolVersion, NodeID: nodeID.String(),
			Mode: ModeEnforcing, Mesh: false, // deny-all: no blanket even with no devices
		}, nil
	}
	return nil, nil // off / no policy -> agent keeps the legacy mesh
}

// CompiledHashesForNodes returns each requested node's canonical PUSHED hash, building
// the org snapshot ONCE (finding #5: the ListNodes read path called CompiledForNode per
// node, rebuilding the whole snapshot — 6 queries + a compile — N times for one org).
// Per node it reproduces CompiledForNode's pick-or-fallback exactly, so the hash a node
// is compared against here is identical to the one it would be served. A node with no
// compiled entry maps to the enforcing deny-all hash (fail-closed) or "" when off.
func (s *Service) CompiledHashesForNodes(ctx context.Context, orgID uuid.UUID, nodeIDs []uuid.UUID) (map[uuid.UUID]string, error) {
	snap, err := s.BuildSnapshot(ctx, orgID)
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]string, len(nodeIDs))
	// OFF mode: there is no enforcement boundary, so no node has a meaningful pushed hash
	// — return "" for every node. The status layer reads "" as "in sync" (finding #C: an
	// off org must never show policy-out-of-sync, and a device-having off node whose agent
	// applied a mesh artifact must not compare against it).
	if snap.Mode != ModeEnforcing {
		for _, id := range nodeIDs {
			out[id] = ""
		}
		return out, nil
	}
	compiled := Compile(snap)
	for _, id := range nodeIDs {
		if c, ok := compiled[id]; ok {
			out[id] = policyspec.CanonicalHash(c)
			continue
		}
		// Enforcing node with no active devices -> the deny-all fallback CompiledForNode
		// serves (SAME policyspec.ProtocolVersion the DesiredState fail-closed path uses).
		out[id] = policyspec.CanonicalHash(policyspec.Compiled{
			Version: policyspec.ProtocolVersion, NodeID: id.String(),
			Mode: ModeEnforcing, Mesh: false,
		})
	}
	return out, nil
}

// AffectedNodeIDs returns the nodes whose compiled policy could change for this
// org — the nodes that currently host active devices. A policy mutation is
// org-wide, so S7.2 recompiles + pushes to exactly these nodes (the invalidation
// target). Model-layer logic, tested here; the push itself is S7.2.
func (s *Service) AffectedNodeIDs(ctx context.Context, orgID uuid.UUID) ([]uuid.UUID, error) {
	return s.q.ListActiveNodeIDsForOrg(ctx, orgID)
}

// ── helpers ─────────────────────────────────────────────────────────────────────

// Notifier signals gateways to re-fetch desired state (the <5s push path). The
// nodepush hub satisfies it; nil = no push (tests / provider-only service).
type Notifier interface{ NotifyMany(nodeIDs []uuid.UUID) }

// mutate runs a mutation in a transaction and, on success, PUSHES the org's
// device-hosting gateways so they re-fetch + recompile within the <5s spec. Every
// compiler input changes through one of these (group/resource/rule CRUD, membership
// add/remove, mode) -- so wrapping them here is the single choke point for the
// recompile+push triggers. The push is best-effort (a missed signal is caught by the
// agent's reconcile-interval safety net); it never fails the mutation.
func (s *Service) mutate(ctx context.Context, orgID uuid.UUID, fn func(*sqlc.Queries) error) error {
	if err := s.withTx(ctx, fn); err != nil {
		return err
	}
	s.pushOrg(ctx, orgID)
	return nil
}

// pushOrg notifies every gateway that currently hosts an active device in the org
// (the nodes whose compiled policy could change). Best-effort.
func (s *Service) pushOrg(ctx context.Context, orgID uuid.UUID) {
	if s.notify == nil {
		return
	}
	ids, err := s.AffectedNodeIDs(ctx, orgID)
	if err != nil || len(ids) == 0 {
		return
	}
	s.notify.NotifyMany(ids)
}

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
	// metadata is NOT NULL — default a nil meta to an empty JSON object, never a nil
	// []byte (which pgx sends as SQL NULL → 23502, silently 500ing every audited DELETE
	// that passes nil: group.deleted / resource.deleted / policy.rule_deleted). The other
	// audit helpers (invites, sso, devices, nodes) already default to "{}"; this one didn't.
	raw := []byte("{}")
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

// canonicalCIDR returns the masked (host-bits-zeroed) form of a prefix already
// validated by validateResource, so the stored + compiled DstCIDR is canonical
// (e.g. 10.0.5.5/24 -> 10.0.5.0/24) and never rejected/mis-read by the S7.2
// nftables/ipset apply.
func canonicalCIDR(s string) string {
	p, err := netip.ParsePrefix(s)
	if err != nil {
		return s // unreachable after validateResource; keep input rather than panic
	}
	return p.Masked().String()
}

func toPgUUID(id *uuid.UUID) pgtype.UUID {
	if id == nil {
		return pgtype.UUID{Valid: false}
	}
	return pgtype.UUID{Bytes: *id, Valid: true}
}

// toPgUUIDVal maps a value UUID to nullable pg: uuid.Nil => SQL NULL (a per-user
// rule has src_group_id NULL, and vice versa).
func toPgUUIDVal(id uuid.UUID) pgtype.UUID {
	if id == uuid.Nil {
		return pgtype.UUID{Valid: false}
	}
	return pgtype.UUID{Bytes: id, Valid: true}
}

func toPgTimestamptz(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{Valid: false}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
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
