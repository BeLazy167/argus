-- Drop JSONB type checks
ALTER TABLE installations DROP CONSTRAINT IF EXISTS feature_flags_is_object;
ALTER TABLE installations DROP CONSTRAINT IF EXISTS default_settings_is_object;
ALTER TABLE repos DROP CONSTRAINT IF EXISTS settings_json_is_object;

-- Drop key_hint column
ALTER TABLE provider_keys DROP COLUMN IF EXISTS key_hint;

-- 9. patterns.repo_id → repos(id) without CASCADE
ALTER TABLE patterns DROP CONSTRAINT patterns_repo_id_fkey;
ALTER TABLE patterns ADD CONSTRAINT patterns_repo_id_fkey
    FOREIGN KEY (repo_id) REFERENCES repos(id);

-- 8. patterns.installation_id → installations(id) without CASCADE
ALTER TABLE patterns DROP CONSTRAINT patterns_installation_id_fkey;
ALTER TABLE patterns ADD CONSTRAINT patterns_installation_id_fkey
    FOREIGN KEY (installation_id) REFERENCES installations(id);

-- 7. activity_log.installation_id → installations(id) without CASCADE
ALTER TABLE activity_log DROP CONSTRAINT activity_log_installation_id_fkey;
ALTER TABLE activity_log ADD CONSTRAINT activity_log_installation_id_fkey
    FOREIGN KEY (installation_id) REFERENCES installations(id);

-- 6. rules.installation_id → installations(id) without CASCADE
ALTER TABLE rules DROP CONSTRAINT rules_installation_id_fkey;
ALTER TABLE rules ADD CONSTRAINT rules_installation_id_fkey
    FOREIGN KEY (installation_id) REFERENCES installations(id);

-- 5. prompt_templates.repo_id → repos(id) without CASCADE
ALTER TABLE prompt_templates DROP CONSTRAINT prompt_templates_repo_id_fkey;
ALTER TABLE prompt_templates ADD CONSTRAINT prompt_templates_repo_id_fkey
    FOREIGN KEY (repo_id) REFERENCES repos(id);

-- 4. provider_keys.installation_id → installations(id) without CASCADE
ALTER TABLE provider_keys DROP CONSTRAINT provider_keys_installation_id_fkey;
ALTER TABLE provider_keys ADD CONSTRAINT provider_keys_installation_id_fkey
    FOREIGN KEY (installation_id) REFERENCES installations(id);

-- 3. model_configs.installation_id → installations(id) without CASCADE
ALTER TABLE model_configs DROP CONSTRAINT model_configs_installation_id_fkey;
ALTER TABLE model_configs ADD CONSTRAINT model_configs_installation_id_fkey
    FOREIGN KEY (installation_id) REFERENCES installations(id);

-- 2. model_configs.repo_id → repos(id) without CASCADE
ALTER TABLE model_configs DROP CONSTRAINT model_configs_repo_id_fkey;
ALTER TABLE model_configs ADD CONSTRAINT model_configs_repo_id_fkey
    FOREIGN KEY (repo_id) REFERENCES repos(id);

-- 1. repos.installation_id → installations(id) without CASCADE
ALTER TABLE repos DROP CONSTRAINT repos_installation_id_fkey;
ALTER TABLE repos ADD CONSTRAINT repos_installation_id_fkey
    FOREIGN KEY (installation_id) REFERENCES installations(id);
