-- Index of agent_templates, created after its column additions.
CREATE INDEX IF NOT EXISTS idx_agent_templates_token ON agent_templates (enrollment_token);
