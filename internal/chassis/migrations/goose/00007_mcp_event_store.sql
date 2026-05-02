-- +goose Up
CREATE TABLE IF NOT EXISTS mcp_event_store (
    stream_id   TEXT NOT NULL,
    event_id    TEXT NOT NULL,
    event_type  TEXT NOT NULL,
    payload     TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (stream_id, event_id)
) STRICT;

CREATE INDEX IF NOT EXISTS idx_mcp_event_store_ts
    ON mcp_event_store(created_at);

-- +goose Down
DROP INDEX IF EXISTS idx_mcp_event_store_ts;
DROP TABLE IF EXISTS mcp_event_store;
