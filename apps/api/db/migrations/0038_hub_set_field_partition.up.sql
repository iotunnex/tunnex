-- 0038 hub-set field partition (S8.6 story-end REDUCE) — org_hub_set.members becomes TWO writer-partitioned
-- fields, the ACTIVE order DERIVED (never stored). This is the one-truth reduce for hub election:
--   configured = ReconcileHubSet's output (the CONFIGURED membership — pins/capability/order).
--   demoted    = the failover controller's output (members currently promoted-past for staleness).
--   active order = deriveActive(configured, demoted) — ONE shared function, consumed by every reader
--                  (data-plane compiler, policy compiler, the view, the controller). Never persisted.
-- WRITER PARTITION (the #2 two-writer-clobber fix): ReconcileHubSet writes `configured` ONLY; the controller
-- writes `demoted` ONLY. Disjoint fields → neither clobbers the other. generation bumps when EITHER
-- authoritative field changes (a safe SUPERSET of active-order changes — generation is a change TAG on the
-- row, not a count of failovers; condition 1a). The promotion/failback AUDIT events remain the record of
-- active-order transitions specifically (condition 1b).
ALTER TABLE org_hub_set RENAME COLUMN members TO configured;
ALTER TABLE org_hub_set ADD COLUMN demoted uuid[] NOT NULL DEFAULT '{}';
