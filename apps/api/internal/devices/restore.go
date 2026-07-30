package devices

import (
	"context"
	"log/slog"

	"github.com/google/uuid"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
	"github.com/tunnexio/tunnex/apps/api/internal/ipalloc"
)

// RestoreResult reports what a cascade restore actually did, per device.
type RestoreResult struct {
	DeviceID uuid.UUID
	Name     string
	// KeptAddress is true when the device got its ORIGINAL address back. False means a fresh one was allocated,
	// which is the case that costs the user a re-import — and the case that must be audited and surfaced distinctly.
	KeptAddress bool
	OldIP       string
	NewIP       string
}

// RestoreCascadeRevokedDevices brings back the devices that were revoked BECAUSE their gateway was (S13.1 D5,
// Wall 6).
//
// WHY THIS EXISTS. Revoking a gateway cascades to every device homed on it, so the documented recovery procedure
// handed back a working gateway with ZERO users, each needing a re-issued one-time config. One rebuild became a
// fleet-wide user event, invisible until people called.
//
// THE RULED SHAPE — reclaim first, allocate second:
//
//   - ATTEMPT THE ORIGINAL ADDRESS. The common case is a gateway rebuilt within minutes with nothing else having
//     taken it, and that case must cost users NOTHING: their existing WireGuard config keeps working, because the
//     interface address it embeds is still theirs.
//   - ALLOCATE A FRESH ONE only when the original is genuinely held. Then the user's config IS stale and must be
//     re-imported, which is why that case alone is marked and audited.
//
// Unconditionally allocating fresh would impose a fleet-wide re-import for a contention that usually did not
// happen; refusing to restore unless the original is free would let whoever took one address decide whether a
// user's device returns.
//
// THE ORACLE IS ASKED, NEVER INFERRED. Whether an address is free is a fact ListActiveDeviceAllocations owns — its
// own comment calls it "the SINGLE definition of live allocation... so there are no two filtered reads to drift
// apart". This reads it once, under the same org advisory lock device-create takes, so allocation and restore
// serialize on one snapshot rather than racing to hand the same address to two devices.
//
// DELIBERATELY REVOKED DEVICES ARE NEVER TOUCHED: the candidate query filters on cause='cascade', and the restore
// statement repeats that predicate so a caller who skipped the filter still cannot revive one.
func (s *Service) RestoreCascadeRevokedDevices(ctx context.Context, orgID, nodeID uuid.UUID) ([]RestoreResult, error) {
	var out []RestoreResult
	err := s.withTx(ctx, func(q *sqlc.Queries) error {
		// The ORG advisory lock, the same one device-create takes, so allocation and restore serialize on one
		// snapshot instead of racing to hand the same free address to two devices.
		if e := q.LockDeviceKey(ctx, orgID.String()); e != nil {
			return e
		}
		candidates, err := q.ListCascadeRevokedDevicesForNode(ctx, nodeID)
		if err != nil {
			return err
		}
		if len(candidates) == 0 {
			return nil
		}
		org, err := q.GetOrganizationByID(ctx, orgID)
		if err != nil {
			return err
		}
		// ONE read of the oracle, then tracked locally as we hand addresses out — otherwise two devices in this
		// same loop could both be told the same free address is theirs.
		allocs, err := q.ListActiveDeviceAllocations(ctx, orgID)
		if err != nil {
			return err
		}
		taken := map[string]bool{}
		used := make([]string, 0, len(allocs)+len(candidates))
		for _, a := range allocs {
			if a.AssignedIp != nil {
				taken[*a.AssignedIp] = true
				used = append(used, *a.AssignedIp)
			}
		}

		for _, c := range candidates {
			old := ""
			if c.AssignedIp != nil {
				old = *c.AssignedIp
			}
			ip := old
			kept := true
			// Reclaim only if we recorded an address AND nobody holds it now. An empty `old` means the row predates
			// 0059's stop-destroying-the-address change: unknown, so allocate rather than guess.
			//
			// TWO LOAD-BEARING LAYERS, and neither is vestigial. This check is the first; the partial unique index
			// devices_org_ip_key is the second. Removing this check does NOT silently double-assign — the index
			// raises a constraint violation instead, which a mutation test confirmed (the failure arrived as
			// SQLSTATE 23505, not as this function's assertion). Worth naming: "defence in depth" and "a blind
			// guard I have not noticed yet" look identical until you check which layer caught it.
			if old == "" || taken[old] {
				fresh, aerr := ipalloc.Allocate(org.PoolCidr, used)
				if aerr != nil {
					// Pool exhausted mid-restore: stop rather than restore a partial set with no addresses. The
					// remaining devices stay cascade-revoked and a retry after freeing space picks them up.
					return aerr
				}
				ip, kept = fresh, false
			}
			restored, rerr := q.RestoreCascadeRevokedDevice(ctx, sqlc.RestoreCascadeRevokedDeviceParams{
				ID: c.ID, AssignedIp: &ip,
			})
			if rerr != nil {
				return rerr
			}
			taken[ip] = true
			used = append(used, ip)
			out = append(out, RestoreResult{
				DeviceID: restored.ID, Name: restored.Name, KeptAddress: kept, OldIP: old, NewIP: ip,
			})

			// AUDITED DISTINCTLY when the address changed, because the operator's mental model is "my gateway came
			// back" and a changed address is the surprise: that user's config no longer works and must be
			// re-imported. Same row otherwise, so the restore itself is always on the record.
			action := "device.restored"
			meta := map[string]any{"cause": "gateway_recovered", "assigned_ip": ip, "kept_address": kept}
			if !kept {
				action = "device.restored_readdressed"
				meta["previous_assigned_ip"] = old
				meta["consequence"] = "the device's exported profile embeds the OLD address and will not connect " +
					"until re-imported"
				// The device SURFACE carries this too, as of Slice 6: devices.provisioned_ip snapshots the address
				// the config baked, and needs_reexport now compares it for every provisioning mode. No flag is
				// written here — staleness stays DERIVED at read time, so re-addressing cannot leave a stored bit
				// that disagrees with the row. Deliberately: the fork's third condition ("mark stale only in the
				// fallback case") is satisfied by the comparison being true only when the address actually moved.
				//
				// Slice 5 shipped with this as a NAMED interim gap (the audit event was the only signal, and the
				// meta carried a `surface_gap` key saying so). Slice 6 closed it; the key is gone rather than left
				// to age into a false claim.
			}
			if aerr := audit(ctx, q, orgID, nil, action, "device", restored.ID.String(), meta); aerr != nil {
				return aerr
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(out) > 0 {
		readdressed := 0
		for _, r := range out {
			if !r.KeptAddress {
				readdressed++
			}
		}
		s.logger.Info("devices_restored_after_gateway_recovery",
			slog.String("node_id", nodeID.String()),
			slog.Int("restored", len(out)),
			slog.Int("kept_original_address", len(out)-readdressed),
			slog.Int("readdressed_needing_reimport", readdressed))
		s.PushOrgNodes(ctx, orgID)
	}
	return out, nil
}
