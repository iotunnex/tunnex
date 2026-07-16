DROP INDEX IF EXISTS policy_rules_user_group_uniq;
DROP INDEX IF EXISTS policy_rules_user_resource_uniq;
DROP INDEX IF EXISTS policy_rules_group_group_uniq;
DROP INDEX IF EXISTS policy_rules_group_resource_uniq;
CREATE UNIQUE INDEX policy_rules_resource_uniq ON policy_rules (org_id, src_group_id, dst_resource_id)
    WHERE dst_kind = 'resource';
CREATE UNIQUE INDEX policy_rules_group_uniq ON policy_rules (org_id, src_group_id, dst_group_id)
    WHERE dst_kind = 'group';

DROP INDEX IF EXISTS policy_rules_expires_idx;
ALTER TABLE policy_rules DROP COLUMN expires_at;
ALTER TABLE policy_rules DROP CONSTRAINT policy_rules_src_check;
ALTER TABLE policy_rules DROP CONSTRAINT policy_rules_src_user_fk;
ALTER TABLE policy_rules DROP COLUMN src_user_id;
ALTER TABLE policy_rules ALTER COLUMN src_group_id SET NOT NULL;
ALTER TABLE policy_rules DROP COLUMN src_kind;
