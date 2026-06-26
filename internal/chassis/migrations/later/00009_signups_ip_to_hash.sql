-- +goose Up
-- RGPD : remplacer la colonne ip (brute) par ip_hash (SHA256+COOKIE_SECRET).
-- Stratégie copy→drop→rename (SQLite STRICT ne supporte pas RENAME/DROP COLUMN de manière portable).
CREATE TABLE signups_new (
  id           TEXT PRIMARY KEY,
  email        TEXT NOT NULL,
  display_name TEXT NOT NULL,
  profile      TEXT NOT NULL,
  fields_json  TEXT NOT NULL DEFAULT '{}',
  message      TEXT NOT NULL DEFAULT '',
  ip_hash      TEXT NOT NULL DEFAULT '',
  created_at   TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  processed_at TEXT
) STRICT;

INSERT INTO signups_new (id, email, display_name, profile, fields_json, message, ip_hash, created_at, processed_at)
  SELECT id, email, display_name, profile, fields_json, message, '', created_at, processed_at FROM signups;

DROP TABLE signups;
ALTER TABLE signups_new RENAME TO signups;

CREATE INDEX idx_signups_email ON signups(email);
CREATE INDEX idx_signups_processed ON signups(processed_at) WHERE processed_at IS NULL;

-- +goose Down
CREATE TABLE signups_old (
  id           TEXT PRIMARY KEY,
  email        TEXT NOT NULL,
  display_name TEXT NOT NULL,
  profile      TEXT NOT NULL,
  fields_json  TEXT NOT NULL DEFAULT '{}',
  message      TEXT NOT NULL DEFAULT '',
  ip           TEXT NOT NULL DEFAULT '',
  created_at   TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  processed_at TEXT
) STRICT;

INSERT INTO signups_old (id, email, display_name, profile, fields_json, message, ip, created_at, processed_at)
  SELECT id, email, display_name, profile, fields_json, message, '', created_at, processed_at FROM signups;

DROP TABLE signups;
ALTER TABLE signups_old RENAME TO signups;

CREATE INDEX idx_signups_email ON signups(email);
