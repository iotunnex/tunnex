DROP INDEX policy_rules_cidr_site_uniq;
DROP INDEX policy_rules_cidr_group_uniq;
DROP INDEX policy_rules_cidr_resource_uniq;
ALTER TABLE policy_rules DROP CONSTRAINT policy_rules_src_check;
ALTER TABLE policy_rules ADD CONSTRAINT policy_rules_src_check CHECK (
    (src_kind = 'group' AND src_group_id IS NOT NULL AND src_user_id IS NULL AND src_site_id IS NULL)
 OR (src_kind = 'user'  AND src_user_id  IS NOT NULL AND src_group_id IS NULL AND src_site_id IS NULL)
 OR (src_kind = 'site'  AND src_site_id  IS NOT NULL AND src_group_id IS NULL AND src_user_id IS NULL));
ALTER TABLE policy_rules DROP CONSTRAINT policy_rules_src_kind_check;
ALTER TABLE policy_rules ADD CONSTRAINT policy_rules_src_kind_check CHECK (src_kind IN ('group', 'user', 'site'));
ALTER TABLE policy_rules DROP COLUMN src_cidr;
