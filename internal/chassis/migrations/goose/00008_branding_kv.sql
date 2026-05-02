-- +goose Up
CREATE TABLE IF NOT EXISTS branding_kv (
    key         TEXT PRIMARY KEY,
    value       TEXT NOT NULL DEFAULT '',
    value_type  TEXT NOT NULL DEFAULT 'text' CHECK (value_type IN ('text','longtext','url','iban','bic','file','color','int','json')),
    file_path   TEXT,
    file_size   INTEGER NOT NULL DEFAULT 0,
    file_mime   TEXT NOT NULL DEFAULT '',
    updated_by  TEXT REFERENCES users(id),
    updated_at  TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
) STRICT;

CREATE INDEX IF NOT EXISTS idx_branding_kv_updated ON branding_kv(updated_at);

-- +goose Down
DROP INDEX IF EXISTS idx_branding_kv_updated;
DROP TABLE IF EXISTS branding_kv;
