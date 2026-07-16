//go:build enterprise

package idpsync

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
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
	"github.com/tunnexio/tunnex/apps/api/internal/crypto"
	"github.com/tunnexio/tunnex/apps/api/internal/idpsyncspec"
)

// Pusher fires the org-wide gateway recompile (<5s). Wired to the device pusher (same one the
// tenancy deactivate sweep uses), so a synced membership change reaches the data plane promptly.
type Pusher interface {
	PushOrgNodes(ctx context.Context, orgID uuid.UUID)
}

// ProviderFactory builds a DirectoryProvider from a stored config + its decrypted secret. Injectable
// so the box-walk (slice 5) can drive a faked directory; the default builds an EntraProvider.
type ProviderFactory func(cfg sqlc.IdpSyncConfig, secret string) (DirectoryProvider, error)

// DefaultProviderFactory builds the real provider for a config. Entra-only in v1 (D4); google is a
// fast-follow behind the same DirectoryProvider interface, rejected loudly until then.
func DefaultProviderFactory(cfg sqlc.IdpSyncConfig, secret string) (DirectoryProvider, error) {
	switch cfg.Provider {
	case "microsoft":
		tenant := ""
		if cfg.TenantID != nil {
			tenant = *cfg.TenantID
		}
		return NewEntraProvider(tenant, cfg.ClientID, secret, nil), nil
	default:
		return nil, apierr.BadRequest("provider_not_supported", "directory sync for this provider is not yet available")
	}
}

// Service is the enterprise IdP-sync port + poller. It OWNS the config credential lifecycle and the
// group mapping; the per-poll convergence is delegated to a Reconciler (slice 3) built per config.
// Service also IS the reconciler's Store (methods below), so the sqlc + push wiring lives in one place.
type Service struct {
	pool    *pgxpool.Pool
	q       *sqlc.Queries
	sealer  *crypto.Sealer
	push    Pusher
	deprov  Deprovisioner
	factory ProviderFactory
	now     func() time.Time
	logger  *slog.Logger
}

func NewService(pool *pgxpool.Pool, sealer *crypto.Sealer, push Pusher, deprov Deprovisioner, logger *slog.Logger) *Service {
	return &Service{
		pool: pool, q: sqlc.New(pool), sealer: sealer, push: push, deprov: deprov,
		factory: DefaultProviderFactory, now: time.Now, logger: logger,
	}
}

// SetProviderFactory overrides the directory-client factory (box-walk faked directory).
func (s *Service) SetProviderFactory(f ProviderFactory) { s.factory = f }

// SetClock overrides the clock (tests).
func (s *Service) SetClock(now func() time.Time) { s.now = now }

// perConfigPollTimeout bounds one org's reconcile so a large or hung tenant cannot stall the whole
// poll tick for every other tenant (#5).
const perConfigPollTimeout = 2 * time.Minute

func supportedProvider(p string) error {
	// v1 syncs Microsoft Entra only; Google is a planned fast-follow behind the same DirectoryProvider
	// interface. The OpenAPI enum still lists google for forward-compat, so the sync-capability gate
	// lives HERE — reject an unsupported provider at CONFIG time with a clean 400 (#6), instead of
	// accepting it and surfacing only perpetual-degraded health at sync time.
	if p != "microsoft" {
		return apierr.BadRequest("provider_not_supported", "directory sync currently supports microsoft only")
	}
	return nil
}

// ── config lifecycle (the port) ──────────────────────────────────────────────────

// UpsertConfig connects/updates a provider credential, sealing the secret at rest.
func (s *Service) UpsertConfig(ctx context.Context, orgID uuid.UUID, provider string, in idpsyncspec.ConfigInput) (idpsyncspec.ConfigView, error) {
	if err := supportedProvider(provider); err != nil {
		return idpsyncspec.ConfigView{}, err
	}
	sealed, err := s.sealer.Seal([]byte(in.ClientSecret))
	if err != nil {
		return idpsyncspec.ConfigView{}, err
	}
	fp := s.sealer.Fingerprint([]byte(in.ClientSecret)) // keyed proof-of-secret (S4.5) — never the secret
	var tid *string
	if strings.TrimSpace(in.TenantID) != "" {
		t := in.TenantID
		tid = &t
	}
	var row sqlc.IdpSyncConfig
	err = s.withTx(ctx, func(q *sqlc.Queries) error {
		var e error
		row, e = q.UpsertIdpSyncConfig(ctx, sqlc.UpsertIdpSyncConfigParams{
			OrgID: orgID, Provider: provider, ClientID: in.ClientID,
			SecretSealed: []byte(sealed), TenantID: tid, Enabled: in.Enabled,
		})
		if e != nil {
			return e
		}
		// A credential change is high-privilege — audit it (human actor). Metadata is secret-free:
		// only the fingerprint proves WHICH secret, never the secret or the sealed bytes.
		return s.humanAudit(ctx, q, orgID, "idp_sync.config_updated", "idp_sync_config", provider,
			map[string]any{"provider": provider, "client_id": in.ClientID, "secret_fingerprint": fp, "enabled": in.Enabled})
	})
	if err != nil {
		return idpsyncspec.ConfigView{}, err
	}
	view := s.viewOf(row)
	view.SecretFingerprint = fp
	return view, nil
}

// Health returns the two-tier sync health (derived at read time).
func (s *Service) Health(ctx context.Context, orgID uuid.UUID, provider string) (idpsyncspec.HealthView, error) {
	if err := supportedProvider(provider); err != nil {
		return idpsyncspec.HealthView{}, err
	}
	row, err := s.q.GetIdpSyncConfig(ctx, sqlc.GetIdpSyncConfigParams{OrgID: orgID, Provider: provider})
	if errors.Is(err, pgx.ErrNoRows) {
		return idpsyncspec.HealthView{}, apierr.NotFound("idp_sync_not_configured", "directory sync is not configured for this provider")
	}
	if err != nil {
		return idpsyncspec.HealthView{}, err
	}
	v := s.viewOf(row)
	return idpsyncspec.HealthView{
		Provider: v.Provider, SyncHealth: v.SyncHealth, LastSyncOk: v.LastSyncOk,
		LastSyncAt: v.LastSyncAt, LastSyncError: v.LastSyncError,
	}, nil
}

// Trigger reconciles one org+provider now (the manual "sync now"), returning the resulting health.
func (s *Service) Trigger(ctx context.Context, orgID uuid.UUID, provider string) (idpsyncspec.HealthView, error) {
	if err := supportedProvider(provider); err != nil {
		return idpsyncspec.HealthView{}, err
	}
	cfg, err := s.q.GetIdpSyncConfig(ctx, sqlc.GetIdpSyncConfigParams{OrgID: orgID, Provider: provider})
	if errors.Is(err, pgx.ErrNoRows) {
		return idpsyncspec.HealthView{}, apierr.NotFound("idp_sync_not_configured", "directory sync is not configured for this provider")
	}
	if err != nil {
		return idpsyncspec.HealthView{}, err
	}
	// A reconcile error is recorded on the config's health by the reconciler; we still return the
	// (now-degraded) health view rather than a 500, so "sync now" surfaces the failure legibly.
	_ = s.reconcileConfig(ctx, cfg)
	return s.Health(ctx, orgID, provider)
}

// PollAll reconciles every enabled config across all orgs — the background poller's unit of work.
func (s *Service) PollAll(ctx context.Context) {
	cfgs, err := s.q.ListEnabledIdpSyncConfigs(ctx)
	if err != nil {
		s.logger.Error("idp_sync_poll_list_failed", slog.String("error", err.Error()))
		return
	}
	for _, cfg := range cfgs {
		// #5: bound each org so one huge/hung tenant can't consume the whole tick for everyone else.
		cctx, cancel := context.WithTimeout(ctx, perConfigPollTimeout)
		err := s.reconcileConfig(cctx, cfg)
		cancel()
		if err != nil {
			s.logger.Warn("idp_sync_poll_config_degraded",
				slog.String("org_id", cfg.OrgID.String()), slog.String("provider", cfg.Provider),
				slog.String("error", err.Error()))
		}
	}
}

// reconcileConfig decrypts the credential, builds the provider, and runs a Reconciler for one config.
func (s *Service) reconcileConfig(ctx context.Context, cfg sqlc.IdpSyncConfig) error {
	secret, err := s.sealer.Open(string(cfg.SecretSealed))
	if err != nil {
		// A credential we can't decrypt is a hard failure — record it (fail-static: no membership
		// change) so the operator sees a broken config rather than a silent no-op.
		msg := "credential decrypt failed"
		_ = s.RecordResult(ctx, cfg.OrgID, cfg.Provider, false, false, msg, s.now())
		return errors.New(msg)
	}
	prov, err := s.factory(cfg, string(secret))
	if err != nil {
		msg := "provider unavailable: " + err.Error()
		_ = s.RecordResult(ctx, cfg.OrgID, cfg.Provider, false, false, msg, s.now())
		return err
	}
	r := NewReconciler(prov, s, s.deprov, s.now)
	return r.ReconcileConfig(ctx, cfg.OrgID, cfg.Provider)
}

// ── group mapping (the port) ─────────────────────────────────────────────────────

// MapGroup binds a directory group to a Tunnex group: either a NEW idp_sync group (Name), or an
// EXISTING manual group (GroupID) — the latter refused unless it is empty (D1 refuse-unless-empty).
func (s *Service) MapGroup(ctx context.Context, orgID uuid.UUID, provider string, in idpsyncspec.MapInput) (sqlc.UserGroup, error) {
	if err := supportedProvider(provider); err != nil {
		return sqlc.UserGroup{}, err
	}
	if strings.TrimSpace(in.IdpGroupID) == "" {
		return sqlc.UserGroup{}, apierr.BadRequest("invalid_request", "idp_group_id is required")
	}
	if in.GroupID != nil && strings.TrimSpace(in.Name) != "" {
		return sqlc.UserGroup{}, apierr.BadRequest("invalid_request", "provide either name (create) or group_id (bind), not both")
	}
	// A config must exist first (the mapping references a configured provider).
	if _, err := s.q.GetIdpSyncConfig(ctx, sqlc.GetIdpSyncConfigParams{OrgID: orgID, Provider: provider}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return sqlc.UserGroup{}, apierr.BadRequest("idp_sync_not_configured", "configure the provider credential before mapping groups")
		}
		return sqlc.UserGroup{}, err
	}

	var out sqlc.UserGroup
	err := s.withTx(ctx, func(q *sqlc.Queries) error {
		var e error
		if in.GroupID != nil {
			// BIND an existing group. Refuse-unless-empty (D1): a populated manual group cannot flip.
			g, ge := q.GetUserGroup(ctx, sqlc.GetUserGroupParams{ID: *in.GroupID, OrgID: orgID})
			if errors.Is(ge, pgx.ErrNoRows) {
				return apierr.NotFound("group_not_found", "group not found")
			}
			if ge != nil {
				return ge
			}
			if g.Origin != "manual" {
				return apierr.Conflict("group_already_synced", "group is already directory-managed")
			}
			n, ce := q.CountGroupMembers(ctx, sqlc.CountGroupMembersParams{OrgID: orgID, GroupID: *in.GroupID})
			if ce != nil {
				return ce
			}
			if n > 0 {
				return apierr.Conflict("group_not_empty", "only an empty group can be converted to directory sync; remove its members first")
			}
			out, e = q.BindGroupToIdp(ctx, sqlc.BindGroupToIdpParams{
				ID: *in.GroupID, OrgID: orgID, IdpProvider: &provider, IdpGroupID: &in.IdpGroupID,
			})
			if errors.Is(e, pgx.ErrNoRows) { // lost the manual race
				return apierr.Conflict("group_already_synced", "group is already directory-managed")
			}
			return conflictIfDup(e)
		}
		// CREATE a new idp_sync group.
		name := strings.TrimSpace(in.Name)
		if name == "" {
			name = in.IdpGroupID
		}
		out, e = q.CreateIdpSyncGroup(ctx, sqlc.CreateIdpSyncGroupParams{
			OrgID: orgID, Name: name, IdpProvider: &provider, IdpGroupID: &in.IdpGroupID,
		})
		return conflictIfDup(e)
	})
	return out, err
}

// UnmapGroup reverts an idp_sync group to a plain, empty manual group + pushes (its members leave).
func (s *Service) UnmapGroup(ctx context.Context, orgID uuid.UUID, provider string, groupID uuid.UUID) error {
	if err := supportedProvider(provider); err != nil {
		return err
	}
	err := s.withTx(ctx, func(q *sqlc.Queries) error {
		if _, e := q.UnbindIdpGroup(ctx, sqlc.UnbindIdpGroupParams{ID: groupID, OrgID: orgID}); e != nil {
			if errors.Is(e, pgx.ErrNoRows) {
				return apierr.NotFound("group_not_found", "no synced group with that id")
			}
			return e
		}
		_, e := q.DeleteGroupMembersByGroup(ctx, sqlc.DeleteGroupMembersByGroupParams{OrgID: orgID, GroupID: groupID})
		return e
	})
	if err != nil {
		return err
	}
	s.push.PushOrgNodes(ctx, orgID) // the group's grants disappear org-wide
	return nil
}

// ── reconciler Store (S7.5.2 slice 3) ────────────────────────────────────────────

func (s *Service) ListIdpSyncGroups(ctx context.Context, orgID uuid.UUID, provider string) ([]SyncGroup, error) {
	rows, err := s.q.ListIdpSyncGroups(ctx, sqlc.ListIdpSyncGroupsParams{OrgID: orgID, IdpProvider: &provider})
	if err != nil {
		return nil, err
	}
	out := make([]SyncGroup, 0, len(rows))
	for _, r := range rows {
		gid := ""
		if r.IdpGroupID != nil {
			gid = *r.IdpGroupID
		}
		out = append(out, SyncGroup{ID: r.ID, IdpGroupID: gid})
	}
	return out, nil
}

func (s *Service) ListIdpGroupMembers(ctx context.Context, orgID, groupID uuid.UUID) ([]SyncedMember, error) {
	rows, err := s.q.ListIdpGroupMembers(ctx, sqlc.ListIdpGroupMembersParams{OrgID: orgID, GroupID: groupID})
	if err != nil {
		return nil, err
	}
	out := make([]SyncedMember, 0, len(rows))
	for _, r := range rows {
		ext := ""
		if r.IdpExternalID != nil {
			ext = *r.IdpExternalID
		}
		out = append(out, SyncedMember{UserID: r.UserID, ExternalID: ext})
	}
	return out, nil
}

func (s *Service) ResolveOrgUser(ctx context.Context, orgID uuid.UUID, email string) (uuid.UUID, bool, error) {
	row, err := s.q.GetOrgUserByEmail(ctx, sqlc.GetOrgUserByEmailParams{OrgID: orgID, Email: email})
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, err
	}
	return row.ID, true, nil
}

func (s *Service) AddIdpGroupMember(ctx context.Context, orgID, groupID, userID uuid.UUID, externalID string) (bool, error) {
	var ext *string
	if externalID != "" {
		ext = &externalID
	}
	n, err := s.q.AddIdpGroupMember(ctx, sqlc.AddIdpGroupMemberParams{OrgID: orgID, GroupID: groupID, UserID: userID, IdpExternalID: ext})
	if err != nil {
		return false, err
	}
	if n == 0 {
		return false, nil // already present (ON CONFLICT DO NOTHING) — no audit, no push
	}
	return true, s.systemAudit(ctx, orgID, "group.member_synced_added", groupID, userID, "present_in_directory_group")
}

func (s *Service) RemoveIdpGroupMember(ctx context.Context, orgID, groupID, userID uuid.UUID) (bool, error) {
	n, err := s.q.RemoveIdpGroupMember(ctx, sqlc.RemoveIdpGroupMemberParams{OrgID: orgID, GroupID: groupID, UserID: userID})
	if err != nil {
		return false, err
	}
	if n == 0 {
		return false, nil // nothing to remove (concurrent converge already did) — no audit, no push
	}
	return true, s.systemAudit(ctx, orgID, "group.member_synced_removed", groupID, userID, "absent_from_directory_group")
}

func (s *Service) RecordResult(ctx context.Context, orgID uuid.UUID, provider string, ok, advanceClock bool, errMsg string, now time.Time) error {
	var ep *string
	if errMsg != "" {
		ep = &errMsg
	}
	return s.q.RecordIdpSyncResult(ctx, sqlc.RecordIdpSyncResultParams{
		OrgID: orgID, Provider: provider, LastSyncOk: ok, LastSyncError: ep,
		Column5: advanceClock, UpdatedAt: now,
	})
}

func (s *Service) PushOrg(ctx context.Context, orgID uuid.UUID) { s.push.PushOrgNodes(ctx, orgID) }

// ── helpers ──────────────────────────────────────────────────────────────────────

func (s *Service) viewOf(row sqlc.IdpSyncConfig) idpsyncspec.ConfigView {
	v := idpsyncspec.ConfigView{
		Provider: row.Provider, ClientID: row.ClientID, Enabled: row.Enabled, LastSyncOk: row.LastSyncOk,
	}
	if row.TenantID != nil {
		v.TenantID = *row.TenantID
	}
	if row.LastSyncError != nil {
		v.LastSyncError = *row.LastSyncError
	}
	var lastAt *time.Time
	if row.LastSyncAt.Valid {
		t := row.LastSyncAt.Time
		lastAt = &t
		v.LastSyncAt = &t
	}
	v.SyncHealth = ClassifySyncHealth(row.LastSyncOk, lastAt, row.CreatedAt, s.now(), EscalationCeiling).String()
	return v
}

// humanAudit records a principal-attributed audit row (config changes are human actions via the
// authenticated PUT). Secret-free metadata only.
func (s *Service) humanAudit(ctx context.Context, q *sqlc.Queries, orgID uuid.UUID, action, targetType, targetID string, meta map[string]any) error {
	b, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	actor := pgtype.UUID{}
	if p, ok := authctx.PrincipalFrom(ctx); ok {
		actor = pgtype.UUID{Bytes: p.UserID, Valid: true}
	}
	tt, tid := targetType, targetID
	_, err = q.InsertAuditLog(ctx, sqlc.InsertAuditLogParams{
		OrgID:       pgtype.UUID{Bytes: orgID, Valid: true},
		ActorUserID: actor,
		Action:      action,
		TargetType:  &tt,
		TargetID:    &tid,
		Metadata:    b,
	})
	return err
}

func (s *Service) systemAudit(ctx context.Context, orgID uuid.UUID, action string, groupID, userID uuid.UUID, cause string) error {
	tt, tid := "group", groupID.String()
	as := "idp-sync"
	meta, err := json.Marshal(map[string]any{"user_id": userID.String(), "cause": cause})
	if err != nil {
		return err
	}
	_, err = s.q.InsertSystemAuditLog(ctx, sqlc.InsertSystemAuditLogParams{
		OrgID:       pgtype.UUID{Bytes: orgID, Valid: true},
		ActorSystem: &as,
		Action:      action,
		TargetType:  &tt,
		TargetID:    &tid,
		Metadata:    meta,
	})
	return err
}

func (s *Service) withTx(ctx context.Context, fn func(*sqlc.Queries) error) error {
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

func conflictIfDup(err error) error {
	if err == nil {
		return nil
	}
	// #9: match the SQLSTATE structurally (like enterprise/policy/service.go), not the error text —
	// a driver upgrade or a wrapped/localized message must not silently turn a 409 into a 500.
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return apierr.New(http.StatusConflict, "conflict", "that directory group is already mapped")
	}
	return err
}
