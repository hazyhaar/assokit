-- +goose Up
CREATE TABLE IF NOT EXISTS mcp_invocations (
    id            TEXT PRIMARY KEY,
    action_id     TEXT NOT NULL,
    actor_id      TEXT NOT NULL DEFAULT '',
    params_hash   TEXT NOT NULL DEFAULT '',
    result_status TEXT NOT NULL CHECK (result_status IN ('ok','error','partial','denied')),
    duration_ms   INTEGER NOT NULL DEFAULT 0,
    error_msg     TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
) STRICT;

CREATE INDEX IF NOT EXISTS idx_mcp_invocations_actor_ts
    ON mcp_invocations(actor_id, created_at);

CREATE INDEX IF NOT EXISTS idx_mcp_invocations_action_ts
    ON mcp_invocations(action_id, created_at);

-- +goose Down
DROP INDEX IF EXISTS idx_mcp_invocations_action_ts;
DROP INDEX IF EXISTS idx_mcp_invocations_actor_ts;
DROP TABLE IF EXISTS mcp_invocations;
