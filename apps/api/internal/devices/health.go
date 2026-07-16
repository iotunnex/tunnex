// S7.5.3 device health (posture checks v1). Facts are CLIENT-REPORTED — spoofable
// by a compromised client; this deters honest non-compliance and produces an audit
// trail, it is NOT attestation (docs/S7.5.3-decisions.md §Threat model). Evaluation
// is continuous (every report re-evaluates against the org's per-check config) and
// enforcement rides the existing exclude-then-push machinery: a require-mode failure
// sets devices.health_blocked, the active-device readers drop the device, and the
// org-wide push pulls its /32 from every gateway within seconds.

package devices

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
	"github.com/tunnexio/tunnex/apps/api/internal/apierr"
)

const (
	// healthActorSystem is the first-class system audit actor (0027) for
	// evaluation-driven gate flips: no human initiates them, the evaluator does.
	healthActorSystem = "device-health"

	// HealthStaleTTL: a report older than this is ABSENCE (D4) — the device shows
	// posture_unknown and, critically, a stale block is CLEARED by the sweep (only
	// a FRESH positive non-compliant report gates). ~3 report intervals.
	HealthStaleTTL = 30 * time.Minute

	// HealthSweepInterval paces the staleness sweep (StartHealthSweeper).
	HealthSweepInterval = 5 * time.Minute
)

// Check kinds and modes (v1: os_version + disk_encryption; EDR is S7.5.3b).
const (
	CheckOSVersion      = "os_version"
	CheckDiskEncryption = "disk_encryption"

	ModeWarn    = "warn"
	ModeRequire = "require"
)

// HealthFacts is one client self-report (raw facts; the server evaluates).
// DiskEncrypted nil = the client could NOT determine the fact (reported ABSENT,
// never guessed) — taxonomy class 2: the configured check is SKIPPED (absence
// never blocks) and stored as NULL, the "unknown" the dashboard surfaces.
// Distinct from a garbled POSITIVE value (class 1), which gates.
type HealthFacts struct {
	Platform      string // macos | windows | linux | other
	OSVersion     string
	DiskEncrypted *bool
	CollectedAt   *time.Time // client-claimed; informational only
}

// FailedCheck is one configured check the latest report failed.
type FailedCheck struct {
	Kind string `json:"kind"`
	Mode string `json:"mode"`
}

// HealthEvaluation is the server's verdict on a report.
type HealthEvaluation struct {
	State        string // compliant | noncompliant
	Blocked      bool   // any require-mode check failed
	FailedChecks []FailedCheck
}

// HealthCheckConfig is one org opt-in row (no row = check off).
type HealthCheckConfig struct {
	Kind  string
	Mode  string
	Param json.RawMessage
}

// osVersionParam is the os_version check's param: per-platform minimums. A
// platform absent from Min is NOT enforced on that platform (fail-open per the
// threat model — we block what we can see is bad, not what we can't read).
type osVersionParam struct {
	Min map[string]string `json:"min"`
}

var validPlatforms = map[string]bool{"macos": true, "windows": true, "linux": true, "other": true}

// evaluateHealth applies the org's configured checks to reported facts. Pure —
// unit-testable without a DB.
func evaluateHealth(checks []HealthCheckConfig, f HealthFacts) HealthEvaluation {
	ev := HealthEvaluation{State: "compliant", FailedChecks: []FailedCheck{}}
	for _, c := range checks {
		failed := false
		switch c.Kind {
		case CheckDiskEncryption:
			if f.DiskEncrypted == nil {
				continue // fact reported ABSENT (client couldn't read it) — absence never blocks
			}
			failed = !*f.DiskEncrypted
		case CheckOSVersion:
			var p osVersionParam
			if err := json.Unmarshal(c.Param, &p); err != nil {
				continue // malformed config never blocks; validated at write, belt+braces here
			}
			min, ok := p.Min[f.Platform]
			if !ok {
				continue // platform not configured => not enforced on it
			}
			failed = versionLess(f.OSVersion, min)
		}
		if failed {
			ev.State = "noncompliant"
			ev.FailedChecks = append(ev.FailedChecks, FailedCheck{Kind: c.Kind, Mode: c.Mode})
			if c.Mode == ModeRequire {
				ev.Blocked = true
			}
		}
	}
	return ev
}

// versionLess compares dotted numeric versions ("14.5" < "15.0"); non-numeric
// suffixes are ignored ("22631.foo" -> 22631). An UNPARSEABLE reported version
// counts as LESS (a require-mode min then blocks it): the org explicitly opted
// into a version floor, and "cannot even parse" is not a pass — this is a
// positive garbled report, not absence, so gating it is the honest reading.
func versionLess(v, min string) bool {
	vp, mp := splitVersion(v), splitVersion(min)
	for i := 0; i < len(vp) || i < len(mp); i++ {
		var a, b int
		if i < len(vp) {
			a = vp[i]
		}
		if i < len(mp) {
			b = mp[i]
		}
		if a != b {
			return a < b
		}
	}
	return false
}

func splitVersion(s string) []int {
	parts := strings.Split(strings.TrimSpace(s), ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		// Take the leading digits of each segment ("22631H2" -> 22631).
		j := 0
		for j < len(p) && p[j] >= '0' && p[j] <= '9' {
			j++
		}
		n, err := strconv.Atoi(p[:j])
		if err != nil {
			break
		}
		out = append(out, n)
	}
	return out
}

// validateHealthCheck validates a config write (kind/mode/param).
func validateHealthCheck(kind, mode string, param json.RawMessage) error {
	if mode != ModeWarn && mode != ModeRequire {
		return apierr.BadRequest("invalid_request", "mode must be warn or require")
	}
	switch kind {
	case CheckDiskEncryption:
		if len(param) > 0 && string(param) != "null" {
			return apierr.BadRequest("invalid_request", "disk_encryption takes no param")
		}
	case CheckOSVersion:
		var p osVersionParam
		if len(param) == 0 || json.Unmarshal(param, &p) != nil || len(p.Min) == 0 {
			return apierr.BadRequest("invalid_request", `os_version requires param {"min":{"macos":"14.0",...}}`)
		}
		for plat, v := range p.Min {
			if !validPlatforms[plat] {
				return apierr.BadRequest("invalid_request", fmt.Sprintf("unknown platform %q in min", plat))
			}
			if len(splitVersion(v)) == 0 {
				return apierr.BadRequest("invalid_request", fmt.Sprintf("min version %q is not a dotted numeric version", v))
			}
		}
	default:
		return apierr.BadRequest("invalid_request", "check kind must be os_version or disk_encryption")
	}
	return nil
}

// auditSystem writes a system-actor audit row (0027) in the caller's tx — used
// for evaluation-driven flips where no human is the actor; metadata carries the
// CAUSE ("blocked by device-health because …").
func auditSystem(ctx context.Context, q *sqlc.Queries, orgID uuid.UUID, action, targetType, targetID string, meta map[string]any) error {
	b := []byte("{}")
	if meta != nil {
		b, _ = json.Marshal(meta)
	}
	as := healthActorSystem
	_, err := q.InsertSystemAuditLog(ctx, sqlc.InsertSystemAuditLogParams{
		OrgID: pgtype.UUID{Bytes: [16]byte(orgID), Valid: true}, ActorSystem: &as,
		Action: action, TargetType: &targetType, TargetID: &targetID, Metadata: b,
	})
	return err
}

// ReportHealth ingests one device self-report: verifies ownership (self-report
// only — the same ownership rule as device creation), evaluates against the
// org's checks, persists the snapshot, flips health_blocked on a transition,
// audits the transition, and pushes org-wide when enforcement changed.
func (s *Service) ReportHealth(ctx context.Context, orgID, actorID, deviceID uuid.UUID, facts HealthFacts) (HealthEvaluation, error) {
	if !validPlatforms[facts.Platform] {
		return HealthEvaluation{}, apierr.BadRequest("invalid_request", "platform must be macos, windows, linux or other")
	}
	if strings.TrimSpace(facts.OSVersion) == "" {
		return HealthEvaluation{}, apierr.BadRequest("invalid_request", "os_version is required")
	}

	checks, err := s.ListHealthChecks(ctx, orgID)
	if err != nil {
		return HealthEvaluation{}, err
	}
	ev := evaluateHealth(checks, facts)

	var blockChanged bool
	err = s.withTx(ctx, func(q *sqlc.Queries) error {
		dev, e := q.GetDevice(ctx, sqlc.GetDeviceParams{ID: deviceID, OrgID: orgID})
		if errors.Is(e, pgx.ErrNoRows) {
			return apierr.NotFound("device_not_found", "device not found")
		}
		if e != nil {
			return e
		}
		// Self-report ONLY: posture facts come from the device's owner, never a
		// third party (an admin has no better view of the machine than its owner).
		if dev.UserID != actorID {
			return apierr.New(403, "forbidden", "only the device owner may report its health")
		}
		if dev.Status == "revoked" {
			return apierr.Conflict("device_revoked", "device is revoked")
		}

		// Prior evaluated state (absence => treated as compliant baseline for
		// transition auditing — first noncompliant report still audits).
		priorState := "compliant"
		if prior, e := q.GetDeviceHealth(ctx, deviceID); e == nil {
			priorState = prior.EvaluatedState
		} else if !errors.Is(e, pgx.ErrNoRows) {
			return e
		}

		fc, _ := json.Marshal(ev.FailedChecks)
		collected := pgtype.Timestamptz{}
		if facts.CollectedAt != nil {
			collected = pgtype.Timestamptz{Time: *facts.CollectedAt, Valid: true}
		}
		if _, e := q.UpsertDeviceHealth(ctx, sqlc.UpsertDeviceHealthParams{
			DeviceID: deviceID, Platform: facts.Platform, OsVersion: facts.OSVersion,
			// nil = fact reported absent (client couldn't read it) — stored NULL.
			DiskEncrypted: facts.DiskEncrypted, EvaluatedState: ev.State,
			FailedChecks: fc, CollectedAt: collected,
		}); e != nil {
			return e
		}

		meta := map[string]any{
			"owner": dev.UserID.String(), "platform": facts.Platform,
			"failed_checks": ev.FailedChecks,
		}
		if ev.Blocked != dev.HealthBlocked {
			blockChanged = true
			if _, e := q.SetDeviceHealthBlocked(ctx, sqlc.SetDeviceHealthBlockedParams{
				ID: deviceID, HealthBlocked: ev.Blocked,
			}); e != nil {
				return e
			}
			action, cause := "device.health_blocked", "noncompliant_report"
			if !ev.Blocked {
				action, cause = "device.health_unblocked", "compliant_report"
			}
			meta["cause"] = cause
			return auditSystem(ctx, q, orgID, action, "device", deviceID.String(), meta)
		}
		// No enforcement flip: audit only a warn-level state TRANSITION (the
		// S7.5.2 idempotent no-flood discipline — steady state writes nothing).
		if ev.State != priorState {
			action := "device.health_noncompliant"
			if ev.State == "compliant" {
				action = "device.health_compliant"
			}
			return auditSystem(ctx, q, orgID, action, "device", deviceID.String(), meta)
		}
		return nil
	})
	if err != nil {
		return HealthEvaluation{}, err
	}
	if blockChanged {
		// Org-wide (the S7.3/F1 pin): the device's /32 may be a group-resolved
		// DESTINATION on gateways that don't host it.
		s.PushOrgNodes(ctx, orgID)
	}
	return ev, nil
}

// ListHealthChecks returns the org's configured (opted-in) checks.
func (s *Service) ListHealthChecks(ctx context.Context, orgID uuid.UUID) ([]HealthCheckConfig, error) {
	rows, err := s.q.ListOrgHealthChecks(ctx, orgID)
	if err != nil {
		return nil, err
	}
	out := make([]HealthCheckConfig, 0, len(rows))
	for _, r := range rows {
		out = append(out, HealthCheckConfig{Kind: r.CheckKind, Mode: r.Mode, Param: r.Param})
	}
	return out, nil
}

// SetHealthCheck opts the org into a check (or updates its mode/param), audited.
// The config write NEVER flips any device's gate (D4 grandfather: only a device's
// own next evaluation does) — the returned wouldFail is the best-effort,
// post-commit blast radius: devices whose LAST report would fail this check.
func (s *Service) SetHealthCheck(ctx context.Context, actor, orgID uuid.UUID, kind, mode string, param json.RawMessage) (wouldFail int64, err error) {
	if err := validateHealthCheck(kind, mode, param); err != nil {
		return 0, err
	}
	var paramB []byte
	if len(param) > 0 && string(param) != "null" {
		paramB = param
	}
	err = s.withTx(ctx, func(q *sqlc.Queries) error {
		if _, e := q.UpsertOrgHealthCheck(ctx, sqlc.UpsertOrgHealthCheckParams{
			OrgID: orgID, CheckKind: kind, Mode: mode, Param: paramB,
		}); e != nil {
			return e
		}
		return audit(ctx, q, orgID, &actor, "org.health_check_set", "organization", orgID.String(),
			map[string]any{"check_kind": kind, "mode": mode, "param": json.RawMessage(paramOrNull(paramB))})
	})
	if err != nil {
		return 0, err
	}
	// Best-effort AFTER commit (the S7.3 pass-4 #A lesson: a committed setting
	// flip never fails on the count query).
	cfg := HealthCheckConfig{Kind: kind, Mode: mode, Param: paramB}
	if rows, e := s.q.ListDeviceHealthForOrg(ctx, orgID); e != nil {
		s.logger.Warn("health_would_fail_count_failed_after_commit",
			slog.String("org_id", orgID.String()), slog.String("error", e.Error()))
	} else {
		for _, r := range rows {
			f := HealthFacts{Platform: r.Platform, OSVersion: r.OsVersion, DiskEncrypted: r.DiskEncrypted}
			if v := evaluateHealth([]HealthCheckConfig{cfg}, f); v.State == "noncompliant" {
				wouldFail++
			}
		}
	}
	return wouldFail, nil
}

func paramOrNull(b []byte) []byte {
	if len(b) == 0 {
		return []byte("null")
	}
	return b
}

// DeleteHealthCheck opts the org out of a check (idempotent; audited only when a
// row was actually removed). Devices blocked by the removed check unblock on
// their NEXT report (≤ one report interval) — the same only-evaluation-flips-
// gates rule that protects the fleet on enable protects consistency on disable.
func (s *Service) DeleteHealthCheck(ctx context.Context, actor, orgID uuid.UUID, kind string) error {
	if kind != CheckOSVersion && kind != CheckDiskEncryption {
		return apierr.BadRequest("invalid_request", "check kind must be os_version or disk_encryption")
	}
	return s.withTx(ctx, func(q *sqlc.Queries) error {
		n, e := q.DeleteOrgHealthCheck(ctx, sqlc.DeleteOrgHealthCheckParams{OrgID: orgID, CheckKind: kind})
		if e != nil {
			return e
		}
		if n == 0 {
			return nil // already off — idempotent, nothing to audit
		}
		return audit(ctx, q, orgID, &actor, "org.health_check_cleared", "organization", orgID.String(),
			map[string]any{"check_kind": kind})
	})
}

// SweepStaleHealthBlocks clears health_blocked wherever the backing report has
// gone stale (D4: staleness = absence, and absence never blocks — a device that
// goes SILENT is posture_unknown, not blocked; evasion-by-silence is accepted in
// the threat model because a liar defeats blocking anyway). Audited per device
// (system actor), then each affected org is pushed. Returns the cleared count.
func (s *Service) SweepStaleHealthBlocks(ctx context.Context) (int, error) {
	ttl := pgtype.Interval{Microseconds: HealthStaleTTL.Microseconds(), Valid: true}
	var cleared []sqlc.ClearStaleHealthBlocksRow
	err := s.withTx(ctx, func(q *sqlc.Queries) error {
		rows, e := q.ClearStaleHealthBlocks(ctx, ttl)
		if e != nil {
			return e
		}
		cleared = rows
		for _, r := range rows {
			if e := auditSystem(ctx, q, r.OrgID, "device.health_unblocked", "device", r.ID.String(),
				map[string]any{"cause": "report_stale"}); e != nil {
				return e
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	orgs := map[uuid.UUID]bool{}
	for _, r := range cleared {
		if !orgs[r.OrgID] {
			orgs[r.OrgID] = true
			s.PushOrgNodes(ctx, r.OrgID)
		}
	}
	return len(cleared), nil
}

// StartHealthSweeper runs the staleness sweep on an interval until ctx ends.
// Started only when the device-health edition is enabled (main.go); the sweep is
// cheap when nothing is blocked. First run is one interval out (boot is not a
// posture event).
func (s *Service) StartHealthSweeper(ctx context.Context) {
	t := time.NewTicker(HealthSweepInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if n, err := s.SweepStaleHealthBlocks(ctx); err != nil {
				s.logger.Warn("health_stale_sweep_failed", slog.String("error", err.Error()))
			} else if n > 0 {
				s.logger.Info("health_stale_sweep_cleared", slog.Int("count", n))
			}
		}
	}
}
