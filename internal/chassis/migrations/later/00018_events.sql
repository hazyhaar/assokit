-- +goose Up
-- Événements / agenda communautaire. Une instance = une communauté ; un
-- événement a date/lieu/horaires → table dédiée (pas un nœud d'arbre).
CREATE TABLE IF NOT EXISTS events (
  id              TEXT PRIMARY KEY,
  slug            TEXT NOT NULL UNIQUE,
  title           TEXT NOT NULL,
  description_md  TEXT NOT NULL DEFAULT '',
  description_html TEXT NOT NULL DEFAULT '',
  location        TEXT NOT NULL DEFAULT '',
  starts_at       TEXT NOT NULL,
  ends_at         TEXT,
  created_by      TEXT REFERENCES users(id) ON DELETE SET NULL,
  created_at      TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  deleted_at      TEXT
) STRICT;

CREATE INDEX IF NOT EXISTS idx_events_starts  ON events(starts_at);
CREATE INDEX IF NOT EXISTS idx_events_live     ON events(deleted_at, starts_at);

-- +goose Down
DROP INDEX IF EXISTS idx_events_live;
DROP INDEX IF EXISTS idx_events_starts;
DROP TABLE IF EXISTS events;
