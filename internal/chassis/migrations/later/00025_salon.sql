-- +goose Up
-- Salons : chat de groupe à la demande, mot de passe optionnel par salon (L1).
CREATE TABLE IF NOT EXISTS salon (
  id             TEXT PRIMARY KEY,
  name           TEXT NOT NULL,
  slug           TEXT NOT NULL UNIQUE,
  creator_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  password_hash  TEXT,
  visio_enabled  INTEGER NOT NULL DEFAULT 0,
  created_at     TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  archived_at    TEXT
) STRICT;

CREATE TABLE IF NOT EXISTS salon_member (
  salon_id   TEXT NOT NULL REFERENCES salon(id) ON DELETE CASCADE,
  user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role       TEXT NOT NULL DEFAULT 'member',
  joined_at  TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (salon_id, user_id)
) STRICT;

CREATE TABLE IF NOT EXISTS salon_message (
  id         TEXT PRIMARY KEY,
  salon_id   TEXT NOT NULL REFERENCES salon(id) ON DELETE CASCADE,
  author_id  TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  body       TEXT NOT NULL,
  body_html  TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
) STRICT;

CREATE INDEX IF NOT EXISTS idx_salon_message ON salon_message(salon_id, created_at);

-- +goose Down
DROP INDEX IF EXISTS idx_salon_message;
DROP TABLE IF EXISTS salon_message;
DROP TABLE IF EXISTS salon_member;
DROP TABLE IF EXISTS salon;
