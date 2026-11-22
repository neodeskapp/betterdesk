-- Indexes of agent_bundles, created after its column additions.
CREATE INDEX IF NOT EXISTS idx_agent_bundles_bundle_id ON agent_bundles (bundle_id);
CREATE INDEX IF NOT EXISTS idx_agent_bundles_hash ON agent_bundles (branding_hash);
