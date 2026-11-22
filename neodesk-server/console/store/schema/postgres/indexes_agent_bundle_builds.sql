-- Indexes of agent_bundle_builds, created after its column additions.
CREATE INDEX IF NOT EXISTS idx_agent_bundle_builds_hash ON agent_bundle_builds (branding_hash);
CREATE INDEX IF NOT EXISTS idx_agent_bundle_builds_status ON agent_bundle_builds (status);
