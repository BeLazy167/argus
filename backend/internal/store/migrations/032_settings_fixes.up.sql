-- 1. repos.installation_id → installations(id) ON DELETE CASCADE
ALTER TABLE repos DROP CONSTRAINT repos_installation_id_fkey;
ALTER TABLE repos ADD CONSTRAINT repos_installation_id_fkey
    FOREIGN KEY (installation_id) REFERENCES installations(id) ON DELETE CASCADE;

-- 2. model_configs.repo_id → repos(id) ON DELETE CASCADE
ALTER TABLE model_configs DROP CONSTRAINT model_configs_repo_id_fkey;
ALTER TABLE model_configs ADD CONSTRAINT model_configs_repo_id_fkey
    FOREIGN KEY (repo_id) REFERENCES repos(id) ON DELETE CASCADE;

-- 3. model_configs.installation_id → installations(id) ON DELETE CASCADE
ALTER TABLE model_configs DROP CONSTRAINT model_configs_installation_id_fkey;
ALTER TABLE model_configs ADD CONSTRAINT model_configs_installation_id_fkey
    FOREIGN KEY (installation_id) REFERENCES installations(id) ON DELETE CASCADE;

-- 4. provider_keys.installation_id → installations(id) ON DELETE CASCADE
ALTER TABLE provider_keys DROP CONSTRAINT provider_keys_installation_id_fkey;
ALTER TABLE provider_keys ADD CONSTRAINT provider_keys_installation_id_fkey
    FOREIGN KEY (installation_id) REFERENCES installations(id) ON DELETE CASCADE;

-- 5. prompt_templates.repo_id → repos(id) ON DELETE CASCADE
ALTER TABLE prompt_templates DROP CONSTRAINT prompt_templates_repo_id_fkey;
ALTER TABLE prompt_templates ADD CONSTRAINT prompt_templates_repo_id_fkey
    FOREIGN KEY (repo_id) REFERENCES repos(id) ON DELETE CASCADE;

-- 6. rules.installation_id → installations(id) ON DELETE CASCADE
ALTER TABLE rules DROP CONSTRAINT rules_installation_id_fkey;
ALTER TABLE rules ADD CONSTRAINT rules_installation_id_fkey
    FOREIGN KEY (installation_id) REFERENCES installations(id) ON DELETE CASCADE;

-- 7. activity_log.installation_id → installations(id) ON DELETE CASCADE
ALTER TABLE activity_log DROP CONSTRAINT activity_log_installation_id_fkey;
ALTER TABLE activity_log ADD CONSTRAINT activity_log_installation_id_fkey
    FOREIGN KEY (installation_id) REFERENCES installations(id) ON DELETE CASCADE;

-- 8. patterns.installation_id → installations(id) ON DELETE CASCADE
ALTER TABLE patterns DROP CONSTRAINT patterns_installation_id_fkey;
ALTER TABLE patterns ADD CONSTRAINT patterns_installation_id_fkey
    FOREIGN KEY (installation_id) REFERENCES installations(id) ON DELETE CASCADE;

-- 9. patterns.repo_id → repos(id) ON DELETE CASCADE
ALTER TABLE patterns DROP CONSTRAINT patterns_repo_id_fkey;
ALTER TABLE patterns ADD CONSTRAINT patterns_repo_id_fkey
    FOREIGN KEY (repo_id) REFERENCES repos(id) ON DELETE CASCADE;

-- Add key_hint column to provider_keys
ALTER TABLE provider_keys ADD COLUMN IF NOT EXISTS key_hint TEXT DEFAULT '';

-- JSONB type checks
ALTER TABLE repos ADD CONSTRAINT settings_json_is_object CHECK (jsonb_typeof(settings_json) = 'object');
ALTER TABLE installations ADD CONSTRAINT default_settings_is_object CHECK (jsonb_typeof(default_settings) = 'object');
ALTER TABLE installations ADD CONSTRAINT feature_flags_is_object CHECK (jsonb_typeof(feature_flags) = 'object');
