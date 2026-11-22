-- Indexes the Node console created in one batch after network_checks.
CREATE INDEX IF NOT EXISTS idx_peer_online ON peer (status_online) WHERE NOT is_deleted;
CREATE INDEX IF NOT EXISTS idx_peer_deleted ON peer (is_deleted);
CREATE INDEX IF NOT EXISTS idx_token_lookup ON access_tokens (token) WHERE NOT revoked;
CREATE INDEX IF NOT EXISTS idx_token_hash_lookup ON access_tokens (token_hash) WHERE NOT revoked;
CREATE INDEX IF NOT EXISTS idx_relay_sessions_expires ON relay_sessions (expires_at);
CREATE INDEX IF NOT EXISTS idx_activity_device ON activity_sessions (device_id);
CREATE INDEX IF NOT EXISTS idx_activity_time ON activity_sessions (started_at);
CREATE INDEX IF NOT EXISTS idx_alert_history_rule ON alert_history (rule_id);
CREATE INDEX IF NOT EXISTS idx_alert_history_device ON alert_history (device_id);
CREATE INDEX IF NOT EXISTS idx_remote_commands_device ON remote_commands (device_id, status);
CREATE INDEX IF NOT EXISTS idx_network_checks_target ON network_checks (target_id);
CREATE INDEX IF NOT EXISTS idx_network_checks_time ON network_checks (checked_at);
