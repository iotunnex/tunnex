//go:build enterprise

package idpsync

import "time"

// SyncTier is the legible two-tier sync-health state (D2), DERIVED at read time from the stored
// idp_sync_configs row — never a dead-green field. Mirrors the S7.4b freshness-clock pattern:
// the boolean says "degraded now", the clock says "how long / how bad".
type SyncTier int

const (
	TierOK        SyncTier = iota // last poll succeeded
	TierDegraded                  // immediate tier: a poll is currently failing, within the ceiling
	TierEscalated                 // escalated tier: still failing past 3× the poll interval (30 min)
)

func (t SyncTier) String() string {
	switch t {
	case TierOK:
		return "ok"
	case TierDegraded:
		return "degraded"
	case TierEscalated:
		return "escalated"
	default:
		return "unknown"
	}
}

// EscalationCeiling is 3× the 10-minute poll interval (D2): a sync failing longer than this has
// been broken across three whole cycles and escalates.
const EscalationCeiling = 30 * time.Minute

// ClassifySyncHealth projects the two-tier state.
//   - lastSyncOk true                        → TierOK
//   - lastSyncOk false, last GOOD sync fresh → TierDegraded (immediate tier; a transient blip, or a
//     stable known-bad mapping like a gone group whose fetch still advanced the clock)
//   - lastSyncOk false, last GOOD sync stale → TierEscalated (the clock froze at the last success
//     and now exceeds the ceiling: a sustained outage)
//
// The escalation anchor is the last SUCCESSFUL sync; if a config never synced, it is the creation
// time, so a config that fails from birth still escalates a ceiling after it was created.
func ClassifySyncHealth(lastSyncOk bool, lastSyncAt *time.Time, createdAt, now time.Time, ceiling time.Duration) SyncTier {
	if lastSyncOk {
		return TierOK
	}
	anchor := createdAt
	if lastSyncAt != nil {
		anchor = *lastSyncAt
	}
	if now.Sub(anchor) > ceiling {
		return TierEscalated
	}
	return TierDegraded
}
