DROP INDEX IF EXISTS policy_rules_src_node_id_idx;
ALTER TABLE policy_rules DROP COLUMN IF EXISTS src_node_id;
